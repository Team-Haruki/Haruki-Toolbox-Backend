package oauth2

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/systemlog"

	"github.com/gofiber/fiber/v3"
)

// anonymousBrowserPost is a browser request that passed Oathkeeper's guard
// but carries no session locals.
func (e *deviceBrowserEnv) anonymousBrowserPost(path, body string) deviceTestResponse {
	e.t.Helper()
	return e.do(path, "application/json", body, map[string]string{"Origin": testFrontendURL})
}

func TestDeviceBrowserWithoutSessionIs503(t *testing.T) {
	env := newDeviceBrowserEnv(t)
	_, flowID := env.issue(testPublicClientID)
	for path, body := range map[string]string{
		"/api/oauth2/device/lookup":  `{"userCode":"` + env.lastUserCode() + `"}`,
		"/api/oauth2/device/approve": `{"flowHandle":"dfh_x","userCode":"` + env.lastUserCode() + `","acknowledged":true}`,
		"/api/oauth2/device/deny":    `{"flowHandle":"dfh_x"}`,
	} {
		t.Run(path, func(t *testing.T) {
			expectBrowserError(t, env.anonymousBrowserPost(path, body), http.StatusServiceUnavailable, deviceCodeTemporarilyUnavailable)
		})
	}
	if env.field(flowID, "st") != deviceFlowStatePending {
		t.Fatalf("state = %s", env.field(flowID, "st"))
	}
}

// Without an auth-proxy session ID the claim binds to the Kratos identity, and
// without either to the user ID.
func TestDeviceLookupSessionBindingFallbacks(t *testing.T) {
	env := newDeviceBrowserEnv(t)
	kratosOnly := func(c fiber.Ctx) error {
		c.Locals("userID", testUserID)
		c.Locals("identityID", "kratos-"+testUserID)
		return c.Next()
	}
	userOnly := func(c fiber.Ctx) error {
		c.Locals("userID", testUserID)
		return c.Next()
	}
	env.app.Post("/api/oauth2/device/kratos-only/lookup", kratosOnly, env.handlers.handleDeviceLookup)
	env.app.Post("/api/oauth2/device/user-only/lookup", userOnly, env.handlers.handleDeviceLookup)
	keys := env.db.Redis.KeyBuilder()
	for _, tc := range []struct{ path, session string }{
		{"/api/oauth2/device/kratos-only/lookup", "kratos:kratos-" + testUserID},
		{"/api/oauth2/device/user-only/lookup", "user:" + testUserID},
	} {
		t.Run(tc.session, func(t *testing.T) {
			_, flowID := env.issue(testPublicClientID)
			resp := env.anonymousBrowserPost(tc.path, `{"userCode":"`+env.lastUserCode()+`"}`)
			if resp.Status != http.StatusOK {
				t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
			}
			if got, want := env.field(flowID, "csh"), keys.HashOAuth2DeviceIdentifier("sess", tc.session); got != want {
				t.Fatalf("claim session hash = %q, want hash of %q", got, tc.session)
			}
		})
	}
}

func TestDeviceBrowserRedisUnavailable(t *testing.T) {
	env := newDeviceBrowserEnv(t)
	flowID, userCode, handle := env.claim()
	env.redis.SetError(testRedisLoadingError)
	expectBrowserError(t, env.lookup(userCode, browserCall{}), http.StatusServiceUnavailable, deviceCodeTemporarilyUnavailable)
	expectBrowserError(t, env.approve(handle, userCode, browserCall{}), http.StatusServiceUnavailable, deviceCodeTemporarilyUnavailable)
	expectBrowserError(t, env.deny(handle, "", browserCall{}), http.StatusServiceUnavailable, deviceCodeTemporarilyUnavailable)
	env.redis.SetError("")
	if env.field(flowID, "st") != deviceFlowStateClaimed || env.chain.stageHits(chainStageVerifyStart) != 0 {
		t.Fatalf("state = %s", env.field(flowID, "st"))
	}
}

// A claim that fails after the failure budget was reserved gives the budget
// back.
func TestDeviceLookupClaimFailureReleasesBudget(t *testing.T) {
	env := newDeviceBrowserEnv(t)
	_, flowID := env.issue(testPublicClientID)
	userCode := env.lastUserCode()
	indexKey := env.db.Redis.KeyBuilder().BuildOAuth2DeviceUserCodeIndexKey(userCode)
	flowIDValue, _ := env.redis.Get(indexKey)
	env.redis.Del(indexKey)
	env.redis.HSet(indexKey, "corrupt", flowIDValue) // GET now fails with WRONGTYPE
	expectBrowserError(t, env.lookup(userCode, browserCall{}), http.StatusServiceUnavailable, deviceCodeTemporarilyUnavailable)
	if got := env.lookupFailCounters(testUserID); got != [3]string{} {
		t.Fatalf("failure counters = %v, want all released", got)
	}
	if env.field(flowID, "st") != deviceFlowStatePending {
		t.Fatalf("state = %s", env.field(flowID, "st"))
	}
}

