package usersponsor

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/sponsor"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/jsoncodec"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	redisManager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"
	"github.com/redis/go-redis/v9"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type testEnv struct {
	h         *handlers
	app       *fiber.App
	db        *postgresql.Client
	redis     *miniredis.Miniredis
	exchanges atomic.Int32
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	db := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s-%d?mode=memory&cache=shared&_fk=1", strings.ReplaceAll(t.Name(), "/", "-"), time.Now().UnixNano()))
	t.Cleanup(func() { _ = db.Close() })
	mr := miniredis.RunT(t)
	r := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = r.Close() })
	h := &handlers{api: &api.HarukiToolboxRouterHelpers{DBManager: &database.HarukiToolboxDBManager{DB: db, Redis: &redisManager.HarukiRedisManager{Redis: r}}},
		oauth: OAuthConfig{ClientID: "app", ClientSecret: "server-only-secret", RedirectURI: "https://toolbox.example/user/afdian/callback"}, client: newOAuthHTTPClient()}
	for _, id := range []string{"alice", "bob"} {
		if _, err := db.User.Create().SetID(id).SetName(id).SetEmail(id + "@example.com").Save(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	app := fiber.New()
	e := &testEnv{h: h, app: app, db: db, redis: mr}
	h.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		e.exchanges.Add(1)
		if r.URL.String() != "https://afdian.com/api/oauth2/access_token" || r.Method != "POST" {
			t.Error("incorrect Afdian endpoint")
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("client_secret") != "server-only-secret" || r.Form.Get("redirect_uri") != h.oauth.RedirectURI || r.Form.Get("grant_type") != "authorization_code" {
			t.Error("invalid form exchange")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ec":200,"data":{"user_id":"afdian-alice"}}`)), Header: make(http.Header)}, nil
	})
	group := app.Group("/api/user/:toolbox_user_id", func(c fiber.Ctx) error {
		if c.Get("X-Test-User") == "" {
			return c.SendStatus(401)
		}
		c.Locals("userID", c.Get("X-Test-User"))
		c.Locals("emailVerified", c.Get("X-Test-Unverified") != "true")
		c.Locals("authProxySessionID", c.Get("X-Test-Session"))
		return c.Next()
	}, usercore.RequireSelfUserParam("toolbox_user_id"), usercore.CheckUserNotBanned(h.api))
	group.Get("/afdian", h.status)
	group.Post("/afdian/authorize", h.authorize)
	group.Post("/afdian/callback", h.callback)
	group.Post("/afdian/verify-order", usercore.RequireVerifiedEmail(), h.verifyOrder)
	group.Delete("/afdian", h.unlink)
	group.Get("/sponsor-frames", h.frames)
	group.Put("/sponsor-frames/:server/:game_user_id", h.saveFrame)
	app.Get("/game-frame/:server/:game_user_id", h.gameFrame)
	return e
}

func (e *testEnv) request(t *testing.T, method, path, uid, session string, payload any) (int, []byte) {
	t.Helper()
	raw, _ := jsoncodec.Marshal(payload)
	req := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-User", uid)
	req.Header.Set("X-Test-Session", session)
	resp, err := e.app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

func (e *testEnv) begin(t *testing.T, uid string) string {
	t.Helper()
	status, body := e.request(t, "POST", "/api/user/"+uid+"/afdian/authorize", uid, "session", nil)
	if status != 200 {
		t.Fatalf("begin: %d %s", status, body)
	}
	var result struct {
		Data struct {
			URL string `json:"authorizeUrl"`
		} `json:"updatedData"`
	}
	if err := jsoncodec.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(result.Data.URL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "afdian.com" || u.Query().Get("scope") != "basic" || strings.Contains(string(body), "server-only-secret") {
		t.Fatal("unsafe authorization response")
	}
	return u.Query().Get("state")
}

func TestAuthorizationBindsOnlyVerifiedIdentityAndConsumesState(t *testing.T) {
	e := newEnv(t)
	state := e.begin(t, "alice")
	payload := map[string]string{"code": "valid-code", "state": state, "user_id": "forged-id"}
	// Neither another user nor another session may consume Alice's flow.
	for _, tc := range []struct{ uid, session string }{{"bob", "session"}, {"alice", "changed-session"}} {
		status, _ := e.request(t, "POST", "/api/user/"+tc.uid+"/afdian/callback", tc.uid, tc.session, payload)
		if status != 400 {
			t.Fatalf("wrong owner accepted: %d", status)
		}
	}
	status, body := e.request(t, "POST", "/api/user/alice/afdian/callback", "alice", "session", payload)
	if status != 200 {
		t.Fatalf("callback: %d %s", status, body)
	}
	u, _ := e.db.User.Get(t.Context(), "alice")
	if u.AfdianUserID == nil || *u.AfdianUserID != "afdian-alice" {
		t.Fatal("did not bind exchanged identity")
	}
	status, _ = e.request(t, "POST", "/api/user/alice/afdian/callback", "alice", "session", payload)
	if status != 400 || e.exchanges.Load() != 1 {
		t.Fatal("state replay reached Afdian")
	}
	// DB uniqueness protects the same Afdian identity from a second owner.
	other := e.begin(t, "bob")
	status, _ = e.request(t, "POST", "/api/user/bob/afdian/callback", "bob", "session", map[string]string{"code": "other-code", "state": other})
	if status != 409 {
		t.Fatalf("duplicate identity accepted: %d", status)
	}
}

func TestExpiredOrFailedAuthorizationCannotBeReplayed(t *testing.T) {
	e := newEnv(t)
	state := e.begin(t, "alice")
	e.redis.FastForward(oauthStateTTL + time.Second)
	status, _ := e.request(t, "POST", "/api/user/alice/afdian/callback", "alice", "session", map[string]string{"code": "code", "state": state})
	if status != 400 || e.exchanges.Load() != 0 {
		t.Fatal("expired flow redeemed")
	}
	state = e.begin(t, "alice")
	e.h.client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ec":400,"data":{"user_id":"forged"}}`)), Header: make(http.Header)}, nil
	})
	payload := map[string]string{"code": "bad-code", "state": state}
	status, _ = e.request(t, "POST", "/api/user/alice/afdian/callback", "alice", "session", payload)
	if status != 502 {
		t.Fatalf("provider error accepted: %d", status)
	}
	status, _ = e.request(t, "POST", "/api/user/alice/afdian/callback", "alice", "session", payload)
	if status != 400 {
		t.Fatal("failed redemption was reusable")
	}
	u, _ := e.db.User.Get(t.Context(), "alice")
	if u.AfdianUserID != nil {
		t.Fatal("failed provider bound identity")
	}
}

func TestFrameAccessIsPermanentAndScopedToVerifiedGameOwner(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	frame := frameCatalog()[0]
	e.db.User.UpdateOneID("alice").SetAfdianUserID("afdian-alice").ExecX(ctx)
	// An expired sponsorship still grants frames, even with is_active=false.
	e.db.Sponsor.Create().SetID("afdian_afdian-alice").SetAfdianUserID("afdian-alice").SetPlanExpiresAt(time.Now().Add(-365 * 24 * time.Hour)).SetIsActive(false).SaveX(ctx)
	b := e.db.GameAccountBinding.Create().SetServer("cn").SetGameUserID("12345").SetUserID("alice").SetVerified(true).SaveX(ctx)
	e.db.GameAccountBinding.Create().SetServer("jp").SetGameUserID("99999").SetUserID("alice").SaveX(ctx)
	e.db.GameAccountBinding.Create().SetServer("en").SetGameUserID("67890").SetUserID("bob").SetVerified(true).SaveX(ctx)
	for _, tc := range []struct {
		uid, path, frame string
		want             int
	}{
		{"alice", "cn/12345", frame.ID, 200}, {"alice", "jp/99999", frame.ID, 404}, {"alice", "en/67890", frame.ID, 404},
		{"bob", "en/67890", frame.ID, 403}, {"alice", "cn/12345", "../../secret", 400}, {"alice", "cn/12345", "https://evil.example/a.png", 400},
	} {
		status, body := e.request(t, "PUT", "/api/user/"+tc.uid+"/sponsor-frames/"+tc.path, tc.uid, "session", map[string]string{"frameId": tc.frame})
		if status != tc.want {
			t.Fatalf("%s %s: %d %s", tc.uid, tc.path, status, body)
		}
	}
	status, body := e.request(t, "GET", "/game-frame/cn/12345", "", "", nil)
	if status != 200 || !strings.Contains(string(body), frame.Horizontal.Base) {
		t.Fatalf("bot frame absent: %d %s", status, body)
	}
	for _, secret := range []string{"afdian-alice", "expiresAt", "totalAmount", "toolboxUserId"} {
		if strings.Contains(string(body), secret) {
			t.Fatal("private frame leaked sponsorship identity")
		}
	}
	status, body = e.request(t, "GET", "/api/user/alice/afdian", "alice", "session", nil)
	if status != 200 || !strings.Contains(string(body), `"eligible":true`) || !strings.Contains(string(body), `"category":"former"`) {
		t.Fatalf("past sponsor rejected: %s", body)
	}
	e.request(t, "DELETE", "/api/user/alice/afdian", "alice", "session", nil)
	_, body = e.request(t, "GET", "/game-frame/cn/12345", "", "", nil)
	if string(body) != `{"frame":null}` {
		t.Fatalf("unlinked identity kept frame: %s", body)
	}
	// Reset stays available even after unlinking or losing eligibility.
	status, _ = e.request(t, "PUT", "/api/user/alice/sponsor-frames/cn/12345", "alice", "session", map[string]string{"frameId": ""})
	if status != 200 || e.db.GameAccountBinding.GetX(ctx, b.ID).SponsorFrameID != nil {
		t.Fatal("could not reset frame")
	}
}

func TestAfdianConfigurationAndRateLimitFailClosed(t *testing.T) {
	e := newEnv(t)
	for _, uri := range []string{"http://toolbox.example/user/afdian/callback", "https://toolbox.example/other", "https://toolbox.example/user/afdian/callback?next=evil"} {
		cfg := e.h.oauth
		cfg.RedirectURI = uri
		if cfg.Enabled() {
			t.Fatalf("unsafe callback allowed: %s", uri)
		}
	}
	for i := 0; i < 10; i++ {
		e.begin(t, "alice")
	}
	status, _ := e.request(t, "POST", "/api/user/alice/afdian/authorize", "alice", "session", nil)
	if status != 429 {
		t.Fatalf("rate limit status %d", status)
	}
	e.h.oauth.ClientSecret = ""
	status, _ = e.request(t, "POST", "/api/user/alice/afdian/authorize", "alice", "session", nil)
	if status != 503 {
		t.Fatal("unconfigured provider started")
	}
	status, _ = e.request(t, "GET", "/api/user/bob/afdian", "alice", "session", nil)
	if status != 403 {
		t.Fatal("cross-user read allowed")
	}
}

func TestRegisteredBrowserRoutesRequireAuthentication(t *testing.T) {
	app := fiber.New()
	helper := &api.HarukiToolboxRouterHelpers{Router: app, SessionHandler: &api.SessionHandler{}}
	RegisterRoutes(helper, OAuthConfig{}, sponsor.NewAfdianConfig(sponsor.AfdianConfigOptions{}))
	for _, tc := range []struct{ method, path string }{{"GET", "afdian"}, {"GET", "sponsor-frames"}, {"POST", "afdian/authorize"}, {"POST", "afdian/callback"}, {"POST", "afdian/verify-order"}, {"DELETE", "afdian"}, {"PUT", "sponsor-frames/cn/1"}} {
		resp, err := app.Test(httptest.NewRequest(tc.method, "/api/user/alice/"+tc.path, nil))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Fatalf("unguarded %s %s: %d", tc.method, tc.path, resp.StatusCode)
		}
	}
}
