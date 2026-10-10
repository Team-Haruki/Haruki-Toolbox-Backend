package upload

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/background"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiHttp "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/http"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

const notifyTestToken = "notify-test-token"

var fastNotifyRetryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}

// lockedBuffer lets the test read log output while background retries write it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// notifyTestServer answers the n-th request (1-based) with respond(n).
type notifyTestServer struct {
	*httptest.Server
	requests    atomic.Int32
	badAuth     atomic.Int32
	badEventIDs atomic.Int32
}

func newNotifyTestServer(t *testing.T, respond func(n int32, w http.ResponseWriter) bool) *notifyTestServer {
	t.Helper()
	s := &notifyTestServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := s.requests.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/internal/events" || r.Header.Get("Authorization") != "Bearer "+notifyTestToken {
			s.badAuth.Add(1)
		}
		var body hmesEventNotifyRequest
		if err := json.UnmarshalRead(r.Body, &body); err != nil || body.EventID == "" || body.SubscriptionID == "" {
			s.badEventIDs.Add(1)
		}
		if respond(n, w) {
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(s.Close)
	return s
}

func respondWithStatuses(statuses ...int) func(int32, http.ResponseWriter) bool {
	return func(n int32, w http.ResponseWriter) bool {
		status := statuses[len(statuses)-1]
		if int(n) <= len(statuses) {
			status = statuses[n-1]
		}
		w.WriteHeader(status)
		return true
	}
}

func newNotifyTestHandler(baseURL string, runner background.Runner, delays []time.Duration, logs *lockedBuffer) *DataHandler {
	cfg := NewBirthdaySubscriptionConfig(BirthdaySubscriptionConfigOptions{
		HMESInternalBaseURL: baseURL,
		HMESInternalToken:   notifyTestToken,
		RequestTimeout:      2 * time.Second,
	})
	cfg.notifyRetryDelays = &delays
	return &DataHandler{
		BackgroundTasks:      runner,
		HttpClient:           harukiHttp.NewClient("", 0),
		Logger:               harukiLogger.NewLogger("notify-test", "DEBUG", logs),
		BirthdaySubscription: cfg,
	}
}

func testBirthdayEvent() *BirthdayMonitorEvent {
	return &BirthdayMonitorEvent{
		EventID:             "event-1",
		SubscriptionID:      "subscription-1",
		SubscriptionVersion: "v1",
		PayloadRef:          "payload-ref",
	}
}

func assertNotifyRequests(t *testing.T, server *notifyTestServer, want int32) {
	t.Helper()
	if got := server.requests.Load(); got != want {
		t.Fatalf("notify requests = %d, want %d", got, want)
	}
	if server.badAuth.Load() != 0 || server.badEventIDs.Load() != 0 {
		t.Fatalf("malformed notify requests: auth/route=%d body=%d", server.badAuth.Load(), server.badEventIDs.Load())
	}
}

func TestDeliverBirthdayEventSucceedsOnFirstAttempt(t *testing.T) {
	t.Parallel()
	server := newNotifyTestServer(t, respondWithStatuses(http.StatusOK))
	logs := &lockedBuffer{}
	h := newNotifyTestHandler(server.URL, background.InlineRunner{}, fastNotifyRetryDelays, logs)

	h.deliverBirthdayEvent(testBirthdayEvent())

	assertNotifyRequests(t, server, 1)
	if strings.Contains(logs.String(), "WARNING") {
		t.Fatalf("unexpected warning: %s", logs.String())
	}
}

func TestDeliverBirthdayEventRetriesTransientFailureOnce(t *testing.T) {
	t.Parallel()
	server := newNotifyTestServer(t, respondWithStatuses(http.StatusServiceUnavailable, http.StatusOK))
	logs := &lockedBuffer{}
	h := newNotifyTestHandler(server.URL, background.InlineRunner{}, fastNotifyRetryDelays, logs)

	h.deliverBirthdayEvent(testBirthdayEvent())

	assertNotifyRequests(t, server, 2)
	if strings.Contains(logs.String(), "WARNING") {
		t.Fatalf("a recovered notification must not warn: %s", logs.String())
	}
}

