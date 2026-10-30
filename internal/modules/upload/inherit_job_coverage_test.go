package upload

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	json "encoding/json/v2"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/inheritvault"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/systemlog"
	harukiSekai "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/game/sekai"

	"github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v3"
)

// testInheritRunner starts tasks on goroutines, can refuse them like a
// draining task group, and reports shutdown through its channel.
type testInheritRunner struct {
	refuse   atomic.Bool
	shutdown chan struct{}
}

func (r *testInheritRunner) Go(_ string, task func()) bool {
	if r.refuse.Load() {
		return false
	}
	go task()
	return true
}

func (r *testInheritRunner) ShuttingDown() <-chan struct{} { return r.shutdown }

func (env *inheritTestEnv) confirmAndWait(id string, statuses ...string) map[string]any {
	env.t.Helper()
	env.waitStatus(id, "awaiting_confirm")
	if res := env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/confirm", ownerPW()); res.Status != http.StatusAccepted {
		env.t.Fatalf("confirm = %d %s", res.Status, res.Raw)
	}
	return env.waitStatus(id, statuses...)
}

func warningList(job map[string]any) string { return strings.Join(warningCodes(job), ",") }

func TestCreateJobRefusals(t *testing.T) {
	env := newInheritTestEnv(t)
	runner := &testInheritRunner{}
	env.svc.runner = runner
	var qqDown atomic.Bool
	env.svc.hasVerifiedQQ = func(_ context.Context, userID string) (bool, error) {
		if qqDown.Load() {
			return false, errors.New("db down")
		}
		return userID == testOwner, nil
	}
	create := func(user string, req map[string]any) (apiResult, http.Header) {
		raw, _ := json.Marshal(req)
		return env.callRawBody(user, "POST", "/api/inherit/jobs", string(raw))
	}

	qqDown.Store(true)
	mysekai := validJobRequest()
	mysekai["upload_type"] = "mysekai"
	if res, _ := create(testOwner, mysekai); res.Status != http.StatusServiceUnavailable || res.code() != "store_unavailable" {
		t.Fatalf("QQ lookup down = %d %s", res.Status, res.Raw)
	}
	qqDown.Store(false)

	// An open breaker and a full server both refuse with Retry-After.
	for range 5 {
		_, _, token := env.svc.breaker.Allow("jp")
		env.svc.breaker.RecordResult("jp", token, true)
	}
	if res, h := create(testOwner, validJobRequest()); res.Status != http.StatusServiceUnavailable || res.code() != "game_degraded" || h.Get("Retry-After") == "" {
		t.Fatalf("breaker open = %d %s (Retry-After %q)", res.Status, res.Raw, h.Get("Retry-After"))
	}
	env.svc.breaker = newInheritCircuitBreaker(5, time.Minute, time.Second)
	env.svc.limiter = newInheritConcurrencyLimiter(1)
	env.svc.limiter.acquire("jp")
	if res, h := create(testOwner, validJobRequest()); res.Status != http.StatusTooManyRequests || res.code() != "server_busy" || h.Get("Retry-After") != "1" {
		t.Fatalf("server full = %d %s (Retry-After %q)", res.Status, res.Raw, h.Get("Retry-After"))
	}
	env.svc.limiter.release("jp")

	// A draining process takes no job, and the refused job leaves no lock.
	runner.refuse.Store(true)
	if res, _ := create(testOwner, validJobRequest()); res.Status != http.StatusServiceUnavailable || res.code() != "vault_unavailable" {
		t.Fatalf("draining = %d %s", res.Status, res.Raw)
	}
	runner.refuse.Store(false)
	if env.redis.Exists(inheritActiveKeyPrefix + testOwner) {
		t.Fatal("refused job kept the active-job lock")
	}
	id := env.createJob(validJobRequest())
	env.waitStatus(id, "awaiting_confirm")

	// A job record of this process that is not running here any more still
	// holds the lock; the owner gets its ID back.
	stuck := newInheritJob(testIntruder, "jp", "suite", "random", time.Now().UTC())
	stuck.Status = jobStatusAwaitingConfirm
	if err := env.svc.jobs.save(context.Background(), stuck); err != nil {
		t.Fatal(err)
	}
	env.redis.Set(inheritActiveKeyPrefix+testIntruder, stuck.ID)
	if res, _ := create(testIntruder, validJobRequest()); res.Status != http.StatusConflict || res.code() != "job_already_running" || res.data()["job_id"] != stuck.ID {
		t.Fatalf("stuck job = %d %s", res.Status, res.Raw)
	}

	// Ten jobs an hour per owner.
	hour := env.svc.now().Unix() / 3600
	for _, h := range []int64{hour, hour + 1} {
		env.redis.Set(inheritRateKeyPrefix+testOwner+":"+strconv.FormatInt(h, 10), strconv.Itoa(inheritJobsPerUserHour))
	}
	if res, _ := create(testOwner, validJobRequest()); res.Status != http.StatusTooManyRequests || res.code() != "rate_limited" {
		t.Fatalf("over the hourly limit = %d %s", res.Status, res.Raw)
	}
	env.mu.Lock()
	games := env.games
	env.mu.Unlock()
	if games != 1 {
		t.Fatalf("game sessions = %d, want only the admitted job's", games)
	}

	if res := env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/cancel", nil); res.Status != http.StatusOK {
		t.Fatalf("cancel = %d %s", res.Status, res.Raw)
	}
	env.waitStatus(id, "cancelled")
	env.redis.Close()
	if res, _ := create(testOwner, validJobRequest()); res.Status != http.StatusServiceUnavailable || res.code() != "store_unavailable" {
		t.Fatalf("Redis down = %d %s", res.Status, res.Raw)
	}
	// A job no longer running here is read from Redis.
	if res := env.call(testOwner, "GET", "/api/inherit/jobs/"+id, nil); res.Status != http.StatusServiceUnavailable || res.code() != "store_unavailable" {
		t.Fatalf("finished job with Redis down = %d %s", res.Status, res.Raw)
	}
}

