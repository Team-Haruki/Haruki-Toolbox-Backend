package sekai

import (
	"context"
	"errors"
	"math"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
)

func hangUp(w http.ResponseWriter, _ *http.Request) {
	if hj, ok := w.(http.Hijacker); ok {
		if conn, _, err := hj.Hijack(); err == nil {
			_ = conn.Close()
		}
	}
}

func statusHandler(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
}

func rawHandler(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }
}

func countCalls(g *fakeGame, call string) int {
	n := 0
	for _, e := range g.snapshot() {
		if e == call {
			n++
		}
	}
	return n
}

func authStep(err error) string {
	var authErr *AuthError
	if errors.As(err, &authErr) {
		return authErr.Step
	}
	return ""
}

// The signature request is retried once after a pause; a second failure ends
// the bootstrap before the version request.
func TestBootstrapRetriesTheSignatureThenFails(t *testing.T) {
	g, srv := newFakeGame(t)
	g.override["/signature"] = statusHandler(http.StatusBadGateway)
	recordPauses(t, g)
	var steps []string
	s := newTestSession(t, ServerJP, srv.URL, func(e ProgressEvent) { steps = append(steps, e.Step+":"+string(e.Status)+":"+e.Note) })
	err := s.Bootstrap(context.Background())
	if authStep(err) != "getCookies" || APIStatus(err) != http.StatusBadGateway {
		t.Fatalf("Bootstrap = %v, want getCookies AuthError wrapping status 502", err)
	}
	want := []string{"POST /signature", "pause 500ms", "POST /signature"}
	if got := g.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(steps, []string{"cookies:running:", "cookies:failed:status 502"}) {
		t.Fatalf("steps = %q", steps)
	}
}

func TestBootstrapSignatureWithoutCookiesFails(t *testing.T) {
	g, srv := newFakeGame(t)
	g.override["/signature"] = statusHandler(http.StatusOK)
	recordPauses(t, g)
	s := newTestSession(t, ServerJP, srv.URL, nil)
	err := s.Bootstrap(context.Background())
	if authStep(err) != "getCookies" || !strings.Contains(err.Error(), "empty Set-Cookie header") {
		t.Fatalf("Bootstrap = %v, want an empty Set-Cookie failure", err)
	}
}

