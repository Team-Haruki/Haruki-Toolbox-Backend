package upload

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	harukiSekai "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/game/sekai"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
)

// errOutdated is what the session returns when a 426 survived a version
// refresh: ErrGameVersionOutdated wrapping the game's answer.
func errOutdated(path string) error {
	return fmt.Errorf("%w: %w", harukiSekai.ErrGameVersionOutdated, harukiSekai.NewAPIError(path, "GET", 426, "non-200 response", nil))
}

// A lagging version source is not a bad code: the preview fails with its own
// code, and the executing call is never made.
func TestPreviewVersionOutdatedIsNotABadCode(t *testing.T) {
	env := newInheritTestEnv(t)
	env.game.previewErr = errOutdated("/inherit/user/<redacted>")
	job := env.waitStatus(env.createJob(validJobRequest()), "failed")
	if errorCode(job) != codeGameVersionOutdated || env.game.called("execute") {
		t.Fatalf("job = %v, calls %v", job, env.game.calls)
	}
}

// ⚠️ A 426 on the executing call is the game's definite answer: one call,
// reported as game_version_outdated (not execute_uncertain), nothing after.
func TestExecute426IsReportedOnceAndNotRetried(t *testing.T) {
	env := newInheritTestEnv(t)
	env.game.executeErr = harukiSekai.NewAPIError("/inherit/user/<redacted>", "POST", 426, "non-200 response", nil)
	id := env.createJob(validJobRequest())
	env.waitStatus(id, "awaiting_confirm")
	env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/confirm", map[string]any{"password": testOwnerPassword})
	job := env.waitStatus(id, "failed")
	if errorCode(job) != codeGameVersionOutdated {
		t.Fatalf("error = %v", job["error"])
	}
	if n := strings.Count(fmt.Sprint(env.game.calls), "execute"); n != 1 || env.game.called("login") {
		t.Fatalf("calls = %v, want exactly one execute and no login", env.game.calls)
	}
}

// After the transfer the code is used; a fetch that still gets 426 fails with
// the distinct code, and the new code is still delivered.
func TestFetchVersionOutdatedKeepsTheNewCode(t *testing.T) {
	env := newInheritTestEnv(t)
	env.game.suiteErr = harukiSekai.NewDataRetrievalError("suite", "api_call", "failed to call suite API", errOutdated("/suite/user/<redacted>"))
	id := env.createJob(validJobRequest())
	env.waitStatus(id, "awaiting_confirm")
	env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/confirm", map[string]any{"password": testOwnerPassword})
	job := env.waitStatus(id, "failed")
	if errorCode(job) != codeGameVersionOutdated {
		t.Fatalf("error = %v", job["error"])
	}
	if code, _ := job["new_code"].(map[string]any); code["inherit_id"] != testIssuedID {
		t.Fatalf("new code = %v", job["new_code"])
	}
	if n := strings.Count(fmt.Sprint(env.game.calls), "execute"); n != 1 {
		t.Fatalf("execute calls = %d", n)
	}
}

// Login keeps login_failed (credential saved, re-issue later) and says why.
func TestLoginVersionOutdatedWarns(t *testing.T) {
	env := newInheritTestEnv(t)
	env.game.loginErr = errOutdated("/user/<redacted>/auth")
	id := env.createJob(validJobRequest())
	env.waitStatus(id, "awaiting_confirm")
	env.call(testOwner, "POST", "/api/inherit/jobs/"+id+"/confirm", map[string]any{"password": testOwnerPassword})
	job := env.waitStatus(id, "failed")
	if errorCode(job) != codeLoginFailed || fmt.Sprint(warningCodes(job)) != fmt.Sprint([]string{codeGameVersionOutdated}) {
		t.Fatalf("job = %v", job)
	}
}

func TestGameFailureCodeFor426(t *testing.T) {
	plain := harukiSekai.NewAPIError("/x", "POST", 426, "non-200 response", nil)
	for _, err := range []error{plain, errOutdated("/x"), fmt.Errorf("wrapped: %w", plain)} {
		if got := gameFailureCode(err); got != codeGameVersionOutdated {
			t.Fatalf("gameFailureCode(%v) = %s", err, got)
		}
	}
	if gameFailureCode(errFakeForbidden) != codeInheritInvalid {
		t.Fatal("403 no longer means a bad code")
	}
	if jobMessages[codeGameVersionOutdated] == "" {
		t.Fatal("no message for game_version_outdated")
	}
	if inheritFailureIsUpstreamDegradation(plain) {
		t.Fatal("a 426 must not trip the circuit breaker")
	}
}

// The refresh outcome is logged with the job, step and versions only.
func TestVersionRefreshIsLogged(t *testing.T) {
	var out bytes.Buffer
	svc := &inheritService{logger: harukiLogger.NewLogger("InheritTest", "DEBUG", &out)}
	rt := &jobRuntime{job: &inheritJob{ID: "job-1", Server: "jp"}}
	svc.logVersionRefresh(rt, stepFetchSuite, harukiSekai.ProgressEvent{Step: harukiSekai.SubStepVersionRefresh, Status: harukiSekai.ProgressDone, Note: "login data=7.1.0.45 asset=7.1.0.45"})
	svc.logVersionRefresh(rt, stepPreview, harukiSekai.ProgressEvent{Step: harukiSekai.SubStepVersionRefresh, Status: harukiSekai.ProgressFailed, Note: "unchanged"})
	svc.logVersionRefresh(rt, stepPreview, harukiSekai.ProgressEvent{Step: harukiSekai.SubStepVersionRefresh, Status: harukiSekai.ProgressRunning})
	svc.logVersionRefresh(rt, stepPreview, harukiSekai.ProgressEvent{Step: harukiSekai.SubStepSuite, Status: harukiSekai.ProgressDone})
	got := out.String()
	if strings.Count(got, "inherit job game version outdated") != 2 ||
		!strings.Contains(got, "refreshed job=job-1 server=jp step=fetch_suite result=login data=7.1.0.45") ||
		!strings.Contains(got, "no newer version found job=job-1 server=jp step=preview result=unchanged") {
		t.Fatalf("log = %q", got)
	}
}