func TestDeviceLookupGlobalFailureWarning(t *testing.T) {
	env := newDeviceBrowserEnv(t, func(options *DeviceFlowConfigOptions) { options.Limits.LookupFailGlobalPer10m = 2 })
	env.claim()
	if !strings.Contains(env.logs.String(), "event=lookup_fail_global_warn") || !strings.Contains(env.logs.String(), "count=1") {
		t.Fatalf("global warning not logged: %s", env.logs.String())
	}
}

// A session whose user row is gone cannot be shown a review card or approve.
func TestDeviceBrowserMissingUserRowIs503(t *testing.T) {
	t.Run("lookup", func(t *testing.T) {
		env := newDeviceBrowserEnv(t)
		env.issue(testPublicClientID)
		expectBrowserError(t, env.lookup(env.lastUserCode(), browserCall{userID: "7777"}), http.StatusServiceUnavailable, deviceCodeTemporarilyUnavailable)
	})
	t.Run("approve", func(t *testing.T) {
		env := newDeviceBrowserEnv(t)
		flowID, userCode, handle := env.claim()
		env.db.DB.User.DeleteOneID(testUserID).ExecX(t.Context())
		expectBrowserError(t, env.approve(handle, userCode, browserCall{}), http.StatusServiceUnavailable, deviceCodeTemporarilyUnavailable)
		if env.field(flowID, "st") != deviceFlowStateClaimed || env.field(flowID, "att") != "" {
			t.Fatal("an approve without a user row began an attempt")
		}
	})
}

func TestDeviceApproveDailyLimit(t *testing.T) {
	env := newDeviceBrowserEnv(t, func(options *DeviceFlowConfigOptions) { options.Limits.DecisionUserPerDay = 1 })
	flowID, userCode, handle := env.claim()
	expectBrowserError(t, env.deny("dfh_unknown", "", browserCall{}), http.StatusConflict, deviceCodeFlowConflict)
	limited := env.approve(handle, userCode, browserCall{})
	expectBrowserError(t, limited, http.StatusTooManyRequests, deviceCodeRateLimited)
	if updatedData(t, limited)["retryAfter"] == nil {
		t.Fatal("429 without retryAfter")
	}
	if env.field(flowID, "st") != deviceFlowStateClaimed {
		t.Fatalf("state = %s", env.field(flowID, "st"))
	}
}

func TestDeviceApproveClientLookupFailureIs503(t *testing.T) {
	env := newDeviceBrowserEnv(t)
	flowID, userCode, handle := env.claim()
	env.hydra.clientLookupStatus = http.StatusInternalServerError
	expectBrowserError(t, env.approve(handle, userCode, browserCall{}), http.StatusServiceUnavailable, deviceCodeTemporarilyUnavailable)
	if env.field(flowID, "st") != deviceFlowStateClaimed || env.chain.stageHits(chainStageVerifyStart) != 0 {
		t.Fatalf("state = %s", env.field(flowID, "st"))
	}
}

// A client without a display name gets a default label built from its ID.
func TestDeviceApproveDefaultLabelFallsBackToClientID(t *testing.T) {
	env := newDeviceBrowserEnv(t)
	flowID, userCode, handle := env.claim()
	client := testDeviceClient(testPublicClientID, "none", "openid profile offline_access user:read bindings:read game-data:read game-data:write email", testDeviceGrantTypes, nil)
	client["client_name"] = "  "
	env.hydra.setClient(client)
	data := approvedData(t, env.approve(handle, userCode, browserCall{}))
	if data["clientName"] != testPublicClientID {
		t.Fatalf("clientName = %v", data["clientName"])
	}
	if got, want := env.field(flowID, "lbl"), testPublicClientID+" · 2026-10-07"; got != want || env.field(flowID, "lsrc") != deviceLabelSourceDefault {
		t.Fatalf("label = %q (%s), want %q", got, env.field(flowID, "lsrc"), want)
	}
}