func TestJobPreviewPhaseFailures(t *testing.T) {
	t.Run("no game session", func(t *testing.T) {
		env := newInheritTestEnv(t)
		env.svc.newGame = func(harukiUtils.SupportedInheritUploadServer, harukiSekai.Progress) (inheritGame, error) {
			return nil, errors.New("bad config")
		}
		job := env.waitStatus(env.createJob(validJobRequest()), "failed")
		if errorCode(job) != codeInternal || jobStepStatus(job, stepBootstrap) != "failed" || jobStepStatus(job, stepPreview) != "skipped" {
			t.Fatalf("job = %v", job)
		}
	})
	t.Run("bootstrap fails", func(t *testing.T) {
		env := newInheritTestEnv(t)
		g := &scriptedInheritGame{bootstrap: func(context.Context) error { return harukiSekai.NewAPIError("/v", "GET", 503, "", nil) }}
		env.useScriptedGame(g)
		job := env.waitStatus(env.createJob(validJobRequest()), "failed")
		if errorCode(job) != codeGameUnavailable || jobStepStatus(job, stepBootstrap) != "failed" || g.count("preview") != 0 {
			t.Fatalf("job = %v", job)
		}
	})
	t.Run("shutdown during preview", func(t *testing.T) {
		env := newInheritTestEnv(t)
		runner := &testInheritRunner{shutdown: make(chan struct{})}
		env.svc.runner = runner
		started := make(chan struct{})
		env.useScriptedGame(&scriptedInheritGame{preview: func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}})
		id := env.createJob(validJobRequest())
		<-started
		close(runner.shutdown)
		job := env.waitStatus(id, "failed")
		if errorCode(job) != codeJobInterrupted || jobStepStatus(job, stepPreview) != "skipped" {
			t.Fatalf("job = %v", job)
		}
	})
	t.Run("shutdown while awaiting confirmation", func(t *testing.T) {
		env := newInheritTestEnv(t)
		runner := &testInheritRunner{shutdown: make(chan struct{})}
		env.svc.runner = runner
		id := env.createJob(validJobRequest())
		env.waitStatus(id, "awaiting_confirm")
		close(runner.shutdown)
		job := env.waitStatus(id, "failed")
		if errorCode(job) != codeJobInterrupted || jobStepStatus(job, stepAwaitingConfirm) != "skipped" || env.game.called("execute") {
			t.Fatalf("job = %v", job)
		}
	})
}

// Cancelling while the game is still being reached stops the job without
// reaching the executing call; a repeated cancel is accepted.
func TestCancelBeforeThePreviewFinishes(t *testing.T) {
	t.Run("bootstrap", func(t *testing.T) {
		env := newInheritTestEnv(t)
		started, release := make(chan struct{}), make(chan struct{})
		g := &scriptedInheritGame{bootstrap: func(context.Context) error {
			close(started)
			<-release
			return errors.New("aborted")
		}}
		env.useScriptedGame(g)
		id := env.createJob(validJobRequest())
		<-started
		for range 2 {
			if res := env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/cancel", nil); res.Status != http.StatusOK {
				t.Fatalf("cancel = %d %s", res.Status, res.Raw)
			}
		}
		close(release)
		job := env.waitStatus(id, "cancelled")
		if errorCode(job) != codeCancelled || jobStepStatus(job, stepBootstrap) != "skipped" || g.count("preview") != 0 {
			t.Fatalf("job = %v", job)
		}
	})
	t.Run("preview", func(t *testing.T) {
		env := newInheritTestEnv(t)
		started := make(chan struct{})
		g := &scriptedInheritGame{preview: func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}}
		env.useScriptedGame(g)
		id := env.createJob(validJobRequest())
		<-started
		if res := env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/cancel", nil); res.Status != http.StatusOK {
			t.Fatalf("cancel = %d %s", res.Status, res.Raw)
		}
		job := env.waitStatus(id, "cancelled")
		if errorCode(job) != codeCancelled || jobStepStatus(job, stepPreview) != "skipped" || g.count("execute") != 0 {
			t.Fatalf("job = %v", job)
		}
	})
}

