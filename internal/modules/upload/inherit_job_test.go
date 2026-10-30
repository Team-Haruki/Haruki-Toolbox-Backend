package upload

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	json "encoding/json/v2"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/inheritvault"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/systemlog"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiSekai "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/game/sekai"
	harukiSMTP "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/smtp"

	"github.com/alicebob/miniredis/v2"
	"github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v3"
)

const (
	testOwner          = "owner-1"
	testIntruder       = "intruder-2"
	testOwnerPassword  = "toolbox-pass-1"
	testGameUID        = int64(164337024457871363)
	testEnteredID      = "ABCDEFGH12345678"
	testEnteredPW      = "entered-pw1"
	testGameCredential = "GAME-CREDENTIAL-SECRET"
	testIssuedID       = "NEWCODE987654321"
)

var errFakeForbidden = harukiSekai.NewAPIError("/inherit/user/<redacted>", "POST", 403, "non-200 response", nil)

type fakeInheritGame struct {
	mu         sync.Mutex
	progress   harukiSekai.Progress
	calls      []string
	previewErr error
	executeErr error
	loginErr   error
	suiteErr   error
	issueFails int
	restrict   *harukiSekai.TransferRestriction
	onExecute  func()
	gotCode    harukiSekai.InheritCode
	loginCred  string
	issuedPW   string
}

func (g *fakeInheritGame) record(call string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, call)
}

func (g *fakeInheritGame) called(call string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, c := range g.calls {
		if c == call {
			return true
		}
	}
	return false
}

func (g *fakeInheritGame) Bootstrap(context.Context) error {
	g.record("bootstrap")
	if g.progress != nil {
		g.progress(harukiSekai.ProgressEvent{Step: harukiSekai.SubStepVersion, Status: harukiSekai.ProgressDone})
	}
	return nil
}

func (g *fakeInheritGame) Preview(_ context.Context, code harukiSekai.InheritCode) (*harukiSekai.InheritPreview, error) {
	g.record("preview")
	g.mu.Lock()
	g.gotCode = code
	g.mu.Unlock()
	if g.previewErr != nil {
		return nil, g.previewErr
	}
	return &harukiSekai.InheritPreview{UserID: testGameUID, Name: "Miku", Rank: 150, Restriction: g.restrict}, nil
}

func (g *fakeInheritGame) Execute(context.Context, harukiSekai.InheritCode) (string, error) {
	g.record("execute")
	if g.onExecute != nil {
		g.onExecute()
	}
	if g.executeErr != nil {
		return "", g.executeErr
	}
	return testGameCredential, nil
}

func (g *fakeInheritGame) Login(_ context.Context, _ int64, credential string) error {
	g.record("login")
	g.mu.Lock()
	g.loginCred = credential
	g.mu.Unlock()
	return g.loginErr
}

func (g *fakeInheritGame) IssueInheritCode(_ context.Context, password string) (string, error) {
	g.record("issue")
	g.mu.Lock()
	defer g.mu.Unlock()
	g.issuedPW = password
	if g.issueFails != 0 {
		if g.issueFails > 0 {
			g.issueFails--
		}
		return "", harukiSekai.NewAPIError("/user/x/inherit", "PUT", 500, "non-200 response", nil)
	}
	return testIssuedID, nil
}

func (g *fakeInheritGame) FetchSuite(context.Context) ([]byte, error) {
	g.record("suite")
	if g.suiteErr != nil {
		return nil, g.suiteErr
	}
	return []byte("suite-bytes"), nil
}

func (g *fakeInheritGame) FetchMysekai(context.Context) ([]byte, error) {
	g.record("mysekai")
	return []byte("mysekai-bytes"), nil
}

func (g *fakeInheritGame) RedactError(err error) error { return err }

type inheritTestEnv struct {
	t           *testing.T
	svc         *inheritService
	app         *fiber.App
	redis       *miniredis.Miniredis
	db          *postgresql.Client
	vaultDB     string
	vaultClient *postgresql.Client
	game        *fakeInheritGame
	games       int
	uploads     int
	mu          sync.Mutex
	mails       []string
	mailBodies  []string
	mailTexts   []string
	identity    map[string]bool // user -> has password
	password    map[string]string
}

