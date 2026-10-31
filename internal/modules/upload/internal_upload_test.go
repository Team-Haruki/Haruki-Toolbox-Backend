package upload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	json "encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	utils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/background"
	database "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/uploadlog"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
)

const testInternalUploadToken = "internal-upload-test-token"

func testInternalUploadDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func testInternalUploadConfig(t *testing.T) InternalUploadConfig {
	t.Helper()
	cfg, err := ParseInternalUploadTokenSHA256(testInternalUploadDigest(testInternalUploadToken))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func writeInternalUploadTokenFile(t *testing.T, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token.sha256")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadInternalUploadConfig(t *testing.T) {
	digest := testInternalUploadDigest(testInternalUploadToken)
	for name, content := range map[string]string{
		"plain":            digest,
		"trailing newline": digest + "\n",
		"upper case":       strings.ToUpper(digest),
		"padded":           "  " + digest + " \r\n",
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := LoadInternalUploadConfig(writeInternalUploadTokenFile(t, []byte(content)))
			if err != nil || !cfg.Enabled() {
				t.Fatalf("LoadInternalUploadConfig: %v (enabled %v)", err, cfg.Enabled())
			}
			if reason := cfg.authorize([][]byte{[]byte("Bearer " + testInternalUploadToken)}); reason != "" {
				t.Fatalf("loaded digest does not authorize its token: %s", reason)
			}
		})
	}

	secretLooking := "not-a-digest-but-a-secret-value"
	for name, path := range map[string]string{
		"empty path":   "  ",
		"missing file": filepath.Join(t.TempDir(), "absent"),
		"raw token":    writeInternalUploadTokenFile(t, []byte(secretLooking)),
		"not hex":      writeInternalUploadTokenFile(t, []byte(strings.Repeat("zz", sha256.Size))),
		"too short":    writeInternalUploadTokenFile(t, []byte(digest[:63])),
		"oversized":    writeInternalUploadTokenFile(t, bytes.Repeat([]byte("a"), internalUploadTokenFileMaxBytes+1)),
		"directory":    t.TempDir(),
		"fifo":         testInternalUploadFIFO(t),
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := LoadInternalUploadConfig(path)
			if err == nil || cfg.Enabled() {
				t.Fatalf("invalid token file accepted (enabled %v)", cfg.Enabled())
			}
			if !errors.Is(err, ErrInternalUploadToken) {
				t.Fatalf("error %v does not wrap ErrInternalUploadToken", err)
			}
			if strings.Contains(err.Error(), secretLooking) || strings.Contains(err.Error(), "zz") {
				t.Fatalf("error repeats the file content: %v", err)
			}
		})
	}
}

// testInternalUploadFIFO is a named pipe with no writer: opening it for
// reading would block forever.
func testInternalUploadFIFO(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	return path
}

func TestIdentityContentEncoding(t *testing.T) {
	for _, tc := range []struct {
		fields []string
		want   bool
	}{
		{nil, true},
		{[]string{""}, true},
		{[]string{"identity"}, true},
		{[]string{"Identity, identity"}, true},
		{[]string{"gzip"}, false},
		{[]string{"identity", "br"}, false},
		{[]string{"identity, deflate"}, false},
	} {
		fields := make([][]byte, len(tc.fields))
		for i, f := range tc.fields {
			fields[i] = []byte(f)
		}
		if got := identityContentEncoding(fields); got != tc.want {
			t.Errorf("identityContentEncoding(%q) = %v, want %v", tc.fields, got, tc.want)
		}
	}
}

func TestInternalUploadAuthorize(t *testing.T) {
	cfg := testInternalUploadConfig(t)
	for name, tc := range map[string]struct {
		headers [][]byte
		want    string
	}{
		"valid":            {[][]byte{[]byte("Bearer " + testInternalUploadToken)}, ""},
		"scheme case":      {[][]byte{[]byte("bearer " + testInternalUploadToken)}, ""},
		"missing":          {nil, "missing"},
		"blank":            {[][]byte{[]byte("  ")}, "missing"},
		"multiple":         {[][]byte{[]byte("Bearer " + testInternalUploadToken), []byte("Bearer " + testInternalUploadToken)}, "multiple"},
		"basic":            {[][]byte{[]byte("Basic dXNlcjpwYXNz")}, "malformed"},
		"no token":         {[][]byte{[]byte("Bearer")}, "malformed"},
		"wrong token":      {[][]byte{[]byte("Bearer other-token")}, "mismatch"},
		"digest as token":  {[][]byte{[]byte("Bearer " + testInternalUploadDigest(testInternalUploadToken))}, "mismatch"},
		"token with extra": {[][]byte{[]byte("Bearer " + testInternalUploadToken + " x")}, "malformed"},
	} {
		if got := cfg.authorize(tc.headers); got != tc.want {
			t.Errorf("%s: authorize = %q, want %q", name, got, tc.want)
		}
	}
	if got := (InternalUploadConfig{}).authorize([][]byte{[]byte("Bearer " + testInternalUploadToken)}); got != "mismatch" {
		t.Fatalf("disabled config authorized a token: %q", got)
	}
}

