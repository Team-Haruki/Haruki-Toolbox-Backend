package oauth2

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/systemlog"

	"github.com/gofiber/fiber/v3"
)

func (e *deviceBrowserEnv) counter(key string) string {
	value, _ := e.redis.Get(key)
	return value
}

func (e *deviceBrowserEnv) lookupFailCounters(userID string) [3]string {
	keys := e.db.Redis.KeyBuilder()
	return [3]string{
		e.counter(keys.BuildOAuth2DeviceLookupFailUserKey(userID)),
		e.counter(keys.BuildOAuth2DeviceLookupFailUserDayKey(userID)),
		e.counter(keys.BuildOAuth2DeviceLookupFailGlobalKey()),
	}
}

func TestDeviceLookupReturnsReviewCard(t *testing.T) {
	t.Run("public client with write", func(t *testing.T) {
		env := newDeviceBrowserEnv(t)
		env.hydra.setClient(testDeviceClient(testPublicClientID, "none", "openid profile offline_access user:read game-data:write", testDeviceGrantTypes,
			map[string]any{"haruki": map[string]any{"device": map[string]any{"allow_write": true, "first_party": true}}}))
		resp := env.deviceAuth(url.Values{"client_id": {testPublicClientID}, "scope": {"user:read game-data:write offline_access openid profile"}, "device_label": {"Haruki-Client @ home"}}, "")
		if resp.Status != http.StatusOK {
			t.Fatalf("device/auth status = %d", resp.Status)
		}
		flowID := env.mustFlowFor(resp.json(t)["device_code"].(string))
		lookup := env.lookup(env.lastUserCode(), browserCall{})
		if lookup.Status != http.StatusOK || lookup.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("lookup status = %d, body %s", lookup.Status, lookup.Body)
		}
		card := updatedData(t, lookup)
		if !strings.HasPrefix(card["flowHandle"].(string), deviceFlowHandlePrefix) || card["userCode"] != formatDeviceUserCode(env.lastUserCode()) {
			t.Fatalf("card = %v", card)
		}
		client := card["client"].(map[string]any)
		// A public client is never first party, whatever its policy says.
		if client["clientId"] != testPublicClientID || client["clientType"] != oauthClientTypePublic || client["firstParty"] != false || client["initiatorVerified"] != false {
			t.Fatalf("client = %v", client)
		}
		wantRisks := map[string]string{"game-data:write": "write", "offline_access": "offline", "openid": "identity", "profile": "identity", "user:read": "read"}
		scopes := card["scopes"].([]any)
		if len(scopes) != len(wantRisks) || card["writeWarning"] != true {
			t.Fatalf("scopes = %v, writeWarning = %v", scopes, card["writeWarning"])
		}
		for _, raw := range scopes {
			scope := raw.(map[string]any)
			if wantRisks[scope["scope"].(string)] != scope["risk"] {
				t.Fatalf("scope %v has risk %v", scope["scope"], scope["risk"])
			}
		}
		if card["deviceLabel"] != "Haruki-Client @ home" || card["requestedAt"] != "2026-10-07T08:00:00Z" || card["expiresAt"] != "2026-10-07T08:09:59Z" {
			t.Fatalf("card = %v", card)
		}
		if account := card["account"].(map[string]any); account["userId"] != testUserID || account["name"] != testUserName {
			t.Fatalf("account = %v", account)
		}
		if env.field(flowID, "st") != deviceFlowStateClaimed || env.field(flowID, "cby") != testUserID || env.field(flowID, "csh") == testUserSession ||
			env.field(flowID, "cuntil") != "1791360300000" {
			t.Fatalf("flow st=%s cby=%s cuntil=%s", env.field(flowID, "st"), env.field(flowID, "cby"), env.field(flowID, "cuntil"))
		}
		handleKey := env.db.Redis.KeyBuilder().BuildOAuth2DeviceFlowHandleIndexKey(card["flowHandle"].(string))
		if ttl := env.redis.TTL(handleKey); ttl != 300*time.Second {
			t.Fatalf("handle index TTL = %s", ttl)
		}
		audits, err := env.db.DB.SystemLog.Query().Where(systemlog.ActionEQ(deviceAuditActionClaim)).All(t.Context())
		if err != nil || len(audits) != 1 || audits[0].Metadata["deviceFlowID"] != flowID {
			t.Fatalf("claim audit = %+v (%v)", audits, err)
		}
		// A successful claim gives the failure budget back.
		if counters := env.lookupFailCounters(testUserID); counters != [3]string{} {
			t.Fatalf("failure counters after a claim = %v", counters)
		}
		env.assertNoSecretLeaks()
	})
	t.Run("confidential first party", func(t *testing.T) {
		env := newDeviceBrowserEnv(t)
		env.hydra.setClient(testDeviceClient(testConfidentialClientID, "client_secret_basic", "openid offline_access user:read game-data:read", testDeviceGrantTypes,
			map[string]any{"haruki": map[string]any{"device": map[string]any{"first_party": true}}}))
		env.issue(testConfidentialClientID)
		card := updatedData(t, env.lookup(env.lastUserCode(), browserCall{}))
		client := card["client"].(map[string]any)
		if client["clientType"] != oauthClientTypeConfidential || client["firstParty"] != true || client["initiatorVerified"] != true || card["writeWarning"] != false {
			t.Fatalf("card = %v", card)
		}
		env.assertNoSecretLeaks()
	})
}