func TestBootstrapSignatureTransportErrorIsStatusZero(t *testing.T) {
	g, srv := newFakeGame(t)
	g.override["/signature"] = hangUp
	recordPauses(t, g)
	s := newTestSession(t, ServerJP, srv.URL, nil)
	err := s.Bootstrap(context.Background())
	if authStep(err) != "getCookies" {
		t.Fatalf("Bootstrap = %v, want getCookies AuthError", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 0 {
		t.Fatalf("Bootstrap = %v, want APIError{StatusCode: 0} for the circuit breaker", err)
	}
}

func TestBootstrapSignatureRetryStopsWhenCancelled(t *testing.T) {
	g, srv := newFakeGame(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g.override["/signature"] = func(w http.ResponseWriter, _ *http.Request) {
		cancel()
		w.WriteHeader(http.StatusInternalServerError)
	}
	s := newTestSession(t, ServerJP, srv.URL, nil)
	err := s.Bootstrap(ctx)
	if authStep(err) != "getCookies" || !errors.Is(err, context.Canceled) {
		t.Fatalf("Bootstrap = %v, want an interrupted getCookies", err)
	}
	if n := countCalls(g, "POST /signature"); n != 1 {
		t.Fatalf("signature calls = %d, want 1", n)
	}
}

// A bad status and an unparsable body each use up one version attempt.
func TestBootstrapVersionFailsAfterBadStatusAndBadBody(t *testing.T) {
	g, srv := newFakeGame(t)
	attempt := 0
	g.override["/version"] = func(w http.ResponseWriter, r *http.Request) {
		attempt++
		if attempt == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("not json"))
	}
	recordPauses(t, g)
	s := newTestSession(t, ServerEN, srv.URL, nil)
	err := s.Bootstrap(context.Background())
	if authStep(err) != "parseAppVersion" || !strings.Contains(err.Error(), "EN server: failed after 2 attempts") {
		t.Fatalf("Bootstrap = %v, want parseAppVersion after 2 attempts", err)
	}
	if _, ok := s.headers["X-App-Version"]; ok {
		t.Fatal("a failed bootstrap must not set version headers")
	}
}

func TestBootstrapVersionTransportErrorThenSuccess(t *testing.T) {
	g, srv := newFakeGame(t)
	attempt := 0
	g.override["/version"] = func(w http.ResponseWriter, r *http.Request) {
		attempt++
		if attempt == 1 {
			hangUp(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"appVersion":"5.0.0","appHash":"h","dataVersion":"d","assetVersion":"a"}`))
	}
	recordPauses(t, g)
	s := newTestSession(t, ServerEN, srv.URL, nil)
	if err := s.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.headers["X-App-Version"] != "5.0.0" || s.headers["X-Asset-Version"] != "a" {
		t.Fatalf("headers = %v", s.headers)
	}
}

func TestExecuteStopsDuringThePauseAfterPreview(t *testing.T) {
	g, srv := newFakeGame(t)
	s := newTestSession(t, ServerEN, srv.URL, nil)
	s.pacing.AfterInheritCheck = time.Hour
	s.previewedAt = time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	_, err := s.Execute(ctx, testCode)
	if authStep(err) != "inherit" || !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute = %v, want an interrupted inherit", err)
	}
	if len(g.snapshot()) != 0 {
		t.Fatalf("calls after cancellation: %q", g.snapshot())
	}
}

func TestExecuteFailures(t *testing.T) {
	path := "/api/inherit/user/" + testInheritID
	cases := map[string]struct {
		handler func(g *fakeGame) http.HandlerFunc
		check   func(t *testing.T, err error)
	}{
		"used code": {
			handler: func(*fakeGame) http.HandlerFunc { return statusHandler(http.StatusForbidden) },
			check: func(t *testing.T, err error) {
				if APIStatus(err) != http.StatusForbidden {
					t.Fatalf("err = %v, want 403", err)
				}
			},
		},
		"no credential": {
			handler: func(g *fakeGame) http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) { g.write(w, map[string]any{"other": "x"}) }
			},
			check: func(t *testing.T, err error) {
				if err == nil || !strings.Contains(err.Error(), "response has no credential") {
					t.Fatalf("err = %v, want a missing credential", err)
				}
			},
		},
		"garbled body": {
			handler: func(*fakeGame) http.HandlerFunc { return rawHandler("garbage") },
			check: func(t *testing.T, err error) {
				if err == nil {
					t.Fatal("expected an unpack error")
				}
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			g, srv := newFakeGame(t)
			g.override[path] = tc.handler(g)
			recordPauses(t, g)
			var steps []string
			s := newTestSession(t, ServerEN, srv.URL, func(e ProgressEvent) { steps = append(steps, e.Step+":"+string(e.Status)) })
			credential, err := s.Execute(context.Background(), testCode)
			if credential != "" {
				t.Fatalf("credential = %q on failure", credential)
			}
			tc.check(t, err)
			if strings.Contains(err.Error(), testInheritID) || strings.Contains(err.Error(), testInheritPassword) {
				t.Fatalf("error leaked a secret: %v", err)
			}
			if !reflect.DeepEqual(steps, []string{"inherit_execute:running", "inherit_execute:failed"}) {
				t.Fatalf("steps = %q", steps)
			}
			if !s.executedAt.IsZero() {
				t.Fatal("a failed Execute must not start the pre-login pause")
			}
		})
	}
}

func TestLoginRejectsMissingInput(t *testing.T) {
	g, srv := newFakeGame(t)
	s := newTestSession(t, ServerEN, srv.URL, nil)
	for _, tc := range []struct {
		uid        int64
		credential string
	}{{0, testCredential}, {testUserID, ""}} {
		if err := s.Login(context.Background(), tc.uid, tc.credential); authStep(err) != "login" {
			t.Fatalf("Login(%d, %q) = %v, want login AuthError", tc.uid, tc.credential, err)
		}
	}
	if len(g.snapshot()) != 0 {
		t.Fatalf("calls = %q, want none", g.snapshot())
	}
}

func TestLoginFailures(t *testing.T) {
	uid := strconv.FormatInt(testUserID, 10)
	cases := map[string]func(g *fakeGame) http.HandlerFunc{
		"server error": func(*fakeGame) http.HandlerFunc { return statusHandler(http.StatusInternalServerError) },
		"garbled body": func(*fakeGame) http.HandlerFunc { return rawHandler("garbage") },
		"no session token": func(g *fakeGame) http.HandlerFunc {
			return func(w http.ResponseWriter, _ *http.Request) { g.write(w, map[string]any{}) }
		},
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			g, srv := newFakeGame(t)
			g.override["/api/user/"+uid+"/auth"] = handler(g)
			s := newTestSession(t, ServerEN, srv.URL, nil)
			err := s.Login(context.Background(), testUserID, testCredential)
			if err == nil {
				t.Fatal("expected a login failure")
			}
			if strings.Contains(err.Error(), testCredential) || strings.Contains(err.Error(), uid) {
				t.Fatalf("error leaked the credential or the user ID: %v", err)
			}
			if _, ok := s.headers[headerSessionToken]; ok {
				t.Fatal("a failed login must not leave a session token")
			}
		})
	}
}

func TestIssueInheritCodeFailures(t *testing.T) {
	uid := strconv.FormatInt(testUserID, 10)
	cases := map[string]func(g *fakeGame) http.HandlerFunc{
		"server error": func(*fakeGame) http.HandlerFunc { return statusHandler(http.StatusInternalServerError) },
		"garbled body": func(*fakeGame) http.HandlerFunc { return rawHandler("garbage") },
		"no inherit id": func(g *fakeGame) http.HandlerFunc {
			return func(w http.ResponseWriter, _ *http.Request) {
				g.write(w, map[string]any{"userInherit": map[string]any{}})
			}
		},
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			g, srv := newFakeGame(t)
			g.override["/api/user/"+uid+"/inherit"] = handler(g)
			s := newTestSession(t, ServerEN, srv.URL, nil)
			s.userID = testUserID
			id, err := s.IssueInheritCode(context.Background(), "newpass1234")
			if id != "" || err == nil {
				t.Fatalf("IssueInheritCode = %q, %v, want a failure", id, err)
			}
			if strings.Contains(err.Error(), "newpass1234") {
				t.Fatalf("error leaked the new password: %v", err)
			}
		})
	}
}

// Response headers update the session: a rotated session token replaces the
// old one and a login bonus switches the home refresh to the login payload.
func TestCallKeepsRotatedTokenAndLoginBonus(t *testing.T) {
	g, srv := newFakeGame(t)
	uid := strconv.FormatInt(testUserID, 10)
	g.override["/api/suite/user/"+uid] = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(headerSessionToken, "rotated")
		w.Header().Set(headerLoginBonus, "true")
		g.write(w, map[string]any{"userFriends": []any{}})
	}
	var refreshBodies []map[string]any
	g.override["/api/user/"+uid+"/home/refresh"] = func(w http.ResponseWriter, r *http.Request) {
		refreshBodies = append(refreshBodies, g.read(r))
		g.write(w, map[string]any{})
	}
	recordPauses(t, g)
	s := newTestSession(t, ServerEN, srv.URL, nil)
	s.userID = testUserID
	if _, err := s.FetchSuite(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.headers[headerSessionToken] != "rotated" || !s.loginBonus {
		t.Fatalf("token = %q, loginBonus = %v", s.headers[headerSessionToken], s.loginBonus)
	}
	if n := countCalls(g, "GET /api/user/"+uid+"/invitation"); n != 1 {
		t.Fatalf("invitation calls = %d, want 1 (suite has userFriends)", n)
	}
	if len(refreshBodies) != 2 {
		t.Fatalf("home refreshes = %d, want 2", len(refreshBodies))
	}
	first, _ := refreshBodies[0]["refreshableTypes"].([]any)
	second, _ := refreshBodies[1]["refreshableTypes"].([]any)
	if !containsAny(first, "login_bonus") || containsAny(second, "login_bonus") {
		t.Fatalf("refresh payloads = %v / %v, want login_bonus only in the first", first, second)
	}
}

func containsAny(list []any, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func TestFetchSuiteGarbledBodyIsAnUnpackError(t *testing.T) {
	g, srv := newFakeGame(t)
	uid := strconv.FormatInt(testUserID, 10)
	g.override["/api/suite/user/"+uid] = rawHandler("garbage")
	recordPauses(t, g)
	s := newTestSession(t, ServerEN, srv.URL, nil)
	s.userID = testUserID
	_, err := s.FetchSuite(context.Background())
	var retrievalErr *DataRetrievalError
	if !errors.As(err, &retrievalErr) || retrievalErr.DataType != "suite" || retrievalErr.Step != "unpack" {
		t.Fatalf("FetchSuite = %v, want a suite unpack DataRetrievalError", err)
	}
}

// Cancelling during the second follow-up pause stops the step.
func TestSuiteFollowupStopsAtTheSecondPause(t *testing.T) {
	g, srv := newFakeGame(t)
	origPause := pause
	t.Cleanup(func() { pause = origPause })
	pauses := 0
	pause = func(ctx context.Context, d time.Duration) error {
		pauses++
		if pauses == 2 {
			return context.Canceled
		}
		return nil
	}
	s := newTestSession(t, ServerEN, srv.URL, nil)
	s.userID = testUserID
	_, err := s.FetchSuite(context.Background())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("FetchSuite = %v, want context.Canceled", err)
	}
	if n := countCalls(g, "GET /api/system"); n != 0 {
		t.Fatalf("/system called %d times after cancellation", n)
	}
}

func TestFetchMysekaiFailures(t *testing.T) {
	uid := strconv.FormatInt(testUserID, 10)
	t.Run("maintenance status unreadable", func(t *testing.T) {
		g, srv := newFakeGame(t)
		g.override["/api/module-maintenance/MYSEKAI"] = statusHandler(http.StatusInternalServerError)
		s := newTestSession(t, ServerEN, srv.URL, nil)
		s.userID = testUserID
		_, err := s.FetchMysekai(context.Background())
		var retrievalErr *DataRetrievalError
		if !errors.As(err, &retrievalErr) || retrievalErr.Step != "maintenance_check" {
			t.Fatalf("FetchMysekai = %v, want a maintenance_check failure", err)
		}
	})
	t.Run("mysekai call fails", func(t *testing.T) {
		g, srv := newFakeGame(t)
		g.override["/api/user/"+uid+"/mysekai"] = statusHandler(http.StatusInternalServerError)
		s := newTestSession(t, ServerEN, srv.URL, nil)
		s.userID = testUserID
		_, err := s.FetchMysekai(context.Background())
		var retrievalErr *DataRetrievalError
		if !errors.As(err, &retrievalErr) || retrievalErr.Step != "api_call" || APIStatus(err) != http.StatusInternalServerError {
			t.Fatalf("FetchMysekai = %v, want an api_call failure", err)
		}
	})
}

// The room module, the room call and the Diarkis call are not critical: their
// failures (or an unreadable status) become warnings and the data is returned.
func TestFetchMysekaiToleratesOptionalFailures(t *testing.T) {
	for _, failRoom := range []bool{true, false} {
		g, srv := newFakeGame(t)
		g.override["/api/module-maintenance/MYSEKAI"] = rawHandler("garbage")
		g.override["/api/module-maintenance/MYSEKAI_ROOM"] = statusHandler(http.StatusInternalServerError)
		if failRoom {
			g.override["/api/user/"+strconv.FormatInt(testUserID, 10)+"/mysekai/"+strconv.FormatInt(testUserID, 10)+"/room"] = statusHandler(http.StatusInternalServerError)
		}
		g.override["/api/user/"+strconv.FormatInt(testUserID, 10)+"/diarkis-auth"] = statusHandler(http.StatusServiceUnavailable)
		var steps []string
		s := newTestSession(t, ServerEN, srv.URL, func(e ProgressEvent) { steps = append(steps, e.Step+":"+string(e.Status)+":"+e.Note) })
		s.userID = testUserID
		data, err := s.FetchMysekai(context.Background())
		if err != nil || len(data) == 0 {
			t.Fatalf("FetchMysekai(failRoom=%v) = %d bytes, %v", failRoom, len(data), err)
		}
		note := "status 503"
		if failRoom {
			note = "status 500"
		}
		if last := steps[len(steps)-1]; last != "mysekai_followup:warning:"+note {
			t.Fatalf("failRoom=%v: last step = %q, want the first follow-up failure as a warning", failRoom, last)
		}
	}
}

func TestUnpackRejectsANonMapResponse(t *testing.T) {
	s := newTestSession(t, ServerEN, "http://127.0.0.1:0", nil)
	packed, err := testServerCryptor().Pack([]any{1, 2}, harukiUtils.SupportedDataUploadServerEN)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.unpack(packed); err == nil || !strings.Contains(err.Error(), "unexpected response type") {
		t.Fatalf("unpack = %v, want an unexpected type error", err)
	}
}

func TestParseInheritPreviewVariants(t *testing.T) {
	if _, err := parseInheritPreview(map[string]any{}); !errors.Is(err, errPreviewMissingUserID) {
		t.Fatalf("no afterUserGamedata: %v", err)
	}
	if _, err := parseInheritPreview(map[string]any{"afterUserGamedata": map[string]any{"userId": "x"}}); !errors.Is(err, errPreviewMissingUserID) {
		t.Fatalf("non-numeric userId: %v", err)
	}
	if _, err := parseInheritPreview(map[string]any{"afterUserGamedata": map[string]any{"userId": int64(0)}}); !errors.Is(err, errPreviewMissingUserID) {
		t.Fatalf("zero userId: %v", err)
	}
	// Some responses nest the restriction inside afterUserGamedata.
	p, err := parseInheritPreview(map[string]any{"afterUserGamedata": map[string]any{
		"userId": uint32(42), "name": "Rin", "rank": int8(7),
		"userEventDeviceTransferRestrict": map[string]any{"isWorldBloomChapter": true, "restrictRank": uint16(100), "gameCharacterId": int16(3)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := &InheritPreview{UserID: 42, Name: "Rin", Rank: 7, Restriction: &TransferRestriction{IsWorldBloomChapter: true, RestrictRank: 100, GameCharacterID: 3}}
	if !reflect.DeepEqual(p, want) {
		t.Fatalf("preview = %+v, want %+v", p, want)
	}
}

func TestInt64FieldAcceptsEveryMsgpackWidth(t *testing.T) {
	for _, v := range []any{int64(9), int(9), int32(9), int16(9), int8(9), uint64(9), uint32(9), uint16(9), uint8(9), float64(9)} {
		if got, ok := int64Field(map[string]any{"k": v}, "k"); !ok || got != 9 {
			t.Fatalf("int64Field(%T) = %d, %v", v, got, ok)
		}
	}
	for _, v := range []any{uint64(math.MaxInt64) + 1, "9", nil} {
		if _, ok := int64Field(map[string]any{"k": v}, "k"); ok {
			t.Fatalf("int64Field(%T %v) accepted", v, v)
		}
	}
}

func TestRemainingIsTheFullWaitWithoutAStart(t *testing.T) {
	if got := remaining(time.Time{}, 3*time.Second); got != 3*time.Second {
		t.Fatalf("remaining = %s", got)
	}
}

func TestNewInheritSessionConfigPerRegion(t *testing.T) {
	cfg := harukiConfig.SekaiClientConfig{
		JPServerAPIHost: "jp.example", JPServerAppVersionUrl: "https://jp.example/v", JPServerInheritToken: "jp-secret",
		JPServerInheritClientHeaders: map[string]string{"X-R": "jp"},
		ENServerAPIHost:              "en.example", ENServerAppVersionUrl: "https://en.example/v", ENServerInheritToken: "en-secret",
		ENServerInheritClientHeaders: map[string]string{"X-R": "en"},
	}
	for _, server := range []harukiUtils.SupportedInheritUploadServer{ServerJP, ServerEN} {
		out, err := NewInheritSessionConfig(cfg, "http://proxy", server, testServerCryptor(), InheritPacing{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		r := string(server)
		if out.API != "https://"+r+".example/api" || out.VersionURL != "https://"+r+".example/v" ||
			out.InheritJWTSecret != r+"-secret" || out.Headers["X-R"] != r || out.Proxy != "http://proxy" || out.Server != server {
			t.Fatalf("%s config = %+v", server, out)
		}
	}
}

func TestRedactErrorScrubsSecretsAndKeepsNil(t *testing.T) {
	s := newTestSession(t, ServerEN, "http://127.0.0.1:0", nil)
	if s.RedactError(nil) != nil {
		t.Fatal("RedactError(nil) must stay nil")
	}
	s.AddSecrets("", testCredential)
	s.userID = testUserID
	err := s.RedactError(errors.New("bad " + testCredential + " for " + strconv.FormatInt(testUserID, 10)))
	if strings.Contains(err.Error(), testCredential) || strings.Contains(err.Error(), strconv.FormatInt(testUserID, 10)) {
		t.Fatalf("RedactError = %v", err)
	}
}
