package upload

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	json "encoding/json/v2"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/inheritvault"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/mailtemplate"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/inheritvaultkey"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/systemlog"
	harukiSekai "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/game/sekai"
	harukiSMTP "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/smtp"

	"github.com/gofiber/fiber/v3"
)

// scriptedInheritGame is a fake game whose calls can each be replaced; a
// nil hook behaves like a healthy game server.
type scriptedInheritGame struct {
	mu        sync.Mutex
	progress  harukiSekai.Progress
	calls     []string
	bootstrap func(context.Context) error
	preview   func(context.Context) error
	execute   func(context.Context) (string, error)
	login     func(context.Context) error
	issue     func(context.Context) (string, error)
	suite     func(context.Context) ([]byte, error)
	mysekai   func(context.Context) ([]byte, error)
}

func (g *scriptedInheritGame) record(call string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, call)
}

func (g *scriptedInheritGame) count(call string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, c := range g.calls {
		if c == call {
			n++
		}
	}
	return n
}

func (g *scriptedInheritGame) Bootstrap(ctx context.Context) error {
	g.record("bootstrap")
	if g.bootstrap != nil {
		return g.bootstrap(ctx)
	}
	return nil
}

func (g *scriptedInheritGame) Preview(ctx context.Context, _ harukiSekai.InheritCode) (*harukiSekai.InheritPreview, error) {
	g.record("preview")
	if g.preview != nil {
		if err := g.preview(ctx); err != nil {
			return nil, err
		}
	}
	return &harukiSekai.InheritPreview{UserID: testGameUID, Name: "Miku", Rank: 150}, nil
}

func (g *scriptedInheritGame) Execute(ctx context.Context, _ harukiSekai.InheritCode) (string, error) {
	g.record("execute")
	if g.execute != nil {
		return g.execute(ctx)
	}
	return testGameCredential, nil
}

func (g *scriptedInheritGame) Login(ctx context.Context, _ int64, _ string) error {
	g.record("login")
	if g.login != nil {
		return g.login(ctx)
	}
	return nil
}

func (g *scriptedInheritGame) IssueInheritCode(ctx context.Context, _ string) (string, error) {
	g.record("issue")
	if g.issue != nil {
		return g.issue(ctx)
	}
	return testIssuedID, nil
}

func (g *scriptedInheritGame) FetchSuite(ctx context.Context) ([]byte, error) {
	g.record("suite")
	if g.suite != nil {
		return g.suite(ctx)
	}
	return []byte("suite-bytes"), nil
}

func (g *scriptedInheritGame) FetchMysekai(ctx context.Context) ([]byte, error) {
	g.record("mysekai")
	if g.mysekai != nil {
		return g.mysekai(ctx)
	}
	return []byte("mysekai-bytes"), nil
}

func (g *scriptedInheritGame) RedactError(err error) error { return err }

// useScriptedGame makes every new game session of env return g. Call it
// while no job of env is running.
func (env *inheritTestEnv) useScriptedGame(g *scriptedInheritGame) {
	env.svc.newGame = func(_ harukiUtils.SupportedInheritUploadServer, progress harukiSekai.Progress) (inheritGame, error) {
		g.mu.Lock()
		g.progress = progress
		g.mu.Unlock()
		return g, nil
	}
}

// identityFaults switches the identity collaborators of env into failure
// modes from the test goroutine without racing the handlers that read them.
type identityFaults struct {
	emailErr  atomic.Bool
	hasPwErr  atomic.Bool
	verifyErr atomic.Bool
	onVerify  func()
}

func installIdentityFaults(env *inheritTestEnv) *identityFaults {
	f := &identityFaults{}
	email, hasPassword, verify := env.svc.userEmail, env.svc.hasPassword, env.svc.verifyPassword
	env.svc.userEmail = func(ctx context.Context, userID string) (string, string, error) {
		if f.emailErr.Load() {
			return "", "", errors.New("user lookup failed")
		}
		return email(ctx, userID)
	}
	env.svc.hasPassword = func(ctx context.Context, identityID string) (bool, error) {
		if f.hasPwErr.Load() {
			return false, errors.New("kratos unreachable")
		}
		return hasPassword(ctx, identityID)
	}
	env.svc.verifyPassword = func(ctx context.Context, identityID, password string) error {
		if f.onVerify != nil {
			f.onVerify()
		}
		if f.verifyErr.Load() {
			return errors.New("kratos unreachable")
		}
		return verify(ctx, identityID, password)
	}
	return f
}