func TestJobTransferPhaseFailures(t *testing.T) {
	maintenance := harukiSekai.ErrMaintenance
	cases := []struct {
		name     string
		game     *scriptedInheritGame
		upload   error
		mysekai  bool
		status   string
		code     string
		step     string
		stepWant string
		newCode  bool
	}{
		{name: "execute connection lost", game: &scriptedInheritGame{execute: func(context.Context) (string, error) {
			return "", harukiSekai.NewAPIError("/inherit", "POST", 0, "", nil)
		}}, status: "failed", code: codeExecuteUncertain, step: stepExecute, stepWant: "failed"},
		{name: "suite maintenance", game: &scriptedInheritGame{suite: func(context.Context) ([]byte, error) { return nil, maintenance }},
			status: "failed", code: codeGameMaintenance, step: stepFetchSuite, stepWant: "failed", newCode: true},
		{name: "upload fails", game: &scriptedInheritGame{}, upload: errors.New("unpack failed"),
			status: "failed", code: codeUploadFailed, step: stepUpload, stepWant: "failed", newCode: true},
		{name: "upload owned by another user", game: &scriptedInheritGame{}, upload: errUploadOwnershipMismatch,
			status: "failed", code: codeUploadForbidden, step: stepUpload, stepWant: "failed", newCode: true},
		{name: "mysekai", game: &scriptedInheritGame{}, mysekai: true,
			status: "succeeded", step: stepFetchMysekai, stepWant: "done", newCode: true},
		{name: "mysekai empty", game: &scriptedInheritGame{mysekai: func(context.Context) ([]byte, error) { return nil, nil }}, mysekai: true,
			status: "failed", code: codeMysekaiUnavailable, step: stepFetchMysekai, stepWant: "failed", newCode: true},
		{name: "mysekai maintenance", game: &scriptedInheritGame{mysekai: func(context.Context) ([]byte, error) { return nil, maintenance }}, mysekai: true,
			status: "failed", code: codeGameMaintenance, step: stepFetchMysekai, stepWant: "failed", newCode: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newInheritTestEnv(t)
			env.useScriptedGame(tc.game)
			var uploaded atomic.Pointer[[]byte]
			env.svc.upload = func(_ context.Context, _ *inheritJob, _ int64, _, mysekai []byte) error {
				uploaded.Store(&mysekai)
				return tc.upload
			}
			req := validJobRequest()
			if tc.mysekai {
				req["upload_type"] = "mysekai"
			}
			job := env.confirmAndWait(env.createJob(req), "succeeded", "failed")
			if job["status"] != tc.status || errorCode(job) != tc.code || jobStepStatus(job, tc.step) != tc.stepWant || (job["new_code"] != nil) != tc.newCode {
				t.Fatalf("job = %v", job)
			}
			if tc.name == "mysekai" {
				if got := uploaded.Load(); got == nil || string(*got) != "mysekai-bytes" {
					t.Fatalf("uploaded mysekai = %v", got)
				}
			}
			if tc.code == codeExecuteUncertain && tc.game.count("login") != 0 {
				t.Fatal("logged in after an uncertain execute")
			}
		})
	}
}

// The breaker and the slot limit are checked again before the executing
// call; a refused job never consumes the code.
func TestJobTransferPhaseGates(t *testing.T) {
	t.Run("breaker", func(t *testing.T) {
		env := newInheritTestEnv(t)
		id := env.createJob(validJobRequest())
		env.waitStatus(id, "awaiting_confirm")
		for range 5 {
			_, _, token := env.svc.breaker.Allow("jp")
			env.svc.breaker.RecordResult("jp", token, true)
		}
		env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/confirm", ownerPW())
		job := env.waitStatus(id, "failed")
		if errorCode(job) != codeGameDegraded || jobStepStatus(job, stepExecute) != "failed" || env.game.called("execute") {
			t.Fatalf("job = %v", job)
		}
	})
	t.Run("no slot", func(t *testing.T) {
		env := newInheritTestEnv(t)
		id := env.createJob(validJobRequest())
		env.waitStatus(id, "awaiting_confirm")
		for range 4 {
			if !env.svc.limiter.acquire("jp") {
				t.Fatal("acquire")
			}
		}
		env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/confirm", ownerPW())
		job := env.waitStatus(id, "failed")
		if errorCode(job) != codeServerBusy || env.game.called("execute") {
			t.Fatalf("job = %v", job)
		}
		for range 4 {
			env.svc.limiter.release("jp")
		}
	})
}