type internalUploadTestEnv struct {
	app    *fiber.App
	db     *postgresql.Client
	helper *harukiAPIHelper.HarukiToolboxRouterHelpers
	deps   Dependencies
}

func newInternalUploadTestEnv(t *testing.T, withRedis bool) *internalUploadTestEnv {
	t.Helper()
	return newInternalUploadTestEnvAt(t, withRedis, time.Now)
}

func newInternalUploadTestEnvAt(t *testing.T, withRedis bool, now func() time.Time) *internalUploadTestEnv {
	t.Helper()
	db := enttest.Open(t, "sqlite3", uniqueUploadAuditSQLiteDSN(t, "internal-upload"))
	t.Cleanup(func() { _ = db.Close() })
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{DBManager: &database.HarukiToolboxDBManager{DB: db}}
	if withRedis {
		helper.DBManager.Redis = newTestAPIHelperWithRedis(t).DBManager.Redis
	}
	deps := testUploadDependencies()
	deps.BackgroundTasks = background.InlineRunner{}
	deps.InternalUpload = testInternalUploadConfig(t)
	// The default Fiber limit (4 MiB) is below the route's own limit.
	app := fiber.New(fiber.Config{BodyLimit: 2 * internalUploadMaxBodyBytes})
	app.Post(InternalUploadPath, handleInternalUpload(helper, deps, now, harukiLogger.NewLogger("InternalUploadTest", "DEBUG", io.Discard)))
	return &internalUploadTestEnv{app: app, db: db, helper: helper, deps: deps}
}

type internalUploadRequest struct {
	path    string
	body    []byte
	headers map[string]string
}

func (e *internalUploadTestEnv) do(t *testing.T, r internalUploadRequest) (*http.Response, proxyTestResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, r.path, bytes.NewReader(r.body))
	req.Header.Set("Content-Type", "application/octet-stream")
	if _, ok := r.headers["Authorization"]; !ok {
		req.Header.Set("Authorization", "Bearer "+testInternalUploadToken)
	}
	for key, value := range r.headers {
		if value == "" {
			req.Header.Del(key)
			continue
		}
		req.Header.Set(key, value)
	}
	resp, err := e.app.Test(req, fiber.TestConfig{Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var decoded proxyTestResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	if resp.Header.Get("X-Request-ID") == "" || decoded.Data.RequestID != resp.Header.Get("X-Request-ID") {
		t.Fatalf("request id header %q does not match body %q", resp.Header.Get("X-Request-ID"), decoded.Data.RequestID)
	}
	if resp.Header.Get(fiber.HeaderCacheControl) != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", resp.Header.Get(fiber.HeaderCacheControl))
	}
	return resp, decoded
}

func (e *internalUploadTestEnv) uploadLogs(t *testing.T) []*postgresql.UploadLog {
	t.Helper()
	logs, err := e.db.UploadLog.Query().Order(uploadlog.ByID()).All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return logs
}