func TestDeviceBrowserCorruptHandleIndex(t *testing.T) {
	keys := func(env *deviceBrowserEnv) (string, string) {
		_, _, handle := env.claim()
		return handle, env.db.Redis.KeyBuilder().BuildOAuth2DeviceFlowHandleIndexKey(handle)
	}
	t.Run("unreadable index", func(t *testing.T) {
		env := newDeviceBrowserEnv(t)
		handle, indexKey := keys(env)
		env.redis.Del(indexKey)
		env.redis.HSet(indexKey, "corrupt", "1")
		expectBrowserError(t, env.approve(handle, env.lastUserCode(), browserCall{}), http.StatusServiceUnavailable, deviceCodeTemporarilyUnavailable)
		expectBrowserError(t, env.deny(handle, "", browserCall{}), http.StatusServiceUnavailable, deviceCodeTemporarilyUnavailable)
	})
	t.Run("unreadable flow", func(t *testing.T) {
		env := newDeviceBrowserEnv(t)
		handle, indexKey := keys(env)
		if err := env.redis.Set(indexKey, "ghost-flow"); err != nil {
			t.Fatal(err)
		}
		if err := env.redis.Set(env.flowKey("ghost-flow"), "not a hash"); err != nil {
			t.Fatal(err)
		}
		expectBrowserError(t, env.approve(handle, env.lastUserCode(), browserCall{}), http.StatusServiceUnavailable, deviceCodeTemporarilyUnavailable)
	})
	t.Run("flow gone", func(t *testing.T) {
		env := newDeviceBrowserEnv(t)
		handle, indexKey := keys(env)
		if err := env.redis.Set(indexKey, "ghost-flow"); err != nil {
			t.Fatal(err)
		}
		expectBrowserError(t, env.approve(handle, env.lastUserCode(), browserCall{}), http.StatusConflict, deviceCodeFlowConflict)
		expectBrowserError(t, env.deny(handle, "", browserCall{}), http.StatusConflict, deviceCodeFlowConflict)
	})
}

// approveAuditResult returns the result recorded by the approve audit row.
func (e *deviceBrowserEnv) approveAuditResult(t *testing.T) string {
	t.Helper()
	rows, err := e.db.DB.SystemLog.Query().Where(systemlog.ActionEQ(deviceAuditActionApprove)).All(t.Context())
	if err != nil || len(rows) != 1 {
		t.Fatalf("approve audit rows = %d (%v)", len(rows), err)
	}
	result, _ := rows[0].Metadata["result"].(string)
	return result
}