// The account is handed back even when the vault cannot keep it: the job
// still issues the new code and says what was not stored.
func TestJobVaultStoreFailures(t *testing.T) {
	t.Run("credential lost", func(t *testing.T) {
		env := newInheritTestEnv(t)
		env.svc.vault = inheritvault.NewStore(env.vaultClient, nil, env.svc.vault.Master())
		env.useScriptedGame(&scriptedInheritGame{execute: func(context.Context) (string, error) {
			_ = env.vaultClient.Close()
			return testGameCredential, nil
		}})
		job := env.confirmAndWait(env.createJob(validJobRequest()), "succeeded", "failed")
		if job["status"] != "succeeded" || warningList(job) != codeVaultStoreFailed || job["new_code"] == nil || job["vault_entry_id"] != nil ||
			jobStepStatus(job, stepVaultStoreCredential) != "failed" || jobStepStatus(job, stepVaultStoreCode) != "skipped" {
			t.Fatalf("job = %v", job)
		}
		if env.auditCount("inherit.vault.store", "failure") != 1 {
			t.Fatal("lost credential not audited as a failure")
		}
	})
	t.Run("code lost", func(t *testing.T) {
		env := newInheritTestEnv(t)
		env.svc.vault = inheritvault.NewStore(env.vaultClient, nil, env.svc.vault.Master())
		env.useScriptedGame(&scriptedInheritGame{login: func(context.Context) error {
			_ = env.vaultClient.Close()
			return nil
		}})
		job := env.confirmAndWait(env.createJob(validJobRequest()), "succeeded", "failed")
		if job["status"] != "succeeded" || warningList(job) != codeVaultStoreFailed || job["new_code"] == nil || job["vault_entry_id"] == nil ||
			jobStepStatus(job, stepVaultStoreCode) != "failed" {
			t.Fatalf("job = %v", job)
		}
	})
	t.Run("code parked", func(t *testing.T) {
		env := newInheritTestEnv(t)
		env.useScriptedGame(&scriptedInheritGame{login: func(context.Context) error {
			_ = env.vaultClient.Close()
			return nil
		}})
		job := env.confirmAndWait(env.createJob(validJobRequest()), "succeeded", "failed")
		if job["status"] != "succeeded" || warningList(job) != codeVaultFallback || jobStepStatus(job, stepVaultStoreCode) != "done" {
			t.Fatalf("job = %v", job)
		}
		if !env.redis.Exists("inherit:vault:fallback:" + job["vault_entry_id"].(string)) {
			t.Fatal("code not parked in Redis")
		}
	})
}

// A cancel that lands while the confirmation is checking the password wins:
// the job is cancelled and the confirmation is refused.
func TestConfirmLosesToACancelDuringUnlock(t *testing.T) {
	env := newInheritTestEnv(t)
	faults := installIdentityFaults(env)
	var target atomic.Pointer[string]
	var once sync.Once
	faults.onVerify = func() {
		if id := target.Load(); id != nil {
			once.Do(func() {
				if _, p := env.svc.cancel(context.Background(), requestMeta{UserID: testOwner}, *id); p != nil {
					t.Errorf("cancel = %v", p)
				}
			})
		}
	}
	id := env.createJob(validJobRequest())
	env.waitStatus(id, "awaiting_confirm")
	target.Store(&id)
	res := env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/confirm", ownerPW())
	if res.Status != http.StatusConflict || res.code() != "job_not_awaiting_confirm" {
		t.Fatalf("confirm after cancel = %d %s", res.Status, res.Raw)
	}
	if job := env.waitStatus(id, "cancelled"); errorCode(job) != codeCancelled || env.game.called("execute") {
		t.Fatalf("job = %v", job)
	}
}

func TestJobLookupEdges(t *testing.T) {
	env := newInheritTestEnv(t)
	if res := env.call(testOwner, "GET", "/api/inherit/jobs/no-such-job", nil); res.Status != http.StatusNotFound || res.code() != "job_not_found" {
		t.Fatalf("unknown job = %d %s", res.Status, res.Raw)
	}
	if res := env.call(testOwner, "POST", "/api/inherit/jobs/no-such-job/ws-ticket", nil); res.Status != http.StatusNotFound {
		t.Fatalf("ticket for an unknown job = %d %s", res.Status, res.Raw)
	}
	env.redis.Set(inheritJobKeyPrefix+"broken", "{not json")
	if res := env.call(testOwner, "GET", "/api/inherit/jobs/broken", nil); res.Status != http.StatusServiceUnavailable || res.code() != "store_unavailable" {
		t.Fatalf("unreadable job = %d %s", res.Status, res.Raw)
	}
	id, _ := env.runToSuccess()
	if res := env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/cancel", nil); res.Status != http.StatusConflict || res.code() != "cannot_cancel" {
		t.Fatalf("cancel a finished job = %d %s", res.Status, res.Raw)
	}
	if res := env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/confirm", ownerPW()); res.Status != http.StatusConflict || res.code() != "job_not_awaiting_confirm" {
		t.Fatalf("confirm a finished job = %d %s", res.Status, res.Raw)
	}
	ctx := context.Background()
	if env.svc.redeemWSTicket(ctx, "", testOwner, id) || env.svc.redeemWSTicket(ctx, strings.Repeat("t", 129), testOwner, id) {
		t.Fatal("empty or oversized ticket redeemed")
	}
}