// callRawBody sends body unencoded (to test malformed JSON) and also returns
// the response headers.
func (env *inheritTestEnv) callRawBody(user, method, path, body string) (apiResult, http.Header) {
	env.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if user != "" {
		req.Header.Set("X-Test-User", user)
	}
	res, err := env.app.Test(req, fiber.TestConfig{Timeout: 10 * time.Second})
	if err != nil {
		env.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := apiResult{Status: res.StatusCode, Raw: string(raw)}
	_ = json.Unmarshal(raw, &out.Body)
	return out, res.Header
}

func (env *inheritTestEnv) auditCount(action, result string) int {
	env.t.Helper()
	return env.db.SystemLog.Query().Where(systemlog.ActionEQ(action), systemlog.ResultEQ(systemlog.Result(result))).CountX(context.Background())
}

func entryPath(id string) string { return "/api/inherit/vault/entries/" + id }

func ownerPW() map[string]any { return map[string]any{"password": testOwnerPassword} }

func TestInheritEndpointsRequireSignIn(t *testing.T) {
	env := newInheritTestEnv(t)
	for _, tc := range []struct{ method, path string }{
		{"POST", "/api/inherit/jobs"},
		{"GET", "/api/inherit/jobs/j1"},
		{"POST", "/api/inherit/jobs/j1/confirm"},
		{"POST", "/api/inherit/jobs/j1/cancel"},
		{"POST", "/api/inherit/jobs/j1/ws-ticket"},
		{"GET", "/api/inherit/jobs/j1/ws"},
		{"GET", "/api/inherit/vault/key"},
		{"POST", "/api/inherit/vault/key/reset"},
		{"GET", "/api/inherit/vault/entries"},
		{"POST", entryPath("e1") + "/reveal"},
		{"POST", entryPath("e1") + "/reissue"},
		{"DELETE", entryPath("e1")},
	} {
		res := env.call("", tc.method, tc.path, map[string]any{"password": testOwnerPassword, "confirm": true, "new_password_mode": "random"})
		if res.Status != http.StatusUnauthorized || res.code() != "unauthenticated" {
			t.Errorf("%s %s anonymous = %d %s", tc.method, tc.path, res.Status, res.Raw)
		}
	}
}

func TestInheritEndpointsAnswer503WithoutAVault(t *testing.T) {
	env := newInheritTestEnv(t)
	env.svc.vault = inheritvault.NewStore(nil, nil, nil)
	for _, tc := range []struct{ method, path string }{
		{"POST", "/api/inherit/jobs"},
		{"GET", "/api/inherit/vault/key"},
		{"POST", "/api/inherit/vault/key/reset"},
		{"GET", "/api/inherit/vault/entries"},
		{"POST", entryPath("e1") + "/reveal"},
		{"POST", entryPath("e1") + "/reissue"},
		{"DELETE", entryPath("e1")},
	} {
		body := validJobRequest()
		body["password"], body["confirm"], body["new_password_mode"] = testOwnerPassword, true, "random"
		res := env.call(testOwner, tc.method, tc.path, body)
		if res.Status != http.StatusServiceUnavailable || res.code() != "vault_unavailable" {
			t.Errorf("%s %s without a vault = %d %s", tc.method, tc.path, res.Status, res.Raw)
		}
	}
	if _, _, p := env.svc.unlockVault(context.Background(), requestMeta{UserID: testOwner}, unlockInput{Password: testOwnerPassword}, true); p != errVaultUnavailable {
		t.Fatalf("unlockVault without a vault = %v", p)
	}
	env.mu.Lock()
	defer env.mu.Unlock()
	if env.games != 0 {
		t.Fatalf("game sessions started without a vault: %d", env.games)
	}
}

func TestInheritEndpointsRejectMalformedBodies(t *testing.T) {
	env := newInheritTestEnv(t)
	for _, tc := range []struct{ method, path string }{
		{"POST", "/api/inherit/jobs"},
		{"POST", "/api/inherit/jobs/j1/confirm"},
		{"POST", "/api/inherit/vault/key/reset"},
		{"POST", entryPath("e1") + "/reveal"},
		{"POST", entryPath("e1") + "/reissue"},
	} {
		res, _ := env.callRawBody(testOwner, tc.method, tc.path, `{"password":`)
		if res.Status != http.StatusBadRequest || res.code() != "invalid_request" {
			t.Errorf("%s %s malformed = %d %s", tc.method, tc.path, res.Status, res.Raw)
		}
	}
	res := env.call(testOwner, "POST", entryPath("e1")+"/reissue", map[string]any{"password": testOwnerPassword, "new_password_mode": "keep"})
	if res.Status != http.StatusBadRequest || res.code() != "new_password_mode_invalid" {
		t.Fatalf("reissue with a bad mode = %d %s", res.Status, res.Raw)
	}
}

func TestVaultKeyViewAfterTheKeyExists(t *testing.T) {
	env := newInheritTestEnv(t)
	_, done := env.runToSuccess()
	entry := env.vaultClient.InheritVaultEntry.GetX(context.Background(), done["vault_entry_id"].(string))
	key := env.call(testOwner, "GET", "/api/inherit/vault/key", nil)
	d := key.data()
	if key.Status != http.StatusOK || d["initialized"] != true || d["kind"] != "password" || d["key_version"] != float64(entry.KeyVersion) || d["created_at"] == nil {
		t.Fatalf("key = %d %s", key.Status, key.Raw)
	}
	if _, ok := d["identity_has_password"]; ok {
		t.Fatalf("an initialized key still reports identity_has_password: %s", key.Raw)
	}
}

// The vault database going away is a 503 everywhere, never a 500 or an
// empty answer, and a failed destroy or reset is audited.
func TestVaultDatabaseOutageIs503(t *testing.T) {
	env := newInheritTestEnv(t)
	_, done := env.runToSuccess()
	entryID := done["vault_entry_id"].(string)
	// A second job waits for its confirmation while the database goes away.
	id := env.createJob(validJobRequest())
	env.waitStatus(id, "awaiting_confirm")
	_ = env.vaultClient.Close()

	for _, tc := range []struct {
		method, path string
		body         map[string]any
	}{
		{"GET", "/api/inherit/vault/key", nil},
		{"GET", "/api/inherit/vault/entries", nil},
		{"POST", entryPath(entryID) + "/reveal", ownerPW()},
		{"POST", entryPath(entryID) + "/reissue", map[string]any{"password": testOwnerPassword, "new_password_mode": "random"}},
		{"DELETE", entryPath(entryID), nil},
		{"POST", "/api/inherit/vault/key/reset", map[string]any{"password": testOwnerPassword, "confirm": true}},
		{"POST", "/api/inherit/jobs/" + id + "/confirm", ownerPW()},
	} {
		res := env.call(testOwner, tc.method, tc.path, tc.body)
		if res.Status != http.StatusServiceUnavailable || res.code() != "store_unavailable" {
			t.Errorf("%s %s with the database down = %d %s", tc.method, tc.path, res.Status, res.Raw)
		}
	}
	// A new job cannot tell which secret its confirmation needs.
	if res := env.call(testIntruder, "POST", "/api/inherit/jobs", validJobRequest()); res.Status != http.StatusServiceUnavailable || res.code() != "store_unavailable" {
		t.Fatalf("create with the database down = %d %s", res.Status, res.Raw)
	}
	for _, action := range []string{"inherit.vault.destroy", "inherit.vault.key.reset", "inherit.vault.reveal", "inherit.vault.reissue"} {
		if env.auditCount(action, "failure") != 1 {
			t.Errorf("audit %s failure rows = %d", action, env.auditCount(action, "failure"))
		}
	}
	env.game.mu.Lock()
	executes := strings.Count(strings.Join(env.game.calls, ","), "execute")
	env.game.mu.Unlock()
	if executes != 1 {
		t.Fatalf("executing calls = %d; the waiting job must not execute", executes)
	}
	if res := env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/cancel", nil); res.Status != http.StatusOK {
		t.Fatalf("cancel = %d %s", res.Status, res.Raw)
	}
	env.waitStatus(id, "cancelled")
}

