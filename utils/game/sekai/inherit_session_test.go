package sekai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/redact"
)

const (
	testUserID          int64 = 164337024457871363
	testInheritID             = "FAKEinheritID002"
	testInheritPassword       = "FAKEpassword0002"
	testCredential            = "FAKE-credential-0002"
	testNewInheritID          = "NEWinheritID0003"
)

var testCode = InheritCode{InheritID: testInheritID, Password: testInheritPassword}

// fakeGame is a minimal game server. Handlers may be overridden per path.
type fakeGame struct {
	t        *testing.T
	mu       sync.Mutex
	events   []string
	override map[string]http.HandlerFunc
	bodies   map[string]map[string]any
}

func newFakeGame(t *testing.T) (*fakeGame, *httptest.Server) {
	t.Helper()
	g := &fakeGame{t: t, override: map[string]http.HandlerFunc{}, bodies: map[string]map[string]any{}}
	srv := httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(srv.Close)
	return g, srv
}

func (g *fakeGame) add(e string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.events = append(g.events, e)
}

func (g *fakeGame) snapshot() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.events...)
}

func (g *fakeGame) serve(w http.ResponseWriter, r *http.Request) {
	e := r.Method + " " + r.URL.Path
	if v := r.URL.Query().Get("isExecuteInherit"); v != "" {
		e += "?isExecuteInherit=" + v
	}
	g.add(e)
	if h, ok := g.override[r.URL.Path]; ok {
		h(w, r)
		return
	}
	uid := strconv.FormatInt(testUserID, 10)
	switch {
	case r.URL.Path == "/signature":
		w.Header().Add("Set-Cookie", "CloudFront-Policy=p; Path=/; Secure, CloudFront-Signature=s; Path=/")
	case r.URL.Path == "/version":
		_, _ = w.Write([]byte(`{"appVersion":"1","appHash":"2","dataVersion":"3","assetVersion":"4"}`))
	case strings.HasPrefix(r.URL.Path, "/api/inherit/user/") && r.URL.Query().Get("isExecuteInherit") == "False":
		g.write(w, map[string]any{
			"afterUserGamedata": map[string]any{"userId": testUserID, "name": "Miku", "rank": int64(120)},
			"userEventDeviceTransferRestrict": map[string]any{
				"isRestrictDeviceTransfer": true, "restTransferCount": int64(1), "restrictEndAt": int64(1700000000000),
			},
		})
	case strings.HasPrefix(r.URL.Path, "/api/inherit/user/"):
		g.write(w, map[string]any{"credential": testCredential})
	case r.URL.Path == "/api/user/"+uid+"/auth":
		g.write(w, map[string]any{"sessionToken": "session-token"})
	case r.URL.Path == "/api/user/"+uid+"/inherit":
		g.mu.Lock()
		g.bodies[r.URL.Path] = g.read(r)
		g.mu.Unlock()
		g.write(w, map[string]any{"userInherit": map[string]any{"inheritId": testNewInheritID}})
	case r.URL.Path == "/api/suite/user/"+uid:
		g.write(w, map[string]any{"userId": testUserID})
	default:
		g.write(w, map[string]any{"ok": true})
	}
}

func (g *fakeGame) write(w http.ResponseWriter, payload map[string]any) {
	packed, err := testServerCryptor().Pack(payload, harukiUtils.SupportedDataUploadServerEN)
	if err != nil {
		g.t.Errorf("pack: %v", err)
		return
	}
	_, _ = w.Write(packed)
}

func (g *fakeGame) read(r *http.Request) map[string]any {
	buf := make([]byte, r.ContentLength)
	_, _ = r.Body.Read(buf)
	v, err := testServerCryptor().Unpack(buf, harukiUtils.SupportedDataUploadServerEN)
	if err != nil {
		g.t.Errorf("unpack request: %v", err)
		return nil
	}
	m, _ := v.(map[string]any)
	return m
}

