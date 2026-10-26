package usersponsor

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/sponsor"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/jsoncodec"
)

const testOrderNumber = "202401011200001234567890123"
const otherOrderNumber = "202401021200001234567890123"

func mockOrders(t *testing.T, e *testEnv, orders []map[string]any, ec, status int) *atomic.Int32 {
	t.Helper()
	calls := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.URL.Path != "/query-order" {
			t.Error("unexpected order API request")
		}
		var body map[string]string
		data, _ := io.ReadAll(r.Body)
		if err := jsoncodec.Unmarshal(data, &body); err != nil {
			t.Error(err)
		}
		// Check the creator credentials and exact signed params contract.
		sum := md5.Sum([]byte("creator-secretparams" + body["params"] + "ts" + body["ts"] + "user_idcreator"))
		if body["user_id"] != "creator" || body["sign"] != hex.EncodeToString(sum[:]) {
			t.Error("order lookup was not signed for the configured creator")
		}
		var params map[string]string
		if err := jsoncodec.Unmarshal([]byte(body["params"]), &params); err != nil {
			t.Error(err)
		}
		if len(params) != 1 || !orderNumberPattern.MatchString(params["out_trade_no"]) {
			t.Error("order query contains unintended parameters")
		}
		w.WriteHeader(status)
		out, _ := jsoncodec.Marshal(map[string]any{"ec": ec, "data": map[string]any{"list": orders}})
		_, _ = w.Write(out)
	}))
	t.Cleanup(server.Close)
	e.h.orders = sponsor.NewAfdianConfig(sponsor.AfdianConfigOptions{UserID: "creator", APIToken: "creator-secret", APIBaseURL: server.URL})
	return calls
}

func paidOrder(number string) map[string]any {
	return map[string]any{"out_trade_no": number, "user_id": "afdian-alice", "status": 2, "product_type": 0, "month": 1, "total_amount": "5.00", "create_time": time.Date(2024, 1, 1, 4, 0, 0, 0, time.UTC).Unix()}
}