// Kratos or the user table being unreachable is identity_unavailable, on
// every path that needs to know or check the owner's secret.
func TestVaultIdentityOutageIs503(t *testing.T) {
	env := newInheritTestEnv(t)
	faults := installIdentityFaults(env)
	id := env.createJob(validJobRequest())
	env.waitStatus(id, "awaiting_confirm")

	expect := func(what string, res apiResult) {
		t.Helper()
		if res.Status != http.StatusServiceUnavailable || res.code() != "identity_unavailable" {
			t.Errorf("%s = %d %s", what, res.Status, res.Raw)
		}
	}
	for name, flag := range map[string]*atomic.Bool{"user lookup": &faults.emailErr, "kratos": &faults.hasPwErr} {
		flag.Store(true)
		expect(name+": key", env.call(testOwner, "GET", "/api/inherit/vault/key", nil))
		expect(name+": reset", env.call(testOwner, "POST", "/api/inherit/vault/key/reset", map[string]any{"password": testOwnerPassword, "confirm": true}))
		expect(name+": create", env.call(testIntruder, "POST", "/api/inherit/jobs", validJobRequest()))
		// The first confirmation creates the key, which needs the secret kind.
		expect(name+": first confirm", env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/confirm", ownerPW()))
		flag.Store(false)
	}
	if res := env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/confirm", ownerPW()); res.Status != http.StatusAccepted {
		t.Fatalf("confirm once identity is back = %d %s", res.Status, res.Raw)
	}
	entryID := env.waitStatus(id, "succeeded")["vault_entry_id"].(string)

	faults.emailErr.Store(true)
	expect("reveal: user lookup", env.call(testOwner, "POST", entryPath(entryID)+"/reveal", ownerPW()))
	faults.emailErr.Store(false)
	faults.verifyErr.Store(true)
	expect("reveal: kratos verify", env.call(testOwner, "POST", entryPath(entryID)+"/reveal", ownerPW()))
	faults.verifyErr.Store(false)
	if res := env.call(testOwner, "POST", entryPath(entryID)+"/reveal", ownerPW()); res.Status != http.StatusOK {
		t.Fatalf("reveal once identity is back = %d %s", res.Status, res.Raw)
	}
}