func TestFormatDeviceTimestampIsUTC(t *testing.T) {
	shanghai := time.FixedZone("UTC+8", 8*60*60)
	for _, at := range []time.Time{
		time.Date(2026, 10, 7, 16, 0, 0, 0, shanghai),
		time.Date(2026, 10, 7, 8, 0, 0, 0, time.UTC),
		time.UnixMilli(1791360000000).In(shanghai),
	} {
		if got := formatDeviceTimestamp(at); got != "2026-10-07T08:00:00Z" {
			t.Errorf("formatDeviceTimestamp(%s) = %q, want 2026-10-07T08:00:00Z", at, got)
		}
	}
}

func TestDeviceScopeRiskTableIsTheOnlySource(t *testing.T) {
	for _, scope := range deviceFlowGrantableScopes {
		if _, ok := deviceScopeRisks[scope]; !ok {
			t.Errorf("grantable scope %s has no risk class", scope)
		}
	}
	if deviceScopeRisk("unknown:scope") != deviceScopeRiskWrite {
		t.Fatal("an unclassified scope must be shown as write")
	}
}

func TestDeviceLookupInvalidCodeCountsTowardBudget(t *testing.T) {
	env := newDeviceBrowserEnv(t)
	_, flowID := env.issue(testPublicClientID)
	userCode := env.lastUserCode()
	if resp := env.lookup(userCode, browserCall{}); resp.Status != http.StatusOK {
		t.Fatalf("claim status = %d", resp.Status)
	}

	// Taken by Alice: Bob gets the same answer as for an unknown code.
	taken := env.lookup(userCode, browserCall{userID: testOtherUserID, session: "bob"})
	unknown := env.lookup("ZZZZ-ZZZZ", browserCall{userID: testOtherUserID, session: "bob"})
	for _, resp := range []deviceTestResponse{taken, unknown} {
		expectBrowserError(t, resp, http.StatusBadRequest, deviceCodeInvalidCode)
	}
	if string(taken.Body) != string(unknown.Body) {
		t.Fatalf("taken and unknown answers differ: %s vs %s", taken.Body, unknown.Body)
	}
	if counters := env.lookupFailCounters(testOtherUserID); counters != [3]string{"2", "2", "2"} {
		t.Fatalf("Bob's failure counters = %v", counters)
	}
	if !strings.Contains(env.logs.String(), "event=lookup_conflict fid="+flowID) || !strings.Contains(env.logs.String(), "event=lookup_fail") {
		t.Fatalf("missing lookup_fail / lookup_conflict logs: %s", env.logs.String())
	}
	if env.field(flowID, "cby") != testUserID {
		t.Fatal("a failed lookup changed the claim")
	}

	// Expired before anyone claimed it.
	env.issue(testPublicClientID)
	expiredCode := env.lastUserCode()
	env.advance(testHydraDeviceTTL * time.Second)
	expectBrowserError(t, env.lookup(expiredCode, browserCall{userID: testOtherUserID, session: "bob"}), http.StatusBadRequest, deviceCodeInvalidCode)

	// Five failures per 10 minutes: the sixth attempt is refused without
	// touching any counter.
	for range 2 {
		expectBrowserError(t, env.lookup("ZZZZ-ZZZZ", browserCall{userID: testOtherUserID, session: "bob"}), http.StatusBadRequest, deviceCodeInvalidCode)
	}
	limited := env.lookup("ZZZZ-ZZZZ", browserCall{userID: testOtherUserID, session: "bob"})
	expectBrowserError(t, limited, http.StatusTooManyRequests, deviceCodeRateLimited)
	if limited.Header.Get("Retry-After") == "" || updatedData(t, limited)["retryAfter"] == nil {
		t.Fatalf("429 without retry hints: %v %s", limited.Header, limited.Body)
	}
	if counters := env.lookupFailCounters(testOtherUserID); counters != [3]string{"5", "5", "5"} {
		t.Fatalf("counters after refusal = %v", counters)
	}
	env.assertNoSecretLeaks()
}

