package sekaiapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeSekaiAPI answers every profile request with the configured status and
// body, after an optional delay, and counts the requests it receives.
type fakeSekaiAPI struct {
	status atomic.Int64
	body   atomic.Value
	delay  atomic.Int64
	hits   atomic.Int64
}

func newFakeSekaiAPI(t *testing.T, status int, body string) (*fakeSekaiAPI, *HarukiSekaiAPIClient) {
	t.Helper()
	fake := &fakeSekaiAPI{}
	fake.set(status, body)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.hits.Add(1)
		if r.Header.Get("X-Haruki-Sekai-Token") != "token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if d := time.Duration(fake.delay.Load()); d > 0 {
			select {
			case <-time.After(d):
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(int(fake.status.Load()))
		_, _ = w.Write([]byte(fake.body.Load().(string)))
	}))
	t.Cleanup(srv.Close)
	return fake, NewHarukiSekaiAPIClient(srv.URL, "token")
}

func (f *fakeSekaiAPI) set(status int, body string) {
	f.status.Store(int64(status))
	f.body.Store(body)
}

func TestGetUserProfileClassifiesResponses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name                  string
		status                int
		body                  string
		wantErr               bool
		wantAvailable, wantOK bool
	}{
		{"ok", 200, `{"userProfile":{"word":"hi"}}`, false, true, true},
		{"not found status", 404, ``, true, true, false},
		{"not found error body", 200, `{"errorCode":"not_found","httpStatus":404}`, true, true, false},
		{"maintenance status", 503, ``, true, false, false},
		{"maintenance error body", 200, `{"errorCode":"maintenance","httpStatus":503}`, true, false, false},
		{"server error body", 200, `{"errorCode":"boom","httpStatus":502}`, true, false, false},
		{"client error body", 200, `{"errorCode":"bad","httpStatus":400}`, true, true, false},
		{"busy", 500, ``, true, false, false},
		{"unexpected status", 418, ``, true, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, client := newFakeSekaiAPI(t, tc.status, tc.body)
			result, body, err := client.GetUserProfile(context.Background(), "123", "jp")
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if result == nil || result.ServerAvailable != tc.wantAvailable || result.AccountExists != tc.wantOK {
				t.Fatalf("result = %#v", result)
			}
			if tc.wantOK && string(body) != tc.body {
				t.Fatalf("body = %q", body)
			}
		})
	}
}

func TestGetUserProfileRejectsUnknownServer(t *testing.T) {
	t.Parallel()

	fake, client := newFakeSekaiAPI(t, 200, `{}`)
	if result, _, err := client.GetUserProfile(context.Background(), "123", "xx"); err == nil || result != nil {
		t.Fatalf("unknown server = (%v, %v)", result, err)
	}
	if fake.hits.Load() != 0 {
		t.Fatal("an unknown server reached the API")
	}
}

func tripCount(t *testing.T, client *HarukiSekaiAPIClient, server string) {
	t.Helper()
	for range breakerFailureThreshold {
		if _, _, err := client.GetUserProfile(context.Background(), "123", server); err == nil {
			t.Fatal("expected the upstream failure to surface")
		}
	}
}

func TestBreakerOpensOnServerErrorsAndFailsFast(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"5xx status", 500, ``},
		{"maintenance", 503, ``},
		{"5xx error body", 200, `{"errorCode":"boom","httpStatus":500}`},
		{"unexpected 5xx", 504, ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake, client := newFakeSekaiAPI(t, tc.status, tc.body)
			tripCount(t, client, "jp")
			hits := fake.hits.Load()

			result, body, err := client.GetUserProfile(context.Background(), "123", "jp")
			if !errors.Is(err, ErrCircuitOpen) {
				t.Fatalf("err = %v, want ErrCircuitOpen", err)
			}
			var open *CircuitOpenError
			if !errors.As(err, &open) || open.Server != "jp" || open.RetryAfter < breakerRetryAfterFloor || !strings.Contains(open.Error(), "jp") {
				t.Fatalf("open error = %#v", open)
			}
			if result == nil || result.ServerAvailable || body != nil {
				t.Fatalf("open breaker result = %#v, body %q", result, body)
			}
			if fake.hits.Load() != hits {
				t.Fatal("an open breaker still called the API")
			}
			// Breakers are per server.
			fake.set(200, `{}`)
			if _, _, err := client.GetUserProfile(context.Background(), "123", "tw"); err != nil {
				t.Fatalf("tw failed while only jp is open: %v", err)
			}
		})
	}
}