func newInheritTestEnv(t *testing.T) *inheritTestEnv {
	t.Helper()
	inheritvault.DefaultKDFParams = inheritvault.KDFParams{Algorithm: "argon2id", Iterations: 1, MemoryKiB: 64, Parallelism: 1}
	env := &inheritTestEnv{
		t: t, redis: miniredis.RunT(t), game: &fakeInheritGame{},
		identity: map[string]bool{testOwner: true, testIntruder: true},
		password: map[string]string{testOwner: testOwnerPassword, testIntruder: "intruder-pass"},
	}
	port, _ := strconv.Atoi(env.redis.Port())
	rdb := harukiRedis.NewRedisClient(harukiConfig.RedisConfig{Host: env.redis.Host(), Port: port}, "test-sign-token")
	t.Cleanup(func() { _ = rdb.Close() })
	env.db = enttest.Open(t, "sqlite3", uniqueUploadAuditSQLiteDSN(t, "inherit-job"))
	t.Cleanup(func() { _ = env.db.Close() })
	env.vaultDB = filepath.Join(t.TempDir(), "vault.db")
	vaultClient := env.openVaultDB()
	env.vaultClient = vaultClient
	for _, id := range []string{testOwner, testIntruder} {
		env.db.User.Create().SetID(id).SetName(id).SetEmail(id + "@example.com").ExecX(context.Background())
		vaultClient.User.Create().SetID(id).SetName(id).SetEmail(id + "@example.com").ExecX(context.Background())
	}
	master, err := inheritvault.NewMasterKey(bytes.Repeat([]byte{9}, 32), 1)
	if err != nil {
		t.Fatal(err)
	}
	api := &harukiAPIHelper.HarukiToolboxRouterHelpers{DBManager: &database.HarukiToolboxDBManager{DB: env.db, Redis: rdb}}
	deps := testUploadDependencies()
	deps.InheritVault = inheritvault.NewStore(vaultClient, rdb.Redis, master)
	svc := newInheritService(api, deps)
	svc.limiter = newInheritConcurrencyLimiter(4)
	svc.breaker = newInheritCircuitBreaker(5, time.Minute, time.Second)
	svc.confirmTimeout = 2 * time.Second
	svc.slotWait = 100 * time.Millisecond
	svc.issueBackoff = func(int) time.Duration { return time.Millisecond }
	svc.newGame = func(_ harukiUtils.SupportedInheritUploadServer, progress harukiSekai.Progress) (inheritGame, error) {
		env.mu.Lock()
		env.games++
		env.mu.Unlock()
		env.game.progress = progress
		return env.game, nil
	}
	svc.upload = func(context.Context, *inheritJob, int64, []byte, []byte) error {
		env.mu.Lock()
		env.uploads++
		env.mu.Unlock()
		return nil
	}
	svc.verifyPassword = func(_ context.Context, identityID, password string) error {
		if env.password[identityID] != password {
			return errPasswordRejected
		}
		return nil
	}
	svc.hasPassword = func(_ context.Context, identityID string) (bool, error) { return env.identity[identityID], nil }
	svc.hasVerifiedQQ = func(_ context.Context, userID string) (bool, error) { return userID == testOwner, nil }
	svc.userEmail = func(_ context.Context, userID string) (string, string, error) {
		return userID + "@example.com", userID, nil
	}
	svc.sendMail = func(to string, msg harukiSMTP.Message) {
		env.mu.Lock()
		env.mails = append(env.mails, to+"|"+msg.Subject)
		env.mailBodies = append(env.mailBodies, msg.HTML+"\n"+msg.Text)
		env.mailTexts = append(env.mailTexts, msg.Text)
		env.mu.Unlock()
	}
	env.svc = svc

	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		if user := c.Get("X-Test-User"); user != "" {
			c.Locals("userID", user)
			c.Locals("emailVerified", true)
		}
		return c.Next()
	})
	app.Post("/api/inherit/jobs", svc.handleCreateJob)
	app.Get("/api/inherit/jobs/:job_id", svc.handleGetJob)
	app.Post("/api/inherit/jobs/:job_id/confirm", svc.handleConfirm)
	app.Post("/api/inherit/jobs/:job_id/cancel", svc.handleCancel)
	app.Post("/api/inherit/jobs/:job_id/ws-ticket", svc.handleWSTicket)
	app.Get("/api/inherit/jobs/:job_id/ws", svc.handleWS)
	app.Get("/api/inherit/vault/key", svc.handleVaultKey)
	app.Post("/api/inherit/vault/key/reset", svc.handleVaultReset)
	app.Get("/api/inherit/vault/entries", svc.handleVaultList)
	app.Post("/api/inherit/vault/entries/:entry_id/reveal", svc.handleVaultReveal)
	app.Post("/api/inherit/vault/entries/:entry_id/reissue", svc.handleVaultReissue)
	app.Delete("/api/inherit/vault/entries/:entry_id", svc.handleVaultDestroy)
	env.app = app
	return env
}

func (env *inheritTestEnv) openVaultDB() *postgresql.Client {
	c := enttest.Open(env.t, "sqlite3", "file:"+env.vaultDB+"?_fk=1&_busy_timeout=5000")
	env.t.Cleanup(func() { _ = c.Close() })
	return c
}

type apiResult struct {
	Status int
	Body   map[string]any
	Raw    string
}

func (r apiResult) data() map[string]any {
	d, _ := r.Body["updatedData"].(map[string]any)
	return d
}

func (r apiResult) code() string {
	c, _ := r.data()["code"].(string)
	return c
}

func (env *inheritTestEnv) call(user, method, path string, body any) apiResult {
	env.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
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
	return out
}

func validJobRequest() map[string]any {
	return map[string]any{
		"server": "jp", "upload_type": "suite", "inherit_id": testEnteredID,
		"inherit_password": testEnteredPW, "new_password_mode": "reuse",
	}
}

func (env *inheritTestEnv) createJob(req map[string]any) string {
	env.t.Helper()
	res := env.call(testOwner, "POST", "/api/inherit/jobs", req)
	if res.Status != http.StatusAccepted {
		env.t.Fatalf("create = %d %s", res.Status, res.Raw)
	}
	return res.data()["job_id"].(string)
}

func (env *inheritTestEnv) job(id string) map[string]any {
	env.t.Helper()
	res := env.call(testOwner, "GET", "/api/inherit/jobs/"+id, nil)
	if res.Status != http.StatusOK {
		env.t.Fatalf("get job = %d %s", res.Status, res.Raw)
	}
	return res.data()
}

