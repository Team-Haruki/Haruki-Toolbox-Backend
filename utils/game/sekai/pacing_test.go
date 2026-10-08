package sekai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
)

// The defaults are the pacing the inherit flow has always used. Changing them
// is an owner decision (anti-cheat risk), so this test pins them.
func TestDefaultInheritPacingIsUnchanged(t *testing.T) {
	t.Parallel()
	want := InheritPacing{
		AfterInheritCheck: 1 * time.Second,
		BeforeLogin:       2 * time.Second,
		SuiteFollowup:     1 * time.Second,
	}
	if got := DefaultInheritPacing(); got != want {
		t.Fatalf("DefaultInheritPacing() = %+v, want %+v", got, want)
	}
	for _, cfg := range []harukiConfig.SekaiInheritPacingConfig{
		{},
		{AfterInheritCheckMS: -1, BeforeLoginMS: -5, SuiteFollowupMS: 0},
		harukiConfig.DefaultSekaiInheritPacingConfig(),
	} {
		if got := InheritPacingFromConfig(cfg); got != want {
			t.Fatalf("InheritPacingFromConfig(%+v) = %+v, want defaults %+v", cfg, got, want)
		}
	}
	client := NewSekaiClientWithConfig(ClientConfig{Server: EN})
	if client.pacing != want {
		t.Fatalf("client without Pacing uses %+v, want defaults", client.pacing)
	}
}

func TestInheritPacingFromConfigConvertsMilliseconds(t *testing.T) {
	t.Parallel()
	got := InheritPacingFromConfig(harukiConfig.SekaiInheritPacingConfig{
		AfterInheritCheckMS: 300, BeforeLoginMS: 450, SuiteFollowupMS: 1,
	})
	want := InheritPacing{AfterInheritCheck: 300 * time.Millisecond, BeforeLogin: 450 * time.Millisecond, SuiteFollowup: time.Millisecond}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestSleepContextWaitsTheFullDuration(t *testing.T) {
	t.Parallel()
	start := time.Now()
	if err := sleepContext(context.Background(), 20*time.Millisecond); err != nil {
		t.Fatalf("sleepContext = %v", err)
	}
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
		t.Fatalf("returned after %s, before the 20ms pause", elapsed)
	}
}

func TestSleepContextStopsWhenCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(10*time.Millisecond, cancel)
	start := time.Now()
	err := sleepContext(ctx, time.Hour)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("sleepContext = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}
}

func TestSleepContextStopsAtDeadline(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := sleepContext(ctx, time.Hour); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("sleepContext = %v, want context.DeadlineExceeded", err)
	}
}

func TestSleepContextZeroDurationReportsContextState(t *testing.T) {
	t.Parallel()
	if err := sleepContext(context.Background(), 0); err != nil {
		t.Fatalf("live context: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepContext(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context: %v", err)
	}
}

// eventLog records game API calls and pauses in the order they happen.
type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *eventLog) add(e string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
}