func TestDeviceLookupMalformedCodeNotCounted(t *testing.T) {
	env := newDeviceBrowserEnv(t)
	for _, code := range []string{"", "BCD", "AAAA-AAAA", "BCDF-GHJK-L", "BCDF\u0000GHJK"} {
		expectBrowserError(t, env.lookup(code, browserCall{}), http.StatusBadRequest, deviceCodeMalformedCode)
	}
	if counters := env.lookupFailCounters(testUserID); counters != [3]string{} {
		t.Fatalf("malformed codes were counted: %v", counters)
	}
	// Full-width input with separators is a well-formed code.
	expectBrowserError(t, env.lookup("ＢＣＤＦ－ＧＨＪＫ", browserCall{}), http.StatusBadRequest, deviceCodeInvalidCode)
	env.assertNoSecretLeaks()
}

func TestDeviceLookupPerUserLimit(t *testing.T) {
	env := newDeviceBrowserEnv(t, func(options *DeviceFlowConfigOptions) { options.Limits.LookupUserPer10m = 2 })
	for range 2 {
		expectBrowserError(t, env.lookup("BCD", browserCall{}), http.StatusBadRequest, deviceCodeMalformedCode)
	}
	expectBrowserError(t, env.lookup("BCD", browserCall{}), http.StatusTooManyRequests, deviceCodeRateLimited)
	env.assertNoSecretLeaks()
}

func TestDeviceLookupOwnFlowStates(t *testing.T) {
	env := newDeviceBrowserEnv(t)
	flowID, userCode, firstHandle := env.claim()

	// Looking up again renews the claim with a new handle; the old one is
	// replaced.
	renewed := env.lookup(userCode, browserCall{})
	secondHandle := updatedData(t, renewed)["flowHandle"].(string)
	if secondHandle == firstHandle {
		t.Fatal("renewal kept the handle")
	}
	expectBrowserError(t, env.approve(firstHandle, userCode, browserCall{}), http.StatusConflict, deviceCodeFlowConflict)

	// Another session of the same user may not decide.
	expectBrowserError(t, env.approve(secondHandle, userCode, browserCall{session: "other-session"}), http.StatusConflict, deviceCodeSessionChanged)
	expectBrowserError(t, env.deny(secondHandle, "", browserCall{session: "other-session"}), http.StatusConflict, deviceCodeSessionChanged)
	// Bob holding Alice's handle is not the claimer.
	expectBrowserError(t, env.deny(secondHandle, "", browserCall{userID: testOtherUserID, session: "bob"}), http.StatusConflict, deviceCodeAlreadyHandled)

	// An approval in progress.
	env.setFlow(flowID, "st", deviceFlowStateApproving, "auntil", "9999999999999")
	expectBrowserError(t, env.lookup(userCode, browserCall{}), http.StatusConflict, deviceCodeFlowConflict)
	expectBrowserError(t, env.deny(secondHandle, "", browserCall{}), http.StatusConflict, deviceCodeFlowConflict)
	expectBrowserError(t, env.lookup(userCode, browserCall{userID: testOtherUserID, session: "bob"}), http.StatusBadRequest, deviceCodeInvalidCode)

	// Handled flows.
	for _, state := range []string{deviceFlowStateApproved, deviceFlowStateUnconfirmed, deviceFlowStateIssued, deviceFlowStateDenied, deviceFlowStateFailed} {
		env.setFlow(flowID, "st", state)
		expectBrowserError(t, env.lookup(userCode, browserCall{}), http.StatusConflict, deviceCodeAlreadyHandled)
		expectBrowserError(t, env.deny(secondHandle, "", browserCall{}), http.StatusConflict, deviceCodeAlreadyHandled)
	}
	// None of these own-flow answers consumed failure budget; the global
	// counter holds only Bob's failure.
	if counters := env.lookupFailCounters(testUserID); counters != [3]string{"", "", "1"} {
		t.Fatalf("own-flow answers were counted: %v", counters)
	}

	// Expired while claimed.
	env.setFlow(flowID, "st", deviceFlowStateClaimed)
	env.advance(testHydraDeviceTTL * time.Second)
	expectBrowserError(t, env.lookup(userCode, browserCall{}), http.StatusGone, deviceCodeCodeExpired)
	expectBrowserError(t, env.approve(secondHandle, userCode, browserCall{}), http.StatusGone, deviceCodeCodeExpired)
	env.assertNoSecretLeaks()
}