func (env *inheritTestEnv) waitStatus(id string, statuses ...string) map[string]any {
	env.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		job := env.job(id)
		for _, s := range statuses {
			if job["status"] == s {
				return job
			}
		}
		if time.Now().After(deadline) {
			env.t.Fatalf("job status = %v, want one of %v: %v", job["status"], statuses, job)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func errorCode(job map[string]any) string {
	e, _ := job["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

func warningCodes(job map[string]any) []string {
	ws, _ := job["warnings"].([]any)
	var out []string
	for _, w := range ws {
		m, _ := w.(map[string]any)
		out = append(out, fmt.Sprint(m["code"]))
	}
	return out
}

func jobStepStatus(job map[string]any, name string) string {
	steps, _ := job["steps"].([]any)
	for _, s := range steps {
		m, _ := s.(map[string]any)
		if m["name"] == name {
			return fmt.Sprint(m["status"])
		}
	}
	return ""
}

func (env *inheritTestEnv) runToSuccess() (string, map[string]any) {
	env.t.Helper()
	id := env.createJob(validJobRequest())
	env.waitStatus(id, "awaiting_confirm")
	if res := env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/confirm", map[string]any{"password": testOwnerPassword}); res.Status != http.StatusAccepted {
		env.t.Fatalf("confirm = %d %s", res.Status, res.Raw)
	}
	return id, env.waitStatus(id, "succeeded")
}

func (env *inheritTestEnv) vaultEntries(user string) []any {
	env.t.Helper()
	res := env.call(user, "GET", "/api/inherit/vault/entries", nil)
	if res.Status != http.StatusOK {
		env.t.Fatalf("list = %d %s", res.Status, res.Raw)
	}
	entries, _ := res.Body["updatedData"].([]any)
	return entries
}

func TestCreateJobRejectsBadInputBeforeTheGame(t *testing.T) {
	env := newInheritTestEnv(t)
	cases := map[string]struct {
		mutate func(map[string]any)
		code   string
	}{
		"server":            {func(m map[string]any) { m["server"] = "tw" }, "invalid_server"},
		"upload type":       {func(m map[string]any) { m["upload_type"] = "birthday" }, "invalid_upload_type"},
		"inherit id short":  {func(m map[string]any) { m["inherit_id"] = "ABC" }, "inherit_id_invalid"},
		"inherit id symbol": {func(m map[string]any) { m["inherit_id"] = "ABCDEFGH1234567!" }, "inherit_id_invalid"},
		"no password":       {func(m map[string]any) { m["inherit_password"] = "" }, "inherit_password_invalid"},
		"mode":              {func(m map[string]any) { m["new_password_mode"] = "keep" }, "new_password_mode_invalid"},
		"reuse 7 chars":     {func(m map[string]any) { m["inherit_password"] = "1234567" }, "reuse_password_invalid"},
		"reuse 17 chars":    {func(m map[string]any) { m["inherit_password"] = "12345678901234567" }, "reuse_password_invalid"},
		"reuse with space":  {func(m map[string]any) { m["inherit_password"] = "pass word1" }, "reuse_password_invalid"},
	}
	for name, tc := range cases {
		req := validJobRequest()
		tc.mutate(req)
		res := env.call(testOwner, "POST", "/api/inherit/jobs", req)
		if res.Status != http.StatusBadRequest || res.code() != tc.code {
			t.Errorf("%s: %d %s, want 400 %s", name, res.Status, res.Raw, tc.code)
		}
	}
	// A short password is fine when the new code gets a random one.
	req := validJobRequest()
	req["inherit_password"], req["new_password_mode"] = "1234", "random"
	if res := env.call(testOwner, "POST", "/api/inherit/jobs", req); res.Status != http.StatusAccepted {
		t.Fatalf("random mode with short password = %d %s", res.Status, res.Raw)
	}
	if res := env.call("", "POST", "/api/inherit/jobs", validJobRequest()); res.Status != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", res.Status)
	}
	env.mu.Lock()
	defer env.mu.Unlock()
	if env.games != 1 {
		t.Fatalf("game sessions = %d, want only the valid one", env.games)
	}
}

func TestPasswordValidationRange(t *testing.T) {
	for pw, want := range map[string]bool{"1234567": false, "12345678": true, "1234567890123456": true, "12345678901234567": false, "abc\tdefgh": false, "ÄÖÜäöüßxy": false} {
		if got := validInheritPassword(pw); got != want {
			t.Errorf("validInheritPassword(%q) = %v", pw, got)
		}
	}
	for range 50 {
		pw := randomInheritPassword()
		if len(pw) != 12 || !validInheritPassword(pw) || strings.Trim(pw, randomPasswordAlphabet) != "" {
			t.Fatalf("random password %q", pw)
		}
	}
}

func TestMysekaiRequiresVerifiedQQ(t *testing.T) {
	env := newInheritTestEnv(t)
	req := validJobRequest()
	req["upload_type"] = "mysekai"
	if res := env.call(testIntruder, "POST", "/api/inherit/jobs", req); res.Status != http.StatusForbidden || res.code() != "mysekai_requires_qq" {
		t.Fatalf("no QQ = %d %s", res.Status, res.Raw)
	}
}

func TestJobRunsToSuccessAndHandsBackTheAccount(t *testing.T) {
	env := newInheritTestEnv(t)
	id := env.createJob(validJobRequest())
	waiting := env.waitStatus(id, "awaiting_confirm")
	if env.game.called("execute") {
		t.Fatal("executed before confirmation")
	}
	preview, _ := waiting["preview"].(map[string]any)
	if preview["nickname"] != "Miku" || preview["game_user_id_masked"] != inheritvault.MaskGameUserID(testGameUID) {
		t.Fatalf("preview = %v", preview)
	}
	if unlock, _ := waiting["unlock"].(map[string]any); unlock["kind"] != "password" || unlock["initialized"] != false {
		t.Fatalf("unlock = %v", unlock)
	}
	if waiting["confirm_deadline"] == nil {
		t.Fatal("no confirm deadline")
	}
	if strings.Contains(fmt.Sprint(waiting), strconv.FormatInt(testGameUID, 10)) {
		t.Fatal("snapshot exposes the full game user ID")
	}
	if res := env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/confirm", map[string]any{"password": "wrong"}); res.Status != http.StatusForbidden || res.code() != "password_invalid" {
		t.Fatalf("wrong password = %d %s", res.Status, res.Raw)
	}
	if res := env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/confirm", map[string]any{"password": testOwnerPassword}); res.Status != http.StatusAccepted {
		t.Fatalf("confirm = %d %s", res.Status, res.Raw)
	}
	done := env.waitStatus(id, "succeeded")
	code, _ := done["new_code"].(map[string]any)
	if code["inherit_id"] != testIssuedID || code["password"] != testEnteredPW {
		t.Fatalf("new code = %v", code)
	}
	for _, step := range inheritJobSteps {
		want := "done"
		if step == stepFetchMysekai {
			want = "skipped"
		}
		if got := jobStepStatus(done, step); got != want {
			t.Errorf("step %s = %s, want %s", step, got, want)
		}
	}
	g := env.game
	if want := []string{"bootstrap", "preview", "execute", "login", "issue", "suite"}; fmt.Sprint(g.calls) != fmt.Sprint(want) {
		t.Fatalf("game calls = %v", g.calls)
	}
	if g.loginCred != testGameCredential || g.issuedPW != testEnteredPW {
		t.Fatalf("login with %q, issued with %q", g.loginCred, g.issuedPW)
	}
	if env.uploads != 1 {
		t.Fatalf("uploads = %d", env.uploads)
	}
	// The job record in Redis never holds a secret in the clear.
	raw, err := env.redis.Get(inheritJobKeyPrefix + id)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{testGameCredential, testEnteredPW, testEnteredID, testIssuedID, strconv.FormatInt(testGameUID, 10)} {
		if strings.Contains(raw, secret) {
			t.Fatalf("job record holds %q", secret)
		}
	}
	// The vault entry opens with the owner's password, and the code reveal
	// mails the owner and is audited.
	entries := env.vaultEntries(testOwner)
	if len(entries) != 1 {
		t.Fatalf("entries = %v", entries)
	}
	entry := entries[0].(map[string]any)
	if entry["has_code"] != true || entry["readable"] != true || entry["id"] != done["vault_entry_id"] {
		t.Fatalf("entry = %v", entry)
	}
	reveal := env.call(testOwner, "POST", "/api/inherit/vault/entries/"+entry["id"].(string)+"/reveal", map[string]any{"password": testOwnerPassword})
	if reveal.Status != http.StatusOK || reveal.data()["inherit_id"] != testIssuedID || reveal.data()["password"] != testEnteredPW {
		t.Fatalf("reveal = %d %s", reveal.Status, reveal.Raw)
	}
	waitFor(t, func() bool { env.mu.Lock(); defer env.mu.Unlock(); return len(env.mails) == 1 })
	env.mu.Lock()
	for _, body := range env.mailBodies {
		for _, secret := range []string{testGameCredential, testEnteredPW, testEnteredID, testIssuedID, strconv.FormatInt(testGameUID, 10)} {
			if strings.Contains(body, secret) {
				env.mu.Unlock()
				t.Fatalf("notification mail holds %q", secret)
			}
		}
	}
	env.mu.Unlock()
	for _, action := range []string{"inherit.job.create", "inherit.job.confirm", "inherit.vault.key.create", "inherit.vault.store", "inherit.vault.reveal"} {
		if n := env.db.SystemLog.Query().Where(systemlog.ActionEQ(action)).CountX(context.Background()); n != 1 {
			t.Errorf("audit %s rows = %d", action, n)
		}
	}
	if n := env.db.SystemLog.Query().Where(systemlog.ActionEQ("inherit.vault.reveal")).CountX(context.Background()); n == 1 {
		row := env.db.SystemLog.Query().Where(systemlog.ActionEQ("inherit.vault.reveal")).OnlyX(context.Background())
		if strings.Contains(fmt.Sprint(row.Metadata), testIssuedID) || strings.Contains(fmt.Sprint(row.Metadata), strconv.FormatInt(testGameUID, 10)) {
			t.Fatalf("audit metadata leaks: %v", row.Metadata)
		}
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRandomModeIssuesAFreshPassword(t *testing.T) {
	env := newInheritTestEnv(t)
	req := validJobRequest()
	req["new_password_mode"] = "random"
	id := env.createJob(req)
	env.waitStatus(id, "awaiting_confirm")
	env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/confirm", map[string]any{"password": testOwnerPassword})
	done := env.waitStatus(id, "succeeded")
	pw := done["new_code"].(map[string]any)["password"].(string)
	if pw == testEnteredPW || len(pw) != 12 || pw != env.game.issuedPW {
		t.Fatalf("random password %q (issued %q)", pw, env.game.issuedPW)
	}
}

func TestConfirmTimeoutCancelsWithoutExecuting(t *testing.T) {
	env := newInheritTestEnv(t)
	env.svc.confirmTimeout = 50 * time.Millisecond
	id := env.createJob(validJobRequest())
	job := env.waitStatus(id, "cancelled")
	if errorCode(job) != codeConfirmTimeout || env.game.called("execute") {
		t.Fatalf("job = %v, calls = %v", job, env.game.calls)
	}
	if res := env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/confirm", map[string]any{"password": testOwnerPassword}); res.Status != http.StatusConflict {
		t.Fatalf("late confirm = %d", res.Status)
	}
	// The active-job lock is released: a new job can start.
	env.createJob(validJobRequest())
}

// TestCancelOnlyBeforeExecute checks that only the owner can cancel, that a
// job can be cancelled while it waits for confirmation and not after, and
// that the lock is free by the time the cancellation is published.
func TestCancelOnlyBeforeExecute(t *testing.T) {
	env := newInheritTestEnv(t)
	id := env.createJob(validJobRequest())
	env.waitStatus(id, "awaiting_confirm")
	rt := env.svc.registry.get(id)
	if rt == nil {
		t.Fatal("job not registered")
	}
	events, stop := rt.subscribe()
	defer stop()
	if res := env.call(testIntruder, "POST", "/api/inherit/jobs/"+id+"/cancel", nil); res.Status != http.StatusNotFound {
		t.Fatalf("intruder cancel = %d", res.Status)
	}
	if res := env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/cancel", nil); res.Status != http.StatusOK {
		t.Fatalf("cancel = %d %s", res.Status, res.Raw)
	}
	// The lock is released before the cancelled state is published, so the
	// next create below never races the finished job.
	lockAtPublish := waitPublishedStatus(t, events, jobStatusCancelled, func() bool {
		return env.redis.Exists(inheritActiveKeyPrefix + testOwner)
	})
	if lockAtPublish {
		t.Fatal("the cancelled state was published while the job still held the active-job lock")
	}
	job := env.waitStatus(id, "cancelled")
	if errorCode(job) != codeCancelled || env.game.called("execute") {
		t.Fatalf("job = %v", job)
	}

	// Once confirmed, the job can no longer be cancelled.
	env.game = &fakeInheritGame{}
	block := make(chan struct{})
	env.game.onExecute = func() { <-block }
	id = env.createJob(validJobRequest())
	env.waitStatus(id, "awaiting_confirm")
	env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/confirm", map[string]any{"password": testOwnerPassword})
	waitFor(t, func() bool { return env.game.called("execute") })
	if res := env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/cancel", nil); res.Status != http.StatusConflict || res.code() != "cannot_cancel" {
		t.Fatalf("cancel after confirm = %d %s", res.Status, res.Raw)
	}
	close(block)
	env.waitStatus(id, "succeeded")
}