func TestOrderVerificationWorksWithoutOAuthAndKeepsPastSponsorship(t *testing.T) {
	e := newEnv(t)
	e.h.oauth = OAuthConfig{}
	calls := mockOrders(t, e, []map[string]any{paidOrder(testOrderNumber)}, 200, 200)
	status, body := e.request(t, "GET", "/api/user/alice/afdian", "alice", "session", nil)
	if status != 200 || !strings.Contains(string(body), `"oauthEnabled":false`) || !strings.Contains(string(body), `"orderVerificationEnabled":true`) {
		t.Fatalf("incorrect method availability: %s", body)
	}
	status, body = e.request(t, "POST", "/api/user/alice/afdian/verify-order", "alice", "session", map[string]string{"orderNumber": " " + testOrderNumber + " ", "user_id": "forged-id"})
	if status != 200 || calls.Load() != 1 {
		t.Fatalf("verify: %d %s", status, body)
	}
	for _, want := range []string{`"verificationMethod":"order"`, `"eligible":true`, `"category":"former"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("missing %s: %s", want, body)
		}
	}
	for _, secret := range []string{testOrderNumber, "creator-secret", "totalAmount"} {
		if strings.Contains(string(body), secret) {
			t.Fatal("proof or payment details leaked in response")
		}
	}
	u := e.db.User.GetX(t.Context(), "alice")
	if u.AfdianUserID == nil || *u.AfdianUserID != "afdian-alice" {
		t.Fatal("trusted client-supplied identity")
	}
	frame := frameCatalog()[0]
	e.db.GameAccountBinding.Create().SetServer("cn").SetGameUserID("12345").SetVerified(true).SetUserID("alice").SaveX(t.Context())
	status, _ = e.request(t, "PUT", "/api/user/alice/sponsor-frames/cn/12345", "alice", "session", map[string]string{"frameId": frame.ID})
	if status != 200 {
		t.Fatal("order-verified former sponsor cannot save frame")
	}
	// Re-verification neither creates duplicate orders nor extends expiry.
	before := e.db.Sponsor.GetX(t.Context(), "afdian_afdian-alice").PlanExpiresAt
	e.request(t, "POST", "/api/user/alice/afdian/verify-order", "alice", "session", map[string]string{"orderNumber": testOrderNumber})
	after := e.db.Sponsor.GetX(t.Context(), "afdian_afdian-alice").PlanExpiresAt
	if e.db.SponsorAfdianOrder.Query().CountX(t.Context()) != 1 || before == nil || after == nil || !before.Equal(*after) {
		t.Fatal("re-verification duplicated or extended sponsorship")
	}
	e.request(t, "DELETE", "/api/user/alice/afdian", "alice", "session", nil)
	u = e.db.User.GetX(t.Context(), "alice")
	if u.AfdianUserID != nil || u.AfdianVerificationMethod != nil {
		t.Fatal("unlink did not clear the verification method")
	}
}

func TestOrdersCannotClaimAnotherBoundAfdianAccount(t *testing.T) {
	for _, number := range []string{testOrderNumber, otherOrderNumber} {
		t.Run(number, func(t *testing.T) {
			e := newEnv(t)
			mockOrders(t, e, []map[string]any{paidOrder(number)}, 200, 200)
			e.db.User.UpdateOneID("alice").SetAfdianUserID("afdian-alice").SetAfdianVerificationMethod("oauth2").ExecX(t.Context())
			status, body := e.request(t, "POST", "/api/user/bob/afdian/verify-order", "bob", "session", map[string]string{"orderNumber": number})
			if status != 400 || !strings.Contains(string(body), "could not be verified or is already linked") {
				t.Fatalf("duplicate claim: %d %s", status, body)
			}
			if e.db.User.GetX(t.Context(), "bob").AfdianUserID != nil {
				t.Fatal("other account acquired sponsor identity")
			}
		})
	}
}

func TestOrderAndOAuthProofsCanUpgradeButNeverDowngrade(t *testing.T) {
	e := newEnv(t)
	mockOrders(t, e, []map[string]any{paidOrder(testOrderNumber)}, 200, 200)
	e.request(t, "POST", "/api/user/alice/afdian/verify-order", "alice", "session", map[string]string{"orderNumber": testOrderNumber})
	state := e.begin(t, "alice")
	status, _ := e.request(t, "POST", "/api/user/alice/afdian/callback", "alice", "session", map[string]string{"code": "code", "state": state})
	if status != 200 {
		t.Fatal("OAuth could not confirm order-verified identity")
	}
	status, body := e.request(t, "POST", "/api/user/alice/afdian/verify-order", "alice", "session", map[string]string{"orderNumber": testOrderNumber})
	if status != 200 || !strings.Contains(string(body), `"verificationMethod":"oauth2"`) {
		t.Fatal("order verification downgraded OAuth proof")
	}
	e.db.User.UpdateOneID("alice").ClearAfdianVerificationMethod().ExecX(t.Context())
	status, body = e.request(t, "POST", "/api/user/alice/afdian/verify-order", "alice", "session", map[string]string{"orderNumber": testOrderNumber})
	if status != 200 || !strings.Contains(string(body), `"verificationMethod":"oauth2"`) {
		t.Fatal("legacy OAuth proof downgraded")
	}
}

func TestOrderVerificationRejectsInvalidOrUnpaidProviderResults(t *testing.T) {
	unpaid := paidOrder(testOrderNumber)
	unpaid["status"] = 1
	missingUser := paidOrder(testOrderNumber)
	delete(missingUser, "user_id")
	for _, tc := range []struct {
		name             string
		orders           []map[string]any
		ec, status, want int
	}{
		{"unknown or foreign creator", nil, 200, 200, 400},
		{"unpaid", []map[string]any{unpaid}, 200, 200, 400},
		{"wrong order", []map[string]any{paidOrder(otherOrderNumber)}, 200, 200, 400},
		{"missing payer", []map[string]any{missingUser}, 200, 200, 400},
		{"provider error", []map[string]any{paidOrder(testOrderNumber)}, 400, 200, 502},
		{"missing success ec", []map[string]any{paidOrder(testOrderNumber)}, 0, 200, 502},
		{"upstream unavailable", nil, 200, 503, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			mockOrders(t, e, tc.orders, tc.ec, tc.status)
			status, body := e.request(t, "POST", "/api/user/alice/afdian/verify-order", "alice", "session", map[string]string{"orderNumber": testOrderNumber})
			if status != tc.want {
				t.Fatalf("got %d %s", status, body)
			}
			if e.db.User.GetX(t.Context(), "alice").AfdianUserID != nil || e.db.SponsorAfdianOrder.Query().CountX(t.Context()) != 0 {
				t.Fatal("unverified response changed sponsorship")
			}
		})
	}
}

func TestOrderVerificationValidationAuthenticationAndLimits(t *testing.T) {
	for i, number := range []string{"", "123", testOrderNumber + "," + otherOrderNumber, "２０２４０１０１１２００００１２３４５６７８９０１２３", strings.Repeat("1", 129)} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			e := newEnv(t)
			calls := mockOrders(t, e, nil, 200, 200)
			status, _ := e.request(t, "POST", "/api/user/alice/afdian/verify-order", "alice", "session", map[string]string{"orderNumber": number})
			if status != 400 || calls.Load() != 0 {
				t.Fatal("invalid/batch order reached provider")
			}
		})
	}
	e := newEnv(t)
	calls := mockOrders(t, e, nil, 200, 200)
	req := httptest.NewRequest("POST", "/api/user/alice/afdian/verify-order", strings.NewReader(`{"orderNumber":"`+testOrderNumber+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-User", "alice")
	req.Header.Set("X-Test-Unverified", "true")
	resp, err := e.app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 || calls.Load() != 0 {
		t.Fatal("unverified email reached provider")
	}
	status, _ := e.request(t, "POST", "/api/user/bob/afdian/verify-order", "alice", "session", map[string]string{"orderNumber": testOrderNumber})
	if status != 403 || calls.Load() != 0 {
		t.Fatal("cross-user request reached provider")
	}
	for batch := 0; batch < 4; batch++ {
		for i := 0; i < 5; i++ {
			status, _ = e.request(t, "POST", "/api/user/alice/afdian/verify-order", "alice", "session", map[string]string{"orderNumber": testOrderNumber})
			if status != 400 {
				t.Fatalf("attempt %d/%d unexpectedly rejected: %d", batch, i, status)
			}
		}
		status, _ = e.request(t, "POST", "/api/user/alice/afdian/verify-order", "alice", "session", map[string]string{"orderNumber": testOrderNumber})
		if status != 429 || calls.Load() != int32((batch+1)*5) {
			t.Fatal("short rate limit bypassed")
		}
		e.redis.FastForward(10*time.Minute + time.Second)
	}
	status, _ = e.request(t, "POST", "/api/user/alice/afdian/verify-order", "alice", "session", map[string]string{"orderNumber": testOrderNumber})
	if status != 429 || calls.Load() != 20 {
		t.Fatal("daily limit bypassed")
	}
	manager := e.h.api.DBManager.Redis
	e.redis.Set(manager.KeyBuilder().BuildAfdianOrderRateKey("global", "all"), "120")
	status, _ = e.request(t, "POST", "/api/user/bob/afdian/verify-order", "bob", "session", map[string]string{"orderNumber": testOrderNumber})
	if status != 429 {
		t.Fatal("global rate limit bypassed")
	}
}
