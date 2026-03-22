package adminsyslog

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	"time"
)

type systemLogQueryFilters struct {
	From        time.Time
	To          time.Time
	ActorTypes  []string
	ActorUserID string
	TargetType  string
	TargetID    string
	Action      string
	Result      string
	Page        int
	PageSize    int
	Sort        string
}

type systemLogListItem = adminCoreModule.SystemLogListItem

type systemLogAppliedFilters struct {
	ActorTypes  []string `json:"actorTypes,omitempty"`
	ActorUserID string   `json:"actorUserId,omitempty"`
	TargetType  string   `json:"targetType,omitempty"`
	TargetID    string   `json:"targetId,omitempty"`
	Action      string   `json:"action,omitempty"`
	Result      string   `json:"result,omitempty"`
}

type systemLogQueryResponse struct {
	GeneratedAt time.Time               `json:"generatedAt"`
	From        time.Time               `json:"from"`
	To          time.Time               `json:"to"`
	Page        int                     `json:"page"`
	PageSize    int                     `json:"pageSize"`
	Total       int                     `json:"total"`
	TotalPages  int                     `json:"totalPages"`
	HasMore     bool                    `json:"hasMore"`
	Sort        string                  `json:"sort"`
	Filters     systemLogAppliedFilters `json:"filters"`
	Items       []systemLogListItem     `json:"items"`
}

type systemLogSummaryResponse struct {
	GeneratedAt time.Time       `json:"generatedAt"`
	From        time.Time       `json:"from"`
	To          time.Time       `json:"to"`
	Total       int             `json:"total"`
	Success     int             `json:"success"`
	Failure     int             `json:"failure"`
	ByAction    []categoryCount `json:"byAction"`
	ByActorType []categoryCount `json:"byActorType"`
	ByResult    []categoryCount `json:"byResult"`
	ByReason    []categoryCount `json:"byReason"`
}
