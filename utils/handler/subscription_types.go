package handler

type hmesEventNotifyRequest struct {
	EventID             string `json:"event_id"`
	SubscriptionID      string `json:"subscription_id"`
	SubscriptionVersion string `json:"subscription_version"`
	PayloadRef          string `json:"payload_ref,omitempty"`
	EmptyResult         bool   `json:"empty_result"`
}

type BirthdayMonitorMirror struct {
	SubscriptionID      string   `json:"subscription_id"`
	SubscriptionVersion string   `json:"subscription_version"`
	Region              string   `json:"region"`
	UID                 string   `json:"uid"`
	Materials           []string `json:"materials"`
	MaterialIDs         []int    `json:"material_ids"`
	ExpiresAt           int64    `json:"expires_at"`
	NotifyEmpty         bool     `json:"notify_empty"`
}

type BirthdayMonitorSubscriptionIndex struct {
	SubscriptionID string `json:"subscription_id"`
	MonitorKey     string `json:"monitor_key"`
	Region         string `json:"region"`
	UID            string `json:"uid"`
}

type BirthdayMonitorEvent struct {
	EventID             string         `json:"event_id"`
	SubscriptionID      string         `json:"subscription_id"`
	SubscriptionVersion string         `json:"subscription_version"`
	PayloadRef          string         `json:"payload_ref,omitempty"`
	Region              string         `json:"region"`
	UID                 string         `json:"uid"`
	MatchedMaterialIDs  []int          `json:"matched_material_ids"`
	EmptyResult         bool           `json:"empty_result"`
	FilteredPayload     map[string]any `json:"filtered_payload,omitempty"`
	UploadTime          int64          `json:"upload_time"`
	CreatedAt           int64          `json:"created_at"`
}
