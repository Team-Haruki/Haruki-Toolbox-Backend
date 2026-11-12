package sekaiapi

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// closedURL returns the address of a server that is no longer listening, so
// every request to it is refused.
func closedURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(nil)
	url := srv.URL
	srv.Close()
	return url
}

// newFailoverClient builds a client with a backup endpoint and a frozen clock
// for the primary's down window; advance moves that clock.
func newFailoverClient(t *testing.T, primaryURL, backupURL string) (client *HarukiSekaiAPIClient, advance func(time.Duration)) {
	t.Helper()
	client = NewHarukiSekaiAPIClientWithOptions(primaryURL, "token", Options{BackupEndpoint: backupURL})
	now := time.Unix(1_700_000_000, 0)
	client.primary.now = func() time.Time { return now }
	return client, func(d time.Duration) { now = now.Add(d) }
}

func primaryIsDown(client *HarukiSekaiAPIClient) bool {
	client.primary.mu.Lock()
	defer client.primary.mu.Unlock()
	return client.primary.down
}

func mustGetProfile(t *testing.T, client *HarukiSekaiAPIClient, server, wantBody string) {
	t.Helper()
	result, body, err := client.GetUserProfile(context.Background(), "123", server)
	if err != nil || result == nil || !result.AccountExists || string(body) != wantBody {
		t.Fatalf("GetUserProfile = (%#v, %q, %v), want body %q", result, body, err, wantBody)
	}
}

func TestBackupUntouchedWhilePrimaryHealthy(t *testing.T) {
	t.Parallel()

	primary := newFakeSekaiAPIServer(t, 200, `{"from":"primary"}`)
	backup := newFakeSekaiAPIServer(t, 200, `{"from":"backup"}`)
	client, _ := newFailoverClient(t, primary.url, backup.url)

	for range 5 {
		mustGetProfile(t, client, "jp", `{"from":"primary"}`)
	}
	// A 404 is an answer too.
	primary.set(404, ``)
	if result, _, err := client.GetUserProfile(context.Background(), "123", "jp"); err == nil || result == nil || !result.ServerAvailable {
		t.Fatalf("404 = (%#v, %v)", result, err)
	}
	if primary.hits.Load() != 6 || backup.hits.Load() != 0 {
		t.Fatalf("hits primary=%d backup=%d, want 6 and 0", primary.hits.Load(), backup.hits.Load())
	}
	if primaryIsDown(client) {
		t.Fatal("a healthy primary was marked down")
	}
}

func TestTransportFailureFailsOverWithinOneCall(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		primary func(t *testing.T) string
	}{
		{"connection refused", closedURL},
		{"connection closed without answer", func(t *testing.T) string {
			fake := newFakeSekaiAPIServer(t, 200, `{}`)
			fake.broken.Store(true)
			return fake.url
		}},
		{"connection reset", func(t *testing.T) string {
			fake := newFakeSekaiAPIServer(t, 200, `{}`)
			fake.broken.Store(true)
			fake.reset.Store(true)
			return fake.url
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			backup := newFakeSekaiAPIServer(t, 200, `{"from":"backup"}`)
			client, _ := newFailoverClient(t, tc.primary(t), backup.url)
			// No down window: every call meets the dead primary first, so each
			// one fails over. None of them may count against the breaker.
			client.primary.window = 0

			for range 2 * breakerFailureThreshold {
				mustGetProfile(t, client, "jp", `{"from":"backup"}`)
			}
			if got := backup.hits.Load(); got != int64(2*breakerFailureThreshold) {
				t.Fatalf("backup hits = %d", got)
			}
			if !primaryIsDown(client) {
				t.Fatal("the primary was not marked down")
			}
		})
	}
}

func TestHTTPErrorsFromPrimaryDoNotFailOver(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"503 maintenance", 503, ``},
		{"503 error body", 200, `{"errorCode":"maintenance","httpStatus":503}`},
		{"500", 500, ``},
		{"502", 502, ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			primary := newFakeSekaiAPIServer(t, tc.status, tc.body)
			backup := newFakeSekaiAPIServer(t, 200, `{"from":"backup"}`)
			client, _ := newFailoverClient(t, primary.url, backup.url)

			result, body, err := client.GetUserProfile(context.Background(), "123", "jp")
			if err == nil || result == nil || result.ServerAvailable || body != nil {
				t.Fatalf("GetUserProfile = (%#v, %q, %v), want the primary's error", result, body, err)
			}
			if backup.hits.Load() != 0 {
				t.Fatal("an HTTP error from the primary failed over")
			}
			if primaryIsDown(client) {
				t.Fatal("an HTTP error marked the primary down")
			}
			// The breaker still counts the 5xx exactly as before.
			tripCount(t, client, "tw")
			if _, _, err := client.GetUserProfile(context.Background(), "123", "tw"); !errors.Is(err, ErrCircuitOpen) {
				t.Fatalf("err = %v, want ErrCircuitOpen", err)
			}
			if backup.hits.Load() != 0 {
				t.Fatal("the backup was called")
			}
		})
	}
}

