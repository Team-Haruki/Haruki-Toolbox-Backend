package upload

import (
	"context"
	"fmt"
	"slices"
	"sync/atomic"
	"time"

	redis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
)

const IngressRetention = 35 * 24 * time.Hour

var ingressResults = []string{"accepted", "invalid_client_metadata", "client_version_unsupported", "client_channel_disabled", "invalid_client_credentials", "invalid_token", "insufficient_scope", "invalid_upload_payload", "protocol_retired", "rate_limited", "internal_error", "temporarily_unavailable"}
var ingressLastFailure atomic.Int64
var ingressLastWarning atomic.Int64

func IngressResults() []string      { return slices.Clone(ingressResults) }
func IngressLastFailure() time.Time { return time.Unix(ingressLastFailure.Load(), 0).UTC() }
func IngressKey(day time.Time, protocol, result string) string {
	return fmt.Sprintf("upload:ingress:%s:haruki_proxy:%s:%s", day.UTC().Format("2006-01-02"), protocol, result)
}
func RecordProxyIngress(r *redis.HarukiRedisManager, at time.Time, protocol, result string) {
	if protocol != "2" && protocol != "3" {
		return
	}
	if !slices.Contains(ingressResults, result) {
		result = "internal_error"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	var err error
	if r == nil || r.Redis == nil {
		err = fmt.Errorf("redis unavailable")
	} else {
		_, err = r.IncrementWithTTL(ctx, IngressKey(at, protocol, result), IngressRetention)
	}
	if err != nil {
		now := time.Now().Unix()
		ingressLastFailure.Store(now)
		previous := ingressLastWarning.Load()
		if now-previous >= 60 && ingressLastWarning.CompareAndSwap(previous, now) {
			harukiLogger.Warnf("Upload ingress statistics unavailable")
		}
	}
}
