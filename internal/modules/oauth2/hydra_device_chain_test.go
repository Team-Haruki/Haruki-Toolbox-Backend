package oauth2

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/systemlog"
)

func approvedData(t *testing.T, resp deviceTestResponse) map[string]any {
	t.Helper()
	if resp.Status != http.StatusOK {
		t.Fatalf("approve status = %d, body %s", resp.Status, resp.Body)
	}
	data := updatedData(t, resp)
	if data["status"] != deviceFlowStateApproved {
		t.Fatalf("approve data = %v", data)
	}
	return data
}

func expectApprovalFailed(t *testing.T, resp deviceTestResponse, retryable bool) {
	t.Helper()
	expectBrowserError(t, resp, http.StatusBadGateway, deviceCodeApprovalFailed)
	if got, _ := updatedData(t, resp)["retryable"].(bool); got != retryable {
		t.Fatalf("retryable = %v, want %v (body %s)", got, retryable, resp.Body)
	}
}

func expectUnconfirmed(t *testing.T, resp deviceTestResponse) {
	t.Helper()
	if resp.Status != http.StatusAccepted || updatedData(t, resp)["status"] != deviceFlowStateUnconfirmed {
		t.Fatalf("status = %d, body %s; want 202 unconfirmed", resp.Status, resp.Body)
	}
}

func TestDeviceChainCompletes(t *testing.T) {
	env := newDeviceBrowserEnv(t)
	hdc, _ := env.issue(testPublicClientID)
	flowID := env.mustFlowFor(hdc)
	userCode := env.lastUserCode()
	resp := env.lookup(strings.ToLower(userCode[:4])+" "+userCode[4:], browserCall{})
	handle := updatedData(t, resp)["flowHandle"].(string)

	data := approvedData(t, env.approve(handle, formatDeviceUserCode(userCode), browserCall{}))
	if data["clientName"] != "Client "+testPublicClientID || data["accountName"] != testUserName || data["consentRequestId"] != "consent-request-1" {
		t.Fatalf("approve data = %v", data)
	}

	// H9b carries the flow marker only, without cookies or user_code.
	verifies := env.hydra.callsTo(http.MethodGet, hydraDeviceVerifyPath)
	if len(verifies) != 4 {
		t.Fatalf("public verify calls = %d, want 4", len(verifies))
	}
	if verifies[0].RawQuery != deviceFlowMarkerParam+"="+flowID || verifies[0].Header.Get("Cookie") != "" {
		t.Fatalf("first verify query = %q, cookie = %q", verifies[0].RawQuery, verifies[0].Header.Get("Cookie"))
	}
	for _, call := range verifies {
		if strings.Contains(call.RawQuery, "user_code") || call.Header.Get("X-Forwarded-Proto") != "" || call.Header.Get("X-Forwarded-Host") != "" {
			t.Fatalf("verify call carries user_code or forwarding headers: %q", call.RawQuery)
		}
	}
	accepts := env.hydra.callsTo(http.MethodPut, hydraDeviceAcceptEndpoint)
	if len(accepts) != 1 || accepts[0].Body != `{"user_code":"`+userCode+`"}` {
		t.Fatalf("device accept calls = %+v", accepts)
	}

	if env.field(flowID, "st") != deviceFlowStateApproved || env.field(flowID, "crid") != "consent-request-1" ||
		env.field(flowID, "sub") != "kratos-"+testUserID || !env.inUnredeemed(flowID) {
		t.Fatalf("flow st=%s crid=%s sub=%s unredeemed=%v", env.field(flowID, "st"), env.field(flowID, "crid"), env.field(flowID, "sub"), env.inUnredeemed(flowID))
	}
	if env.field(flowID, "lbl") != "Client haruki-client · 2026-10-07" || env.field(flowID, "lsrc") != deviceLabelSourceDefault {
		t.Fatalf("label = %q (%s)", env.field(flowID, "lbl"), env.field(flowID, "lsrc"))
	}
	if env.field(flowID, "anonce") != "" || env.field(flowID, "att") != "1" {
		t.Fatalf("anonce = %q, att = %q", env.field(flowID, "anonce"), env.field(flowID, "att"))
	}
	audits, err := env.db.DB.SystemLog.Query().Where(systemlog.ActionEQ(deviceAuditActionApprove)).All(t.Context())
	if err != nil || len(audits) != 1 || audits[0].Result != systemlog.ResultSuccess || audits[0].Metadata["consentRequestId"] != "consent-request-1" {
		t.Fatalf("approve audit = %+v (%v)", audits, err)
	}
	if !strings.Contains(env.logs.String(), "event=approve fid="+flowID) {
		t.Fatalf("no approve log line: %s", env.logs.String())
	}

	// The device redeems the approval.
	env.hydra.token = hydraTokenAnswer(http.StatusOK, testTokenResponse)
	env.advance(6 * time.Second)
	if token := env.poll(hdc, testPublicClientID); token.Status != http.StatusOK {
		t.Fatalf("token status = %d, body %s", token.Status, token.Body)
	}
	if env.field(flowID, "st") != deviceFlowStateIssued || env.inUnredeemed(flowID) {
		t.Fatal("approved flow was not issued")
	}
	// A second approve of the handled flow is refused before any Hydra call.
	before := len(env.hydra.callsTo(http.MethodGet, hydraDeviceVerifyPath))
	expectBrowserError(t, env.approve(handle, userCode, browserCall{}), http.StatusConflict, deviceCodeAlreadyHandled)
	if len(env.hydra.callsTo(http.MethodGet, hydraDeviceVerifyPath)) != before {
		t.Fatal("a handled flow reached Hydra again")
	}
	env.assertNoSecretLeaks()
}

