package adminrisk

import "time"

type riskEventFilters struct {
	From         time.Time
	To           time.Time
	Status       string
	Severity     string
	ActorUserID  string
	TargetUserID string
	Action       string
	Page         int
	PageSize     int
	Sort         string
}

type riskEventItem struct {
	ID           int            `json:"id"`
	EventTime    time.Time      `json:"eventTime"`
	Status       string         `json:"status"`
	Severity     string         `json:"severity"`
	Source       string         `json:"source"`
	ActorUserID  string         `json:"actorUserId,omitempty"`
	TargetUserID string         `json:"targetUserId,omitempty"`
	IP           string         `json:"ip,omitempty"`
	Action       string         `json:"action,omitempty"`
	Reason       string         `json:"reason,omitempty"`
	ResolvedAt   *time.Time     `json:"resolvedAt,omitempty"`
	ResolvedBy   string         `json:"resolvedBy,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

type riskEventQueryResponse struct {
	GeneratedAt time.Time       `json:"generatedAt"`
	From        time.Time       `json:"from"`
	To          time.Time       `json:"to"`
	Page        int             `json:"page"`
	PageSize    int             `json:"pageSize"`
	Total       int             `json:"total"`
	TotalPages  int             `json:"totalPages"`
	HasMore     bool            `json:"hasMore"`
	Sort        string          `json:"sort"`
	Items       []riskEventItem `json:"items"`
}

type createRiskEventPayload struct {
	Severity     string         `json:"severity"`
	Source       string         `json:"source"`
	ActorUserID  string         `json:"actorUserId,omitempty"`
	TargetUserID string         `json:"targetUserId,omitempty"`
	IP           string         `json:"ip,omitempty"`
	Action       string         `json:"action,omitempty"`
	Reason       string         `json:"reason,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

type resolveRiskEventPayload struct {
	Reason string `json:"reason,omitempty"`
}

type riskRuleItem struct {
	Key         string         `json:"key"`
	Description string         `json:"description,omitempty"`
	Config      map[string]any `json:"config"`
	UpdatedAt   time.Time      `json:"updatedAt"`
	UpdatedBy   string         `json:"updatedBy,omitempty"`
}

type riskRuleUpsertPayload struct {
	Rules []riskRuleItem `json:"rules"`
}

type riskRuleListResponse struct {
	GeneratedAt time.Time      `json:"generatedAt"`
	Items       []riskRuleItem `json:"items"`
}
