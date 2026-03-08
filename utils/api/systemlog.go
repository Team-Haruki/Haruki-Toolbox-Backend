package api

import (
	"context"
	"haruki-suite/utils/database/postgresql/systemlog"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

const (
	SystemLogActorTypeAnonymous = "anonymous"
	SystemLogActorTypeUser      = "user"
	SystemLogActorTypeAdmin     = "admin"
	SystemLogActorTypeSystem    = "system"

	SystemLogResultSuccess = "success"
	SystemLogResultFailure = "failure"
)

type SystemLogEntry struct {
	EventTime   *time.Time
	ActorUserID *string
	ActorRole   *string
	ActorType   string
	Action      string
	TargetType  *string
	TargetID    *string
	Result      string
	IP          *string
	UserAgent   *string
	Method      *string
	Path        *string
	RequestID   *string
	Metadata    map[string]any
}

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

func BuildSystemLogEntryFromFiber(c fiber.Ctx, action string, result string, targetType *string, targetID *string, metadata map[string]any) SystemLogEntry {
	userID, _ := c.Locals("userID").(string)
	userRole, _ := c.Locals("userRole").(string)

	actorType := SystemLogActorTypeAnonymous
	if strings.TrimSpace(userID) != "" {
		actorType = roleToActorType(userRole)
	}

	return SystemLogEntry{
		ActorUserID: ptrString(trimAndLimit(userID, 64)),
		ActorRole:   ptrString(trimAndLimit(strings.ToLower(userRole), 32)),
		ActorType:   actorType,
		Action:      trimAndLimit(action, 128),
		TargetType:  targetType,
		TargetID:    targetID,
		Result:      result,
		IP:          ptrString(trimAndLimit(c.IP(), 128)),
		UserAgent:   ptrString(trimAndLimit(c.Get("User-Agent"), 1024)),
		Method:      ptrString(trimAndLimit(c.Method(), 16)),
		Path:        ptrString(trimAndLimit(c.Path(), 512)),
		RequestID:   ptrString(trimAndLimit(c.Get("X-Request-ID"), 128)),
		Metadata:    metadata,
	}
}

func WriteSystemLog(ctx context.Context, apiHelper *HarukiToolboxRouterHelpers, entry SystemLogEntry) error {
	if apiHelper == nil || apiHelper.DBManager == nil || apiHelper.DBManager.DB == nil {
		return nil
	}

	action := trimAndLimit(entry.Action, 128)
	if action == "" {
		return nil
	}

	builder := apiHelper.DBManager.DB.SystemLog.Create().
		SetAction(action).
		SetActorType(normalizeSystemLogActorType(entry.ActorType)).
		SetResult(normalizeSystemLogResult(entry.Result))

	if entry.EventTime != nil {
		builder.SetEventTime(entry.EventTime.UTC())
	} else {
		builder.SetEventTime(time.Now().UTC())
	}
	if entry.ActorUserID != nil {
		builder.SetActorUserID(trimAndLimit(*entry.ActorUserID, 64))
	}
	if entry.ActorRole != nil {
		builder.SetActorRole(trimAndLimit(strings.ToLower(*entry.ActorRole), 32))
	}
	if entry.TargetType != nil {
		builder.SetTargetType(trimAndLimit(*entry.TargetType, 64))
	}
	if entry.TargetID != nil {
		builder.SetTargetID(trimAndLimit(*entry.TargetID, 128))
	}
	if entry.IP != nil {
		builder.SetIP(trimAndLimit(*entry.IP, 128))
	}
	if entry.UserAgent != nil {
		builder.SetUserAgent(trimAndLimit(*entry.UserAgent, 1024))
	}
	if entry.Method != nil {
		builder.SetMethod(strings.ToUpper(trimAndLimit(*entry.Method, 16)))
	}
	if entry.Path != nil {
		builder.SetPath(trimAndLimit(*entry.Path, 512))
	}
	if entry.RequestID != nil {
		builder.SetRequestID(trimAndLimit(*entry.RequestID, 128))
	}
	if entry.Metadata != nil {
		builder.SetMetadata(entry.Metadata)
	}

	_, err := builder.Save(ctx)
	return err
}
