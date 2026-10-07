package oauth2

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
)

const testRedisLoadingError = "LOADING Redis is loading the dataset in memory"

func (e *deviceTestEnv) revocationQueries() []string {
	var queries []string
	for _, call := range e.hydra.callsTo(http.MethodDelete, "/admin/oauth2/auth/sessions/consent") {
		queries = append(queries, call.RawQuery)
	}
	return queries
}

func TestTokenShimCorruptFlowRecords(t *testing.T) {
	t.Run("unreadable record is 503", func(t *testing.T) {
		env := newDeviceTestEnv(t)
		hdc, flowID := env.issue(testPublicClientID)
		env.setFlow(flowID, "exp", "not-a-number")
		resp := env.poll(hdc, testPublicClientID)
		if resp.Status != http.StatusServiceUnavailable || resp.oauthError(t) != oauthErrorTemporarilyUnavailable {
			t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
		}
		if calls := env.hydra.callsTo(http.MethodPost, "/oauth2/token"); len(calls) != 0 {
			t.Fatal("the poll was forwarded")
		}
	})
	t.Run("unsealable device code is server_error", func(t *testing.T) {
		env := newDeviceTestEnv(t)
		hdc, flowID := env.issue(testPublicClientID)
		env.setFlow(flowID, "wdc", "sealed-by-someone-else")
		resp := env.poll(hdc, testPublicClientID)
		if resp.Status != http.StatusInternalServerError || resp.oauthError(t) != oauthErrorServerError {
			t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
		}
		if calls := env.hydra.callsTo(http.MethodPost, "/oauth2/token"); len(calls) != 0 {
			t.Fatal("the poll was forwarded")
		}
		if !strings.Contains(env.logs.String(), "reason=unseal_failed") {
			t.Fatal("unseal_failed not logged")
		}
		env.assertNoSecretLeaks()
	})
}

func TestTokenShimHydraUnreachableIs503(t *testing.T) {
	env := newDeviceTestEnv(t)
	hdc, flowID := env.issue(testPublicClientID)
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	deadHydra := harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{
		PublicURL: dead.URL, BrowserURL: dead.URL, AdminURL: dead.URL, RequestTimeout: time.Second,
	})
	env.app.Post("/dead/token", handleHydraTokenEndpoint(env.apiHelper, deadHydra, env.cfg, env.store))
	form := url.Values{"grant_type": {HydraGrantTypeDeviceCode}, "device_code": {hdc}, "client_id": {testPublicClientID}}
	resp := env.do("/dead/token", formURLEncodedMediaType, form.Encode(), nil)
	if resp.Status != http.StatusServiceUnavailable || resp.oauthError(t) != oauthErrorTemporarilyUnavailable {
		t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
	}
	if !strings.Contains(env.logs.String(), "stage=H12 reason=hydra_unavailable") {
		t.Fatalf("hydra_unavailable not logged: %s", env.logs.String())
	}
	if env.field(flowID, "st") != deviceFlowStatePending {
		t.Fatalf("state = %s", env.field(flowID, "st"))
	}
}

// A flow approved between the poll script and Hydra's answer is settled once
// the client's state was fetched.
func TestTokenShimApprovedDuringPollChecksClient(t *testing.T) {
	env := newDeviceTestEnv(t)
	hdc, flowID := env.issue(testPublicClientID)
	env.hydra.token = func(url.Values, http.Header) (int, any) {
		env.approve(flowID, deviceFlowStateApproved, testConsentRequestID)
		return http.StatusOK, testTokenResponse
	}
	resp := env.poll(hdc, testPublicClientID)
	if resp.Status != http.StatusOK || resp.json(t)["access_token"] != "ory_at_issuedaccesstoken" {
		t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
	}
	if env.field(flowID, "st") != deviceFlowStateIssued || env.inUnredeemed(flowID) {
		t.Fatalf("state = %s, unredeemed = %v", env.field(flowID, "st"), env.inUnredeemed(flowID))
	}
	if calls := env.hydra.callsTo(http.MethodGet, "/admin/clients/"+testPublicClientID); len(calls) != 1 {
		t.Fatalf("client lookups = %d, want 1", len(calls))
	}
}