// Regression: creating a job right after cancelling one used to answer
// job_already_running with the cancelled job's ID while that job was still
// winding down, and the client then resumed the cancelled job.
func TestCreateRightAfterCancelStartsANewJob(t *testing.T) {
	env := newInheritTestEnv(t)
	first := env.createJob(validJobRequest())
	env.waitStatus(first, "awaiting_confirm")
	if res := env.call(testOwner, "POST", "/api/inherit/jobs/"+first+"/cancel", nil); res.Status != http.StatusOK {
		t.Fatalf("cancel = %d %s", res.Status, res.Raw)
	}
	// No wait: the cancelled job may not have reached its terminal state yet.
	second := env.createJob(validJobRequest())
	if second == first {
		t.Fatal("create returned the cancelled job")
	}
	if job := env.waitStatus(first, "cancelled"); errorCode(job) != codeCancelled {
		t.Fatalf("first job = %v", job)
	}
	env.waitStatus(second, "awaiting_confirm")

	// Once the cancelled job is fully gone, its release has left the new
	// job's lock in place, and the conflict names the new job.
	waitFor(t, func() bool { return env.svc.registry.get(first) == nil })
	if got, err := env.redis.Get(inheritActiveKeyPrefix + testOwner); err != nil || got != second {
		t.Fatalf("lock = %q, %v; want %s", got, err, second)
	}
	res := env.call(testOwner, "POST", "/api/inherit/jobs", validJobRequest())
	if res.Status != http.StatusConflict || res.code() != "job_already_running" || res.data()["job_id"] != second {
		t.Fatalf("third job = %d %s", res.Status, res.Raw)
	}
}