func TestPrimarySkippedWhileDownAndRetriedAfterWindow(t *testing.T) {
	t.Parallel()

	primary := newFakeSekaiAPIServer(t, 200, `{"from":"primary"}`)
	backup := newFakeSekaiAPIServer(t, 200, `{"from":"backup"}`)
	client, advance := newFailoverClient(t, primary.url, backup.url)

	primary.broken.Store(true)
	mustGetProfile(t, client, "jp", `{"from":"backup"}`)
	if !primaryIsDown(client) || primary.hits.Load() == 0 {
		t.Fatalf("down=%v primary hits=%d", primaryIsDown(client), primary.hits.Load())
	}

	// Inside the window the primary is not contacted, even once it is back.
	primary.broken.Store(false)
	primaryHits := primary.hits.Load()
	advance(primaryDownWindow - time.Second)
	for range 3 {
		mustGetProfile(t, client, "jp", `{"from":"backup"}`)
	}
	if primary.hits.Load() != primaryHits || backup.hits.Load() != 4 {
		t.Fatalf("hits primary=%d (was %d) backup=%d, want the backup only", primary.hits.Load(), primaryHits, backup.hits.Load())
	}

	// After the window it is tried again; still broken, it is skipped again.
	primary.broken.Store(true)
	advance(time.Second)
	mustGetProfile(t, client, "jp", `{"from":"backup"}`)
	if primary.hits.Load() == primaryHits || backup.hits.Load() != 5 || !primaryIsDown(client) {
		t.Fatalf("retry after window: primary hits=%d backup=%d down=%v", primary.hits.Load(), backup.hits.Load(), primaryIsDown(client))
	}
	primaryHits = primary.hits.Load()
	mustGetProfile(t, client, "jp", `{"from":"backup"}`)
	if primary.hits.Load() != primaryHits {
		t.Fatal("a failed retry did not restart the window")
	}

	// Recovered: a success clears the down state and the backup goes idle.
	primary.broken.Store(false)
	advance(primaryDownWindow)
	mustGetProfile(t, client, "jp", `{"from":"primary"}`)
	if primaryIsDown(client) {
		t.Fatal("a successful retry did not clear the down state")
	}
	backupHits := backup.hits.Load()
	mustGetProfile(t, client, "jp", `{"from":"primary"}`)
	if backup.hits.Load() != backupHits {
		t.Fatal("the backup was called after the primary recovered")
	}
}

// An HTTP error answer from the primary after the window also proves it is
// reachable.
func TestPrimaryRecoversOnAnyHTTPAnswer(t *testing.T) {
	t.Parallel()

	primary := newFakeSekaiAPIServer(t, 503, ``)
	backup := newFakeSekaiAPIServer(t, 200, `{}`)
	client, advance := newFailoverClient(t, primary.url, backup.url)
	primary.broken.Store(true)
	mustGetProfile(t, client, "jp", `{}`)

	primary.broken.Store(false)
	advance(primaryDownWindow)
	if _, _, err := client.GetUserProfile(context.Background(), "123", "jp"); err == nil {
		t.Fatal("want the primary's 503")
	}
	if primaryIsDown(client) || backup.hits.Load() != 1 {
		t.Fatalf("down=%v backup hits=%d", primaryIsDown(client), backup.hits.Load())
	}
}