func TestDeliverBirthdayEventDoesNotRetryStaleSubscription(t *testing.T) {
	t.Parallel()
	server := newNotifyTestServer(t, respondWithStatuses(http.StatusConflict))
	logs := &lockedBuffer{}
	h := newNotifyTestHandler(server.URL, background.InlineRunner{}, fastNotifyRetryDelays, logs)

	h.deliverBirthdayEvent(testBirthdayEvent())

	assertNotifyRequests(t, server, 1)
	output := logs.String()
	if strings.Contains(output, "WARNING") {
		t.Fatalf("a stale subscription must not warn: %s", output)
	}
	if !strings.Contains(output, "[INFO]") || !strings.Contains(output, "subscription is stale") {
		t.Fatalf("expected an info log for the stale subscription: %s", output)
	}
}

func TestDeliverBirthdayEventGivesUpAfterFourAttempts(t *testing.T) {
	t.Parallel()
	server := newNotifyTestServer(t, respondWithStatuses(http.StatusInternalServerError))
	logs := &lockedBuffer{}
	h := newNotifyTestHandler(server.URL, background.InlineRunner{}, fastNotifyRetryDelays, logs)

	h.deliverBirthdayEvent(testBirthdayEvent())

	assertNotifyRequests(t, server, 4)
	output := logs.String()
	if !strings.Contains(output, "[WARNING]") || !strings.Contains(output, "event=event-1 subscription=subscription-1 version=v1 attempts=4") {
		t.Fatalf("expected a final warning with the attempt count: %s", output)
	}
	if strings.Count(output, "[WARNING]") != 1 {
		t.Fatalf("expected exactly one warning: %s", output)
	}
	if strings.Contains(output, notifyTestToken) {
		t.Fatalf("token leaked into logs: %s", output)
	}
}

func TestDeliverBirthdayEventRetriesTransportError(t *testing.T) {
	t.Parallel()
	server := newNotifyTestServer(t, func(n int32, w http.ResponseWriter) bool {
		if n > 1 {
			return false
		}
		conn, _, err := http.NewResponseController(w).Hijack()
		if err != nil {
			panic(err)
		}
		_ = conn.Close()
		return true
	})
	logs := &lockedBuffer{}
	h := newNotifyTestHandler(server.URL, background.InlineRunner{}, fastNotifyRetryDelays, logs)

	h.deliverBirthdayEvent(testBirthdayEvent())

	assertNotifyRequests(t, server, 2)
}

func TestDeliverBirthdayEventSchedulesRetryAsNamedBackgroundTask(t *testing.T) {
	t.Parallel()
	server := newNotifyTestServer(t, respondWithStatuses(http.StatusBadGateway, http.StatusOK))
	runner := &capturedHandlerRunner{accept: true}
	h := newNotifyTestHandler(server.URL, runner, fastNotifyRetryDelays, &lockedBuffer{})

	h.deliverBirthdayEvent(testBirthdayEvent())

	assertNotifyRequests(t, server, 1)
	runner.mu.Lock()
	names, tasks := runner.names, runner.tasks
	runner.mu.Unlock()
	if len(names) != 1 || names[0] != "birthday-subscription-notify-retry" || len(tasks) != 1 {
		t.Fatalf("submitted tasks = %v (%d captured), want one birthday-subscription-notify-retry", names, len(tasks))
	}
	tasks[0]()
	assertNotifyRequests(t, server, 2)
}

func TestDeliverBirthdayEventDropsRetryWhenSubmitRejected(t *testing.T) {
	t.Parallel()
	server := newNotifyTestServer(t, respondWithStatuses(http.StatusInternalServerError))
	logs := &lockedBuffer{}
	h := newNotifyTestHandler(server.URL, &capturedHandlerRunner{accept: false}, fastNotifyRetryDelays, logs)

	h.deliverBirthdayEvent(testBirthdayEvent())

	assertNotifyRequests(t, server, 1)
	if !strings.Contains(logs.String(), "notify retry dropped") {
		t.Fatalf("expected the dropped retry to be logged: %s", logs.String())
	}
}

func TestNotifyHMESBirthdayEventReturnsStaleSentinelOnConflict(t *testing.T) {
	t.Parallel()
	server := newNotifyTestServer(t, respondWithStatuses(http.StatusConflict))
	h := newNotifyTestHandler(server.URL, background.InlineRunner{}, fastNotifyRetryDelays, &lockedBuffer{})

	err := h.notifyHMESBirthdayEvent(context.Background(), testBirthdayEvent())
	if !errors.Is(err, errBirthdaySubscriptionStale) {
		t.Fatalf("err = %v, want errBirthdaySubscriptionStale", err)
	}
}