func TestVaultResetGuards(t *testing.T) {
	env := newInheritTestEnv(t)
	env.runToSuccess()
	reset := func(user string, body map[string]any) apiResult {
		return env.call(user, "POST", "/api/inherit/vault/key/reset", body)
	}
	if res := reset(testOwner, map[string]any{"confirm": true}); res.Status != http.StatusBadRequest || res.code() != "password_required" {
		t.Fatalf("reset without a password = %d %s", res.Status, res.Raw)
	}
	if res := reset(testOwner, map[string]any{"password": "wrong", "confirm": true}); res.Status != http.StatusForbidden || res.code() != "password_invalid" {
		t.Fatalf("reset with a wrong password = %d %s", res.Status, res.Raw)
	}
	// Reset attempts share the unlock limit.
	env.redis.Set(unlockAttemptPrefix+testOwner, "10")
	if res := reset(testOwner, map[string]any{"password": testOwnerPassword, "confirm": true}); res.Status != http.StatusTooManyRequests || res.code() != "unlock_rate_limited" {
		t.Fatalf("reset over the limit = %d %s", res.Status, res.Raw)
	}
	if entries := env.vaultEntries(testOwner); len(entries) != 1 {
		t.Fatalf("a refused reset destroyed entries: %v", entries)
	}
	if env.auditCount("inherit.vault.key.reset", "success") != 0 {
		t.Fatal("refused reset audited as success")
	}

	// A passwordless identity has only its session and the confirm flag.
	env.identity[testIntruder] = false
	if res := reset(testIntruder, map[string]any{"confirm": true}); res.Status != http.StatusOK || res.data()["destroyed_entries"] != float64(0) {
		t.Fatalf("PIN reset = %d %s", res.Status, res.Raw)
	}

	// Redis down: the attempt counter cannot be kept, so nothing is checked.
	env.redis.Close()
	if res := reset(testOwner, map[string]any{"password": testOwnerPassword, "confirm": true}); res.Status != http.StatusServiceUnavailable || res.code() != "store_unavailable" {
		t.Fatalf("reset with Redis down = %d %s", res.Status, res.Raw)
	}
}