func (e *deviceBrowserEnv) mustFlowFor(hdc string) string {
	e.t.Helper()
	flowID, found, err := e.store.flowIDForDeviceCode(e.t.Context(), hdc)
	if err != nil || !found {
		e.t.Fatalf("flow not found: %v", err)
	}
	return flowID
}

func TestDeviceChainReplaysSecureCookiesOverHTTP(t *testing.T) {
	env := newDeviceBrowserEnv(t)
	_, userCode, handle := env.claim()
	approvedData(t, env.approve(handle, userCode, browserCall{}))

	verifies := env.hydra.callsTo(http.MethodGet, hydraDeviceVerifyPath)
	wantCookies := [][]string{nil, {testCSRFCookieDevice}, {testCSRFCookieDevice, testCSRFCookieLogin}, {testCSRFCookieConsent, testCSRFCookieDevice}}
	if len(verifies) != len(wantCookies) {
		t.Fatalf("verify calls = %d", len(verifies))
	}
	for i, call := range verifies {
		req := &http.Request{Header: http.Header{"Cookie": {call.Header.Get("Cookie")}}}
		var names []string
		for _, cookie := range req.Cookies() {
			names = append(names, cookie.Name)
		}
		slices.Sort(names)
		if !slices.Equal(names, wantCookies[i]) {
			t.Fatalf("verify %d cookies = %v, want %v", i, names, wantCookies[i])
		}
	}

	t.Run("jar", func(t *testing.T) {
		jar := deviceCookieJar{}
		now := time.Now()
		resp := &http.Response{Header: http.Header{"Set-Cookie": {
			"a=1; Secure; HttpOnly; Path=/x; Domain=other.example.com; SameSite=Strict",
			"b=2; Secure",
			"gone=3; Expires=" + now.Add(-time.Hour).UTC().Format(http.TimeFormat),
		}}}
		jar.absorb(resp, now)
		if jar.header() != "a=1; b=2" {
			t.Fatalf("jar header = %q", jar.header())
		}
		jar.absorb(&http.Response{Header: http.Header{"Set-Cookie": {"a=; Max-Age=0"}}}, now)
		if jar.header() != "b=2" {
			t.Fatalf("expired cookie kept: %q", jar.header())
		}
	})
	env.assertNoSecretLeaks()
}