// With Redis down a waiting job cannot hand out socket tickets or take a
// confirmation; it carries on once Redis is back.
func TestWaitingJobWithRedisDown(t *testing.T) {
	env := newInheritTestEnv(t)
	env.svc.confirmTimeout = time.Minute
	id := env.createJob(validJobRequest())
	env.waitStatus(id, "awaiting_confirm")
	env.redis.Close()
	if res := env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/ws-ticket", nil); res.Status != http.StatusServiceUnavailable || res.code() != "store_unavailable" {
		t.Fatalf("ticket with Redis down = %d %s", res.Status, res.Raw)
	}
	if res := env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/confirm", ownerPW()); res.Status != http.StatusServiceUnavailable || res.code() != "store_unavailable" {
		t.Fatalf("confirm with Redis down = %d %s", res.Status, res.Raw)
	}
	if err := env.redis.Restart(); err != nil {
		t.Fatal(err)
	}
	if res := env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/confirm", ownerPW()); res.Status != http.StatusAccepted {
		t.Fatalf("confirm once Redis is back = %d %s", res.Status, res.Raw)
	}
	env.waitStatus(id, "succeeded")
}

func TestAwaitConfirmationTakesARacingConfirmation(t *testing.T) {
	env := newInheritTestEnv(t)
	rt := &jobRuntime{
		job:       newInheritJob(testOwner, "jp", "suite", "random", time.Now().UTC()),
		confirmed: true,
		confirmCh: make(chan confirmation, 1),
		cancelCh:  make(chan struct{}, 1),
	}
	// The cancel is the only thing ready when the wait starts, but the
	// confirmation already won: the wait takes it instead of cancelling.
	rt.cancelCh <- struct{}{}
	type result struct {
		conf confirmation
		done bool
	}
	out := make(chan result, 1)
	go func() {
		conf, _, done := env.svc.awaitConfirmation(rt, nil)
		out <- result{conf, done}
	}()
	rt.confirmCh <- confirmation{keyVersion: 7}
	got := <-out
	if got.done || got.conf.keyVersion != 7 {
		t.Fatalf("await = %+v", got)
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.cancelled || rt.job.Status != jobStatusRunning || rt.job.step(stepAwaitingConfirm).Status != stepOK {
		t.Fatalf("job = %+v cancelled=%v", rt.job, rt.cancelled)
	}
}

func TestIssueWithRetryStopsWhenTheContextEnds(t *testing.T) {
	s := &inheritService{issueBackoff: func(int) time.Duration { return time.Hour }}
	failure := harukiSekai.NewAPIError("/user/x/inherit", "PUT", 500, "", nil)
	g := &scriptedInheritGame{issue: func(context.Context) (string, error) { return "", failure }}
	if shutdownRequested(nil) || shutdownRequested(make(chan struct{})) {
		t.Fatal("shutdown reported without being signalled")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.issueWithRetry(ctx, g, "password1")
	if !errors.Is(err, context.Canceled) || !errors.Is(err, failure) || g.count("issue") != 1 {
		t.Fatalf("err = %v after %d attempts", err, g.count("issue"))
	}
}

func TestUploadDataRefusesUndecodableData(t *testing.T) {
	env := newInheritTestEnv(t)
	job := newInheritJob(testOwner, "jp", "mysekai", "random", time.Now().UTC())
	ctx := context.Background()
	if err := env.svc.uploadData(ctx, job, testGameUID, []byte("not suite data"), []byte("not mysekai data")); err == nil {
		t.Fatal("undecodable mysekai data uploaded")
	}
	if err := env.svc.uploadData(ctx, job, testGameUID, []byte("not suite data"), nil); err == nil {
		t.Fatal("undecodable suite data uploaded")
	}
	// Without an API helper a retrieval failure is not recorded (and does not
	// panic).
	(&inheritService{}).recordRetrievalFailure(job, testGameUID, errors.New("x"))
}

func TestJobStoreEdges(t *testing.T) {
	ctx := context.Background()
	var none jobStore
	if err := none.save(ctx, newInheritJob(testOwner, "jp", "suite", "random", time.Now())); err == nil {
		t.Fatal("save without Redis")
	}
	if _, err := none.load(ctx, "x"); err == nil {
		t.Fatal("load without Redis")
	}
	env := newInheritTestEnv(t)
	if job, err := env.svc.jobs.load(ctx, "missing"); job != nil || err != nil {
		t.Fatalf("missing job = %v %v", job, err)
	}
	// A record past its hour still gets a short TTL rather than none.
	old := newInheritJob(testOwner, "jp", "suite", "random", time.Now().Add(-2*inheritJobTTL))
	if err := env.svc.jobs.save(ctx, old); err != nil {
		t.Fatal(err)
	}
	if ttl := env.redis.TTL(inheritJobKeyPrefix + old.ID); ttl <= 0 || ttl > time.Second {
		t.Fatalf("TTL of an expired job = %v", ttl)
	}
	(&inheritService{}).releaseActive(testOwner, old.ID)
}

func TestJobRuntimeDropsSlowSubscribers(t *testing.T) {
	rt := &jobRuntime{}
	slow, _ := rt.subscribe()
	fast, unsubscribe := rt.subscribe()
	rt.mu.Lock()
	for range cap(slow) + 1 {
		rt.publishLocked([]byte("m"))
		select {
		case <-fast:
		default:
		}
	}
	rt.mu.Unlock()
	n := 0
	for range slow {
		n++
	}
	if n != cap(fast) {
		t.Fatalf("slow subscriber got %d messages before being dropped", n)
	}
	unsubscribe()
	if _, ok := <-fast; ok {
		t.Fatal("unsubscribe left the channel open")
	}
	unsubscribe()
}

func TestJobStepBookkeeping(t *testing.T) {
	env := newInheritTestEnv(t)
	rt := &jobRuntime{job: newInheritJob(testOwner, "jp", "suite", "random", time.Now().UTC())}
	before := rt.job.clone()
	env.svc.setStep(rt, "no-such-step", stepRunning, "")
	rt.job.CurrentStep = "no-such-step"
	env.svc.progressFor(rt)(harukiSekai.ProgressEvent{Step: harukiSekai.SubStepVersion, Status: harukiSekai.ProgressDone})
	for i := range rt.job.Steps {
		if rt.job.Steps[i].Status != before.Steps[i].Status || len(rt.job.Steps[i].Substeps) != 0 {
			t.Fatalf("unknown step changed %v", rt.job.Steps[i])
		}
	}

	env.svc.setStep(rt, stepLogin, stepRunning, "")
	progress := env.svc.progressFor(rt)
	progress(harukiSekai.ProgressEvent{Step: harukiSekai.SubStepVersion, Status: harukiSekai.ProgressRunning})
	progress(harukiSekai.ProgressEvent{Step: harukiSekai.SubStepVersion, Status: harukiSekai.ProgressDone, Note: "n"})
	if sub := rt.job.step(stepLogin).Substeps; len(sub) != 1 || sub[0].Status != string(harukiSekai.ProgressDone) || sub[0].Note != "n" {
		t.Fatalf("substeps = %+v", sub)
	}
	// A step still running when the job ends is marked failed.
	env.svc.finish(rt, jobStatusFailed, codeInternal)
	if st := rt.job.step(stepLogin); st.Status != stepFailed || st.FinishedAt == nil {
		t.Fatalf("running step at the end = %+v", st)
	}
	if rt.job.step(stepUpload).Status != stepSkipped || rt.job.Error.Code != codeInternal {
		t.Fatalf("job = %+v", rt.job)
	}
	// A step finished without having started gets its start time.
	env.svc.setStep(rt, stepUpload, stepOK, "")
	if st := rt.job.step(stepUpload); st.StartedAt == nil {
		t.Fatalf("step finished without a start time: %+v", st)
	}
}

func TestWriteAuditActorsAndTargets(t *testing.T) {
	(&inheritService{}).writeAudit(requestMeta{UserID: testOwner}, "inherit.job.create", "success", "j", nil)
	env := newInheritTestEnv(t)
	env.svc.writeAudit(requestMeta{}, "inherit.vault.store", "success", "", nil)
	env.svc.writeAudit(requestMeta{UserID: testOwner, IP: "203.0.113.9", Method: "POST", Path: "/p"}, "inherit.vault.key.create", "success", testOwner, nil)
	ctx := context.Background()
	store := env.db.SystemLog.Query().Where(systemlog.ActionEQ("inherit.vault.store")).OnlyX(ctx)
	if string(store.ActorType) != harukiAPIHelper.SystemLogActorTypeSystem || store.TargetID != nil || store.TargetType == nil || *store.TargetType != "inherit_vault_entry" {
		t.Fatalf("system audit row = %+v", store)
	}
	key := env.db.SystemLog.Query().Where(systemlog.ActionEQ("inherit.vault.key.create")).OnlyX(ctx)
	if string(key.ActorType) != harukiAPIHelper.SystemLogActorTypeUser || *key.TargetType != "user" || key.IP == nil || *key.IP != "203.0.113.9" {
		t.Fatalf("user audit row = %+v", key)
	}
}

func TestInheritServiceDefaults(t *testing.T) {
	if errJobNotFound.Error() != "job_not_found" {
		t.Fatalf("Error() = %q", errJobNotFound.Error())
	}
	s := newInheritService(nil, Dependencies{})
	if s.available() || s.redis != nil {
		t.Fatal("service without Redis or a vault reports available")
	}
	if got := s.issueBackoff(2); got != 2*time.Second {
		t.Fatalf("issue backoff = %v", got)
	}
	if _, err := s.newGame("tw", nil); err == nil {
		t.Fatal("game session for an unsupported server")
	}
	if g, err := s.newGame("jp", nil); err == nil && g == nil {
		t.Fatal("nil game without an error")
	}
}

func TestRegisterInheritRoutes(t *testing.T) {
	want := []string{
		"DELETE /api/inherit/vault/entries/:entry_id",
		"GET /api/inherit/jobs/:job_id",
		"GET /api/inherit/jobs/:job_id/ws",
		"GET /api/inherit/vault/entries",
		"GET /api/inherit/vault/key",
		"POST /api/inherit/:server/:upload_type/submit",
		"POST /api/inherit/jobs",
		"POST /api/inherit/jobs/:job_id/cancel",
		"POST /api/inherit/jobs/:job_id/confirm",
		"POST /api/inherit/jobs/:job_id/ws-ticket",
		"POST /api/inherit/vault/entries/:entry_id/reissue",
		"POST /api/inherit/vault/entries/:entry_id/reveal",
		"POST /api/inherit/vault/key/reset",
	}
	routes := func(app *fiber.App) []string {
		var out []string
		for _, r := range app.GetRoutes(true) {
			if r.Method == fiber.MethodHead {
				continue
			}
			out = append(out, r.Method+" "+r.Path)
		}
		slices.Sort(out)
		return slices.Compact(out)
	}

	// Without sessions only the retired route answers.
	bare := fiber.New()
	registerInheritRoutes(&harukiAPIHelper.HarukiToolboxRouterHelpers{Router: bare}, testUploadDependencies())
	if got := routes(bare); !slices.Equal(got, []string{"POST /api/inherit/:server/:upload_type/submit"}) {
		t.Fatalf("routes without sessions = %v", got)
	}

	app := fiber.New()
	registerInheritRoutes(&harukiAPIHelper.HarukiToolboxRouterHelpers{Router: app, SessionHandler: harukiAPIHelper.NewSessionHandler(nil, "")}, testUploadDependencies())
	if got := routes(app); !slices.Equal(got, want) {
		t.Fatalf("routes = %v\nwant %v", got, want)
	}
	// Every job and vault route sits behind the session check.
	for _, route := range want[:len(want)-1] {
		method, path, _ := strings.Cut(route, " ")
		if strings.HasSuffix(path, "/submit") {
			continue
		}
		path = strings.NewReplacer(":job_id", "j1", ":entry_id", "e1").Replace(path)
		res, err := app.Test(httptest.NewRequest(method, path, nil))
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s without a session = %d", method, path, res.StatusCode)
		}
	}
}

func TestWebSocketRefusals(t *testing.T) {
	env := newInheritTestEnv(t)
	if res := env.call(testOwner, "GET", "/api/inherit/jobs/j1/ws", nil); res.Status != http.StatusUpgradeRequired || res.code() != "websocket_required" {
		t.Fatalf("plain GET = %d %s", res.Status, res.Raw)
	}
	// A valid ticket on a malformed handshake: the upgrader answers 400 and
	// the ticket is used up.
	env.redis.Set(wsTicketKey("t-bad-handshake"), testOwner+"|j1")
	req := httptest.NewRequest("GET", "/api/inherit/jobs/j1/ws?ticket=t-bad-handshake", nil)
	req.Header.Set("X-Test-User", testOwner)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	res, err := env.app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest || env.redis.Exists(wsTicketKey("t-bad-handshake")) {
		t.Fatalf("bad handshake = %d", res.StatusCode)
	}
}

// wsTestServer serves env.app on a real listener for WebSocket clients.
func wsTestServer(t *testing.T, env *inheritTestEnv) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = env.app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true}) }()
	t.Cleanup(func() { _ = env.app.Shutdown() })
	return "ws://" + ln.Addr().String()
}