// A lock that still points to a finished job, or to one cancelled before it
// executed, never blocks the owner and is never named in job_already_running.
func TestLockOfAFinishedJobIsTakenOver(t *testing.T) {
	cases := map[string]func(env *inheritTestEnv, job *inheritJob){
		"finished, still registered": func(env *inheritTestEnv, job *inheritJob) {
			job.Status = jobStatusCancelled
			env.svc.registry.add(&jobRuntime{job: job})
		},
		"cancelled, winding down": func(env *inheritTestEnv, job *inheritJob) {
			job.Status = jobStatusAwaitingConfirm
			env.svc.registry.add(&jobRuntime{job: job, cancelled: true})
		},
		"finished, store only": func(env *inheritTestEnv, job *inheritJob) {
			job.Status = jobStatusSucceeded
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			env := newInheritTestEnv(t)
			old := newInheritJob(testOwner, "jp", "suite", "random", time.Now().UTC())
			setup(env, old)
			if err := env.svc.jobs.save(context.Background(), old); err != nil {
				t.Fatal(err)
			}
			env.redis.Set(inheritActiveKeyPrefix+testOwner, old.ID)
			res := env.call(testOwner, "POST", "/api/inherit/jobs", validJobRequest())
			if res.Status != http.StatusAccepted {
				t.Fatalf("create = %d %s", res.Status, res.Raw)
			}
			id, _ := res.data()["job_id"].(string)
			if id == "" || id == old.ID {
				t.Fatalf("job_id = %q", id)
			}
			if got, _ := env.redis.Get(inheritActiveKeyPrefix + testOwner); got != id {
				t.Fatalf("lock = %q, want %s", got, id)
			}
			env.waitStatus(id, "awaiting_confirm")
		})
	}
}

// waitPublishedStatus reads the job's published snapshots until one shows
// status, and returns probe's result taken as soon as that snapshot arrives.
func waitPublishedStatus(t *testing.T, events <-chan []byte, status inheritJobStatus, probe func() bool) bool {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case raw, ok := <-events:
			if !ok {
				t.Fatalf("subscription closed before status %s", status)
			}
			var msg struct {
				Job *struct {
					Status inheritJobStatus `json:"status"`
				} `json:"job"`
			}
			if err := json.Unmarshal(raw, &msg); err != nil {
				t.Fatal(err)
			}
			if msg.Job != nil && msg.Job.Status == status {
				return probe()
			}
		case <-timeout:
			t.Fatalf("status %s never published", status)
		}
	}
}

// Two requests that found the same stale holder cannot both take the lock
// over: exactly one swap wins and the lock names only the winner.
func TestStaleLockSwapHasOneWinner(t *testing.T) {
	env := newInheritTestEnv(t)
	ctx := context.Background()
	key := inheritActiveKeyPrefix + testOwner
	env.redis.Set(key, "stale-job")
	ttl := inheritActiveLockTTL.Milliseconds()
	first, err := env.svc.redis.Eval(ctx, activeLockSwapScript, []string{key}, "stale-job", "job-a", ttl).Int()
	if err != nil {
		t.Fatal(err)
	}
	second, err := env.svc.redis.Eval(ctx, activeLockSwapScript, []string{key}, "stale-job", "job-b", ttl).Int()
	if err != nil {
		t.Fatal(err)
	}
	if first != 1 || second != 0 {
		t.Fatalf("swaps = %d, %d; want exactly the first to win", first, second)
	}
	if got, _ := env.redis.Get(key); got != "job-a" {
		t.Fatalf("lock = %q, want job-a", got)
	}
	if ttl := env.redis.TTL(key); ttl <= 0 {
		t.Fatalf("swapped lock has no TTL (%v)", ttl)
	}
}

// A lock that names a job still being created (registered, not yet stored)
// is not stale: a concurrent create for the same owner is refused.
func TestLockOfAJobBeingCreatedIsNotTakenOver(t *testing.T) {
	env := newInheritTestEnv(t)
	pending := newInheritJob(testOwner, "jp", "suite", "random", time.Now().UTC())
	env.svc.registry.add(&jobRuntime{job: pending})
	env.redis.Set(inheritActiveKeyPrefix+testOwner, pending.ID)
	res := env.call(testOwner, "POST", "/api/inherit/jobs", validJobRequest())
	if res.Status != http.StatusConflict || res.code() != "job_already_running" || res.data()["job_id"] != pending.ID {
		t.Fatalf("create = %d %s", res.Status, res.Raw)
	}
	if got, _ := env.redis.Get(inheritActiveKeyPrefix + testOwner); got != pending.ID {
		t.Fatalf("lock = %q, want %s", got, pending.ID)
	}
}

// Releasing a lock that another job has taken over leaves it alone.
func TestReleaseActiveOnlyDropsItsOwnLock(t *testing.T) {
	env := newInheritTestEnv(t)
	key := inheritActiveKeyPrefix + testOwner
	env.redis.Set(key, "job-b")
	env.svc.releaseActive(testOwner, "job-a")
	if got, _ := env.redis.Get(key); got != "job-b" {
		t.Fatalf("lock = %q after another job's release", got)
	}
	env.svc.releaseActive(testOwner, "job-b")
	if env.redis.Exists(key) {
		t.Fatal("holder's release kept the lock")
	}
}

func TestPreviewBare403IsInheritInvalid(t *testing.T) {
	env := newInheritTestEnv(t)
	env.game.previewErr = errFakeForbidden
	id := env.createJob(validJobRequest())
	job := env.waitStatus(id, "failed")
	if errorCode(job) != codeInheritInvalid || jobStepStatus(job, stepPreview) != "failed" {
		t.Fatalf("job = %v", job)
	}
	if msg := job["error"].(map[string]any)["message"]; msg != "继承码无效、已使用或已过期" {
		t.Fatalf("message = %v", msg)
	}
	for err, want := range map[error]string{
		errFakeForbidden: codeInheritInvalid,
		harukiSekai.NewAPIError("/x", "POST", 405, "", nil): codeInheritRestricted,
		harukiSekai.NewAPIError("/x", "POST", 0, "", nil):   codeGameUnavailable,
		harukiSekai.NewAPIError("/x", "POST", 503, "", nil): codeGameUnavailable,
		harukiSekai.NewAPIError("/x", "POST", 400, "", nil): codeGameError,
		harukiSekai.ErrMaintenance:                          codeGameMaintenance,
		context.DeadlineExceeded:                            codeGameUnavailable,
	} {
		if got := gameFailureCode(err); got != want {
			t.Errorf("gameFailureCode(%v) = %s, want %s", err, got, want)
		}
	}
}