func TestDeviceChainRewritesOnlyIssuerVerifyURLs(t *testing.T) {
	cfg := NewDeviceFlowConfig(testDeviceFlowOptions(nil))
	hydra := harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{PublicURL: "http://hydra:4444", AdminURL: "http://hydra:4445"})
	chain := &deviceApprovalChain{hydra: hydra, cfg: cfg, flowID: "f1", clientID: testPublicClientID}
	marker := "client_id=" + testPublicClientID + "&device_verifier=v&haruki_dfl=f1"

	rewritten, err := chain.issuerVerifyURL(testIssuerURL+"/oauth2/device/verify?"+marker, "device_verifier")
	if err != nil || rewritten != "http://hydra:4444/oauth2/device/verify?"+marker {
		t.Fatalf("rewritten = %q, %v", rewritten, err)
	}
	for name, redirectTo := range map[string]string{
		"other host":       "https://evil.example.com/oauth2/device/verify?" + marker,
		"other scheme":     "http://api.example.com/oauth2/device/verify?" + marker,
		"other path":       testIssuerURL + "/oauth2/auth?" + marker,
		"relative":         "/oauth2/device/verify?" + marker,
		"frontend":         testFrontendURL + "/oauth2/device/verify?" + marker,
		"userinfo":         "https://user@api.example.com/oauth2/device/verify?" + marker,
		"user_code":        testIssuerURL + "/oauth2/device/verify?" + marker + "&user_code=%2A%2A%2A%2A",
		"prompt":           testIssuerURL + "/oauth2/device/verify?" + marker + "&prompt=none",
		"missing verifier": testIssuerURL + "/oauth2/device/verify?client_id=" + testPublicClientID + "&haruki_dfl=f1",
	} {
		if got, err := chain.issuerVerifyURL(redirectTo, "device_verifier"); err == nil {
			t.Errorf("%s: rewritten to %q", name, got)
		}
	}
	// An issuer with a path prefix keeps it in the match.
	options := testDeviceFlowOptions(nil)
	options.HydraIssuerURL = testIssuerURL + "/hydra"
	chain.cfg = NewDeviceFlowConfig(options)
	if _, err := chain.issuerVerifyURL(testIssuerURL+"/oauth2/device/verify?"+marker, "device_verifier"); err == nil {
		t.Fatal("verify URL outside the issuer path was rewritten")
	}
	if got, err := chain.issuerVerifyURL(testIssuerURL+"/hydra/oauth2/device/verify?"+marker, "device_verifier"); err != nil || got != "http://hydra:4444/oauth2/device/verify?"+marker {
		t.Fatalf("issuer-path verify URL = %q, %v", got, err)
	}

	// End to end, every public request after H9b goes to the internal
	// endpoint, and the frontend Locations are never requested.
	env := newDeviceBrowserEnv(t)
	_, userCode, handle := env.claim()
	approvedData(t, env.approve(handle, userCode, browserCall{}))
	for _, call := range env.hydra.calls {
		if call.Path == "/device" || call.Path == "/oauth2/login" || call.Path == "/oauth2/consent" || call.Path == "/device/done" {
			t.Fatalf("a frontend Location was requested: %s", call.Path)
		}
	}
	env.assertNoSecretLeaks()
}

func TestDeviceChainRejectsForeignRedirect(t *testing.T) {
	for _, tc := range []struct {
		name, stage string
		location    func(string) string
	}{
		{"foreign host", chainStageVerifyStart, func(string) string { return "https://evil.example.com/device?device_challenge=x" }},
		{"user_code echoed", chainStageVerifyStart, func(loc string) string { return loc + "&user_code=BCDFGHJK" }},
		{"wrong page", chainStageVerifyDevice, func(loc string) string { return strings.Replace(loc, "/oauth2/login", "/oauth2/consent", 1) }},
		{"lookalike host", chainStageVerifyLogin, func(loc string) string {
			return strings.Replace(loc, testFrontendURL, "https://haruki.example.com.evil.example", 1)
		}},
		{"prompt", chainStageVerifyLogin, func(loc string) string { return loc + "&prompt=none" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newDeviceBrowserEnv(t)
			env.chain.mutateLocation = func(stage, location string) string {
				if stage == tc.stage {
					return tc.location(location)
				}
				return location
			}
			flowID, userCode, handle := env.claim()
			expectApprovalFailed(t, env.approve(handle, userCode, browserCall{}), false)
			if env.field(flowID, "st") != deviceFlowStateFailed {
				t.Fatalf("state = %s, want failed", env.field(flowID, "st"))
			}
			if env.chain.stageHits(chainStageConsentAcc) != 0 || env.chain.completed != 0 {
				t.Fatal("the chain continued past a foreign redirect")
			}
			if !strings.Contains(env.logs.String(), "event=chain_error fid="+flowID) || !strings.Contains(env.logs.String(), "stage="+tc.stage) {
				t.Fatalf("missing chain_error log: %s", env.logs.String())
			}
			env.assertNoSecretLeaks()
		})
	}
}