// Entries whose stored ciphertext or key no longer fits are refused with a
// specific error, never handed out.
func TestVaultEntryIntegrityFailures(t *testing.T) {
	env := newInheritTestEnv(t)
	_, done := env.runToSuccess()
	entryID := done["vault_entry_id"].(string)
	ctx := context.Background()
	reveal := func() apiResult { return env.call(testOwner, "POST", entryPath(entryID)+"/reveal", ownerPW()) }
	reissue := func(mode string) apiResult {
		return env.call(testOwner, "POST", entryPath(entryID)+"/reissue", map[string]any{"password": testOwnerPassword, "new_password_mode": mode})
	}

	// An entry sealed to another key version is stale.
	keyVersion := env.vaultClient.InheritVaultEntry.GetX(ctx, entryID).KeyVersion
	env.vaultClient.InheritVaultEntry.UpdateOneID(entryID).SetKeyVersion(keyVersion + 1).ExecX(ctx)
	if res := reveal(); res.Status != http.StatusGone || res.code() != "entry_stale" {
		t.Fatalf("stale reveal = %d %s", res.Status, res.Raw)
	}
	if entries := env.vaultEntries(testOwner); len(entries) != 1 || entries[0].(map[string]any)["readable"] != false {
		t.Fatalf("stale entry listed as %v", entries)
	}
	env.vaultClient.InheritVaultEntry.UpdateOneID(entryID).SetKeyVersion(keyVersion).ExecX(ctx)

	// A code that does not decrypt is an internal error, audited.
	env.vaultClient.InheritVaultEntry.UpdateOneID(entryID).SetCodeSealed([]byte("not a sealed code")).ExecX(ctx)
	if res := reveal(); res.Status != http.StatusInternalServerError || res.code() != "internal_error" {
		t.Fatalf("undecryptable code reveal = %d %s", res.Status, res.Raw)
	}
	reasons := map[any]int{}
	for _, row := range env.db.SystemLog.Query().Where(systemlog.ActionEQ("inherit.vault.reveal"), systemlog.ResultEQ(systemlog.ResultFailure)).AllX(ctx) {
		reasons[row.Metadata["reason"]]++
	}
	if reasons["entry_stale"] != 1 || reasons["decrypt_failed"] != 1 {
		t.Fatalf("reveal failure audit reasons = %v", reasons)
	}
	// Reuse needs the previous password from that code.
	if res := reissue("reuse"); res.Status != http.StatusConflict || res.code() != "no_previous_password" {
		t.Fatalf("reuse with an undecryptable code = %d %s", res.Status, res.Raw)
	}
	// A credential that does not decrypt cannot log in.
	env.vaultClient.InheritVaultEntry.UpdateOneID(entryID).SetCredentialSealed([]byte("not a sealed credential")).ExecX(ctx)
	if res := reissue("random"); res.Status != http.StatusInternalServerError || res.code() != "internal_error" {
		t.Fatalf("undecryptable credential reissue = %d %s", res.Status, res.Raw)
	}
	env.game.mu.Lock()
	logins := strings.Count(strings.Join(env.game.calls, ","), "login")
	env.game.mu.Unlock()
	if logins != 1 {
		t.Fatalf("game logins = %d, want only the job's", logins)
	}

	// A key row that cannot be parsed is an internal error, not a wrong secret.
	env.vaultClient.InheritVaultKey.Update().Where(inheritvaultkey.UserIDEQ(testOwner)).SetKdfParams("not json").ExecX(ctx)
	if res := reveal(); res.Status != http.StatusInternalServerError || res.code() != "internal_error" {
		t.Fatalf("broken key reveal = %d %s", res.Status, res.Raw)
	}
	// Without a key at all the entry cannot be opened (and reveal never
	// creates one).
	env.vaultClient.InheritVaultKey.Delete().Where(inheritvaultkey.UserIDEQ(testOwner)).ExecX(ctx)
	if res := reveal(); res.Status != http.StatusConflict || res.code() != "vault_not_initialized" {
		t.Fatalf("reveal without a key = %d %s", res.Status, res.Raw)
	}
	if n := env.vaultClient.InheritVaultKey.Query().CountX(ctx); n != 0 {
		t.Fatalf("reveal created a key: %d", n)
	}
}

func TestVaultReissueReusesThePreviousPassword(t *testing.T) {
	env := newInheritTestEnv(t)
	_, done := env.runToSuccess()
	entryID := done["vault_entry_id"].(string)
	env.game = &fakeInheritGame{}
	res := env.call(testOwner, "POST", entryPath(entryID)+"/reissue", map[string]any{"password": testOwnerPassword, "new_password_mode": "reuse"})
	if res.Status != http.StatusOK || res.data()["password"] != testEnteredPW || res.data()["inherit_id"] != testIssuedID || res.data()["warnings"] != nil {
		t.Fatalf("reuse reissue = %d %s", res.Status, res.Raw)
	}
	if env.game.issuedPW != testEnteredPW || env.game.loginCred != testGameCredential {
		t.Fatalf("issued with %q after logging in with %q", env.game.issuedPW, env.game.loginCred)
	}
	if env.auditCount("inherit.vault.reissue", "success") != 1 {
		t.Fatal("reissue not audited")
	}
	waitFor(t, func() bool {
		env.mu.Lock()
		defer env.mu.Unlock()
		for _, m := range env.mails {
			if strings.HasSuffix(m, "|保险库中的引继码已重新签发 | Haruki 工具箱") {
				return true
			}
		}
		return false
	})
}

func TestVaultReissueGates(t *testing.T) {
	env := newInheritTestEnv(t)
	_, done := env.runToSuccess()
	entryID := done["vault_entry_id"].(string)
	reissue := func() (apiResult, http.Header) {
		raw, _ := json.Marshal(map[string]any{"password": testOwnerPassword, "new_password_mode": "random"})
		return env.callRawBody(testOwner, "POST", entryPath(entryID)+"/reissue", string(raw))
	}

	// An open breaker refuses before any game call, with Retry-After.
	env.svc.breaker = newInheritCircuitBreaker(1, time.Minute, time.Minute)
	_, _, token := env.svc.breaker.Allow("jp")
	env.svc.breaker.RecordResult("jp", token, true)
	res, header := reissue()
	if res.Status != http.StatusServiceUnavailable || res.code() != "game_degraded" || header.Get("Retry-After") == "" {
		t.Fatalf("reissue with the breaker open = %d %s (Retry-After %q)", res.Status, res.Raw, header.Get("Retry-After"))
	}
	env.svc.breaker = newInheritCircuitBreaker(5, time.Minute, time.Second)

	// No free slot on the server.
	env.svc.limiter = newInheritConcurrencyLimiter(1)
	if !env.svc.limiter.acquire("jp") {
		t.Fatal("acquire")
	}
	if res, _ := reissue(); res.Status != http.StatusTooManyRequests || res.code() != "server_busy" {
		t.Fatalf("reissue with no slot = %d %s", res.Status, res.Raw)
	}
	env.svc.limiter.release("jp")

	env.mu.Lock()
	games := env.games
	env.mu.Unlock()
	if games != 1 {
		t.Fatalf("game sessions = %d, want only the job's", games)
	}
	if env.auditCount("inherit.vault.reissue", "failure") != 2 {
		t.Fatalf("refused reissues audited %d times", env.auditCount("inherit.vault.reissue", "failure"))
	}
}

