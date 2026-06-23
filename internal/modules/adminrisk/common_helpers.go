package adminrisk

import (
	platformTime "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/timeutil"
	"time"
)

const (
	adminAuditActionRiskEventCreate  = "admin.risk.event.create"
	adminAuditActionRiskEventResolve = "admin.risk.event.resolve"
	adminAuditActionRiskRulesUpsert  = "admin.risk.rules.upsert"
	adminAuditTargetTypeRiskEvent    = "risk_event"
	adminAuditTargetTypeRiskRule     = "risk_rule"
)

const (
	adminFailureReasonInvalidRequestPayload = "invalid_request_payload"
	adminFailureReasonCreateEventFailed     = "create_event_failed"
)

const (
	defaultRiskEventWindowHours = 24
	maxRiskEventRangeHours      = 24 * 366 // up to ~1 year (leap-safe); coarse buckets keep point count sane
)

func resolveRiskEventTimeRange(fromRaw, toRaw string, now time.Time) (time.Time, time.Time, error) {
	return platformTime.ResolveTimeRange(
		fromRaw,
		toRaw,
		now,
		defaultRiskEventWindowHours*time.Hour,
		maxRiskEventRangeHours*time.Hour,
	)
}

var adminNow = time.Now

func adminNowUTC() time.Time {
	return adminNow().UTC()
}