// 404, a missing account and other 4xx answers prove the upstream is healthy.
func TestBreakerIgnoresClientErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"404", 404, ``},
		{"account missing body", 200, `{"errorCode":"not_found","httpStatus":404}`},
		{"403", 403, ``},
		{"429", 429, ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake, client := newFakeSekaiAPI(t, tc.status, tc.body)
			for range 2 * breakerFailureThreshold {
				_, _, _ = client.GetUserProfile(context.Background(), "123", "cn")
			}
			if _, _, err := client.GetUserProfile(context.Background(), "123", "cn"); errors.Is(err, ErrCircuitOpen) {
				t.Fatal("client errors opened the breaker")
			}
			if got := fake.hits.Load(); got != int64(2*breakerFailureThreshold+1) {
				t.Fatalf("hits = %d", got)
			}
		})
	}
}

func TestBreakerCountsTimeoutsButNotCancellation(t *testing.T) {
	t.Parallel()

	fake, client := newFakeSekaiAPI(t, 200, `{}`)
	fake.delay.Store(int64(time.Second))

	// Cancellation by the caller says nothing about the upstream.
	for range 2 * breakerFailureThreshold {
		ctx, cancel := context.WithCancel(context.Background())
		go func() { time.Sleep(10 * time.Millisecond); cancel() }()
		if _, _, err := client.GetUserProfile(ctx, "123", "en"); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	}
	// Deadlines do count: the upstream hung.
	for range breakerFailureThreshold {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		result, _, err := client.GetUserProfile(ctx, "123", "en")
		cancel()
		if result != nil || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("timeout = (%#v, %v), want a transport error", result, err)
		}
	}
	if _, _, err := client.GetUserProfile(context.Background(), "123", "en"); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("err after timeouts = %v, want ErrCircuitOpen", err)
	}
}

func TestBreakerHalfOpenProbeClosesOnSuccess(t *testing.T) {
	t.Parallel()

	fake, client := newFakeSekaiAPI(t, 500, ``)
	now := time.Unix(1_700_000_000, 0)
	client.breaker.SetClock(func() time.Time { return now })
	tripCount(t, client, "kr")
	now = now.Add(breakerCooldown + time.Second)
	fake.set(200, `{}`)
	if _, _, err := client.GetUserProfile(context.Background(), "123", "kr"); err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	if _, _, err := client.GetUserProfile(context.Background(), "123", "kr"); err != nil {
		t.Fatalf("breaker did not close after a good probe: %v", err)
	}
}

func TestOptionsDefaultsAndOverrides(t *testing.T) {
	t.Parallel()

	defaults := NewHarukiSekaiAPIClient("http://unused", "")
	if got := defaults.ProfileViewTimeout("jp"); got != DefaultProfileViewTimeout {
		t.Fatalf("default view timeout = %s", got)
	}
	if got := defaults.VerifyTimeout(); got != DefaultVerifyTimeout {
		t.Fatalf("default verify timeout = %s", got)
	}
	if got := defaults.ProfileCacheTTL(); got != DefaultProfileCacheTTL {
		t.Fatalf("default cache TTL = %s", got)
	}

	tuned := NewHarukiSekaiAPIClientWithOptions("http://unused", "", Options{
		ProfileViewTimeout:         3 * time.Second,
		ProfileViewTimeoutByServer: map[string]time.Duration{" JP ": 7 * time.Second, "tw": 2 * time.Second, "cn": 0},
		VerifyTimeout:              8 * time.Second,
		ProfileCacheTTL:            -1,
	})
	for server, want := range map[string]time.Duration{
		"jp": 7 * time.Second, // per-server override, normalized key
		"en": 3 * time.Second, // global value
		"cn": 3 * time.Second, // a zero override is ignored
		"tw": 5 * time.Second, // never below the TW floor
	} {
		if got := tuned.ProfileViewTimeout(server); got != want {
			t.Fatalf("ProfileViewTimeout(%s) = %s, want %s", server, got, want)
		}
	}
	if got := tuned.VerifyTimeout(); got != 8*time.Second {
		t.Fatalf("verify timeout = %s", got)
	}
	if got := tuned.ProfileCacheTTL(); got != 0 {
		t.Fatalf("a negative TTL must disable the cache, got %s", got)
	}
}