func (l *eventLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

// recordingServer wraps the retriever test server and logs every request as
// "METHOD path[?isExecuteInherit=…]".
func recordingServer(t *testing.T, log *eventLog, userID int64) *httptest.Server {
	t.Helper()
	inner := newRetrieverTestServer(t, userID, statusCodeOK, statusCodeOK)
	t.Cleanup(inner.Close)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e := r.Method + " " + r.URL.Path
		if v := r.URL.Query().Get("isExecuteInherit"); v != "" {
			e += "?isExecuteInherit=" + v
		}
		log.add(e)
		inner.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// withRecordedPauses replaces pause with one that logs the requested duration
// and returns at once (or with the context's error).
func withRecordedPauses(t *testing.T, log *eventLog) {
	t.Helper()
	original := pause
	pause = func(ctx context.Context, d time.Duration) error {
		log.add(fmt.Sprintf("pause %s", d))
		return ctx.Err()
	}
	t.Cleanup(func() { pause = original })
}

// cancelDuringPause makes the real, context-aware pause cancel ctx 20ms into
// any wait of exactly d, so the test exercises a wait interrupted midway.
func cancelDuringPause(t *testing.T, d time.Duration, cancel context.CancelFunc) {
	t.Helper()
	original := pause
	pause = func(ctx context.Context, wait time.Duration) error {
		if wait == d {
			time.AfterFunc(20*time.Millisecond, cancel)
		}
		return sleepContext(ctx, wait)
	}
	t.Cleanup(func() { pause = original })
}

// A full suite inherit with default pacing makes the same calls, in the same
// order, with the same pauses between them as before the pauses became
// configurable.
func TestRetrieverRunKeepsCallOrderAndDefaultPauses(t *testing.T) {
	log := &eventLog{}
	withRecordedPauses(t, log)

	userID := int64(164337024457871363)
	uid := userIDString(userID)
	srv := recordingServer(t, log, userID)

	retriever := newTestRetriever(srv.URL, userID)
	if _, err := retriever.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := []string{
		"GET /version",
		"POST /api/inherit/user/inherit-id?isExecuteInherit=False",
		"pause 1s",
		"POST /api/inherit/user/inherit-id?isExecuteInherit=True",
		"pause 2s",
		"PUT /api/user/" + uid + "/auth",
		"GET /api/suite/user/" + uid,
		"pause 1s",
		"GET /api/suite/user/" + uid,
		"pause 1s",
		"GET /api/system",
		"pause 1s",
		"GET /api/system",
		"GET /api/information",
		"PUT /api/user/" + uid + "/home/refresh",
		"GET /api/system",
		"GET /api/information",
		"PUT /api/user/" + uid + "/home/refresh",
	}
	if got := log.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("call sequence changed\n got  %q\n want %q", got, want)
	}
}

// Configured pacing reaches every pause.
func TestRetrieverUsesConfiguredPacing(t *testing.T) {
	log := &eventLog{}
	withRecordedPauses(t, log)

	userID := int64(164337024457871363)
	srv := recordingServer(t, log, userID)
	retriever := newTestRetriever(srv.URL, userID)
	retriever.client.pacing = InheritPacing{AfterInheritCheck: 3 * time.Millisecond, BeforeLogin: 5 * time.Millisecond, SuiteFollowup: 7 * time.Millisecond}
	if _, err := retriever.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var pauses []string
	for _, e := range log.snapshot() {
		if len(e) > 6 && e[:6] == "pause " {
			pauses = append(pauses, e)
		}
	}
	want := []string{"pause 3ms", "pause 5ms", "pause 7ms", "pause 7ms", "pause 7ms"}
	if !reflect.DeepEqual(pauses, want) {
		t.Fatalf("pauses = %q, want %q", pauses, want)
	}
}

// Cancelling the inherit during the 2s pre-login pause ends it at once: the
// real (context-aware) pause is used, and login is never called.
func TestRetrieverRunStopsDuringBeforeLoginPause(t *testing.T) {
	log := &eventLog{}
	userID := int64(164337024457871363)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := recordingServer(t, log, userID)

	retriever := newTestRetriever(srv.URL, userID)
	retriever.client.pacing.BeforeLogin = time.Hour
	cancelDuringPause(t, time.Hour, cancel)
	start := time.Now()
	result, err := retriever.Run(ctx)
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("Run took %s after cancellation", elapsed)
	}
	if result != nil {
		t.Fatalf("Run result = %#v, want nil", result)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want it to wrap context.Canceled", err)
	}
	var authErr *AuthError
	if !errors.As(err, &authErr) || authErr.Step != "login" {
		t.Fatalf("Run error = %v, want AuthError at login", err)
	}
	for _, e := range log.snapshot() {
		if e == "PUT /api/user/"+userIDString(userID)+"/auth" {
			t.Fatalf("login was called after cancellation: %q", log.snapshot())
		}
	}
}