func (e *internalUploadTestEnv) pack(t *testing.T, server utils.SupportedDataUploadServer, content map[string]any) []byte {
	t.Helper()
	raw, err := e.deps.ServerCryptor.Pack(content, server)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestInternalUploadRejectsBeforeThePipeline(t *testing.T) {
	env := newInternalUploadTestEnv(t, false)
	for name, tc := range map[string]struct {
		req    internalUploadRequest
		status int
		code   string
	}{
		"no token":       {internalUploadRequest{path: "/internal/upload/jp/123/suite", body: []byte("x"), headers: map[string]string{"Authorization": ""}}, 401, "invalid_client_credentials"},
		"wrong token":    {internalUploadRequest{path: "/internal/upload/jp/123/suite", body: []byte("x"), headers: map[string]string{"Authorization": "Bearer nope"}}, 401, "invalid_client_credentials"},
		"bad server":     {internalUploadRequest{path: "/internal/upload/xx/123/suite", body: []byte("x")}, 400, "invalid_upload_payload"},
		"bad data type":  {internalUploadRequest{path: "/internal/upload/jp/123/unknown", body: []byte("x")}, 400, "invalid_upload_payload"},
		"birthday party": {internalUploadRequest{path: "/internal/upload/jp/123/mysekai_birthday_party", body: []byte("x")}, 400, "invalid_upload_payload"},
		"bad user id":    {internalUploadRequest{path: "/internal/upload/jp/abc/suite", body: []byte("x")}, 400, "invalid_upload_payload"},
		"zero user id":   {internalUploadRequest{path: "/internal/upload/jp/0/suite", body: []byte("x")}, 400, "invalid_upload_payload"},
		"empty body":     {internalUploadRequest{path: "/internal/upload/jp/123/suite"}, 400, "invalid_upload_payload"},
		"gzip body":      {internalUploadRequest{path: "/internal/upload/jp/123/suite", body: []byte("x"), headers: map[string]string{"Content-Encoding": "gzip"}}, 415, "invalid_upload_payload"},
		"gzip in a list": {internalUploadRequest{path: "/internal/upload/jp/123/suite", body: []byte("x"), headers: map[string]string{"Content-Encoding": "identity, gzip"}}, 415, "invalid_upload_payload"},
		"oversized":      {internalUploadRequest{path: "/internal/upload/jp/123/suite", body: make([]byte, internalUploadMaxBodyBytes+1)}, 413, "invalid_upload_payload"},
	} {
		t.Run(name, func(t *testing.T) {
			resp, body := env.do(t, tc.req)
			if resp.StatusCode != tc.status || body.Data.ErrorCode != tc.code || body.Data.Retryable {
				t.Fatalf("got %d %+v, want %d %s", resp.StatusCode, body, tc.status, tc.code)
			}
		})
	}
	// None of them reached the pipeline, so nothing was audited as an upload.
	if logs := env.uploadLogs(t); len(logs) != 0 {
		t.Fatalf("rejected requests wrote %d upload logs", len(logs))
	}
}

func TestInternalUploadRateLimit(t *testing.T) {
	// One fixed clock for the seeded key and the handler, so a minute boundary
	// between the two cannot move the handler to another window.
	now := time.Now().UTC()
	env := newInternalUploadTestEnvAt(t, true, func() time.Time { return now })
	window := int64(internalUploadRateLimitWindow / time.Second)
	key := harukiRedis.BuildInternalUploadRateLimitKey(now.Unix()/window, "0.0.0.0")
	if err := env.helper.DBManager.Redis.Redis.Set(context.Background(), key, internalUploadRateLimitPerIP, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	resp, body := env.do(t, internalUploadRequest{path: "/internal/upload/jp/123/suite", body: []byte("x")})
	if resp.StatusCode != http.StatusTooManyRequests || body.Data.ErrorCode != "rate_limited" || !body.Data.Retryable {
		t.Fatalf("got %d %+v, want 429 rate_limited retryable", resp.StatusCode, body)
	}
	if retryAfter, err := strconv.Atoi(resp.Header.Get(fiber.HeaderRetryAfter)); err != nil || retryAfter < 1 || retryAfter > int(window) {
		t.Fatalf("Retry-After = %q", resp.Header.Get(fiber.HeaderRetryAfter))
	}

	// An unauthenticated caller is refused before it can spend the budget.
	if err := env.helper.DBManager.Redis.Redis.Del(context.Background(), key).Err(); err != nil {
		t.Fatal(err)
	}
	env.do(t, internalUploadRequest{path: "/internal/upload/jp/123/suite", body: []byte("x"), headers: map[string]string{"Authorization": "Bearer nope"}})
	if count, err := env.helper.DBManager.Redis.Redis.Exists(context.Background(), key).Result(); err != nil || count != 0 {
		t.Fatalf("unauthenticated request counted against the limit (exists=%d err=%v)", count, err)
	}
}

func TestInternalUploadRunsTheSharedPipeline(t *testing.T) {
	env := newInternalUploadTestEnv(t, false)
	ctx := context.Background()

	// An undecodable payload fails in the shared decode stage and is audited
	// under the internal method.
	resp, body := env.do(t, internalUploadRequest{path: "/internal/upload/jp/123/suite", body: []byte("not a game payload")})
	if resp.StatusCode != http.StatusBadRequest || body.Data.ErrorCode != "invalid_upload_payload" {
		t.Fatalf("got %d %+v", resp.StatusCode, body)
	}
	logs := env.uploadLogs(t)
	if len(logs) != 1 {
		t.Fatalf("upload logs = %d, want 1", len(logs))
	}
	got := logs[0]
	if got.UploadMethod != string(utils.UploadMethodInternal) || got.Success || got.AuthMethod == nil || *got.AuthMethod != internalUploadAuthMethod ||
		got.FailureStage == nil || *got.FailureStage != uploadStageDecodePayload || got.RequestID == nil || *got.RequestID != body.Data.RequestID ||
		got.Platform == nil || *got.Platform != "not_applicable" || got.ActorUserID != nil {
		t.Fatalf("audit row = %+v", got)
	}
	if got.ErrorMessage != nil && strings.Contains(*got.ErrorMessage, testInternalUploadToken) {
		t.Fatal("audit row carries the token")
	}

	// A payload for another account is refused by the shared identity check.
	// Real game user IDs are 64-bit, and so is their encoding.
	const gameUserID, otherGameUserID = int64(7485938475928374), int64(7485938475928375)
	path := "/internal/upload/jp/" + strconv.FormatInt(gameUserID, 10) + "/suite"
	resp, body = env.do(t, internalUploadRequest{path: path, body: env.pack(t, "jp", map[string]any{"userGamedata": map[string]any{"userId": otherGameUserID}})})
	if resp.StatusCode != http.StatusBadRequest || body.Data.ErrorCode != "invalid_upload_payload" {
		t.Fatalf("mismatched identity: got %d %+v", resp.StatusCode, body)
	}
	if logs := env.uploadLogs(t); *logs[len(logs)-1].FailureStage != uploadStagePreprocess {
		t.Fatalf("mismatched identity failed at %v", *logs[len(logs)-1].FailureStage)
	}

	// A matching payload passes every check up to the game data write (the
	// test has no game data store, so the write itself fails as a 500).
	resp, body = env.do(t, internalUploadRequest{path: path, body: env.pack(t, "jp", map[string]any{"userGamedata": map[string]any{"userId": gameUserID}})})
	if resp.StatusCode != http.StatusInternalServerError || body.Data.ErrorCode != "internal_error" {
		t.Fatalf("matching identity: got %d %+v", resp.StatusCode, body)
	}
	last := env.uploadLogs(t)
	if row := last[len(last)-1]; *row.FailureStage != uploadStagePersist || row.IdentityVerified == nil || !*row.IdentityVerified || row.GameUserID != strconv.FormatInt(gameUserID, 10) {
		t.Fatalf("matching identity audit row = %+v", row)
	}

	// The owner's account policy applies as on every other trusted path.
	owner, err := env.db.User.Create().SetID("owner").SetName("owner").SetEmail("owner@example.com").SetBanned(true).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.db.GameAccountBinding.Create().SetServer("jp").SetGameUserID(strconv.FormatInt(otherGameUserID, 10)).SetVerified(true).SetUser(owner).Save(ctx); err != nil {
		t.Fatal(err)
	}
	resp, body = env.do(t, internalUploadRequest{path: "/internal/upload/jp/" + strconv.FormatInt(otherGameUserID, 10) + "/suite", body: env.pack(t, "jp", map[string]any{"userGamedata": map[string]any{"userId": otherGameUserID}})})
	if resp.StatusCode != http.StatusForbidden || body.Data.ErrorCode != "upload_not_allowed" || body.Message != "upload not allowed" || body.Data.Retryable {
		t.Fatalf("banned owner: got %d %+v", resp.StatusCode, body)
	}

	cnOwner, err := env.db.User.Create().SetID("cn-owner").SetName("cn").SetEmail("cn@example.com").SetAllowCnMysekai(false).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.db.GameAccountBinding.Create().SetServer("cn").SetGameUserID("888").SetVerified(true).SetUser(cnOwner).Save(ctx); err != nil {
		t.Fatal(err)
	}
	resp, body = env.do(t, internalUploadRequest{path: "/internal/upload/cn/888/mysekai", body: env.pack(t, "cn", map[string]any{"updatedResources": map[string]any{}})})
	if resp.StatusCode != http.StatusForbidden || body.Data.ErrorCode != "upload_not_allowed" {
		t.Fatalf("CN MySekai not allowed: got %d %+v", resp.StatusCode, body)
	}
}

func TestInternalUploadIsAnAnonymousTrustedMethod(t *testing.T) {
	if !allowAnonymousBoundAccountUpload(utils.UploadMethodInternal) {
		t.Fatal("internal uploads must be accepted for bound accounts without an acting user")
	}
	a := newAttempt(utils.UploadMethodInternal, 1)
	if a.AuthMethod != internalUploadAuthMethod || a.Client.Platform != "not_applicable" {
		t.Fatalf("attempt = %+v", a)
	}
}
