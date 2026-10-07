package botsecurity

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	stdjson "encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"
)

const testIngestToken = "bot-security-ingest-token-0123456789abcdef"

// testDBSeq keeps every in-memory database distinct, also within one test.
var testDBSeq atomic.Int64

// testNow is the fixed clock of the handler tests.
var testNow = time.Date(2026, time.October, 8, 12, 0, 0, 0, time.UTC)

func testIngestConfig(t *testing.T) IngestConfig {
	t.Helper()
	sum := sha256.Sum256([]byte(testIngestToken))
	cfg, err := ParseIngestConfig(hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatalf("ParseIngestConfig: %v", err)
	}
	return cfg
}

func newTestHelper(t *testing.T) *harukiAPIHelper.HarukiToolboxRouterHelpers {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) + "_" + strconv.FormatInt(testDBSeq.Add(1), 10)
	client := enttest.Open(t, "sqlite3", "file:"+name+"?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	return &harukiAPIHelper.HarukiToolboxRouterHelpers{
		DBManager: &database.HarukiToolboxDBManager{DB: client},
	}
}

// logBuffer is a goroutine-safe log sink.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newTestLogger() (*harukiLogger.Logger, *logBuffer) {
	buf := &logBuffer{}
	return harukiLogger.NewLogger("BotSecurityTest", "DEBUG", buf), buf
}

type testResponse struct {
	Status int
	Header http.Header
	Body   []byte
}

func doRequest(t *testing.T, app *fiber.App, method, path, body string, headers map[string]string) testResponse {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return testResponse{Status: resp.StatusCode, Header: resp.Header, Body: data}
}

func decodeJSON[T any](t *testing.T, data []byte) T {
	t.Helper()
	var out T
	if err := stdjson.Unmarshal(data, &out); err != nil {
		t.Fatalf("decode %s: %v", data, err)
	}
	return out
}

func mustCreateAlert(t *testing.T, db *postgresql.Client, kind, botID, sourceIP, node string, alertTime time.Time) *postgresql.BotSecurityAlert {
	t.Helper()
	subject := botID
	if subject == "" {
		subject = sourceIP
	}
	builder := db.BotSecurityAlert.Create().
		SetKind(kind).
		SetSubject(subject).
		SetSourceIP(sourceIP).
		SetNode(node).
		SetAlertTime(alertTime).
		SetReceivedAt(alertTime.Add(time.Second))
	if botID != "" {
		builder.SetBotID(botID)
	}
	row, err := builder.Save(t.Context())
	if err != nil {
		t.Fatalf("seed alert: %v", err)
	}
	return row
}
