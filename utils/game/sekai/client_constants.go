package sekai

import "time"

const (
	defaultSekaiClientTimeout = 15 * time.Second

	// retrieverRunTimeout caps one full inherit sequence (init + suite + home +
	// mysekai — ~20 sequential per-call-timeout game-API calls with sleeps between
	// them). The request ctx carries no deadline, so without this cap a degraded
	// game API can hang the request goroutine for the unbounded sum of every
	// per-call timeout (~300s worst case). It is set well above a healthy run
	// (~30-50s) so slow-but-successful inherits are not aborted, while still
	// bounding the pathological case.
	retrieverRunTimeout = 90 * time.Second

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