func TestNoTransfersLeftFailsBeforeConfirmation(t *testing.T) {
	env := newInheritTestEnv(t)
	env.game.restrict = &harukiSekai.TransferRestriction{IsRestrictDeviceTransfer: true, RestTransferCount: 0}
	job := env.waitStatus(env.createJob(validJobRequest()), "failed")
	if errorCode(job) != codeTransferRestricted || job["preview"] == nil || env.game.called("execute") {
		t.Fatalf("job = %v", job)
	}
}

func TestExecute403Fails(t *testing.T) {
	env := newInheritTestEnv(t)
	env.game.executeErr = errFakeForbidden
	id := env.createJob(validJobRequest())
	env.waitStatus(id, "awaiting_confirm")
	env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/confirm", map[string]any{"password": testOwnerPassword})
	if job := env.waitStatus(id, "failed"); errorCode(job) != codeInheritInvalid || env.game.called("login") {
		t.Fatalf("job = %v", job)
	}
}

// A failure after the new code was issued still shows the code.
func TestFailureAfterIssueStillDeliversTheCode(t *testing.T) {
	env := newInheritTestEnv(t)
	env.game.suiteErr = harukiSekai.NewDataRetrievalError("suite", "api_call", "failed", harukiSekai.NewAPIError("/suite", "GET", 500, "", nil))
	id := env.createJob(validJobRequest())
	env.waitStatus(id, "awaiting_confirm")
	env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/confirm", map[string]any{"password": testOwnerPassword})
	job := env.waitStatus(id, "failed")
	if errorCode(job) != codeFetchFailed {
		t.Fatalf("error = %v", job["error"])
	}
	if code, _ := job["new_code"].(map[string]any); code["inherit_id"] != testIssuedID {
		t.Fatalf("new code = %v", job["new_code"])
	}
}

// Login failing after the executing call leaves the credential stored and
// readable; the owner re-issues from the vault with it.
func TestLoginFailureLeavesAReadableCredential(t *testing.T) {
	env := newInheritTestEnv(t)
	env.game.loginErr = harukiSekai.NewAPIError("/user/x/auth", "PUT", 0, "request failed", nil)
	id := env.createJob(validJobRequest())
	env.waitStatus(id, "awaiting_confirm")
	env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/confirm", map[string]any{"password": testOwnerPassword})
	job := env.waitStatus(id, "failed")
	if errorCode(job) != codeLoginFailed || job["vault_entry_id"] == nil || job["new_code"] != nil {
		t.Fatalf("job = %v", job)
	}
	entryID := job["vault_entry_id"].(string)
	if res := env.call(testOwner, "POST", "/api/inherit/vault/entries/"+entryID+"/reveal", map[string]any{"password": testOwnerPassword}); res.code() != "code_not_available" {
		t.Fatalf("reveal without code = %d %s", res.Status, res.Raw)
	}
	if res := env.call(testOwner, "POST", "/api/inherit/vault/entries/"+entryID+"/reissue", map[string]any{"password": testOwnerPassword, "new_password_mode": "reuse"}); res.code() != "no_previous_password" {
		t.Fatalf("reuse without a stored code = %d %s", res.Status, res.Raw)
	}
	env.game = &fakeInheritGame{}
	res := env.call(testOwner, "POST", "/api/inherit/vault/entries/"+entryID+"/reissue", map[string]any{"password": testOwnerPassword, "new_password_mode": "random"})
	if res.Status != http.StatusOK || res.data()["inherit_id"] != testIssuedID {
		t.Fatalf("reissue = %d %s", res.Status, res.Raw)
	}
	if env.game.loginCred != testGameCredential || env.game.called("execute") {
		t.Fatalf("reissue logged in with %q, calls %v", env.game.loginCred, env.game.calls)
	}
	reveal := env.call(testOwner, "POST", "/api/inherit/vault/entries/"+entryID+"/reveal", map[string]any{"password": testOwnerPassword})
	if reveal.data()["password"] != res.data()["password"] {
		t.Fatalf("stored code not replaced: %s", reveal.Raw)
	}
}

func TestIssueFailureWarnsButKeepsGoing(t *testing.T) {
	env := newInheritTestEnv(t)
	env.game.issueFails = -1
	id := env.createJob(validJobRequest())
	env.waitStatus(id, "awaiting_confirm")
	env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/confirm", map[string]any{"password": testOwnerPassword})
	job := env.waitStatus(id, "succeeded")
	if fmt.Sprint(warningCodes(job)) != fmt.Sprint([]string{codeIssueCodeFailed}) || jobStepStatus(job, stepIssueNewCode) != "failed" || job["new_code"] != nil {
		t.Fatalf("job = %v", job)
	}
	if n := strings.Count(fmt.Sprint(env.game.calls), "issue"); n != inheritIssueAttempts {
		t.Fatalf("issue attempts = %d", n)
	}
	if entries := env.vaultEntries(testOwner); len(entries) != 1 {
		t.Fatalf("entries = %v", entries)
	}

	// Two failures then success: retried, no warning.
	env.game = &fakeInheritGame{issueFails: 2}
	_, job = env.runToSuccess()
	if len(warningCodes(job)) != 0 || job["new_code"] == nil {
		t.Fatalf("job = %v", job)
	}
}

// PostgreSQL going down right after the executing call: the sealed
// credential is parked in Redis, the job says so, and the next vault read
// moves it into PostgreSQL.
func TestDatabaseDownAfterExecuteUsesRedisFallback(t *testing.T) {
	env := newInheritTestEnv(t)
	id := env.createJob(validJobRequest())
	env.waitStatus(id, "awaiting_confirm")
	// The vault database goes away between the executing call and the
	// credential write (the confirm step itself still reached it).
	env.game.onExecute = func() { _ = env.vaultClient.Close() }
	env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/confirm", map[string]any{"password": testOwnerPassword})
	job := env.waitStatus(id, "succeeded")
	if !strings.Contains(fmt.Sprint(warningCodes(job)), codeVaultFallback) || job["new_code"] == nil {
		t.Fatalf("job = %v", job)
	}
	entryID := job["vault_entry_id"].(string)
	parked, err := env.redis.Get("inherit:vault:fallback:" + entryID)
	if err != nil {
		t.Fatal("credential not parked in Redis")
	}
	if strings.Contains(parked, testGameCredential) || strings.Contains(parked, testIssuedID) {
		t.Fatal("parked record holds plaintext")
	}

	// PostgreSQL is back: the next vault read moves the record over.
	waitFor(t, func() bool { return env.svc.registry.get(id) == nil })
	env.svc.vault = inheritvault.NewStore(env.openVaultDB(), env.svc.redis, env.svc.vault.Master())
	if entries := env.vaultEntries(testOwner); len(entries) != 1 || entries[0].(map[string]any)["pending"] != false {
		t.Fatalf("entries = %v", entries)
	}
	if env.redis.Exists("inherit:vault:fallback:" + entryID) {
		t.Fatal("fallback not reconciled")
	}
	reveal := env.call(testOwner, "POST", "/api/inherit/vault/entries/"+entryID+"/reveal", map[string]any{"password": testOwnerPassword})
	if reveal.Status != http.StatusOK || reveal.data()["inherit_id"] != testIssuedID {
		t.Fatalf("reveal = %d %s", reveal.Status, reveal.Raw)
	}
}

