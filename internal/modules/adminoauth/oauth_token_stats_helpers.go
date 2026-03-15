package adminoauth

import "time"

type adminOAuthTokenStats struct {
	Total           int        `json:"total"`
	Active          int        `json:"active"`
	Revoked         int        `json:"revoked"`
	LatestIssuedAt  *time.Time `json:"latestIssuedAt,omitempty"`
	LatestExpiresAt *time.Time `json:"latestExpiresAt,omitempty"`
}