func dialJobWS(t *testing.T, env *inheritTestEnv, base, jobID, ticket string) *websocket.Conn {
	t.Helper()
	if ticket == "" {
		res := env.call(testOwner, "POST", "/api/inherit/jobs/"+jobID+"/ws-ticket", nil)
		if res.Status != http.StatusOK {
			t.Fatalf("ticket = %d %s", res.Status, res.Raw)
		}
		ticket = res.data()["ticket"].(string)
	}
	conn, _, err := websocket.DefaultDialer.Dial(base+"/api/inherit/jobs/"+jobID+"/ws?ticket="+ticket, http.Header{"X-Test-User": {testOwner}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	return conn
}

// readWS returns the next frame that is not a progress event.
func readWS(t *testing.T, conn *websocket.Conn) wsMessage {
	t.Helper()
	for {
		_, body, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var m wsMessage
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatal(err)
		}
		if m.Type != "event" {
			return m
		}
	}
}

func expectWSClose(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	for {
		_, body, err := conn.ReadMessage()
		if err == nil {
			var m wsMessage
			if json.Unmarshal(body, &m) == nil && m.Type == "event" {
				continue
			}
			t.Fatalf("frame after the end: %s", body)
		}
		if !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
			t.Fatalf("close = %v", err)
		}
		return
	}
}

