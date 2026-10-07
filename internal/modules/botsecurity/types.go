package botsecurity

import "time"

// alertItem is one alert in the admin API. Optional values are null, never
// omitted, so the frontend sees a fixed shape.
type alertItem struct {
	ID            int        `json:"id"`
	Kind          string     `json:"kind"`
	BotID         *string    `json:"botId"`
	OwnerQQ       *string    `json:"ownerQq"`
	SourceIP      string     `json:"sourceIp"`
	BuildID       string     `json:"buildId"`
	ClientVersion string     `json:"clientVersion"`
	Reason        string     `json:"reason"`
	Enforced      bool       `json:"enforced"`
	Count         int64      `json:"count"`
	Threshold     int64      `json:"threshold"`
	WindowSeconds int64      `json:"windowSeconds"`
	Node          string     `json:"node"`
	AlertTime     time.Time  `json:"alertTime"`
	ReceivedAt    time.Time  `json:"receivedAt"`
	Status        string     `json:"status"`
	Note          string     `json:"note"`
	HandledBy     *handledBy `json:"handledBy"`
	HandledAt     *time.Time `json:"handledAt"`
}

// handledBy names the admin who last moved the alert out of open. Name is
// empty when that user no longer exists.
type handledBy struct {
	UserID string `json:"userId"`
	Name   string `json:"name"`
}

type alertListResponse struct {
	Items    []alertItem `json:"items"`
	Total    int         `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"pageSize"`
}

type updateAlertPayload struct {
	Status string  `json:"status"`
	Note   *string `json:"note"`
}

type kindCount struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

type alertSummaryResponse struct {
	Open    int         `json:"open"`
	ByKind  []kindCount `json:"byKind"`
	Last24h int         `json:"last24h"`
	Last7d  int         `json:"last7d"`
}