func TestDeviceChainRequiresFlowMarkerEveryHop(t *testing.T) {
	dropMarker := func(raw string) string {
		parsed, _ := url.Parse(raw)
		query := parsed.Query()
		query.Set(deviceFlowMarkerParam, "0123456789abcdef0123456789abcdef")
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	for _, tc := range []struct {
		stage, field string
		untouched    string
	}{
		{chainStageDeviceAccept, "redirect_to", chainStageVerifyDevice},
		{chainStageLoginGet, "request_url", chainStageLoginAccept},
		{chainStageLoginAccept, "redirect_to", chainStageVerifyLogin},
		{chainStageConsentGet, "request_url", chainStageConsentAcc},
		{chainStageConsentAcc, "redirect_to", chainStageVerifyFinal},
	} {
		t.Run(tc.stage, func(t *testing.T) {
			env := newDeviceBrowserEnv(t)
			env.chain.mutateJSON = func(stage string, body map[string]any) {
				if stage == tc.stage {
					body[tc.field] = dropMarker(body[tc.field].(string))
				}
			}
			flowID, userCode, handle := env.claim()
			expectApprovalFailed(t, env.approve(handle, userCode, browserCall{}), false)
			if env.field(flowID, "st") != deviceFlowStateFailed || env.chain.stageHits(tc.untouched) != 0 {
				t.Fatalf("state = %s, next hop hits = %d", env.field(flowID, "st"), env.chain.stageHits(tc.untouched))
			}
			if !strings.Contains(env.logs.String(), "reason="+deviceChainReasonMarker) {
				t.Fatalf("missing marker_mismatch log: %s", env.logs.String())
			}
			env.assertNoSecretLeaks()
		})
	}
}

func TestDeviceChainRejectsLoginSkip(t *testing.T) {
	env := newDeviceBrowserEnv(t)
	env.chain.mutateJSON = func(stage string, body map[string]any) {
		if stage == chainStageLoginGet {
			body["skip"] = true
			body["subject"] = "kratos-someone-else"
		}
	}
	flowID, userCode, handle := env.claim()
	resp := env.approve(handle, userCode, browserCall{})
	expectApprovalFailed(t, resp, false)
	if strings.Contains(string(resp.Body), "someone-else") || strings.Contains(env.logs.String(), "someone-else") {
		t.Fatal("the skipped login's subject was echoed")
	}
	if env.chain.stageHits(chainStageLoginAccept) != 0 || env.field(flowID, "st") != deviceFlowStateFailed {
		t.Fatalf("login accepted after skip: state %s", env.field(flowID, "st"))
	}
	if !strings.Contains(env.logs.String(), "event=login_skip_unexpected fid="+flowID) {
		t.Fatalf("missing login_skip_unexpected log: %s", env.logs.String())
	}
	env.assertNoSecretLeaks()
}

func TestDeviceChainRejectsClientOrScopeMismatch(t *testing.T) {
	for name, tc := range map[string]struct {
		stage  string
		mutate func(map[string]any)
		reason string
	}{
		"login client":   {chainStageLoginGet, func(b map[string]any) { b["client"] = map[string]any{"client_id": "other"} }, deviceChainReasonClient},
		"login scope":    {chainStageLoginGet, func(b map[string]any) { b["requested_scope"] = []string{"user:read", "game-data:write"} }, deviceChainReasonScope},
		"login audience": {chainStageLoginGet, func(b map[string]any) { b["requested_access_token_audience"] = []string{"x"} }, deviceChainReasonAudience},
		"login url client": {chainStageLoginGet, func(b map[string]any) {
			b["request_url"] = strings.Replace(b["request_url"].(string), "client_id=", "client_id=x", 1)
		}, deviceChainReasonClient},
		"consent client":  {chainStageConsentGet, func(b map[string]any) { b["client"] = map[string]any{"client_id": "other"} }, deviceChainReasonClient},
		"consent scope":   {chainStageConsentGet, func(b map[string]any) { b["requested_scope"] = []string{"user:read"} }, deviceChainReasonScope},
		"consent subject": {chainStageConsentGet, func(b map[string]any) { b["subject"] = "kratos-" + testOtherUserID }, deviceChainReasonSubject},
		"consent no id":   {chainStageConsentGet, func(b map[string]any) { delete(b, "consent_request_id") }, deviceChainReasonNoConsentID},
		"redirect client": {chainStageDeviceAccept, func(b map[string]any) {
			b["redirect_to"] = strings.Replace(b["redirect_to"].(string), "client_id=", "client_id=x", 1)
		}, deviceChainReasonClient},
	} {
		t.Run(name, func(t *testing.T) {
			env := newDeviceBrowserEnv(t)
			env.chain.mutateJSON = func(stage string, body map[string]any) {
				if stage == tc.stage {
					tc.mutate(body)
				}
			}
			flowID, userCode, handle := env.claim()
			expectApprovalFailed(t, env.approve(handle, userCode, browserCall{}), false)
			if env.field(flowID, "st") != deviceFlowStateFailed || env.chain.stageHits(chainStageConsentAcc) != 0 {
				t.Fatalf("state = %s, consent accepts = %d", env.field(flowID, "st"), env.chain.stageHits(chainStageConsentAcc))
			}
			if env.field(flowID, "crid") != "" || env.inUnredeemed(flowID) {
				t.Fatal("a refused consent was recorded")
			}
			if !strings.Contains(env.logs.String(), "reason="+tc.reason) {
				t.Fatalf("missing reason %s: %s", tc.reason, env.logs.String())
			}
			env.assertNoSecretLeaks()
		})
	}
}

func TestDeviceChainSendsRememberFalseNoACR(t *testing.T) {
	env := newDeviceBrowserEnv(t)
	flowID, userCode, handle := env.claim()
	resp := env.post("/api/oauth2/device/approve", map[string]any{
		"flowHandle": handle, "userCode": userCode, "acknowledged": true, "label": " My\u202e  laptop\u200b ",
	}, browserCall{})
	approvedData(t, resp)

	login := env.chain.loginAcceptBody
	if len(login) != 3 || login["subject"] != "kratos-"+testUserID || login["remember"] != false || login["remember_for"] != float64(0) {
		t.Fatalf("login accept body = %v", login)
	}
	consent := env.chain.consentAcceptBody
	if consent["remember"] != false || consent["remember_for"] != float64(0) || len(consent["grant_access_token_audience"].([]any)) != 0 {
		t.Fatalf("consent accept body = %v", consent)
	}
	if _, ok := consent["acr"]; ok {
		t.Fatal("consent accept carries acr")
	}
	grant := consent["grant_scope"].([]any)
	if len(grant) != 2 || grant[0] != "offline_access" || grant[1] != "user:read" {
		t.Fatalf("grant_scope = %v", grant)
	}
	session := consent["session"].(map[string]any)
	accessToken := session["access_token"].(map[string]any)
	if accessToken["uid"] != testUserID || accessToken["flow"] != "device" || accessToken["device_flow_id"] != flowID || accessToken["device_label"] != "My laptop" {
		t.Fatalf("session.access_token = %v", accessToken)
	}
	if _, ok := session["id_token"].(map[string]any)["email"]; ok {
		t.Fatal("id_token carries email")
	}
	haruki := consent["context"].(map[string]any)["haruki"].(map[string]any)
	if haruki["flow"] != "device" || haruki["device_flow_id"] != flowID || haruki["label"] != "My laptop" ||
		haruki["label_source"] != deviceLabelSourceUser || haruki["approved_via"] != hydraDeviceApprovedVia {
		t.Fatalf("context.haruki = %v", haruki)
	}
	if env.field(flowID, "lbl") != "My laptop" || env.field(flowID, "lsrc") != deviceLabelSourceUser {
		t.Fatalf("stored label = %q (%s)", env.field(flowID, "lbl"), env.field(flowID, "lsrc"))
	}
	env.assertNoSecretLeaks()
}

func TestBuildHydraConsentAcceptBodyBrowserPathUnchanged(t *testing.T) {
	body := buildHydraConsentAcceptBody(hydraConsentAcceptBodyInput{
		GrantScope: []string{"openid", "profile"}, GrantAudience: nil, Remember: true, RememberFor: 3600,
		UserID: "u1", UserName: "Alice", UserEmail: "a@example.com", EmailVerified: true,
	})
	if _, ok := body["context"]; ok {
		t.Fatal("browser consent carries a context")
	}
	accessToken := body["session"].(map[string]any)["access_token"].(map[string]any)
	if len(accessToken) != 1 || accessToken["uid"] != "u1" || body["remember"] != true || body["remember_for"] != int64(3600) {
		t.Fatalf("browser consent body = %v", body)
	}
}

func TestDeviceChainFinalHopRetryThenUnconfirmed(t *testing.T) {
	t.Run("retry unknown", func(t *testing.T) {
		env := newDeviceBrowserEnv(t)
		env.chain.hook = func(stage string, _ int, w http.ResponseWriter, _ *http.Request) bool {
			if stage == chainStageVerifyFinal {
				closeConnection(t, w)
				return true
			}
			return false
		}
		flowID, userCode, handle := env.claim()
		expectUnconfirmed(t, env.approve(handle, userCode, browserCall{}))
		if env.chain.stageHits(chainStageVerifyFinal) != 2 || len(env.revocations()) != 0 {
			t.Fatalf("final hop hits = %d, revocations = %v", env.chain.stageHits(chainStageVerifyFinal), env.revocations())
		}
		if env.field(flowID, "st") != deviceFlowStateUnconfirmed || !env.inUnredeemed(flowID) || env.field(flowID, "crid") == "" {
			t.Fatalf("state = %s, unredeemed = %v", env.field(flowID, "st"), env.inUnredeemed(flowID))
		}
		if !strings.Contains(env.logs.String(), "event=unconfirmed fid="+flowID) {
			t.Fatalf("missing unconfirmed log: %s", env.logs.String())
		}
		// The device still gets its tokens: unconfirmed is approved class.
		env.hydra.token = hydraTokenAnswer(http.StatusOK, testTokenResponse)
		env.advance(6 * time.Second)
		hdc := env.issuedHDC[0]
		if token := env.poll(hdc, testPublicClientID); token.Status != http.StatusOK {
			t.Fatalf("token status = %d", token.Status)
		}
		env.assertNoSecretLeaks()
	})
	t.Run("retry succeeds", func(t *testing.T) {
		env := newDeviceBrowserEnv(t)
		env.chain.hook = func(stage string, hit int, w http.ResponseWriter, _ *http.Request) bool {
			if stage == chainStageVerifyFinal && hit == 1 {
				closeConnection(t, w)
				return true
			}
			return false
		}
		flowID, userCode, handle := env.claim()
		approvedData(t, env.approve(handle, userCode, browserCall{}))
		if env.chain.stageHits(chainStageVerifyFinal) != 2 || env.field(flowID, "st") != deviceFlowStateApproved {
			t.Fatalf("hits = %d, state = %s", env.chain.stageHits(chainStageVerifyFinal), env.field(flowID, "st"))
		}
		env.assertNoSecretLeaks()
	})
}

func TestDeviceChainFinalHopRetry403IsUnconfirmed(t *testing.T) {
	env := newDeviceBrowserEnv(t)
	// The first try completes in Hydra but its answer is lost; the retry then
	// finds the verifier used (403).
	var completed bool
	env.chain.hook = func(stage string, hit int, w http.ResponseWriter, r *http.Request) bool {
		if stage != chainStageVerifyFinal || hit != 1 {
			return false
		}
		env.chain.mu.Lock()
		env.chain.consentVerifiers[r.URL.Query().Get("consent_verifier")] = true
		env.chain.completed++
		env.chain.mu.Unlock()
		completed = true
		closeConnection(t, w)
		return true
	}
	flowID, userCode, handle := env.claim()
	expectUnconfirmed(t, env.approve(handle, userCode, browserCall{}))
	if !completed || env.chain.stageHits(chainStageVerifyFinal) != 2 || len(env.revocations()) != 0 {
		t.Fatalf("hits = %d, revocations = %v", env.chain.stageHits(chainStageVerifyFinal), env.revocations())
	}
	if env.field(flowID, "st") != deviceFlowStateUnconfirmed || !env.inUnredeemed(flowID) {
		t.Fatalf("state = %s", env.field(flowID, "st"))
	}
	env.assertNoSecretLeaks()
}

func TestDeviceChainFinalHop5xxRevokesBeforeRevert(t *testing.T) {
	for _, revokeStatus := range []int{http.StatusNoContent, http.StatusInternalServerError} {
		t.Run(http.StatusText(revokeStatus), func(t *testing.T) {
			env := newDeviceBrowserEnv(t)
			env.hydra.revokeStatus = revokeStatus
			env.chain.hook = func(stage string, hit int, w http.ResponseWriter, _ *http.Request) bool {
				if stage == chainStageVerifyFinal && hit == 1 {
					writeFakeHydraJSON(w, http.StatusInternalServerError, map[string]any{"error": "server_error", "error_description": testHydraErrorDetail})
					return true
				}
				return false
			}
			flowID, userCode, handle := env.claim()
			resp := env.approve(handle, userCode, browserCall{})
			if got := env.revocations(); len(got) != 1 || got[0] != "consent_request_id=consent-request-1" {
				t.Fatalf("revocations = %v", got)
			}
			if env.chain.stageHits(chainStageVerifyFinal) != 1 {
				t.Fatal("an explicit final-hop failure was retried")
			}
			if revokeStatus != http.StatusNoContent {
				// Not revoked: Hydra may hold the consent, so it stays unconfirmed.
				expectUnconfirmed(t, resp)
				if env.field(flowID, "st") != deviceFlowStateUnconfirmed || !env.inUnredeemed(flowID) {
					t.Fatalf("state = %s", env.field(flowID, "st"))
				}
				env.assertNoSecretLeaks()
				return
			}
			expectApprovalFailed(t, resp, true)
			if env.field(flowID, "st") != deviceFlowStateClaimed || env.inUnredeemed(flowID) || env.field(flowID, "anonce") != "" {
				t.Fatalf("state = %s, unredeemed = %v", env.field(flowID, "st"), env.inUnredeemed(flowID))
			}
			// The reverted flow can be approved again with the same handle.
			env.chain.hook = nil
			approvedData(t, env.approve(handle, userCode, browserCall{}))
			if env.field(flowID, "att") != "2" || env.field(flowID, "crid") != "consent-request-2" {
				t.Fatalf("att = %s, crid = %s", env.field(flowID, "att"), env.field(flowID, "crid"))
			}
			env.assertNoSecretLeaks()
		})
	}
}

func TestDeviceChainRestartsOnChallenge404(t *testing.T) {
	t.Run("restart succeeds", func(t *testing.T) {
		env := newDeviceBrowserEnv(t)
		env.chain.hook = func(stage string, hit int, w http.ResponseWriter, _ *http.Request) bool {
			if stage == chainStageDeviceAccept && hit == 1 {
				env.chain.adminError(w, http.StatusNotFound)
				return true
			}
			return false
		}
		flowID, userCode, handle := env.claim()
		approvedData(t, env.approve(handle, userCode, browserCall{}))
		verifies := env.hydra.callsTo(http.MethodGet, hydraDeviceVerifyPath)
		if env.chain.stageHits(chainStageVerifyStart) != 2 || verifies[1].Header.Get("Cookie") != "" {
			t.Fatalf("restart did not start over with a new jar: H9b hits %d", env.chain.stageHits(chainStageVerifyStart))
		}
		if env.field(flowID, "att") != "1" {
			t.Fatalf("a restart consumed an attempt: att = %s", env.field(flowID, "att"))
		}
		env.assertNoSecretLeaks()
	})
	t.Run("restart fails", func(t *testing.T) {
		env := newDeviceBrowserEnv(t)
		env.chain.hook = func(stage string, hit int, w http.ResponseWriter, _ *http.Request) bool {
			if stage == chainStageDeviceAccept {
				env.chain.adminError(w, []int{http.StatusUnauthorized, http.StatusNotFound}[(hit-1)%2])
				return true
			}
			return false
		}
		flowID, userCode, handle := env.claim()
		resp := env.approve(handle, userCode, browserCall{})
		expectApprovalFailed(t, resp, true)
		if env.chain.stageHits(chainStageDeviceAccept) != 2 || env.field(flowID, "st") != deviceFlowStateClaimed {
			t.Fatalf("device accept hits = %d, state = %s", env.chain.stageHits(chainStageDeviceAccept), env.field(flowID, "st"))
		}
		env.assertNoSecretLeaks()
	})
}

func TestDeviceChainSecondAttemptAccept400Unconfirmed(t *testing.T) {
	env := newDeviceBrowserEnv(t)
	// Attempt 1 fails retryably at H9d (no completed consent).
	env.chain.hook = func(stage string, hit int, w http.ResponseWriter, _ *http.Request) bool {
		if stage == chainStageVerifyDevice && hit == 1 {
			writeFakeHydraJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "unavailable"})
			return true
		}
		if stage == chainStageDeviceAccept && hit == 2 {
			env.chain.adminError(w, http.StatusBadRequest)
			return true
		}
		return false
	}
	flowID, userCode, handle := env.claim()
	expectApprovalFailed(t, env.approve(handle, userCode, browserCall{}), true)
	if env.field(flowID, "st") != deviceFlowStateClaimed {
		t.Fatalf("state after retryable failure = %s", env.field(flowID, "st"))
	}
	// Attempt 2: device accept 400 means an earlier attempt may have used
	// the code, so the outcome is unknown.
	expectUnconfirmed(t, env.approve(handle, userCode, browserCall{}))
	if env.field(flowID, "st") != deviceFlowStateUnconfirmed || env.field(flowID, "att") != "2" {
		t.Fatalf("state = %s, att = %s", env.field(flowID, "st"), env.field(flowID, "att"))
	}

	t.Run("first attempt", func(t *testing.T) {
		env := newDeviceBrowserEnv(t)
		env.chain.hook = func(stage string, _ int, w http.ResponseWriter, _ *http.Request) bool {
			if stage == chainStageDeviceAccept {
				env.chain.adminError(w, http.StatusBadRequest)
				return true
			}
			return false
		}
		flowID, userCode, handle := env.claim()
		expectBrowserError(t, env.approve(handle, userCode, browserCall{}), http.StatusGone, deviceCodeCodeExpired)
		if env.field(flowID, "st") != deviceFlowStateFailed {
			t.Fatalf("state = %s, want failed", env.field(flowID, "st"))
		}
		env.assertNoSecretLeaks()
	})
	env.assertNoSecretLeaks()
}