// When the device redeems its tokens while the chain is still running, every
// chain outcome reports the approval as done.
func TestDeviceApproveOutcomeAlreadyIssued(t *testing.T) {
	cases := []struct {
		name  string
		setup func(env *deviceBrowserEnv, flowID string)
	}{
		{"approved", func(env *deviceBrowserEnv, flowID string) {
			env.chain.hook = func(stage string, _ int, _ http.ResponseWriter, _ *http.Request) bool {
				if stage == chainStageVerifyFinal {
					env.setFlow(flowID, "st", deviceFlowStateIssued)
				}
				return false
			}
		}},
		{"unconfirmed", func(env *deviceBrowserEnv, flowID string) {
			env.chain.hook = func(stage string, _ int, w http.ResponseWriter, _ *http.Request) bool {
				if stage == chainStageVerifyFinal {
					env.setFlow(flowID, "st", deviceFlowStateIssued)
					closeConnection(t, w)
					return true
				}
				return false
			}
		}},
		{"retryable", func(env *deviceBrowserEnv, flowID string) {
			env.chain.hook = func(stage string, _ int, w http.ResponseWriter, _ *http.Request) bool {
				if stage == chainStageVerifyStart {
					env.setFlow(flowID, "st", deviceFlowStateIssued)
					writeFakeHydraJSON(w, http.StatusBadGateway, map[string]any{"error": "bad gateway"})
					return true
				}
				return false
			}
		}},
		{"failed after consent", func(env *deviceBrowserEnv, flowID string) {
			env.chain.mutateLocation = func(stage, location string) string {
				if stage == chainStageVerifyFinal {
					env.setFlow(flowID, "st", deviceFlowStateIssued)
					return testFrontendURL + "/device/done?client_id=other"
				}
				return location
			}
		}},
		{"failed", func(env *deviceBrowserEnv, flowID string) {
			env.chain.mutateLocation = func(stage, location string) string {
				if stage == chainStageVerifyStart {
					env.setFlow(flowID, "st", deviceFlowStateIssued)
					return testFrontendURL + "/device"
				}
				return location
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newDeviceBrowserEnv(t)
			flowID, userCode, handle := env.claim()
			tc.setup(env, flowID)
			approvedData(t, env.approve(handle, userCode, browserCall{}))
			if env.field(flowID, "st") != deviceFlowStateIssued {
				t.Fatalf("state = %s", env.field(flowID, "st"))
			}
			if got := env.approveAuditResult(t); got != deviceFinishApproved {
				t.Fatalf("audit result = %q", got)
			}
		})
	}
}

// Hydra completed the consent but the attempt could not be finished in Redis:
// the outcome is unknown to the user, and the device may still redeem it.
func TestDeviceApproveFinishFailureIsUnconfirmed(t *testing.T) {
	env := newDeviceBrowserEnv(t)
	flowID, userCode, handle := env.claim()
	env.chain.hook = func(stage string, _ int, _ http.ResponseWriter, _ *http.Request) bool {
		if stage == chainStageVerifyFinal {
			env.redis.SetError(testRedisLoadingError)
		}
		return false
	}
	resp := env.approve(handle, userCode, browserCall{})
	env.redis.SetError("")
	expectUnconfirmed(t, resp)
	if env.field(flowID, "st") != deviceFlowStateApproving || !env.inUnredeemed(flowID) {
		t.Fatalf("state = %s, unredeemed = %v", env.field(flowID, "st"), env.inUnredeemed(flowID))
	}
	logs := env.logs.String()
	if !strings.Contains(logs, "stage=H9k reason="+deviceChainReasonStore) {
		t.Fatalf("finish failure not logged: %s", logs)
	}
	if got := env.approveAuditResult(t); got != deviceFinishUnconfirmed {
		t.Fatalf("audit result = %q", got)
	}
}

func TestDeviceApproveConsentRecordFailures(t *testing.T) {
	t.Run("store unavailable", func(t *testing.T) {
		env := newDeviceBrowserEnv(t)
		flowID, userCode, handle := env.claim()
		env.chain.hook = func(stage string, _ int, _ http.ResponseWriter, _ *http.Request) bool {
			if stage == chainStageConsentGet {
				env.redis.SetError(testRedisLoadingError)
			}
			return false
		}
		resp := env.approve(handle, userCode, browserCall{})
		env.redis.SetError("")
		expectBrowserError(t, resp, http.StatusServiceUnavailable, deviceCodeTemporarilyUnavailable)
		if env.chain.stageHits(chainStageConsentAcc) != 0 || env.field(flowID, "crid") != "" {
			t.Fatal("the consent was accepted without being recorded")
		}
		if got := env.approveAuditResult(t); got != deviceFlowStateFailed {
			t.Fatalf("audit result = %q", got)
		}
	})
	t.Run("nonce lost", func(t *testing.T) {
		env := newDeviceBrowserEnv(t)
		flowID, userCode, handle := env.claim()
		env.chain.hook = func(stage string, _ int, _ http.ResponseWriter, _ *http.Request) bool {
			if stage == chainStageConsentGet {
				env.setFlow(flowID, "anonce", "taken-over")
			}
			return false
		}
		expectBrowserError(t, env.approve(handle, userCode, browserCall{}), http.StatusConflict, deviceCodeFlowConflict)
		if env.chain.stageHits(chainStageConsentAcc) != 0 || env.field(flowID, "crid") != "" || env.inUnredeemed(flowID) {
			t.Fatal("a lost attempt recorded or accepted its consent")
		}
		if !strings.Contains(env.logs.String(), "reason="+deviceChainReasonNonce) {
			t.Fatalf("nonce loss not logged: %s", env.logs.String())
		}
	})
}

// A consent that may have completed is left to the reaper when revoking it
// fails.
func TestDeviceApproveFailedAfterConsentRevokeFailure(t *testing.T) {
	env := newDeviceBrowserEnv(t)
	env.hydra.revokeStatus = http.StatusInternalServerError
	env.chain.mutateLocation = func(stage, location string) string {
		if stage == chainStageVerifyFinal {
			return testFrontendURL + "/device/done?client_id=other"
		}
		return location
	}
	flowID, userCode, handle := env.claim()
	expectApprovalFailed(t, env.approve(handle, userCode, browserCall{}), false)
	if got := env.revocations(); len(got) != 1 || got[0] != "consent_request_id=consent-request-1" {
		t.Fatalf("revocations = %v", got)
	}
	if env.field(flowID, "st") != deviceFlowStateFailed || !env.inUnredeemed(flowID) {
		t.Fatalf("state = %s, unredeemed = %v", env.field(flowID, "st"), env.inUnredeemed(flowID))
	}
	if !strings.Contains(env.logs.String(), "stage=H9j reason="+deviceChainReasonRevoke) {
		t.Fatalf("revoke failure not logged: %s", env.logs.String())
	}
}
