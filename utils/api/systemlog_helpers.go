package api

import (
	"haruki-suite/utils/database/postgresql/systemlog"
	"strings"
)

func ptrString(v string) *string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	out := v
	return &out
}

func trimAndLimit(v string, maxLen int) string {
	trimmed := strings.TrimSpace(v)
	if maxLen > 0 && len(trimmed) > maxLen {
		return trimmed[:maxLen]
	}
	return trimmed
}

func normalizeSystemLogActorType(raw string) systemlog.ActorType {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case SystemLogActorTypeUser:
		return systemlog.ActorTypeUser
	case SystemLogActorTypeAdmin:
		return systemlog.ActorTypeAdmin
	case SystemLogActorTypeSystem:
		return systemlog.ActorTypeSystem
	default:
		return systemlog.ActorTypeAnonymous
	}
}

func normalizeSystemLogResult(raw string) systemlog.Result {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case SystemLogResultFailure:
		return systemlog.ResultFailure
	default:
		return systemlog.ResultSuccess
	}
}

func roleToActorType(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "admin", "super_admin":
		return SystemLogActorTypeAdmin
	default:
		return SystemLogActorTypeUser
	}
}