func TestOwnershipIsEnforced(t *testing.T) {
	env := newInheritTestEnv(t)
	id, job := env.runToSuccess()
	entryID := job["vault_entry_id"].(string)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/inherit/jobs/" + id},
		{"POST", "/api/inherit/jobs/" + id + "/ws-ticket"},
		{"POST", "/api/inherit/jobs/" + id + "/confirm"},
		{"POST", "/api/inherit/vault/entries/" + entryID + "/reveal"},
		{"POST", "/api/inherit/vault/entries/" + entryID + "/reissue"},
		{"DELETE", "/api/inherit/vault/entries/" + entryID},
	} {
		res := env.call(testIntruder, tc.method, tc.path, map[string]any{"password": "intruder-pass", "new_password_mode": "random"})
		if res.Status != http.StatusNotFound {
			t.Errorf("%s %s by another user = %d %s", tc.method, tc.path, res.Status, res.Raw)
		}
	}
	if entries := env.vaultEntries(testIntruder); len(entries) != 0 {
		t.Fatalf("intruder sees %v", entries)
	}
	// Destroy, then nothing is readable.
	if res := env.call(testOwner, "DELETE", "/api/inherit/vault/entries/"+entryID, nil); res.Status != http.StatusOK {
		t.Fatalf("destroy = %d %s", res.Status, res.Raw)
	}
	if res := env.call(testOwner, "POST", "/api/inherit/vault/entries/"+entryID+"/reveal", map[string]any{"password": testOwnerPassword}); res.Status != http.StatusNotFound {
		t.Fatalf("reveal after destroy = %d", res.Status)
	}
}

// After a password change Kratos accepts the new password but it does not
// open the key: the owner gives the previous one once to carry it over, or
// resets the vault.
func TestChangedPasswordNeedsThePreviousOneOrAReset(t *testing.T) {
	env := newInheritTestEnv(t)
	_, job := env.runToSuccess()
	entryID := job["vault_entry_id"].(string)
	env.password[testOwner] = "changed-pass-2"
	res := env.call(testOwner, "POST", "/api/inherit/vault/entries/"+entryID+"/reveal", map[string]any{"password": "changed-pass-2"})
	if res.Status != http.StatusConflict || res.code() != "vault_password_changed" {
		t.Fatalf("after change = %d %s", res.Status, res.Raw)
	}
	res = env.call(testOwner, "POST", "/api/inherit/vault/entries/"+entryID+"/reveal", map[string]any{"password": "changed-pass-2", "previous_password": "not-it"})
	if res.code() != "previous_password_invalid" {
		t.Fatalf("wrong previous = %d %s", res.Status, res.Raw)
	}
	res = env.call(testOwner, "POST", "/api/inherit/vault/entries/"+entryID+"/reveal", map[string]any{"password": "changed-pass-2", "previous_password": testOwnerPassword})
	if res.Status != http.StatusOK {
		t.Fatalf("rewrap = %d %s", res.Status, res.Raw)
	}
	if res := env.call(testOwner, "POST", "/api/inherit/vault/entries/"+entryID+"/reveal", map[string]any{"password": "changed-pass-2"}); res.Status != http.StatusOK {
		t.Fatalf("after rewrap = %d %s", res.Status, res.Raw)
	}

	// A reset (the way out after a recovery-flow password reset) destroys
	// everything.
	if res := env.call(testOwner, "POST", "/api/inherit/vault/key/reset", map[string]any{"password": "changed-pass-2"}); res.code() != "reset_confirmation_required" {
		t.Fatalf("reset without confirm = %s", res.Raw)
	}
	res = env.call(testOwner, "POST", "/api/inherit/vault/key/reset", map[string]any{"password": "changed-pass-2", "confirm": true})
	if res.Status != http.StatusOK || res.data()["destroyed_entries"] != float64(1) {
		t.Fatalf("reset = %d %s", res.Status, res.Raw)
	}
	if entries := env.vaultEntries(testOwner); len(entries) != 0 {
		t.Fatalf("entries after reset = %v", entries)
	}
	if key := env.call(testOwner, "GET", "/api/inherit/vault/key", nil); key.data()["initialized"] != false {
		t.Fatalf("key after reset = %s", key.Raw)
	}
}

// Identities without a password (social or passkey sign-in) set a vault PIN
// at their first confirmation.
func TestPasswordlessIdentityUsesAVaultPIN(t *testing.T) {
	env := newInheritTestEnv(t)
	env.identity[testOwner] = false
	if key := env.call(testOwner, "GET", "/api/inherit/vault/key", nil); key.data()["kind"] != "pin" || key.data()["initialized"] != false {
		t.Fatalf("key = %s", key.Raw)
	}
	id := env.createJob(validJobRequest())
	job := env.waitStatus(id, "awaiting_confirm")
	if job["unlock"].(map[string]any)["kind"] != "pin" {
		t.Fatalf("unlock = %v", job["unlock"])
	}
	if res := env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/confirm", map[string]any{"vault_pin": "short"}); res.code() != "vault_pin_invalid_format" {
		t.Fatalf("short pin = %s", res.Raw)
	}
	if res := env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/confirm", map[string]any{"vault_pin": "my-vault-pin"}); res.Status != http.StatusAccepted {
		t.Fatalf("confirm = %d %s", res.Status, res.Raw)
	}
	job = env.waitStatus(id, "succeeded")
	entryID := job["vault_entry_id"].(string)
	if res := env.call(testOwner, "POST", "/api/inherit/vault/entries/"+entryID+"/reveal", map[string]any{"vault_pin": "wrong-vault-pin"}); res.code() != "vault_pin_invalid" {
		t.Fatalf("wrong pin = %s", res.Raw)
	}
	if res := env.call(testOwner, "POST", "/api/inherit/vault/entries/"+entryID+"/reveal", map[string]any{"vault_pin": "my-vault-pin"}); res.Status != http.StatusOK {
		t.Fatalf("reveal = %d %s", res.Status, res.Raw)
	}
}