// Tokens for a flow whose record vanished are withheld and their consent
// revoked.
func TestTokenShimTokensForVanishedFlowAreRevoked(t *testing.T) {
	env := newDeviceTestEnv(t)
	hdc, flowID := env.issue(testPublicClientID)
	env.approve(flowID, deviceFlowStateApproved, testConsentRequestID)
	env.hydra.token = func(url.Values, http.Header) (int, any) {
		env.redis.Del(env.flowKey(flowID))
		return http.StatusOK, testTokenResponse
	}
	resp := env.poll(hdc, testPublicClientID)
	if resp.Status != http.StatusBadRequest || resp.oauthError(t) != oauthErrorExpiredToken {
		t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
	}
	if strings.Contains(string(resp.Body), "ory_at_") {
		t.Fatal("a withheld token leaked")
	}
	if got := env.revocationQueries(); len(got) != 1 || got[0] != "consent_request_id="+testConsentRequestID {
		t.Fatalf("revocations = %v", got)
	}
	if env.inUnredeemed(flowID) {
		t.Fatal("the revoked flow is still unredeemed")
	}
	if !strings.Contains(env.logs.String(), "event=token_after_terminal fid="+flowID) {
		t.Fatal("token_after_terminal not logged")
	}
}

// A settle that fails once after Hydra issued tokens is retried.
func TestTokenShimSettleRetrySucceeds(t *testing.T) {
	env := newDeviceTestEnv(t)
	hdc, flowID := env.issue(testPublicClientID)
	env.approve(flowID, deviceFlowStateApproved, testConsentRequestID)
	env.hydra.token = func(url.Values, http.Header) (int, any) {
		env.redis.SetError(testRedisLoadingError)
		// Back before the first retry (50 ms later).
		time.AfterFunc(10*time.Millisecond, func() { env.redis.SetError("") })
		return http.StatusOK, testTokenResponse
	}
	resp := env.poll(hdc, testPublicClientID)
	if resp.Status != http.StatusOK {
		t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
	}
	if env.field(flowID, "st") != deviceFlowStateIssued || env.inUnredeemed(flowID) {
		t.Fatalf("state = %s, unredeemed = %v", env.field(flowID, "st"), env.inUnredeemed(flowID))
	}
	if strings.Contains(env.logs.String(), "event=settle_failed") {
		t.Fatal("a recovered settle was reported as failed")
	}
}

func TestTokenShimSettleFailureWithoutTokens(t *testing.T) {
	t.Run("pending answer is 503", func(t *testing.T) {
		env := newDeviceTestEnv(t)
		hdc, _ := env.issue(testPublicClientID)
		env.hydra.token = func(url.Values, http.Header) (int, any) {
			env.redis.SetError(testRedisLoadingError)
			return http.StatusBadRequest, hydraOAuthError(oauthErrorAuthorizationPending)
		}
		resp := env.poll(hdc, testPublicClientID)
		env.redis.SetError("")
		if resp.Status != http.StatusServiceUnavailable || resp.oauthError(t) != oauthErrorTemporarilyUnavailable {
			t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
		}
	})
	t.Run("tokens for an unapproved flow are withheld", func(t *testing.T) {
		env := newDeviceTestEnv(t)
		hdc, flowID := env.issue(testPublicClientID)
		env.hydra.token = func(url.Values, http.Header) (int, any) {
			env.redis.SetError(testRedisLoadingError)
			return http.StatusOK, testTokenResponse
		}
		resp := env.poll(hdc, testPublicClientID)
		env.redis.SetError("")
		if resp.Status != http.StatusBadRequest || resp.oauthError(t) != oauthErrorExpiredToken {
			t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
		}
		if strings.Contains(string(resp.Body), "ory_at_") {
			t.Fatal("a withheld token leaked")
		}
		logs := env.logs.String()
		if !strings.Contains(logs, "event=settle_failed fid="+flowID) || !strings.Contains(logs, "reason=missing_consent_request_id") {
			t.Fatalf("logs = %s", logs)
		}
		if len(env.revocationQueries()) != 0 {
			t.Fatal("revoked without a consent request ID")
		}
	})
	t.Run("tokens for a denied flow are revoked", func(t *testing.T) {
		env := newDeviceTestEnv(t)
		hdc, flowID := env.issue(testPublicClientID)
		env.approve(flowID, deviceFlowStateApproved, testConsentRequestID)
		env.setFlow(flowID, "st", deviceFlowStateDenied)
		env.hydra.token = func(url.Values, http.Header) (int, any) {
			env.redis.SetError(testRedisLoadingError)
			return http.StatusOK, testTokenResponse
		}
		resp := env.poll(hdc, testPublicClientID)
		env.redis.SetError("")
		if resp.Status != http.StatusBadRequest || resp.oauthError(t) != oauthErrorAccessDenied {
			t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
		}
		if got := env.revocationQueries(); len(got) != 1 || got[0] != "consent_request_id="+testConsentRequestID {
			t.Fatalf("revocations = %v", got)
		}
		// Redis was down, so the flow stays unredeemed and the reaper retries.
		if !env.inUnredeemed(flowID) || !strings.Contains(env.logs.String(), "reason=unredeemed_remove_failed") {
			t.Fatalf("unredeemed = %v, logs = %s", env.inUnredeemed(flowID), env.logs.String())
		}
	})
}