// Game failures during a reissue map to distinct errors, and none replaces
// the stored code.
func TestVaultReissueGameFailures(t *testing.T) {
	env := newInheritTestEnv(t)
	_, done := env.runToSuccess()
	entryID := done["vault_entry_id"].(string)
	forbidden := harukiSekai.NewAPIError("/x", "PUT", 403, "", nil)
	badRequest := harukiSekai.NewAPIError("/x", "PUT", 400, "", nil)
	unreachable := harukiSekai.NewAPIError("/x", "PUT", 0, "", nil)
	serverError := harukiSekai.NewAPIError("/x", "PUT", 500, "", nil)
	cases := []struct {
		name   string
		game   *scriptedInheritGame
		status int
		code   string
	}{
		{"bootstrap", &scriptedInheritGame{bootstrap: func(context.Context) error { return unreachable }}, http.StatusBadGateway, "reissue_failed"},
		{"login rejected", &scriptedInheritGame{login: func(context.Context) error { return forbidden }}, http.StatusBadGateway, "reissue_login_failed"},
		{"login game error", &scriptedInheritGame{login: func(context.Context) error { return badRequest }}, http.StatusBadGateway, "reissue_login_failed"},
		{"login unreachable", &scriptedInheritGame{login: func(context.Context) error { return unreachable }}, http.StatusBadGateway, "reissue_failed"},
		{"issue rejected", &scriptedInheritGame{issue: func(context.Context) (string, error) { return "", badRequest }}, http.StatusBadGateway, "reissue_rejected"},
		{"issue server error", &scriptedInheritGame{issue: func(context.Context) (string, error) { return "", serverError }}, http.StatusBadGateway, "reissue_failed"},
	}
	for _, tc := range cases {
		env.useScriptedGame(tc.game)
		res := env.call(testOwner, "POST", entryPath(entryID)+"/reissue", map[string]any{"password": testOwnerPassword, "new_password_mode": "random"})
		if res.Status != tc.status || res.code() != tc.code {
			t.Errorf("%s: %d %s, want %d %s", tc.name, res.Status, res.Raw, tc.status, tc.code)
		}
	}
	env.svc.newGame = func(harukiUtils.SupportedInheritUploadServer, harukiSekai.Progress) (inheritGame, error) {
		return nil, errors.New("no session")
	}
	if res := env.call(testOwner, "POST", entryPath(entryID)+"/reissue", map[string]any{"password": testOwnerPassword, "new_password_mode": "random"}); res.Status != http.StatusInternalServerError || res.code() != "internal_error" {
		t.Fatalf("reissue without a game session = %d %s", res.Status, res.Raw)
	}
	reveal := env.call(testOwner, "POST", entryPath(entryID)+"/reveal", ownerPW())
	if reveal.Status != http.StatusOK || reveal.data()["password"] != testEnteredPW {
		t.Fatalf("stored code changed by failed reissues: %d %s", reveal.Status, reveal.Raw)
	}
	if n := env.auditCount("inherit.vault.reissue", "failure"); n != len(cases)+1 {
		t.Fatalf("failed reissues audited %d times", n)
	}
}