func newTestSession(t *testing.T, server harukiUtils.SupportedInheritUploadServer, baseURL string, progress Progress) *InheritSession {
	t.Helper()
	s, err := NewInheritSession(InheritSessionConfig{
		Server:           server,
		Cryptor:          testServerCryptor(),
		API:              baseURL + "/api",
		VersionURL:       baseURL + "/version",
		CookieURL:        baseURL + "/signature",
		InheritJWTSecret: "secret",
		Progress:         progress,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// recordPauses logs every pause into g and returns at once; elapsed time is
// frozen so the remainder of a pause is always the configured value.
func recordPauses(t *testing.T, g *fakeGame) {
	t.Helper()
	origPause, origSince := pause, since
	pause = func(ctx context.Context, d time.Duration) error {
		g.add(fmt.Sprintf("pause %s", d))
		return ctx.Err()
	}
	since = func(time.Time) time.Duration { return 0 }
	t.Cleanup(func() { pause, since = origPause, origSince })
}

func runFullTransfer(t *testing.T, s *InheritSession) {
	t.Helper()
	ctx := context.Background()
	if err := s.Bootstrap(ctx); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if _, err := s.Preview(ctx, testCode); err != nil {
		t.Fatalf("Preview: %v", err)
	}
	credential, err := s.Execute(ctx, testCode)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := s.Login(ctx, s.UserID(), credential); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if _, err := s.IssueInheritCode(ctx, "newpass1234"); err != nil {
		t.Fatalf("IssueInheritCode: %v", err)
	}
	if _, err := s.FetchSuite(ctx); err != nil {
		t.Fatalf("FetchSuite: %v", err)
	}
}

// The transfer makes the same calls, in the same order, with the same pauses
// as the synchronous flow did; the only addition is the new-code request
// right after login.
func TestInheritSessionKeepsCallOrderAndDefaultPauses(t *testing.T) {
	g, srv := newFakeGame(t)
	recordPauses(t, g)
	runFullTransfer(t, newTestSession(t, ServerEN, srv.URL, nil))

	uid := strconv.FormatInt(testUserID, 10)
	want := []string{
		"GET /version",
		"POST /api/inherit/user/" + testInheritID + "?isExecuteInherit=False",
		"pause 1s",
		"POST /api/inherit/user/" + testInheritID + "?isExecuteInherit=True",
		"pause 2s",
		"PUT /api/user/" + uid + "/auth",
		"PUT /api/user/" + uid + "/inherit",
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
	if got := g.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("call sequence changed\n got  %q\n want %q", got, want)
	}
	if got := g.bodies["/api/user/"+uid+"/inherit"]["password"]; got != "newpass1234" {
		t.Fatalf("issue-code body password = %v", got)
	}
}

func TestInheritSessionUsesConfiguredPacing(t *testing.T) {
	g, srv := newFakeGame(t)
	recordPauses(t, g)
	s := newTestSession(t, ServerEN, srv.URL, nil)
	s.pacing = InheritPacing{AfterInheritCheck: 3 * time.Millisecond, BeforeLogin: 5 * time.Millisecond, SuiteFollowup: 7 * time.Millisecond}
	runFullTransfer(t, s)
	var pauses []string
	for _, e := range g.snapshot() {
		if strings.HasPrefix(e, "pause ") {
			pauses = append(pauses, e)
		}
	}
	want := []string{"pause 3ms", "pause 5ms", "pause 7ms", "pause 7ms", "pause 7ms"}
	if !reflect.DeepEqual(pauses, want) {
		t.Fatalf("pauses = %q, want %q", pauses, want)
	}
}

// A user who takes longer than the pause to confirm does not wait again.
func TestExecutePausesOnlyForTheRemainder(t *testing.T) {
	g, srv := newFakeGame(t)
	recordPauses(t, g)
	since = func(time.Time) time.Duration { return time.Minute }
	s := newTestSession(t, ServerEN, srv.URL, nil)
	if _, err := s.Preview(context.Background(), testCode); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Execute(context.Background(), testCode); err != nil {
		t.Fatal(err)
	}
	for _, e := range g.snapshot() {
		if e == "pause 1s" {
			t.Fatalf("waited the full pause after a slow confirmation: %q", g.snapshot())
		}
	}
}

func TestJPBootstrapSendsSignedCookies(t *testing.T) {
	g, srv := newFakeGame(t)
	var gotCookie string
	g.override["/api/suite/user/"+strconv.FormatInt(testUserID, 10)] = func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		g.write(w, map[string]any{"userId": testUserID})
	}
	recordPauses(t, g)
	s := newTestSession(t, ServerJP, srv.URL, nil)
	runFullTransfer(t, s)
	// resty's cookie jar adds its own copy here because the test serves the
	// signature and the API from one host; production uses two hosts.
	if !strings.HasPrefix(gotCookie, "CloudFront-Policy=p; CloudFront-Signature=s") {
		t.Fatalf("Cookie = %q", gotCookie)
	}
	if got := g.snapshot()[0]; got != "POST /signature" {
		t.Fatalf("first call = %q, want the signature request", got)
	}
}

func TestPreviewParsesAccountAndRestriction(t *testing.T) {
	_, srv := newFakeGame(t)
	s := newTestSession(t, ServerEN, srv.URL, nil)
	p, err := s.Preview(context.Background(), testCode)
	if err != nil {
		t.Fatal(err)
	}
	if p.UserID != testUserID || p.Name != "Miku" || p.Rank != 120 {
		t.Fatalf("preview = %+v", p)
	}
	if p.Restriction == nil || !p.Restriction.IsRestrictDeviceTransfer || p.Restriction.RestTransferCount != 1 || p.Restriction.RestrictEndAt != 1700000000000 {
		t.Fatalf("restriction = %+v", p.Restriction)
	}
}

// A used or expired code makes the game answer a bare 403.
func TestPreviewBare403IsAnAPIError(t *testing.T) {
	g, srv := newFakeGame(t)
	g.override["/api/inherit/user/"+testInheritID] = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}
	var events []ProgressEvent
	s := newTestSession(t, ServerEN, srv.URL, func(e ProgressEvent) { events = append(events, e) })
	_, err := s.Preview(context.Background(), testCode)
	if APIStatus(err) != http.StatusForbidden {
		t.Fatalf("err = %v, want APIError 403", err)
	}
	if strings.Contains(err.Error(), testInheritID) {
		t.Fatalf("error leaked the inherit ID: %v", err)
	}
	want := []ProgressEvent{{Step: SubStepInheritCheck, Status: ProgressRunning}, {Step: SubStepInheritCheck, Status: ProgressFailed, Note: "status 403"}}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %+v", events)
	}
}

func TestCallRedactsInheritIDOnTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
			}
		}
	}))
	defer srv.Close()
	s := newTestSession(t, ServerJP, srv.URL, nil)
	_, err := s.Preview(context.Background(), testCode)
	if err == nil {
		t.Fatal("expected a transport error")
	}
	if strings.Contains(err.Error(), testInheritID) || strings.Contains(err.Error(), testInheritPassword) {
		t.Fatalf("error leaked a secret: %v", err)
	}
	if APIStatus(err) != 0 {
		t.Fatalf("want APIError{StatusCode: 0}, got %v", err)
	}
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		t.Fatal("the transport *url.Error must stay reachable for classification")
	}
	if !strings.Contains(err.Error(), "/inherit/user/<redacted>?isExecuteInherit=False") {
		t.Fatalf("error lost the route context: %v", err)
	}
}

// After the inherit resolves the game user ID, errors carry a per-process
// fingerprint instead of it, and every secret the session saw is scrubbed.
func TestErrorsMaskGameUserIDAndSecrets(t *testing.T) {
	g, srv := newFakeGame(t)
	uid := strconv.FormatInt(testUserID, 10)
	g.override["/api/suite/user/"+uid] = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}
	recordPauses(t, g)
	s := newTestSession(t, ServerEN, srv.URL, nil)
	if err := s.Login(context.Background(), testUserID, testCredential); err != nil {
		t.Fatal(err)
	}
	_, err := s.FetchSuite(context.Background())
	if err == nil {
		t.Fatal("expected a suite error")
	}
	if strings.Contains(err.Error(), uid) {
		t.Fatalf("error leaked the game user ID: %v", err)
	}
	if !strings.Contains(err.Error(), "/suite/user/"+redact.Fingerprint(uid)) {
		t.Fatalf("error = %v, want the fingerprint", err)
	}
	var retrievalErr *DataRetrievalError
	if !errors.As(err, &retrievalErr) || retrievalErr.DataType != "suite" {
		t.Fatalf("err = %v, want suite DataRetrievalError", err)
	}
}

func TestFetchSuiteReportsSubStepsAndToleratesHomeRefreshFailure(t *testing.T) {
	g, srv := newFakeGame(t)
	uid := strconv.FormatInt(testUserID, 10)
	g.override["/api/user/"+uid+"/home/refresh"] = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}
	recordPauses(t, g)
	var steps []string
	s := newTestSession(t, ServerEN, srv.URL, func(e ProgressEvent) { steps = append(steps, e.Step+":"+string(e.Status)) })
	s.userID = testUserID
	suite, err := s.FetchSuite(context.Background())
	if err != nil || len(suite) == 0 {
		t.Fatalf("FetchSuite = %d bytes, %v", len(suite), err)
	}
	want := []string{
		"suite:running", "suite:done",
		"suite_followup:running", "suite_followup:done",
		"home_refresh:running", "home_refresh:warning",
		"home_refresh_final:running", "home_refresh_final:warning",
	}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("steps = %q", steps)
	}
}