func TestDeviceLookupTakeoverAfterClaimLease(t *testing.T) {
	env := newDeviceBrowserEnv(t)
	flowID, userCode, aliceHandle := env.claim()
	env.advance(301 * time.Second)
	takeover := env.lookup(userCode, browserCall{userID: testOtherUserID, session: "bob"})
	if takeover.Status != http.StatusOK || env.field(flowID, "cby") != testOtherUserID {
		t.Fatalf("takeover status = %d, cby = %s", takeover.Status, env.field(flowID, "cby"))
	}
	expectBrowserError(t, env.approve(aliceHandle, userCode, browserCall{}), http.StatusConflict, deviceCodeFlowConflict)
	bobHandle := updatedData(t, takeover)["flowHandle"].(string)
	approvedData(t, env.approve(bobHandle, userCode, browserCall{userID: testOtherUserID, session: "bob"}))
	if env.chain.loginSubject != "kratos-"+testOtherUserID {
		t.Fatalf("login subject = %s", env.chain.loginSubject)
	}
	env.assertNoSecretLeaks()
}

func TestDeviceBrowserEndpointRules(t *testing.T) {
	paths := []string{"/api/oauth2/device/lookup", "/api/oauth2/device/approve", "/api/oauth2/device/deny"}
	body := map[string]any{"userCode": "BCDF-GHJK", "flowHandle": "dfh_x", "acknowledged": true}
	for _, path := range paths {
		t.Run(strings.TrimPrefix(path, "/api/oauth2/device/"), func(t *testing.T) {
			env := newDeviceBrowserEnv(t)
			for name, tc := range map[string]struct {
				call   browserCall
				body   any
				status int
				code   string
			}{
				"form":           {browserCall{contentType: formURLEncodedMediaType}, "userCode=BCDFGHJK", http.StatusUnsupportedMediaType, deviceCodeUnsupportedMediaType},
				"text":           {browserCall{contentType: "text/plain"}, body, http.StatusUnsupportedMediaType, deviceCodeUnsupportedMediaType},
				"no origin":      {browserCall{noOrigin: true}, body, http.StatusForbidden, deviceCodeOriginRejected},
				"foreign origin": {browserCall{origin: "https://evil.example.com"}, body, http.StatusForbidden, deviceCodeOriginRejected},
				"null origin":    {browserCall{origin: "null"}, body, http.StatusForbidden, deviceCodeOriginRejected},
				"too large":      {browserCall{}, `{"userCode":"` + strings.Repeat("B", 1100) + `"}`, http.StatusBadRequest, deviceCodeInvalidRequest},
				"not json":       {browserCall{}, `{"userCode":`, http.StatusBadRequest, deviceCodeInvalidRequest},
				"duplicate name": {browserCall{}, `{"flowHandle":"a","flowHandle":"b","userCode":"x","acknowledged":true}`, http.StatusBadRequest, deviceCodeInvalidRequest},
			} {
				t.Run(name, func(t *testing.T) {
					expectBrowserError(t, env.post(path, tc.body, tc.call), tc.status, tc.code)
				})
			}
			// JSON with a charset is still JSON.
			if resp := env.post(path, body, browserCall{contentType: "application/json; charset=utf-8"}); resp.Status == http.StatusUnsupportedMediaType {
				t.Fatal("application/json with charset was refused")
			}

			env.gateOn.Store(false)
			expectBrowserError(t, env.post(path, body, browserCall{}), http.StatusForbidden, deviceCodeFeatureDisabled)
			env.gateErr.Store(true)
			expectBrowserError(t, env.post(path, body, browserCall{}), http.StatusServiceUnavailable, deviceCodeTemporarilyUnavailable)
			env.assertNoSecretLeaks()
		})
	}
	t.Run("startup switch off", func(t *testing.T) {
		env := newDeviceBrowserEnv(t, func(options *DeviceFlowConfigOptions) { options.Enabled = false })
		for _, path := range paths {
			expectBrowserError(t, env.post(path, body, browserCall{}), http.StatusForbidden, deviceCodeFeatureDisabled)
		}
		// The guard runs before anything is counted or read.
		if keys := env.redis.Keys(); len(keys) != 0 {
			t.Fatalf("refused requests touched Redis: %v", keys)
		}
		env.assertNoSecretLeaks()
	})
}