func TestWebSocketSessions(t *testing.T) {
	env := newInheritTestEnv(t)
	env.svc.confirmTimeout = time.Minute
	base := wsTestServer(t, env)

	// A ticket for a job that does not exist (or is not the owner's).
	env.redis.Set(wsTicketKey("t-missing"), testOwner+"|missing-job")
	conn := dialJobWS(t, env, base, "missing-job", "t-missing")
	if m := readWS(t, conn); m.Type != "error" || m.Code != "job_not_found" {
		t.Fatalf("missing job = %+v", m)
	}

	// A finished job: the snapshot, then a normal close.
	id, _ := env.runToSuccess()
	conn = dialJobWS(t, env, base, id, "")
	if m := readWS(t, conn); m.Type != "snapshot" || m.Job.Status != jobStatusSucceeded {
		t.Fatalf("finished snapshot = %+v", m)
	}
	expectWSClose(t, conn)

	// The client hanging up ends the subscription while the job goes on.
	id = env.createJob(validJobRequest())
	env.waitStatus(id, "awaiting_confirm")
	rt := env.svc.registry.get(id)
	conn = dialJobWS(t, env, base, id, "")
	if m := readWS(t, conn); m.Type != "snapshot" {
		t.Fatalf("snapshot = %+v", m)
	}
	_ = conn.Close()
	waitFor(t, func() bool { rt.mu.Lock(); defer rt.mu.Unlock(); return len(rt.subs) == 0 })
	if res := env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/cancel", nil); res.Status != http.StatusOK {
		t.Fatalf("cancel = %d %s", res.Status, res.Raw)
	}
	env.waitStatus(id, "cancelled")

	// A waiting job takes commands over the socket. This session goes last.
	env.game = &fakeInheritGame{}
	id = env.createJob(validJobRequest())
	env.waitStatus(id, "awaiting_confirm")
	conn = dialJobWS(t, env, base, id, "")
	if m := readWS(t, conn); m.Type != "snapshot" || m.Job.Status != jobStatusAwaitingConfirm {
		t.Fatalf("snapshot = %+v", m)
	}
	send := func(frame string) {
		t.Helper()
		if err := conn.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
			t.Fatal(err)
		}
	}
	send(`not json`)
	if m := readWS(t, conn); m.Type != "error" || m.Code != "invalid_request" {
		t.Fatalf("bad frame = %+v", m)
	}
	send(`{"type":"ping"}`)
	if err := conn.WriteControl(websocket.PongMessage, nil, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	send(`{"type":"launch"}`)
	if m := readWS(t, conn); m.Type != "error" || m.Action != "launch" || m.Code != "invalid_request" {
		t.Fatalf("unknown command = %+v (a ping must not be answered)", m)
	}
	send(`{"type":"confirm","password":"wrong"}`)
	if m := readWS(t, conn); m.Type != "error" || m.Action != "confirm" || m.Code != "password_invalid" {
		t.Fatalf("wrong password = %+v", m)
	}
	send(`{"type":"cancel"}`)
	if m := readWS(t, conn); m.Type != "ack" || m.Action != "cancel" {
		t.Fatalf("cancel = %+v", m)
	}
	expectWSClose(t, conn)
	// An HTTP request right after the server closed the socket: serveWS must
	// not return before its reader goroutine stops, or fasthttp hands that
	// goroutine's pooled buffers to this request (caught by -race).
	env.waitStatus(id, "cancelled")
	if env.game.called("execute") {
		t.Fatal("cancelled job executed")
	}
}