// PostgreSQL going away after the game issued the new code: the code still
// reaches the owner, with a warning saying where (or whether) it is kept.
func TestVaultReissueStoreWarnings(t *testing.T) {
	t.Run("fallback", func(t *testing.T) {
		env := newInheritTestEnv(t)
		_, done := env.runToSuccess()
		entryID := done["vault_entry_id"].(string)
		env.useScriptedGame(&scriptedInheritGame{issue: func(context.Context) (string, error) {
			_ = env.vaultClient.Close()
			return testIssuedID, nil
		}})
		res := env.call(testOwner, "POST", entryPath(entryID)+"/reissue", map[string]any{"password": testOwnerPassword, "new_password_mode": "random"})
		if res.Status != http.StatusOK || res.data()["inherit_id"] != testIssuedID || !strings.Contains(res.Raw, codeVaultFallback) {
			t.Fatalf("reissue with the database gone = %d %s", res.Status, res.Raw)
		}
		if !env.redis.Exists("inherit:vault:fallback:" + entryID) {
			t.Fatal("new code not parked in Redis")
		}
	})
	t.Run("lost", func(t *testing.T) {
		env := newInheritTestEnv(t)
		// No Redis behind the vault: nowhere to park the code.
		env.svc.vault = inheritvault.NewStore(env.vaultClient, nil, env.svc.vault.Master())
		_, done := env.runToSuccess()
		entryID := done["vault_entry_id"].(string)
		env.useScriptedGame(&scriptedInheritGame{issue: func(context.Context) (string, error) {
			_ = env.vaultClient.Close()
			return testIssuedID, nil
		}})
		res := env.call(testOwner, "POST", entryPath(entryID)+"/reissue", map[string]any{"password": testOwnerPassword, "new_password_mode": "random"})
		if res.Status != http.StatusOK || res.data()["inherit_id"] != testIssuedID || !strings.Contains(res.Raw, codeVaultStoreFailed) {
			t.Fatalf("reissue with nowhere to store = %d %s", res.Status, res.Raw)
		}
	})
}

func TestVaultNotifyOnlyMailsVerifiedAddresses(t *testing.T) {
	env := newInheritTestEnv(t)
	entry := &inheritvault.Entry{Server: "jp", GameUserID: testGameUID}
	looked := make(chan string, 4)
	env.svc.userEmail = func(_ context.Context, userID string) (string, string, error) {
		looked <- userID
		if userID == "no-address" {
			return "not-an-address", "", nil
		}
		return userID + "@example.com", userID, nil
	}
	env.svc.notify(requestMeta{UserID: "unverified", EmailVerified: false}, mailtemplate.InheritCodeRevealed, entry)
	env.svc.notify(requestMeta{UserID: "no-address", EmailVerified: true}, mailtemplate.InheritCodeRevealed, entry)
	if got := <-looked; got != "no-address" {
		t.Fatalf("looked up %q; an unverified owner must not be looked up", got)
	}
	env.svc.notify(requestMeta{UserID: testOwner, EmailVerified: true, IP: "203.0.113.9"}, mailtemplate.InheritCodeRevealed, entry)
	waitFor(t, func() bool { env.mu.Lock(); defer env.mu.Unlock(); return len(env.mails) == 1 })
	env.mu.Lock()
	defer env.mu.Unlock()
	if env.mails[0] != testOwner+"@example.com|保险库中的引继码已被查看 | Haruki 工具箱" {
		t.Fatalf("mails = %v", env.mails)
	}
	for _, want := range []string{"查看引继码", "JP", "203.0.113.9", "（北京时间）", "修改 Haruki 工具箱密码"} {
		if !strings.Contains(env.mailBodies[0], want) || !strings.Contains(env.mailTexts[0], want) {
			t.Fatalf("notification mail (html or text) lacks %q: %s", want, env.mailBodies[0])
		}
	}
}

func TestVaultSendSMTPIsBestEffort(t *testing.T) {
	(&inheritService{}).sendSMTP("a@example.com", harukiSMTP.Message{Subject: "s", HTML: "<p>b</p>", Text: "b"})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	env := newInheritTestEnv(t)
	env.svc.api.SMTPClient = harukiSMTP.NewSMTPClient(harukiConfig.SMTPConfig{SMTPAddr: "127.0.0.1", SMTPPort: port, SMTPMail: "noreply@example.com", TimeoutSeconds: 1})
	done := make(chan struct{})
	go func() {
		defer close(done)
		env.svc.sendSMTP("a@example.com", harukiSMTP.Message{Subject: "subject", HTML: "<p>body</p>", Text: "body"})
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("sendSMTP blocked on an unreachable server")
	}
}