func TestBothEndpointsDown(t *testing.T) {
	t.Parallel()

	primary := newFakeSekaiAPIServer(t, 200, `{}`)
	backup := newFakeSekaiAPIServer(t, 200, `{}`)
	primary.broken.Store(true)
	backup.broken.Store(true)
	client, advance := newFailoverClient(t, primary.url, backup.url)

	// Every call still sends requests: the primary first, then the backup.
	result, _, err := client.GetUserProfile(context.Background(), "123", "jp")
	if result != nil || err == nil || errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("both down = (%#v, %v), want a transport error", result, err)
	}
	if primary.hits.Load() == 0 || backup.hits.Load() == 0 || !primaryIsDown(client) {
		t.Fatalf("hits primary=%d backup=%d down=%v", primary.hits.Load(), backup.hits.Load(), primaryIsDown(client))
	}
	// While down, only the backup; after the window the primary first again.
	primaryHits, backupHits := primary.hits.Load(), backup.hits.Load()
	if _, _, err := client.GetUserProfile(context.Background(), "123", "jp"); err == nil {
		t.Fatal("want a transport error")
	}
	if primary.hits.Load() != primaryHits || backup.hits.Load() == backupHits {
		t.Fatalf("while down: primary hits=%d (was %d) backup=%d (was %d)", primary.hits.Load(), primaryHits, backup.hits.Load(), backupHits)
	}
	advance(primaryDownWindow)
	if _, _, err := client.GetUserProfile(context.Background(), "123", "jp"); err == nil {
		t.Fatal("want a transport error")
	}
	if primary.hits.Load() == primaryHits {
		t.Fatal("the primary was not retried after the window")
	}

	// Calls that failed as a whole count toward the breaker.
	tripCount(t, client, "en")
	if _, _, err := client.GetUserProfile(context.Background(), "123", "en"); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("err = %v, want ErrCircuitOpen", err)
	}
}

// A primary that accepts the connection and answers slowly keeps the caller's
// full deadline: well past the old 3/4 share here.
func TestSlowPrimaryWithinDeadlineSucceeds(t *testing.T) {
	t.Parallel()

	const deadline = 2 * time.Second
	primary := newFakeSekaiAPIServer(t, 200, `{"from":"primary"}`)
	primary.delay.Store(int64(deadline * 85 / 100))
	backup := newFakeSekaiAPIServer(t, 200, `{"from":"backup"}`)
	client, _ := newFailoverClient(t, primary.url, backup.url)

	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	result, body, err := client.GetUserProfile(ctx, "123", "jp")
	if err != nil || result == nil || string(body) != `{"from":"primary"}` {
		t.Fatalf("GetUserProfile = (%#v, %q, %v), want the primary's answer", result, body, err)
	}
	if backup.hits.Load() != 0 || primaryIsDown(client) {
		t.Fatalf("backup hits=%d down=%v for a slow but live primary", backup.hits.Load(), primaryIsDown(client))
	}
}

// A primary that hangs past the caller's deadline fails the call as it did
// without a backup: no failover, not marked down.
func TestPrimaryPastCallerDeadlineDoesNotFailOver(t *testing.T) {
	t.Parallel()

	primary := newFakeSekaiAPIServer(t, 200, `{}`)
	primary.delay.Store(int64(5 * time.Second))
	backup := newFakeSekaiAPIServer(t, 200, `{}`)
	client, _ := newFailoverClient(t, primary.url, backup.url)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	result, _, err := client.GetUserProfile(ctx, "123", "jp")
	if result != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("GetUserProfile = (%#v, %v), want a deadline error", result, err)
	}
	if backup.hits.Load() != 0 || primaryIsDown(client) {
		t.Fatalf("backup hits=%d down=%v after the caller's deadline", backup.hits.Load(), primaryIsDown(client))
	}
}