func TestDeviceChainAttemptsRunOut(t *testing.T) {
	env := newDeviceBrowserEnv(t)
	env.chain.hook = func(stage string, _ int, w http.ResponseWriter, _ *http.Request) bool {
		if stage == chainStageVerifyStart {
			writeFakeHydraJSON(w, http.StatusBadGateway, map[string]any{"error": "bad gateway"})
			return true
		}
		return false
	}
	flowID, userCode, handle := env.claim()
	expectApprovalFailed(t, env.approve(handle, userCode, browserCall{}), true)
	expectApprovalFailed(t, env.approve(handle, userCode, browserCall{}), true)
	expectApprovalFailed(t, env.approve(handle, userCode, browserCall{}), false)
	if env.field(flowID, "st") != deviceFlowStateFailed || env.field(flowID, "att") != "3" {
		t.Fatalf("state = %s, att = %s", env.field(flowID, "st"), env.field(flowID, "att"))
	}
	expectBrowserError(t, env.approve(handle, userCode, browserCall{}), http.StatusConflict, deviceCodeAlreadyHandled)
	env.assertNoSecretLeaks()
}

func TestDeviceChainUnexpectedFinalRedirectFailsAndRevokes(t *testing.T) {
	env := newDeviceBrowserEnv(t)
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
	if env.field(flowID, "st") != deviceFlowStateFailed || env.inUnredeemed(flowID) {
		t.Fatalf("state = %s, unredeemed = %v", env.field(flowID, "st"), env.inUnredeemed(flowID))
	}
	env.assertNoSecretLeaks()
}

func TestDeviceChainTimeoutBoundsTheChain(t *testing.T) {
	env := newDeviceBrowserEnv(t, func(options *DeviceFlowConfigOptions) {
		options.Timings.ApprovalTimeout = 200 * time.Millisecond
	})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	env.chain.hook = func(stage string, _ int, _ http.ResponseWriter, r *http.Request) bool {
		if stage == chainStageLoginGet {
			select {
			case <-r.Context().Done():
			case <-release:
			}
			return true
		}
		return false
	}
	flowID, userCode, handle := env.claim()
	started := time.Now()
	expectApprovalFailed(t, env.approve(handle, userCode, browserCall{}), true)
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("approve took %s", elapsed)
	}
	if env.field(flowID, "st") != deviceFlowStateClaimed {
		t.Fatalf("state = %s", env.field(flowID, "st"))
	}
	env.assertNoSecretLeaks()
}