func TestTokenShimWithheldTokenRevokeFailureLeavesReaperWork(t *testing.T) {
	env := newDeviceTestEnv(t)
	hdc, flowID := env.issue(testPublicClientID)
	env.approve(flowID, deviceFlowStateApproved, testConsentRequestID)
	env.setFlow(flowID, "st", deviceFlowStateDenied)
	env.hydra.revokeStatus = http.StatusInternalServerError
	env.hydra.token = hydraTokenAnswer(http.StatusOK, testTokenResponse)
	resp := env.poll(hdc, testPublicClientID)
	if resp.Status != http.StatusBadRequest || resp.oauthError(t) != oauthErrorAccessDenied {
		t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
	}
	if !env.inUnredeemed(flowID) || !strings.Contains(env.logs.String(), "reason=revoke_failed") {
		t.Fatalf("unredeemed = %v, logs = %s", env.inUnredeemed(flowID), env.logs.String())
	}
}

// The token is handed out even when its audit row cannot be written.
func TestTokenShimAuditFailureStillIssues(t *testing.T) {
	env := newDeviceTestEnv(t)
	hdc, flowID := env.issue(testPublicClientID)
	env.approve(flowID, deviceFlowStateApproved, testConsentRequestID)
	env.hydra.token = hydraTokenAnswer(http.StatusOK, testTokenResponse)
	if err := env.db.DB.Close(); err != nil {
		t.Fatal(err)
	}
	resp := env.poll(hdc, testPublicClientID)
	if resp.Status != http.StatusOK || resp.json(t)["access_token"] != "ory_at_issuedaccesstoken" {
		t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
	}
	if env.field(flowID, "st") != deviceFlowStateIssued {
		t.Fatalf("state = %s", env.field(flowID, "st"))
	}
	if !strings.Contains(env.logs.String(), "event=token_issued fid="+flowID) || !strings.Contains(env.logs.String(), "reason=audit_failed") {
		t.Fatalf("logs = %s", env.logs.String())
	}
}

func TestClassifyHydraDeviceTokenResponse(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"issued", http.StatusOK, `{"access_token":"x"}`, deviceHydraResultOK},
		{"pending", http.StatusBadRequest, `{"error":"authorization_pending"}`, deviceHydraResultPending},
		{"expired", http.StatusBadRequest, `{"error":"expired_token"}`, deviceHydraResultExpiredToken},
		{"invalid grant", http.StatusBadRequest, `{"error":"invalid_grant"}`, deviceHydraResultInvalidGrant},
		{"slow down from Hydra", http.StatusBadRequest, `{"error":"slow_down"}`, deviceHydraResultOther},
		{"non-JSON 400", http.StatusBadRequest, `<html>bad request</html>`, deviceHydraResultOther},
		{"wrong secret", http.StatusUnauthorized, `{"error":"invalid_client"}`, deviceHydraResultOther},
		{"server error", http.StatusInternalServerError, `{"error":"invalid_grant"}`, deviceHydraResultOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyHydraDeviceTokenResponse(&hydraPublicResponse{Status: tc.status, Body: []byte(tc.body)})
			if got != tc.want {
				t.Fatalf("classify(%d %s) = %q, want %q", tc.status, tc.body, got, tc.want)
			}
		})
	}
}
