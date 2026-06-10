package admin

import (
	"time"
)

const (
	adminReauthTTL          = 10 * time.Minute
	adminReauthMarkerPrefix = "admin:reauth:"
)

type adminReauthPayload struct {
	Password string `json:"password"`
}

type adminReauthResponse struct {
	ReauthenticatedAt time.Time `json:"reauthenticatedAt"`
}