func TestDeviceBrowserEndpointsNeverReturn401(t *testing.T) {
	t.Run("device accept 401 twice", func(t *testing.T) {
		env := newDeviceBrowserEnv(t)
		env.chain.hook = func(stage string, _ int, w http.ResponseWriter, _ *http.Request) bool {
			if stage == chainStageDeviceAccept {
				env.chain.adminError(w, http.StatusUnauthorized)
				return true
			}
			return false
		}
		_, userCode, handle := env.claim()
		expectApprovalFailed(t, env.approve(handle, userCode, browserCall{}), true)
		env.assertNoSecretLeaks()
	})
	t.Run("admin 401 on login request", func(t *testing.T) {
		env := newDeviceBrowserEnv(t)
		env.chain.hook = func(stage string, _ int, w http.ResponseWriter, _ *http.Request) bool {
			if stage == chainStageLoginGet {
				env.chain.adminError(w, http.StatusUnauthorized)
				return true
			}
			return false
		}
		_, userCode, handle := env.claim()
		expectApprovalFailed(t, env.approve(handle, userCode, browserCall{}), true)
		env.assertNoSecretLeaks()
	})
	t.Run("client lookup 401", func(t *testing.T) {
		env := newDeviceBrowserEnv(t)
		env.issue(testPublicClientID)
		env.hydra.clientLookupStatus = http.StatusUnauthorized
		expectBrowserError(t, env.lookup(env.lastUserCode(), browserCall{}), http.StatusServiceUnavailable, deviceCodeTemporarilyUnavailable)
		env.assertNoSecretLeaks()
	})
	t.Run("deny revoke 401", func(t *testing.T) {
		env := newDeviceBrowserEnv(t)
		flowID, _, handle := env.claim()
		env.setFlow(flowID, "crid", "consent-request-old")
		env.hydra.revokeStatus = http.StatusUnauthorized
		if resp := env.deny(handle, "", browserCall{}); resp.Status != http.StatusOK {
			t.Fatalf("deny status = %d", resp.Status)
		}
		env.assertNoSecretLeaks()
	})
}

func TestDeviceClientUnavailableFailsFlow(t *testing.T) {
	disabled := map[string]any{"haruki": map[string]any{"active": false}}
	t.Run("at lookup", func(t *testing.T) {
		env := newDeviceBrowserEnv(t)
		_, flowID := env.issue(testPublicClientID)
		env.hydra.setClient(testDeviceClient(testPublicClientID, "none", testDeviceScope, testDeviceGrantTypes, disabled))
		expectBrowserError(t, env.lookup(env.lastUserCode(), browserCall{}), http.StatusForbidden, deviceCodeClientUnavailable)
		if env.field(flowID, "st") != deviceFlowStateFailed {
			t.Fatalf("state = %s", env.field(flowID, "st"))
		}
		env.assertNoSecretLeaks()
	})
	t.Run("at approve", func(t *testing.T) {
		env := newDeviceBrowserEnv(t)
		flowID, userCode, handle := env.claim()
		// Grant removed: the device grant is gone.
		env.hydra.setClient(testDeviceClient(testPublicClientID, "none", testDeviceScope, []string{HydraGrantTypeAuthorizationCode}, nil))
		expectBrowserError(t, env.approve(handle, userCode, browserCall{}), http.StatusForbidden, deviceCodeClientUnavailable)
		if env.field(flowID, "st") != deviceFlowStateFailed || env.chain.stageHits(chainStageVerifyStart) != 0 {
			t.Fatalf("state = %s", env.field(flowID, "st"))
		}
		env.assertNoSecretLeaks()
	})
	t.Run("deleted at approve", func(t *testing.T) {
		env := newDeviceBrowserEnv(t)
		flowID, userCode, handle := env.claim()
		env.hydra.mu.Lock()
		delete(env.hydra.clients, testPublicClientID)
		env.hydra.mu.Unlock()
		expectBrowserError(t, env.approve(handle, userCode, browserCall{}), http.StatusForbidden, deviceCodeClientUnavailable)
		if env.field(flowID, "st") != deviceFlowStateFailed {
			t.Fatalf("state = %s", env.field(flowID, "st"))
		}
		env.assertNoSecretLeaks()
	})
}