// Cancelling during the pre-login pause ends Login at once; the auth call is
// never made.
func TestLoginStopsDuringBeforeLoginPause(t *testing.T) {
	g, srv := newFakeGame(t)
	s := newTestSession(t, ServerEN, srv.URL, nil)
	s.pacing.BeforeLogin = time.Hour
	s.executedAt = time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	err := s.Login(ctx, testUserID, testCredential)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Login = %v, want context.Canceled", err)
	}
	var authErr *AuthError
	if !errors.As(err, &authErr) || authErr.Step != "login" {
		t.Fatalf("Login = %v, want AuthError at login", err)
	}
	if len(g.snapshot()) != 0 {
		t.Fatalf("calls after cancellation: %q", g.snapshot())
	}
}

func TestSuiteFollowupStopsAtDeadline(t *testing.T) {
	g, srv := newFakeGame(t)
	s := newTestSession(t, ServerEN, srv.URL, nil)
	s.userID = testUserID
	s.pacing.SuiteFollowup = time.Hour
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	suite, err := s.FetchSuite(ctx)
	if suite != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("FetchSuite = %v, %v", suite, err)
	}
	if got := g.snapshot(); !reflect.DeepEqual(got, []string{"GET /api/suite/user/" + strconv.FormatInt(testUserID, 10)}) {
		t.Fatalf("calls after the suite request: %q", got)
	}
}

func TestFetchMysekaiStopsOnMaintenance(t *testing.T) {
	g, srv := newFakeGame(t)
	g.override["/api/module-maintenance/MYSEKAI"] = func(w http.ResponseWriter, r *http.Request) {
		g.write(w, map[string]any{"isOngoing": true})
	}
	s := newTestSession(t, ServerEN, srv.URL, nil)
	s.userID = testUserID
	if _, err := s.FetchMysekai(context.Background()); !IsMaintenanceError(err) {
		t.Fatalf("FetchMysekai = %v, want ErrMaintenance", err)
	}
}

func TestFetchMysekaiMakesFollowupCalls(t *testing.T) {
	g, srv := newFakeGame(t)
	s := newTestSession(t, ServerEN, srv.URL, nil)
	s.userID = testUserID
	if _, err := s.FetchMysekai(context.Background()); err != nil {
		t.Fatal(err)
	}
	uid := strconv.FormatInt(testUserID, 10)
	want := []string{
		"GET /api/module-maintenance/MYSEKAI",
		"GET /api/module-maintenance/MYSEKAI_ROOM",
		"POST /api/user/" + uid + "/mysekai",
		"POST /api/user/" + uid + "/mysekai/" + uid + "/room",
		"GET /api/user/" + uid + "/diarkis-auth",
	}
	if got := g.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %q", got)
	}
}

func TestVersionRetryStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancel()
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	s := newTestSession(t, ServerEN, srv.URL, nil)
	err := s.Bootstrap(ctx)
	var authErr *AuthError
	if !errors.Is(err, context.Canceled) || !errors.As(err, &authErr) || authErr.Step != "parseAppVersion" {
		t.Fatalf("Bootstrap = %v", err)
	}
}

func TestNewInheritSessionRejectsUnsupportedServer(t *testing.T) {
	if _, err := NewInheritSession(InheritSessionConfig{Server: "kr"}); !errors.Is(err, ErrInvalidServer) {
		t.Fatalf("err = %v, want ErrInvalidServer", err)
	}
	if _, err := NewInheritSessionConfig(harukiConfig.SekaiClientConfig{}, "", "kr", ServerCryptor{}, InheritPacing{}, nil); !errors.Is(err, ErrInvalidServer) {
		t.Fatalf("config err = %v, want ErrInvalidServer", err)
	}
}

func TestBuildCookieHeaderFromCombinedCloudFrontSetCookie(t *testing.T) {
	raw := "CloudFront-Policy=policy-value; Path=/; Secure; HttpOnly, CloudFront-Key-Pair-Id=key-pair-id; Path=/; Secure; HttpOnly, CloudFront-Signature=signature-value; Path=/; Secure; HttpOnly"
	want := "CloudFront-Policy=policy-value; CloudFront-Key-Pair-Id=key-pair-id; CloudFront-Signature=signature-value"
	if got := buildCookieHeader([]string{raw}); got != want {
		t.Fatalf("buildCookieHeader() = %q, want %q", got, want)
	}
	if got := extractCookiePairs("cookie-a=value-a; Path=/, Wed, cookie-b=value-b; Path=/"); !reflect.DeepEqual(got, []string{"cookie-a=value-a", "cookie-b=value-b"}) {
		t.Fatalf("extractCookiePairs() = %v", got)
	}
}
