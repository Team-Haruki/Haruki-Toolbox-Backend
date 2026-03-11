package api

import "time"

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