func TestDeviceApproveValidation(t *testing.T) {
	env := newDeviceBrowserEnv(t)
	flowID, userCode, handle := env.claim()
	approve := func(body map[string]any) deviceTestResponse {
		return env.post("/api/oauth2/device/approve", body, browserCall{})
	}
	expectBrowserError(t, approve(map[string]any{"flowHandle": handle, "userCode": userCode}), http.StatusBadRequest, deviceCodeAckRequired)
	expectBrowserError(t, approve(map[string]any{"flowHandle": handle, "userCode": userCode, "acknowledged": false}), http.StatusBadRequest, deviceCodeAckRequired)
	expectBrowserError(t, approve(map[string]any{"flowHandle": handle, "userCode": userCode, "acknowledged": "true"}), http.StatusBadRequest, deviceCodeInvalidRequest)
	expectBrowserError(t, approve(map[string]any{"flowHandle": "dfh_unknown", "userCode": userCode, "acknowledged": true}), http.StatusConflict, deviceCodeFlowConflict)
	expectBrowserError(t, approve(map[string]any{"flowHandle": "unprefixed", "userCode": userCode, "acknowledged": true}), http.StatusConflict, deviceCodeFlowConflict)
	expectBrowserError(t, approve(map[string]any{"flowHandle": handle, "userCode": "ZZZZ-ZZZZ", "acknowledged": true}), http.StatusConflict, deviceCodeFlowConflict)
	expectBrowserError(t, approve(map[string]any{"flowHandle": handle, "userCode": "BCD", "acknowledged": true}), http.StatusBadRequest, deviceCodeMalformedCode)
	expectBrowserError(t, approve(map[string]any{"flowHandle": handle, "userCode": userCode, "acknowledged": true, "label": "\u200b\u202e\t"}), http.StatusBadRequest, deviceCodeInvalidRequest)
	if env.chain.stageHits(chainStageVerifyStart) != 0 || env.field(flowID, "att") != "" {
		t.Fatal("a refused approve reached Hydra or began an attempt")
	}

	// Less than 30 s left.
	env.advance((testHydraDeviceTTL - 20) * time.Second)
	expectBrowserError(t, env.approve(handle, userCode, browserCall{}), http.StatusGone, deviceCodeCodeExpired)
	if env.field(flowID, "st") != deviceFlowStateClaimed {
		t.Fatalf("state = %s", env.field(flowID, "st"))
	}
	env.assertNoSecretLeaks()
}

func TestDeviceApproveUsesDeviceLabel(t *testing.T) {
	env := newDeviceBrowserEnv(t)
	resp := env.deviceAuth(url.Values{"client_id": {testPublicClientID}, "scope": {testDeviceScope}, "device_label": {"rack-01"}}, "")
	flowID := env.mustFlowFor(resp.json(t)["device_code"].(string))
	userCode := env.lastUserCode()
	handle := updatedData(t, env.lookup(userCode, browserCall{}))["flowHandle"].(string)
	approvedData(t, env.approve(handle, userCode, browserCall{}))
	if env.field(flowID, "lbl") != "rack-01" || env.field(flowID, "lsrc") != deviceLabelSourceDevice {
		t.Fatalf("label = %q (%s)", env.field(flowID, "lbl"), env.field(flowID, "lsrc"))
	}
	env.assertNoSecretLeaks()
}

func TestDeviceDecisionDailyLimit(t *testing.T) {
	env := newDeviceBrowserEnv(t, func(options *DeviceFlowConfigOptions) { options.Limits.DecisionUserPerDay = 2 })
	_, userCode, handle := env.claim()
	expectBrowserError(t, env.approve("dfh_unknown", userCode, browserCall{}), http.StatusConflict, deviceCodeFlowConflict)
	expectBrowserError(t, env.deny("dfh_unknown", "", browserCall{}), http.StatusConflict, deviceCodeFlowConflict)
	limited := env.deny(handle, "", browserCall{})
	expectBrowserError(t, limited, http.StatusTooManyRequests, deviceCodeRateLimited)
	if updatedData(t, limited)["retryAfter"] == nil {
		t.Fatal("429 without retryAfter")
	}
	env.assertNoSecretLeaks()
}