func TestKratosAdaptersNeedKratos(t *testing.T) {
	s := &inheritService{api: &harukiAPIHelper.HarukiToolboxRouterHelpers{}}
	if err := s.verifyKratosPassword(context.Background(), "id", "pw"); err == nil || errors.Is(err, errPasswordRejected) {
		t.Fatalf("verify without Kratos = %v", err)
	}
	if ok, err := s.kratosHasPassword(context.Background(), "id"); err == nil || ok {
		t.Fatalf("hasPassword without Kratos = %v %v", ok, err)
	}

	kratos := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/admin/identities/with-password" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"with-password","traits":{"email":"a@example.com"},"credentials":{"password":{"type":"password"}}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer kratos.Close()
	sessions := harukiAPIHelper.NewSessionHandler(nil, "")
	sessions.ConfigureIdentityProvider("kratos", kratos.URL, kratos.URL, "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)
	s.api.SessionHandler = sessions
	if ok, err := s.kratosHasPassword(context.Background(), "with-password"); err != nil || !ok {
		t.Fatalf("hasPassword = %v %v", ok, err)
	}
	// Kratos refusing the input is a rejected password, not an outage.
	if err := s.verifyKratosPassword(context.Background(), "", "pw"); !errors.Is(err, errPasswordRejected) {
		t.Fatalf("verify with no identity = %v", err)
	}
	if err := s.verifyKratosPassword(context.Background(), "missing", "pw"); err == nil || errors.Is(err, errPasswordRejected) {
		t.Fatalf("verify for an unknown identity = %v", err)
	}
}

func TestQueryUserEmailAndVerifiedQQ(t *testing.T) {
	env := newInheritTestEnv(t)
	ctx := context.Background()
	if _, _, err := env.svc.queryUserEmail(ctx, "nobody"); err == nil {
		t.Fatal("unknown user resolved")
	}
	if email, identity, err := env.svc.queryUserEmail(ctx, testOwner); err == nil || email != testOwner+"@example.com" || identity != "" {
		t.Fatalf("unlinked user = %q %q %v", email, identity, err)
	}
	env.db.User.UpdateOneID(testOwner).SetKratosIdentityID(" identity-1 ").ExecX(ctx)
	if email, identity, err := env.svc.queryUserEmail(ctx, testOwner); err != nil || email != testOwner+"@example.com" || identity != "identity-1" {
		t.Fatalf("linked user = %q %q %v", email, identity, err)
	}

	if ok, err := env.svc.queryVerifiedQQ(ctx, testOwner); err != nil || ok {
		t.Fatalf("no QQ = %v %v", ok, err)
	}
	env.db.SocialPlatformInfo.Create().SetPlatform("qq").SetPlatformUserID("100").SetVerified(false).SetUserSocialPlatformInfo(testIntruder).ExecX(ctx)
	if ok, _ := env.svc.queryVerifiedQQ(ctx, testIntruder); ok {
		t.Fatal("unverified QQ accepted")
	}
	env.db.SocialPlatformInfo.Create().SetPlatform("qq").SetPlatformUserID("101").SetVerified(true).SetUserSocialPlatformInfo(testOwner).ExecX(ctx)
	if ok, err := env.svc.queryVerifiedQQ(ctx, testOwner); err != nil || !ok {
		t.Fatalf("verified QQ = %v %v", ok, err)
	}
}

// unlockVault never opens the key for an entry when the owner has none, and
// a key that cannot be created is an internal error.
func TestUnlockVaultCreateFailures(t *testing.T) {
	env := newInheritTestEnv(t)
	ctx := context.Background()
	meta := requestMeta{UserID: testOwner}
	if _, _, p := env.svc.unlockVault(ctx, meta, unlockInput{Password: testOwnerPassword}, false); p != errVaultNotInitialized {
		t.Fatalf("unlock without a key = %v", p)
	}
	// A key appears between the check and the create (a concurrent
	// confirmation): the create fails and nothing is overwritten.
	faults := installIdentityFaults(env)
	var once sync.Once
	faults.onVerify = func() {
		once.Do(func() {
			u, err := env.svc.vault.CreateKey(ctx, testOwner, inheritvault.SecretKindPassword, []byte(testOwnerPassword))
			if err != nil {
				t.Error(err)
				return
			}
			u.Close()
		})
	}
	if _, _, p := env.svc.unlockVault(ctx, meta, unlockInput{Password: testOwnerPassword}, true); p != errInternal {
		t.Fatalf("racing create = %v", p)
	}
	u, res, p := env.svc.unlockVault(ctx, meta, unlockInput{Password: testOwnerPassword}, true)
	if p != nil || res.created {
		t.Fatalf("unlock of the existing key = %v %+v", p, res)
	}
	u.Close()

	// PIN identities must send a PIN.
	env.identity[testIntruder] = false
	if _, _, p := env.svc.unlockVault(ctx, requestMeta{UserID: testIntruder}, unlockInput{Password: "intruder-pass"}, true); p != errPINRequired {
		t.Fatalf("PIN identity with a password = %v", p)
	}
	if _, _, p := env.svc.unlockVault(ctx, requestMeta{UserID: testIntruder}, unlockInput{VaultPIN: strings.Repeat("p", vaultPINMaxLen+1)}, true); p != errPINFormat {
		t.Fatalf("overlong PIN = %v", p)
	}
	if n := env.vaultClient.InheritVaultKey.Query().Where(inheritvaultkey.UserIDEQ(testIntruder)).CountX(ctx); n != 0 {
		t.Fatalf("invalid PIN created a key: %d", n)
	}
}