func TestBirthdaySubscriptionConfigDefaultRetryDelays(t *testing.T) {
	t.Parallel()
	got := NewBirthdaySubscriptionConfig(BirthdaySubscriptionConfigOptions{}).retryDelays()
	want := []time.Duration{time.Second, 5 * time.Second, 15 * time.Second}
	if len(got) != len(want) {
		t.Fatalf("retry delays = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("retry delays = %v, want %v", got, want)
		}
	}
}

// TestProcessBirthdaySubscriptionDoesNotWaitForRetries runs the real
// processing path against Redis and checks that it returns after the first
// failed notification, while the retry completes later on the task group.
func TestProcessBirthdaySubscriptionDoesNotWaitForRetries(t *testing.T) {
	t.Parallel()
	const retryDelay = 500 * time.Millisecond

	server := newNotifyTestServer(t, respondWithStatuses(http.StatusServiceUnavailable, http.StatusOK))
	tasks := background.NewTaskGroup(nil)
	h := newNotifyTestHandler(server.URL, tasks, []time.Duration{retryDelay}, &lockedBuffer{})

	mini := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	redisManager := &harukiRedis.HarukiRedisManager{Redis: client}
	h.DBManager = &database.HarukiToolboxDBManager{Redis: redisManager}

	if err := UpsertBirthdayMonitorMirror(context.Background(), redisManager, BirthdayMonitorMirror{
		SubscriptionID:      "subscription-1",
		SubscriptionVersion: "v1",
		Region:              string(utils.SupportedDataUploadServerJP),
		UID:                 "123",
		MaterialIDs:         []int{12},
		ExpiresAt:           time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("seed monitor: %v", err)
	}
	data := map[string]any{
		"updatedResources": map[string]any{
			"userMysekaiHarvestMaps": []any{
				map[string]any{
					"mysekaiSiteId": 5,
					"userMysekaiSiteHarvestResourceDrops": []any{
						map[string]any{"resourceType": "mysekai_material", "resourceId": 12, "positionX": 1.0, "positionZ": 2.0},
					},
				},
			},
		},
	}

	start := time.Now()
	h.processBirthdaySubscription(123, utils.SupportedDataUploadServerJP, data)
	elapsed := time.Since(start)

	if elapsed >= retryDelay {
		t.Fatalf("processing took %v, it must not wait for the %v retry delay", elapsed, retryDelay)
	}
	assertNotifyRequests(t, server, 1)

	// The retry runs on its own once the delay elapses. Shutting down first
	// would drop it, so wait for the second request before draining.
	deadline := time.Now().Add(10 * time.Second)
	for server.requests.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := tasks.Shutdown(ctx); err != nil {
		t.Fatalf("drain retry task: %v", err)
	}
	assertNotifyRequests(t, server, 2)
}

// TestRetryStopsWhenShutdownBeginsDuringBackoff checks that a pending retry
// does not hold up the task-group drain: shutdown during the backoff drops
// the event promptly and logs it.
func TestRetryStopsWhenShutdownBeginsDuringBackoff(t *testing.T) {
	t.Parallel()
	server := newNotifyTestServer(t, respondWithStatuses(http.StatusInternalServerError))
	tasks := background.NewTaskGroup(nil)
	logs := &lockedBuffer{}
	h := newNotifyTestHandler(server.URL, tasks, []time.Duration{time.Hour, time.Hour, time.Hour}, logs)

	h.deliverBirthdayEvent(testBirthdayEvent())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if err := tasks.Shutdown(ctx); err != nil {
		t.Fatalf("drain did not finish while a retry was backing off: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("drain took %v", elapsed)
	}
	assertNotifyRequests(t, server, 1)
	if output := logs.String(); !strings.Contains(output, "[WARNING]") || !strings.Contains(output, "retry dropped: shutdown in progress event=event-1 subscription=subscription-1 version=v1 attempts=1") {
		t.Fatalf("expected the dropped retry to be logged at Warn: %s", output)
	}
}

// TestInlineRetryObservesParentShutdown covers the iOS path, where the retry
// runs inline inside an already tracked parent task.
func TestInlineRetryObservesParentShutdown(t *testing.T) {
	t.Parallel()
	server := newNotifyTestServer(t, respondWithStatuses(http.StatusInternalServerError))
	parentShutdown := make(chan struct{})
	close(parentShutdown)
	h := newNotifyTestHandler(server.URL, background.InlineRunner{Shutdown: parentShutdown}, []time.Duration{time.Hour}, &lockedBuffer{})

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.deliverBirthdayEvent(testBirthdayEvent())
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("inline retry ignored the parent's shutdown")
	}
	assertNotifyRequests(t, server, 1)
}
