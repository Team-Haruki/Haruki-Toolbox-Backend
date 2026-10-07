package oauth2

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
)

// Each chain stage's failure maps to its outcome: transport and status
// failures before the consent is accepted are retryable (the flow returns to
// claimed), an unexpected Location fails the flow.
func TestDeviceChainStageFailures(t *testing.T) {
	type outcome int
	const (
		retryable outcome = iota
		failed
	)
	cases := []struct {
		name     string
		stage    string
		reason   string
		hook     func(t *testing.T, w http.ResponseWriter) // answers the stage itself
		location func(location string) string              // or rewrites its Location
		want     outcome
	}{
		{name: "verify start transport", stage: chainStageVerifyStart, reason: deviceChainReasonTransport,
			hook: func(t *testing.T, w http.ResponseWriter) { closeConnection(t, w) }, want: retryable},
		{name: "verify start without CSRF cookie", stage: chainStageVerifyStart, reason: deviceChainReasonNoCSRF,
			hook: func(_ *testing.T, w http.ResponseWriter) {
				w.Header().Set("Location", testFrontendURL+"/device?device_challenge=c")
				w.WriteHeader(http.StatusFound)
			}, want: retryable},
		{name: "verify start Location with fragment", stage: chainStageVerifyStart, reason: deviceChainReasonUnexpected,
			location: func(location string) string { return location + "#fragment" }, want: failed},
		{name: "verify start without challenge", stage: chainStageVerifyStart, reason: deviceChainReasonUnexpected,
			location: func(string) string { return testFrontendURL + "/device" }, want: failed},
		{name: "device accept 500", stage: chainStageDeviceAccept, reason: deviceChainReasonTransport,
			hook: func(_ *testing.T, w http.ResponseWriter) {
				writeFakeHydraJSON(w, http.StatusInternalServerError, map[string]any{"error": "server_error"})
			}, want: retryable},
		{name: "verify device transport", stage: chainStageVerifyDevice, reason: deviceChainReasonTransport,
			hook: func(t *testing.T, w http.ResponseWriter) { closeConnection(t, w) }, want: retryable},
		{name: "verify device without login challenge", stage: chainStageVerifyDevice, reason: deviceChainReasonUnexpected,
			location: func(string) string { return testFrontendURL + "/oauth2/login" }, want: failed},
		{name: "login accept 500", stage: chainStageLoginAccept, reason: deviceChainReasonTransport,
			hook: func(_ *testing.T, w http.ResponseWriter) {
				writeFakeHydraJSON(w, http.StatusInternalServerError, map[string]any{"error": "server_error"})
			}, want: retryable},
		{name: "verify login transport", stage: chainStageVerifyLogin, reason: deviceChainReasonTransport,
			hook: func(t *testing.T, w http.ResponseWriter) { closeConnection(t, w) }, want: retryable},
		{name: "verify login 500", stage: chainStageVerifyLogin, reason: deviceChainReasonStatus,
			hook: func(_ *testing.T, w http.ResponseWriter) {
				writeFakeHydraJSON(w, http.StatusInternalServerError, map[string]any{"error": "server_error"})
			}, want: retryable},
		{name: "verify login without consent challenge", stage: chainStageVerifyLogin, reason: deviceChainReasonUnexpected,
			location: func(string) string { return testFrontendURL + "/oauth2/consent" }, want: failed},
		{name: "consent request 500", stage: chainStageConsentGet, reason: deviceChainReasonTransport,
			hook: func(_ *testing.T, w http.ResponseWriter) {
				writeFakeHydraJSON(w, http.StatusInternalServerError, map[string]any{"error": "server_error"})
			}, want: retryable},
		{name: "consent accept 500", stage: chainStageConsentAcc, reason: deviceChainReasonTransport,
			hook: func(_ *testing.T, w http.ResponseWriter) {
				writeFakeHydraJSON(w, http.StatusInternalServerError, map[string]any{"error": "server_error"})
			}, want: retryable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newDeviceBrowserEnv(t)
			if tc.hook != nil {
				env.chain.hook = func(stage string, _ int, w http.ResponseWriter, _ *http.Request) bool {
					if stage != tc.stage {
						return false
					}
					tc.hook(t, w)
					return true
				}
			}
			if tc.location != nil {
				env.chain.mutateLocation = func(stage, location string) string {
					if stage == tc.stage {
						return tc.location(location)
					}
					return location
				}
			}
			flowID, userCode, handle := env.claim()
			resp := env.approve(handle, userCode, browserCall{})
			wantState := deviceFlowStateClaimed
			if tc.want == failed {
				wantState = deviceFlowStateFailed
			}
			expectApprovalFailed(t, resp, tc.want == retryable)
			if got := env.field(flowID, "st"); got != wantState {
				t.Fatalf("state = %s, want %s", got, wantState)
			}
			if env.inUnredeemed(flowID) || env.chain.stageHits(chainStageVerifyFinal) != 0 {
				t.Fatal("a chain that failed before the final hop left a consent behind")
			}
			if !strings.Contains(env.logs.String(), "stage="+tc.stage+" reason="+tc.reason) {
				t.Fatalf("logs do not name stage %s reason %s: %s", tc.stage, tc.reason, env.logs.String())
			}
			env.assertNoSecretLeaks()
		})
	}
}