// A dark primary (connect never completes) hits the connect timeout and the
// call fails over within the caller's deadline.
func TestDarkPrimaryConnectTimeoutFailsOver(t *testing.T) {
	t.Parallel()

	primary := newFakeSekaiAPIServer(t, 200, `{"from":"primary"}`)
	backup := newFakeSekaiAPIServer(t, 200, `{"from":"backup"}`)
	client, _ := newFailoverClient(t, primary.url, backup.url)
	if client.dialer == nil || client.dialer.Timeout != failoverDialTimeout {
		t.Fatalf("failover dialer = %#v, want a %s connect timeout", client.dialer, failoverDialTimeout)
	}
	// Black-hole connects to the primary with the real dialer: the connect
	// blocks until the dial timeout expires, as with dropped SYNs.
	primaryAddr := strings.TrimPrefix(primary.url, "http://")
	const dialTimeout = 150 * time.Millisecond
	client.dialer.Timeout = dialTimeout
	client.dialer.ControlContext = func(ctx context.Context, _, address string, _ syscall.RawConn) error {
		if address == primaryAddr {
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}

	const deadline = 2 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	start := time.Now()
	result, body, err := client.GetUserProfile(ctx, "123", "jp")
	elapsed := time.Since(start)
	if err != nil || result == nil || string(body) != `{"from":"backup"}` {
		t.Fatalf("GetUserProfile = (%#v, %q, %v) after %s", result, body, err, elapsed)
	}
	if elapsed < dialTimeout || elapsed >= deadline {
		t.Fatalf("elapsed %s, want just over the %s connect timeout", elapsed, dialTimeout)
	}
	if primary.hits.Load() != 0 || !primaryIsDown(client) {
		t.Fatalf("primary hits=%d down=%v", primary.hits.Load(), primaryIsDown(client))
	}
}

func TestPrimaryUnreachableClassification(t *testing.T) {
	t.Parallel()

	wrap := func(err error) error { return &url.Error{Op: "Get", URL: "http://primary/api", Err: err} }
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"connection refused", wrap(&net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}), true},
		{"connect timeout", wrap(&net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded}), true},
		{"no route", wrap(&net.OpError{Op: "dial", Net: "tcp", Err: syscall.EHOSTUNREACH}), true},
		{"connection reset", wrap(&net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("read", syscall.ECONNRESET)}), true},
		{"EOF before response", wrap(io.EOF), true},
		{"unexpected EOF", wrap(io.ErrUnexpectedEOF), true},
		{"caller deadline", wrap(context.DeadlineExceeded), false},
		{"caller cancellation", wrap(context.Canceled), false},
		{"read timeout", wrap(&net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}), false},
		{"client ceiling", wrap(errors.New("net/http: request canceled (Client.Timeout exceeded while awaiting headers)")), false},
		{"nil", nil, false},
	} {
		if got := primaryUnreachable(tc.err); got != tc.want {
			t.Errorf("%s: primaryUnreachable = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCallerCancellationDoesNotMarkPrimaryDown(t *testing.T) {
	t.Parallel()

	primary := newFakeSekaiAPIServer(t, 200, `{}`)
	primary.delay.Store(int64(5 * time.Second))
	backup := newFakeSekaiAPIServer(t, 200, `{}`)
	client, _ := newFailoverClient(t, primary.url, backup.url)

	for range 2 * breakerFailureThreshold {
		ctx, cancel := context.WithCancel(context.Background())
		go func() { time.Sleep(20 * time.Millisecond); cancel() }()
		if _, _, err := client.GetUserProfile(ctx, "123", "jp"); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	}
	if primaryIsDown(client) || backup.hits.Load() != 0 {
		t.Fatalf("down=%v backup hits=%d after caller cancellations", primaryIsDown(client), backup.hits.Load())
	}
	// Nor do cancellations count toward the breaker.
	primary.delay.Store(0)
	if _, _, err := client.GetUserProfile(context.Background(), "123", "jp"); err != nil {
		t.Fatalf("after cancellations: %v", err)
	}
}

// Without a backup a transport failure behaves as it always did: one request,
// a transport error, no health state.
func TestNoBackupKeepsSingleEndpointBehaviour(t *testing.T) {
	t.Parallel()

	for _, backup := range []string{"", "   "} {
		client := NewHarukiSekaiAPIClientWithOptions(closedURL(t), "token", Options{BackupEndpoint: backup})
		if client.options.BackupEndpoint != "" {
			t.Fatalf("BackupEndpoint %q not normalized to empty", backup)
		}
		result, body, err := client.GetUserProfile(context.Background(), "123", "jp")
		if result != nil || body != nil || err == nil {
			t.Fatalf("GetUserProfile = (%#v, %q, %v), want a transport error", result, body, err)
		}
		if primaryIsDown(client) || client.dialer != nil {
			t.Fatal("failover state was set up without a backup")
		}
		tripCount(t, client, "tw")
		if _, _, err := client.GetUserProfile(context.Background(), "123", "tw"); !errors.Is(err, ErrCircuitOpen) {
			t.Fatalf("err = %v, want ErrCircuitOpen", err)
		}
	}
}

func TestBackupEndpointUsesSameToken(t *testing.T) {
	t.Parallel()

	// The fake answers 401 to any other token, which would surface as an error.
	backup := newFakeSekaiAPIServer(t, 200, `{}`)
	client, _ := newFailoverClient(t, closedURL(t), " "+backup.url+" ")
	mustGetProfile(t, client, "kr", `{}`)
}