func TestDeviceDeny(t *testing.T) {
	env := newDeviceBrowserEnv(t)
	hdc, _ := env.issue(testPublicClientID)
	flowID := env.mustFlowFor(hdc)
	userCode := env.lastUserCode()
	handle := updatedData(t, env.lookup(userCode, browserCall{}))["flowHandle"].(string)

	expectBrowserError(t, env.deny(handle, "because", browserCall{}), http.StatusBadRequest, deviceCodeInvalidRequest)
	resp := env.deny(handle, deviceDenyReasonNotMine, browserCall{})
	if resp.Status != http.StatusOK || updatedData(t, resp)["status"] != deviceFlowStateDenied || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("deny status = %d, body %s", resp.Status, resp.Body)
	}
	if env.field(flowID, "st") != deviceFlowStateDenied || env.field(flowID, "dres") != deviceDenyReasonNotMine {
		t.Fatalf("state = %s, dres = %s", env.field(flowID, "st"), env.field(flowID, "dres"))
	}
	// No consent was recorded, so nothing is revoked, and Hydra's reject is
	// never called.
	if len(env.revocations()) != 0 || env.chain.stageHits(chainStageVerifyStart) != 0 {
		t.Fatal("deny called Hydra")
	}
	for _, call := range env.hydra.calls {
		if strings.HasSuffix(call.Path, "/reject") {
			t.Fatal("deny called a Hydra reject endpoint")
		}
	}
	if !strings.Contains(env.logs.String(), "event=phishing_signal fid="+flowID) {
		t.Fatalf("missing phishing_signal: %s", env.logs.String())
	}
	audits, err := env.db.DB.SystemLog.Query().Where(systemlog.ActionEQ(deviceAuditActionDeny)).All(t.Context())
	if err != nil || len(audits) != 1 || audits[0].Metadata["reason"] != deviceDenyReasonNotMine {
		t.Fatalf("deny audit = %+v (%v)", audits, err)
	}
	// The device learns it at its next poll.
	if poll := env.poll(hdc, testPublicClientID); poll.oauthError(t) != oauthErrorAccessDenied {
		t.Fatalf("poll after deny = %s", poll.Body)
	}
	expectBrowserError(t, env.deny(handle, "", browserCall{}), http.StatusConflict, deviceCodeAlreadyHandled)
	expectBrowserError(t, env.approve(handle, userCode, browserCall{}), http.StatusConflict, deviceCodeAlreadyHandled)
	env.assertNoSecretLeaks()
}

// simulateCrashAfterRecord leaves a flow as a backend that crashed between
// H9h and H9k would: approving, consent request recorded, unredeemed.
func (e *deviceBrowserEnv) simulateCrashAfterRecord(flowID, handle, consentRequestID string) {
	e.t.Helper()
	ctx := context.Background()
	sessionHash := e.db.Redis.KeyBuilder().HashOAuth2DeviceIdentifier("sess", testUserSession)
	nonce := newDeviceFlowNonce()
	code, _, err := e.store.beginApprove(ctx, flowID, deviceDecisionActor{UserID: testUserID, SessionHash: sessionHash, FlowHandle: handle}, e.cfg.approvalTimings(), nonce)
	if err != nil || code != deviceDecisionOK {
		e.t.Fatalf("begin = %s, %v", code, err)
	}
	if recorded, err := e.store.recordConsent(ctx, flowID, nonce, consentRequestID, "kratos-"+testUserID); err != nil || !recorded {
		e.t.Fatalf("record = %v, %v", recorded, err)
	}
}