// A remembered consent of a confidential client is still decided by the
// chain, and only logged.
func TestDeviceChainConsentSkipIsLogged(t *testing.T) {
	env := newDeviceBrowserEnv(t)
	env.chain.mutateJSON = func(stage string, body map[string]any) {
		if stage == chainStageConsentGet {
			body["skip"] = true
		}
	}
	flowID, userCode, handle := env.claim()
	approvedData(t, env.approve(handle, userCode, browserCall{}))
	if env.field(flowID, "st") != deviceFlowStateApproved || env.chain.stageHits(chainStageConsentAcc) != 1 {
		t.Fatalf("state = %s", env.field(flowID, "st"))
	}
	if !strings.Contains(env.logs.String(), "stage=H9h consent_skip=true") {
		t.Fatalf("consent skip not logged: %s", env.logs.String())
	}
	env.assertNoSecretLeaks()
}

// A final Location off the frontend means Hydra did something unexpected
// after the consent was accepted: the consent is revoked and the flow failed.
func TestDeviceChainFinalHopForeignOriginFailsAndRevokes(t *testing.T) {
	env := newDeviceBrowserEnv(t)
	env.chain.mutateLocation = func(stage, location string) string {
		if stage == chainStageVerifyFinal {
			return "https://evil.example.com/device/done?client_id=" + testPublicClientID
		}
		return location
	}
	flowID, userCode, handle := env.claim()
	expectApprovalFailed(t, env.approve(handle, userCode, browserCall{}), false)
	if got := env.revocations(); len(got) != 1 || got[0] != "consent_request_id=consent-request-1" {
		t.Fatalf("revocations = %v", got)
	}
	if env.field(flowID, "st") != deviceFlowStateFailed || env.inUnredeemed(flowID) {
		t.Fatalf("state = %s, unredeemed = %v", env.field(flowID, "st"), env.inUnredeemed(flowID))
	}
	env.assertNoSecretLeaks()
}

func TestDeviceChainMismatchError(t *testing.T) {
	mismatch := &deviceChainMismatch{reason: deviceChainReasonMarker}
	if mismatch.Error() != "device chain mismatch: "+deviceChainReasonMarker {
		t.Fatalf("Error() = %q", mismatch.Error())
	}
	if !errors.Is(mismatch, errUnexpectedHydraRedirect) {
		t.Fatal("a mismatch does not unwrap to errUnexpectedHydraRedirect")
	}
	if got := reasonOf(mismatch); got != deviceChainReasonMarker {
		t.Fatalf("reasonOf(mismatch) = %q", got)
	}
	if got := reasonOf(errors.New("other")); got != deviceChainReasonUnexpected {
		t.Fatalf("reasonOf(other) = %q", got)
	}
}

func TestRewriteIssuerVerifyURL(t *testing.T) {
	hydra := harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{PublicURL: "http://hydra:4444"})
	got, err := rewriteIssuerVerifyURL(hydra, "flow=f&client_id=c")
	if err != nil || got != "http://hydra:4444"+hydraDeviceVerifyPath+"?flow=f&client_id=c" {
		t.Fatalf("rewrite = %q, %v", got, err)
	}
	_, err = rewriteIssuerVerifyURL(nil, "flow=f")
	var mismatch *deviceChainMismatch
	if !errors.As(err, &mismatch) || mismatch.reason != deviceChainReasonUnexpected {
		t.Fatalf("rewrite without Hydra err = %v", err)
	}
}