func TestUnlockAttemptsAreLimited(t *testing.T) {
	env := newInheritTestEnv(t)
	_, job := env.runToSuccess()
	entryID := job["vault_entry_id"].(string)
	var last apiResult
	for range unlockAttemptLimit + 1 {
		last = env.call(testOwner, "POST", "/api/inherit/vault/entries/"+entryID+"/reveal", map[string]any{"password": "wrong"})
	}
	if last.Status != http.StatusTooManyRequests || last.code() != "unlock_rate_limited" {
		t.Fatalf("after %d failures = %d %s", unlockAttemptLimit+1, last.Status, last.Raw)
	}
}

func TestOneActiveJobPerUser(t *testing.T) {
	env := newInheritTestEnv(t)
	id := env.createJob(validJobRequest())
	env.waitStatus(id, "awaiting_confirm")
	res := env.call(testOwner, "POST", "/api/inherit/jobs", validJobRequest())
	if res.Status != http.StatusConflict || res.code() != "job_already_running" {
		t.Fatalf("second job = %d %s", res.Status, res.Raw)
	}
	// The conflict names the owner's running job so a client that lost its
	// ID can reattach to it.
	if got := res.data()["job_id"]; got != id {
		t.Fatalf("job_id = %v, want %s", got, id)
	}
	if got := env.job(id); got["status"] != "awaiting_confirm" {
		t.Fatalf("running job = %v", got["status"])
	}
}

// A job left by an earlier start of this process that is still not finished
// holds the lock too; the conflict names it as well.
func TestRunningJobFromStoreIsReportedToItsOwner(t *testing.T) {
	env := newInheritTestEnv(t)
	job := newInheritJob(testOwner, "jp", "suite", "random", time.Now().UTC())
	job.Instance = inheritInstanceID
	job.Status = jobStatusAwaitingConfirm
	if err := env.svc.jobs.save(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	env.redis.Set(inheritActiveKeyPrefix+testOwner, job.ID)
	res := env.call(testOwner, "POST", "/api/inherit/jobs", validJobRequest())
	if res.Status != http.StatusConflict || res.code() != "job_already_running" || res.data()["job_id"] != job.ID {
		t.Fatalf("second job = %d %s", res.Status, res.Raw)
	}
}

func TestOwnedJobIDHidesOtherOwners(t *testing.T) {
	if got := ownedJobID("j1", "owner", "owner"); got != "j1" {
		t.Fatalf("own job = %q", got)
	}
	if got := ownedJobID("j1", "someone-else", "owner"); got != "" {
		t.Fatalf("foreign job = %q, want empty", got)
	}
}

// A job record left behind by a stopped process is reported as interrupted,
// and its active-job lock does not block the owner.
func TestJobFromAStoppedProcessIsInterrupted(t *testing.T) {
	env := newInheritTestEnv(t)
	job := newInheritJob(testOwner, "jp", "suite", "random", time.Now().UTC())
	job.Instance = "another-process"
	job.Status = jobStatusAwaitingConfirm
	if err := env.svc.jobs.save(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	env.redis.Set(inheritActiveKeyPrefix+testOwner, job.ID)
	env.createJob(validJobRequest())
	got := env.job(job.ID)
	if got["status"] != "failed" || errorCode(got) != codeJobInterrupted {
		t.Fatalf("job = %v", got)
	}
}

func TestWSTicketIsOneTimeAndOwnerBound(t *testing.T) {
	env := newInheritTestEnv(t)
	ctx := context.Background()
	key := wsTicketKey("t1")
	env.redis.Set(key, testOwner+"|job1")
	if env.svc.redeemWSTicket(ctx, "t1", testIntruder, "job1") {
		t.Fatal("ticket redeemed by another user")
	}
	env.redis.Set(key, testOwner+"|job1")
	if env.svc.redeemWSTicket(ctx, "t1", testOwner, "job2") {
		t.Fatal("ticket redeemed for another job")
	}
	env.redis.Set(key, testOwner+"|job1")
	if !env.svc.redeemWSTicket(ctx, "t1", testOwner, "job1") || env.svc.redeemWSTicket(ctx, "t1", testOwner, "job1") {
		t.Fatal("ticket not exactly once")
	}
}

// The WebSocket sends a snapshot, then events; confirm goes over the socket.
func TestWebSocketStreamsTheJobAndTakesConfirm(t *testing.T) {
	env := newInheritTestEnv(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = env.app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true}) }()
	t.Cleanup(func() { _ = env.app.Shutdown() })
	id := env.createJob(validJobRequest())
	env.waitStatus(id, "awaiting_confirm")
	ticket := env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/ws-ticket", nil).data()["ticket"].(string)

	url := "ws://" + ln.Addr().String() + "/api/inherit/jobs/" + id + "/ws?ticket=" + ticket
	if _, resp, err := websocket.DefaultDialer.Dial("ws://"+ln.Addr().String()+"/api/inherit/jobs/"+id+"/ws?ticket=bogus", http.Header{"X-Test-User": {testOwner}}); err == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bogus ticket accepted: %v", err)
	}
	conn, _, err := websocket.DefaultDialer.Dial(url, http.Header{"X-Test-User": {testOwner}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	read := func() wsMessage {
		t.Helper()
		_, body, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var m wsMessage
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if first := read(); first.Type != "snapshot" || first.Job.Status != jobStatusAwaitingConfirm {
		t.Fatalf("first = %+v", first)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"confirm","password":"`+testOwnerPassword+`"}`)); err != nil {
		t.Fatal(err)
	}
	sawAck, sawEvent := false, false
	for {
		m := read()
		switch m.Type {
		case "ack":
			sawAck = m.Action == "confirm"
		case "event":
			sawEvent = true
			if m.Job.Status == jobStatusSucceeded {
				if !sawAck || m.Job.NewCode == nil || m.Job.NewCode.InheritID != testIssuedID {
					t.Fatalf("final = %+v (ack %v)", m.Job, sawAck)
				}
				if _, _, err := conn.ReadMessage(); err == nil {
					t.Fatal("socket stayed open after the job finished")
				}
				return
			}
		case "error":
			t.Fatalf("error = %+v", m)
		}
		_ = sawEvent
	}
}
