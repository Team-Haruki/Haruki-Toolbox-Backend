package oauth2

import (
	"context"
	"strconv"
	"sync"
	"time"

	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
)

// deviceReaperBatch bounds the flows one reaper round handles.
const deviceReaperBatch = 100

// DeviceFlowReaperOptions wires the device-flow reaper. Interval and grace come
// from Config.Timings().
type DeviceFlowReaperOptions struct {
	Config      DeviceFlowConfig
	HydraConfig *harukiOAuth2.HydraConfig
	DBManager   *database.HarukiToolboxDBManager
	Logger      *harukiLogger.Logger
}

// StartDeviceFlowReaper revokes consent sessions of flows that recorded a
// consent request ID but never handed out tokens. Hydra refuses a late
// redemption after exp but keeps the consent session, so without the reaper an
// approved-but-unredeemed flow would stay among the user's authorized apps and
// within webhook delivery. Each round handles flows whose exp is older than the
// grace period. It runs whenever the device flow is enabled at startup,
// independent of the runtime switch. The returned wait blocks until the
// goroutine has exited after ctx is canceled.
func StartDeviceFlowReaper(ctx context.Context, options DeviceFlowReaperOptions) (wait func()) {
	logger := deviceFlowLogger(options.Logger)
	if !options.Config.Enabled() {
		logger.Infof("oauth2 device flow reaper disabled: device flow is not enabled")
		return func() {}
	}
	grace := options.Config.Timings().ReaperGrace
	if grace <= 0 {
		grace = time.Minute
	}
	reaper := &deviceFlowReaper{
		store:       newDeviceFlowStore(options.DBManager),
		hydraConfig: options.HydraConfig,
		logger:      logger,
		grace:       grace,
	}
	interval := options.Config.Timings().ReaperInterval
	if interval <= 0 {
		interval = time.Minute
	}
	logger.Infof("oauth2 device flow reaper enabled with interval %s", interval)
	var wg sync.WaitGroup
	wg.Go(func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				logger.Infof("oauth2 device flow reaper stopped")
				return
			case <-ticker.C:
				reaper.runOnce(ctx)
			}
		}
	})
	return wg.Wait
}

type deviceFlowReaper struct {
	store       *deviceFlowStore
	hydraConfig *harukiOAuth2.HydraConfig
	logger      *harukiLogger.Logger
	grace       time.Duration
}

// runOnce handles one batch: claim each due flow by ZREM (so several instances
// never handle one twice), revoke its consent session by consent request ID,
// and mark it expired. A failed revocation puts the flow back with score now,
// so it is retried after another grace period.
func (r *deviceFlowReaper) runOnce(ctx context.Context) {
	now := r.store.now()
	due, err := r.store.dueUnredeemed(ctx, now.Add(-r.grace).UnixMilli(), deviceReaperBatch)
	if err != nil {
		if ctx.Err() == nil {
			logDeviceEvent(r.logger, "warn", "reaped", "", "", "reason=list_failed")
		}
		return
	}
	for _, flowID := range due {
		if ctx.Err() != nil {
			return
		}
		r.reapFlow(ctx, flowID)
	}
}

func (r *deviceFlowReaper) reapFlow(ctx context.Context, flowID string) {
	action, consentRequestID, err := r.store.reap(ctx, flowID)
	if err != nil {
		logDeviceEvent(r.logger, "warn", "reaped", flowID, "", "reason=claim_failed")
		return
	}
	switch action {
	case "REVOKE":
		if err := RevokeHydraConsentSessionByID(ctx, r.hydraConfig, consentRequestID); err != nil {
			logDeviceEvent(r.logger, "warn", "reaped", flowID, "", "reason=revoke_failed")
			if requeueErr := r.store.requeueUnredeemed(ctx, flowID, r.store.now().UnixMilli()); requeueErr != nil {
				logDeviceEvent(r.logger, "error", "reaped", flowID, "", "reason=requeue_failed")
			}
			return
		}
	case "MARK":
	default:
		return
	}
	if err := r.store.markExpired(ctx, flowID); err != nil {
		logDeviceEvent(r.logger, "warn", "reaped", flowID, "", "reason=mark_failed")
		return
	}
	logDeviceEvent(r.logger, "info", "reaped", flowID, "", "revoked="+strconv.FormatBool(action == "REVOKE"))
}