// Regression: when a job ends, the server closes its socket and serveWS
// returns. Its reader goroutine must be gone by then, because fasthttp pools
// the hijacked connection's bufio.Reader and hands it to the next request.
// The HTTP request right after each close is what the race detector caught.
func TestWebSocketCloseThenHTTPRequestDoesNotShareBuffers(t *testing.T) {
	env := newInheritTestEnv(t)
	env.svc.confirmTimeout = time.Minute
	base := wsTestServer(t, env)
	for range 8 {
		for _, key := range env.redis.Keys() {
			if strings.HasPrefix(key, inheritRateKeyPrefix) {
				env.redis.Del(key)
			}
		}
		id := env.createJob(validJobRequest())
		env.waitStatus(id, "awaiting_confirm")
		conn := dialJobWS(t, env, base, id, "")
		if m := readWS(t, conn); m.Type != "snapshot" {
			t.Fatalf("snapshot = %+v", m)
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"cancel"}`)); err != nil {
			t.Fatal(err)
		}
		if m := readWS(t, conn); m.Type != "ack" || m.Action != "cancel" {
			t.Fatalf("cancel = %+v", m)
		}
		expectWSClose(t, conn)
		// Straight after the server-side close, before anything else.
		// The job record is final before its subscribers are closed.
		if job := env.job(id); job["status"] != "cancelled" {
			t.Fatalf("job = %v", job)
		}
		_ = conn.Close()
	}
}