// Cancelling during the pause after the checking inherit call stops before
// the executing call.
func TestClientInitStopsDuringAfterCheckPause(t *testing.T) {
	log := &eventLog{}
	userID := int64(164337024457871363)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := recordingServer(t, log, userID)
	retriever := newTestRetriever(srv.URL, userID)
	retriever.client.pacing.AfterInheritCheck = time.Hour
	cancelDuringPause(t, time.Hour, cancel)
	err := retriever.client.Init(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Init error = %v, want context.Canceled", err)
	}
	var authErr *AuthError
	if !errors.As(err, &authErr) || authErr.Step != "inherit" {
		t.Fatalf("Init error = %v, want AuthError at inherit", err)
	}
	for _, e := range log.snapshot() {
		if e == "POST /api/inherit/user/inherit-id?isExecuteInherit=True" {
			t.Fatalf("executing inherit call was made after cancellation: %q", log.snapshot())
		}
	}
}

// A deadline reached during the suite follow-up pauses ends the suite
// retrieval with an error instead of carrying on with the remaining calls.
func TestRetrieveSuiteStopsDuringFollowupPause(t *testing.T) {
	log := &eventLog{}
	userID := int64(164337024457871363)
	srv := recordingServer(t, log, userID)
	retriever := newTestRetriever(srv.URL, userID)
	retriever.client.userID = userID
	retriever.client.pacing.SuiteFollowup = time.Hour

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	suite, err := retriever.RetrieveSuite(ctx)
	if suite != nil {
		t.Fatal("RetrieveSuite returned a body after the deadline")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RetrieveSuite error = %v, want context.DeadlineExceeded", err)
	}
	var retrievalErr *DataRetrievalError
	if !errors.As(err, &retrievalErr) || retrievalErr.DataType != "suite" || retrievalErr.Step != "pause" {
		t.Fatalf("RetrieveSuite error = %v, want suite DataRetrievalError at pause", err)
	}
	if got := log.snapshot(); !reflect.DeepEqual(got, []string{"GET /api/suite/user/" + userIDString(userID)}) {
		t.Fatalf("calls after the suite request: %q", got)
	}
}

// The version-request retry backoff honours cancellation too.
func TestParseAppVersionStopsDuringRetryBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancel()
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := NewSekaiClientWithConfig(ClientConfig{Server: EN, VersionURL: srv.URL + "/version"})
	err := client.parseAppVersion(ctx, 3)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("parseAppVersion error = %v, want context.Canceled", err)
	}
	var authErr *AuthError
	if !errors.As(err, &authErr) || authErr.Step != "parseAppVersion" {
		t.Fatalf("parseAppVersion error = %v, want AuthError at parseAppVersion", err)
	}
}

func TestRetryBackoffIsUnchanged(t *testing.T) {
	t.Parallel()
	for attempt, want := range map[int]time.Duration{1: 500 * time.Millisecond, 2: time.Second, 3: 1500 * time.Millisecond} {
		if got := retryBackoff(attempt); got != want {
			t.Fatalf("retryBackoff(%d) = %s, want %s", attempt, got, want)
		}
	}
}

// The retriever takes the injected pacing; fields left unset keep their
// defaults, so a caller that forgets to wire pacing still paces like production.
func TestNewSekaiDataRetrieverUsesInjectedPacing(t *testing.T) {
	t.Parallel()
	r := NewSekaiDataRetriever(EN, harukiUtils.InheritInformation{}, harukiUtils.UploadDataTypeSuite, testServerCryptor(),
		InheritPacing{AfterInheritCheck: 1500 * time.Millisecond})
	if r.client == nil {
		t.Fatalf("retriever has no client: %s", r.ErrorMessage)
	}
	want := InheritPacing{AfterInheritCheck: 1500 * time.Millisecond, BeforeLogin: 2 * time.Second, SuiteFollowup: time.Second}
	if r.client.pacing != want {
		t.Fatalf("pacing = %+v, want %+v", r.client.pacing, want)
	}

	unset := NewSekaiDataRetriever(EN, harukiUtils.InheritInformation{}, harukiUtils.UploadDataTypeSuite, testServerCryptor(), InheritPacing{})
	if unset.client == nil || unset.client.pacing != DefaultInheritPacing() {
		t.Fatalf("unwired pacing = %+v, want defaults", unset.client.pacing)
	}
}