func TestDenyAfterExpiredApproveLeaseRevokesConsent(t *testing.T) {
	env := newDeviceBrowserEnv(t)
	hdc, _ := env.issue(testPublicClientID)
	flowID := env.mustFlowFor(hdc)
	handle := updatedData(t, env.lookup(env.lastUserCode(), browserCall{}))["flowHandle"].(string)
	env.simulateCrashAfterRecord(flowID, handle, "consent-request-crashed")

	// While the lease runs, the approval is still in progress.
	expectBrowserError(t, env.deny(handle, "", browserCall{}), http.StatusConflict, deviceCodeFlowConflict)
	env.advance(31 * time.Second)
	resp := env.deny(handle, "", browserCall{})
	if resp.Status != http.StatusOK {
		t.Fatalf("deny status = %d, body %s", resp.Status, resp.Body)
	}
	if got := env.revocations(); len(got) != 1 || got[0] != "consent_request_id=consent-request-crashed" {
		t.Fatalf("revocations = %v", got)
	}
	if env.field(flowID, "st") != deviceFlowStateDenied || env.inUnredeemed(flowID) || env.field(flowID, "anonce") != "" {
		t.Fatalf("state = %s, unredeemed = %v", env.field(flowID, "st"), env.inUnredeemed(flowID))
	}
	// Hydra cascaded the device code away; the device is told access_denied.
	env.hydra.token = hydraTokenAnswer(http.StatusBadRequest, hydraOAuthError(oauthErrorInvalidGrant))
	if poll := env.poll(hdc, testPublicClientID); poll.oauthError(t) != oauthErrorAccessDenied {
		t.Fatalf("poll after deny = %s", poll.Body)
	}

	t.Run("revocation fails", func(t *testing.T) {
		env := newDeviceBrowserEnv(t)
		_, flowID := env.issue(testPublicClientID)
		handle := updatedData(t, env.lookup(env.lastUserCode(), browserCall{}))["flowHandle"].(string)
		env.simulateCrashAfterRecord(flowID, handle, "consent-request-crashed")
		env.advance(31 * time.Second)
		env.hydra.revokeStatus = http.StatusInternalServerError
		if resp := env.deny(handle, "", browserCall{}); resp.Status != http.StatusOK {
			t.Fatalf("deny status = %d", resp.Status)
		}
		// The denial stands and the flow stays unredeemed for the reaper.
		if env.field(flowID, "st") != deviceFlowStateDenied || !env.inUnredeemed(flowID) {
			t.Fatalf("state = %s, unredeemed = %v", env.field(flowID, "st"), env.inUnredeemed(flowID))
		}
		env.assertNoSecretLeaks()
	})
	env.assertNoSecretLeaks()
}

func TestDeviceFlowReaperRevokesConsentOfStuckApprovingFlow(t *testing.T) {
	env := newDeviceBrowserEnv(t)
	flowID, _, handle := env.claim()
	env.simulateCrashAfterRecord(flowID, handle, "consent-request-stuck")
	reaper := &deviceFlowReaper{store: env.store, hydraConfig: env.hydraConfig, logger: env.logger, grace: time.Minute}

	// Not yet due: exp + grace has not passed.
	env.advance(testHydraDeviceTTL * time.Second)
	reaper.runOnce(t.Context())
	if len(env.revocations()) != 0 || !env.inUnredeemed(flowID) {
		t.Fatal("the reaper acted before exp + grace")
	}
	env.advance(61 * time.Second)
	reaper.runOnce(t.Context())
	if got := env.revocations(); len(got) != 1 || got[0] != "consent_request_id=consent-request-stuck" {
		t.Fatalf("revocations = %v", got)
	}
	if env.field(flowID, "st") != deviceFlowStateExpired || env.inUnredeemed(flowID) {
		t.Fatalf("state = %s, unredeemed = %v", env.field(flowID, "st"), env.inUnredeemed(flowID))
	}
	if !strings.Contains(env.logs.String(), "event=reaped fid="+flowID) {
		t.Fatalf("missing reaped log: %s", env.logs.String())
	}
	env.assertNoSecretLeaks()
}

func TestDeviceRoutesRequireSession(t *testing.T) {
	env := newDeviceBrowserEnv(t)
	env.issue(testPublicClientID)
	app := fiber.New()
	apiHelper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
		Router:         app,
		DBManager:      env.db,
		SessionHandler: harukiAPIHelper.NewSessionHandler(nil, ""),
	}
	registerHydraOAuth2Routes(apiHelper, env.hydraConfig, env.cfg)
	keysBefore := len(env.redis.Keys())
	callsBefore := len(env.hydra.calls)
	for _, path := range []string{"/api/oauth2/device/lookup", "/api/oauth2/device/approve", "/api/oauth2/device/deny"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"userCode":"`+env.lastUserCode()+`","flowHandle":"dfh_x","acknowledged":true}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", testFrontendURL)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		// The session guard answers; the handler never runs.
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s without a session: status %d", path, resp.StatusCode)
		}
		if strings.Contains(string(raw), env.lastUserCode()) || strings.Contains(string(raw), deviceFlowHandlePrefix) {
			t.Fatalf("%s echoed request secrets: %s", path, raw)
		}
	}
	if len(env.redis.Keys()) != keysBefore || len(env.hydra.calls) != callsBefore {
		t.Fatal("a request without a session reached the handler")
	}
	env.assertNoSecretLeaks()
}
