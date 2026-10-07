package oauth2

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
)

// overdueTestFlow issues an approved flow whose consent is long overdue for
// the reaper.
func overdueTestFlow(t *testing.T, env *deviceTestEnv) string {
	t.Helper()
	_, flowID := env.issue(testPublicClientID)
	env.approve(flowID, deviceFlowStateApproved, testConsentRequestID)
	if _, err := env.redis.ZAdd(env.unredeemedKey(), 0, flowID); err != nil {
		t.Fatal(err)
	}
	return flowID
}

// revokeServer is a Hydra admin whose consent revocation runs onRevoke and
// answers status.
func revokeServer(t *testing.T, status int, onRevoke func()) *harukiOAuth2.HydraConfig {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && r.URL.Path == "/admin/oauth2/auth/sessions/consent" {
			onRevoke()
			w.WriteHeader(status)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	return harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{PublicURL: server.URL, AdminURL: server.URL, RequestTimeout: 5 * time.Second})
}

func TestDeviceFlowReaperFailures(t *testing.T) {
	t.Run("list failure", func(t *testing.T) {
		env := newDeviceTestEnv(t)
		reaper := &deviceFlowReaper{store: env.store, hydraConfig: env.hydraConfig, logger: env.logger, grace: time.Minute}
		env.redis.SetError(testRedisLoadingError)
		reaper.runOnce(t.Context())
		env.redis.SetError("")
		if !strings.Contains(env.logs.String(), "event=reaped reason=list_failed") {
			t.Fatalf("list failure not logged: %s", env.logs.String())
		}
	})
	t.Run("list failure after cancel is quiet", func(t *testing.T) {
		env := newDeviceTestEnv(t)
		reaper := &deviceFlowReaper{store: env.store, hydraConfig: env.hydraConfig, logger: env.logger, grace: time.Minute}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		reaper.runOnce(ctx)
		if strings.Contains(env.logs.String(), "list_failed") {
			t.Fatal("a canceled round logged a list failure")
		}
	})
	t.Run("claim failure", func(t *testing.T) {
		env := newDeviceTestEnv(t)
		flowID := overdueTestFlow(t, env)
		// The crid key read by the reap script has the wrong type.
		cridKey := env.db.Redis.KeyBuilder().BuildOAuth2DeviceConsentRequestKey(flowID)
		env.redis.HSet(cridKey, "corrupt", "1")
		reaper := &deviceFlowReaper{store: env.store, hydraConfig: env.hydraConfig, logger: env.logger, grace: time.Minute}
		reaper.runOnce(t.Context())
		if !strings.Contains(env.logs.String(), "event=reaped fid="+flowID+" reason=claim_failed") {
			t.Fatalf("claim failure not logged: %s", env.logs.String())
		}
		if len(env.revocationQueries()) != 0 {
			t.Fatal("revoked a flow it could not claim")
		}
	})
	t.Run("requeue failure", func(t *testing.T) {
		env := newDeviceTestEnv(t)
		flowID := overdueTestFlow(t, env)
		hydra := revokeServer(t, http.StatusInternalServerError, func() { env.redis.SetError(testRedisLoadingError) })
		reaper := &deviceFlowReaper{store: env.store, hydraConfig: hydra, logger: env.logger, grace: time.Minute}
		reaper.runOnce(t.Context())
		env.redis.SetError("")
		logs := env.logs.String()
		if !strings.Contains(logs, "fid="+flowID+" reason=revoke_failed") || !strings.Contains(logs, "fid="+flowID+" reason=requeue_failed") {
			t.Fatalf("logs = %s", logs)
		}
	})
	t.Run("mark failure", func(t *testing.T) {
		env := newDeviceTestEnv(t)
		flowID := overdueTestFlow(t, env)
		hydra := revokeServer(t, http.StatusNoContent, func() { env.redis.SetError(testRedisLoadingError) })
		reaper := &deviceFlowReaper{store: env.store, hydraConfig: hydra, logger: env.logger, grace: time.Minute}
		reaper.runOnce(t.Context())
		env.redis.SetError("")
		if !strings.Contains(env.logs.String(), "fid="+flowID+" reason=mark_failed") {
			t.Fatalf("mark failure not logged: %s", env.logs.String())
		}
		if env.inUnredeemed(flowID) || env.field(flowID, "st") != deviceFlowStateApproved {
			t.Fatal("the claimed flow should have left the set without being marked")
		}
	})
}

// Unset reaper timings fall back to one minute.
func TestStartDeviceFlowReaperDefaultTimings(t *testing.T) {
	env := newDeviceTestEnv(t)
	options := testDeviceFlowOptions(env.logger)
	options.Timings.ReaperGrace = 0
	options.Timings.ReaperInterval = 0
	ctx, cancel := context.WithCancel(t.Context())
	wait := StartDeviceFlowReaper(ctx, DeviceFlowReaperOptions{
		Config: NewDeviceFlowConfig(options), HydraConfig: env.hydraConfig, DBManager: env.db, Logger: env.logger,
	})
	cancel()
	wait()
	logs := env.logs.String()
	if !strings.Contains(logs, "reaper enabled with interval 1m0s") || !strings.Contains(logs, "reaper stopped") {
		t.Fatalf("logs = %s", logs)
	}
}

func TestDeviceFlowConfigFallbackTimings(t *testing.T) {
	var cfg DeviceFlowConfig
	if cfg.minPollInterval() != 5*time.Second || cfg.recordGrace() != 30*time.Minute || cfg.claimTTL() != 300*time.Second {
		t.Fatalf("durations = %s %s %s", cfg.minPollInterval(), cfg.recordGrace(), cfg.claimTTL())
	}
	if cfg.maxIntervalSeconds() != 60 || cfg.maxSlowDown() != 30 {
		t.Fatalf("limits = %d %d", cfg.maxIntervalSeconds(), cfg.maxSlowDown())
	}
	timings := cfg.approvalTimings()
	if timings.ApproveLease != 30*time.Second || timings.ApprovalTimeout != 15*time.Second ||
		timings.MinRemainingToApprove != 30*time.Second || timings.MaxApproveAttempts != 3 {
		t.Fatalf("approval timings = %+v", timings)
	}

	configured := NewDeviceFlowConfig(testDeviceFlowOptions(nil))
	if configured.claimTTL() != 300*time.Second || configured.approvalTimings().ApproveLease != 30*time.Second {
		t.Fatal("configured timings were replaced by fallbacks")
	}
	// Dropping the runtime gate leaves only the startup switch.
	ungated := configured.WithRuntimeGate(func(context.Context) (bool, error) { return false, nil }).WithRuntimeGate(nil)
	if active, err := ungated.Active(t.Context()); !active || err != nil {
		t.Fatalf("ungated Active = %v, %v", active, err)
	}
}
