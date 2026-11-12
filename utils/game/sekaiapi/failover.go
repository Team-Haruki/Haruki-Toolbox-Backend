package sekaiapi

import (
	"errors"
	"io"
	"net"
	"sync"
	"syscall"
	"time"

	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/go-resty/resty/v2"
)

// The backup endpoint is a standby entry node that forwards every call to the
// primary while the primary is alive, so both answer the same. It only helps
// when there is no route to the primary, which is why the client fails over
// only on connection-level failures (see primaryUnreachable): any HTTP answer
// from the primary, including 5xx and 503 (game maintenance), is final, and so
// is a slow primary. The primary attempt keeps the caller's full deadline.
const (
	// primaryDownWindow is how long the primary is skipped after it was found
	// unreachable, before it is tried again.
	primaryDownWindow = 30 * time.Second
	// failoverDialTimeout bounds connection setup when a backup is configured.
	// It is what detects a dark primary: a new connection to a host that drops
	// packets fails after 2 s, leaving the rest of the caller's deadline to the
	// backup. Healthy connects to an entry node take well under 100 ms.
	failoverDialTimeout = 2 * time.Second
)

// primaryHealth is the passive health state of the primary endpoint. It is
// shared by all servers: it describes the entry node, not a game server.
type primaryHealth struct {
	mu      sync.Mutex
	down    bool
	retryAt time.Time
	window  time.Duration
	now     func() time.Time
}

func newPrimaryHealth() *primaryHealth {
	return &primaryHealth{window: primaryDownWindow, now: time.Now}
}

// shouldTry reports whether the next call should go to the primary: it is up,
// or its down window has passed.
func (h *primaryHealth) shouldTry() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return !h.down || !h.now().Before(h.retryAt)
}

// markDown records a transport failure of the primary and skips it for the
// down window. Only the transition from up to down is logged.
func (h *primaryHealth) markDown(cause error) {
	h.mu.Lock()
	wasDown := h.down
	h.down = true
	h.retryAt = h.now().Add(h.window)
	window := h.window
	h.mu.Unlock()
	if wasDown {
		harukiLogger.Debugf("Sekai API primary endpoint still unreachable, staying on the backup for %s: %v", window, cause)
		return
	}
	harukiLogger.Warnf("Sekai API primary endpoint unreachable, using the backup endpoint for %s: %v", window, cause)
}

// markUp records that the primary answered (any HTTP status) and clears the
// down state.
func (h *primaryHealth) markUp() {
	h.mu.Lock()
	wasDown := h.down
	h.down = false
	h.retryAt = time.Time{}
	h.mu.Unlock()
	if wasDown {
		harukiLogger.Infof("Sekai API primary endpoint reachable again, leaving the backup endpoint")
	}
}

// primaryUnreachable reports whether a primary request that got no usable
// answer failed because there is no route to the primary: a dial error
// (refused, unreachable, connect timeout), a connection reset, or EOF before a
// response. Those, and only those, mark the primary down and fail over.
//
// Anything else is final for the call, as it was without a backup: the caller's
// deadline or cancellation (checked by the caller before this), the client's
// 15 s ceiling, and any other read timeout on an established connection. A
// slow primary is a live one, and the standby would only forward to it.
//
// Accepted edge: a request written to an already-pooled connection of a host
// that died silently gets no reset and no EOF, so it hangs until the caller's
// deadline and is lost without failing over. The next request dials, hits
// failoverDialTimeout and fails over.
func primaryUnreachable(err error) bool {
	if err == nil {
		return false
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return true
	}
	return errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF)
}

// useFailoverDialTimeout shortens the connect timeout of the client's transport
// (resty's default transport otherwise; every other setting is kept) and
// returns the dialer it installed.
func useFailoverDialTimeout(client *resty.Client) *net.Dialer {
	transport, err := client.Transport()
	if err != nil {
		return nil
	}
	dialer := &net.Dialer{Timeout: failoverDialTimeout, KeepAlive: 30 * time.Second}
	transport.DialContext = dialer.DialContext
	return dialer
}
