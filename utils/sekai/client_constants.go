package sekai

import "time"

const (
	defaultSekaiClientTimeout = 15 * time.Second

	httpMethodGet  = "GET"
	httpMethodPost = "POST"
	httpMethodPut  = "PUT"

	statusCodeOK        = 200
	statusCodeForbidden = 403

	headerSessionToken   = "X-Session-Token"
	headerLoginBonus     = "X-Login-Bonus-Status"
	headerRequestID      = "X-Request-Id"
	headerInheritIDToken = "x-inherit-id-verify-token"
)
