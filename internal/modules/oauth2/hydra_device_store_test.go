package oauth2

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
)

func TestDeviceFlowConfigZeroValue(t *testing.T) {
	var cfg DeviceFlowConfig
	active, err := cfg.Active(t.Context())
	if cfg.Enabled() || active || err != nil {
		t.Fatalf("zero config: enabled=%v active=%v err=%v", cfg.Enabled(), active, err)
	}
	if !cfg.ClientAllowed("any") || cfg.OriginAllowed("https://x.example.com") || cfg.VerificationURL() != "" || cfg.UserCodeTTL() != 0 {
		t.Fatal("zero config methods returned unexpected values")
	}
	gated := cfg.WithRuntimeGate(func(context.Context) (bool, error) { return true, nil })
	if active, _ := gated.Active(t.Context()); active {
		t.Fatal("a runtime gate must not enable a flow disabled at startup")
	}
	// The store can be built without a DB manager and answers unavailable.
	store := newDeviceFlowStore(nil)
	if _, _, err := store.flowIDForDeviceCode(t.Context(), "hdc_x"); !errors.Is(err, errDeviceStoreUnavailable) {
		t.Fatalf("err = %v, want errDeviceStoreUnavailable", err)
	}
	if _, _, err := newDeviceFlowStore(&database.HarukiToolboxDBManager{}).flowIDForDeviceCode(t.Context(), "hdc_x"); !errors.Is(err, errDeviceStoreUnavailable) {
		t.Fatalf("err = %v, want errDeviceStoreUnavailable", err)
	}
}

func TestDeviceFlowConfigDerivedValues(t *testing.T) {
	options := testDeviceFlowOptions(nil)
	options.HydraIssuerURL = "https://API.example.com/hydra/"
	options.ClientAllowlist = []string{" haruki-client "}
	cfg := NewDeviceFlowConfig(options)
	if cfg.VerificationURL() != testFrontendURL+"/device" || cfg.VerificationURLComplete("BCDF-GHJK") != testFrontendURL+"/device?user_code=BCDF-GHJK" {
		t.Fatalf("verification URL = %q", cfg.VerificationURL())
	}
	if cfg.HydraIssuerOrigin() != "https://api.example.com" || cfg.HydraIssuerPath() != "/hydra" || cfg.FrontendOrigin() != testFrontendURL {
		t.Fatalf("issuer %q %q, frontend %q", cfg.HydraIssuerOrigin(), cfg.HydraIssuerPath(), cfg.FrontendOrigin())
	}
	// allowed_origins empty means the frontend's origin.
	if !cfg.OriginAllowed(testFrontendURL) || cfg.OriginAllowed("https://evil.example.com") || cfg.OriginAllowed("") || cfg.OriginAllowed("null") {
		t.Fatal("origin allowlist mismatch")
	}
	if !cfg.ClientAllowed("haruki-client") || cfg.ClientAllowed("other") {
		t.Fatal("client allowlist mismatch")
	}
	options.AllowedOrigins = []string{"https://a.example.com"}
	options.VerificationURL = "https://short.example.com/d"
	cfg = NewDeviceFlowConfig(options)
	if cfg.OriginAllowed(testFrontendURL) || !cfg.OriginAllowed("https://A.example.com") || cfg.VerificationURL() != "https://short.example.com/d" {
		t.Fatal("explicit allowed_origins or verification_url ignored")
	}
}

func TestDeviceFlowGateCachesSuccessOnly(t *testing.T) {
	var reads atomic.Int32
	failing := atomic.Bool{}
	cfg := NewDeviceFlowConfig(testDeviceFlowOptions(nil)).WithRuntimeGate(func(context.Context) (bool, error) {
		reads.Add(1)
		if failing.Load() {
			return false, errors.New("redis down")
		}
		return true, nil
	})
	now := time.Unix(1000, 0)
	cfg.gate.now = func() time.Time { return now }

	for range 5 {
		if active, err := cfg.Active(t.Context()); !active || err != nil {
			t.Fatalf("active=%v err=%v", active, err)
		}
	}
	if reads.Load() != 1 {
		t.Fatalf("runtime switch read %d times within 1 s, want 1", reads.Load())
	}
	// Within the cache window a failing store is not even consulted.
	failing.Store(true)
	if active, err := cfg.Active(t.Context()); !active || err != nil {
		t.Fatal("a cached success must be served for 1 s")
	}
	now = now.Add(1100 * time.Millisecond)
	if _, err := cfg.Active(t.Context()); err == nil {
		t.Fatal("an unreadable switch past the cache window must be an error")
	}
	if _, err := cfg.Active(t.Context()); err == nil || reads.Load() != 3 {
		t.Fatalf("errors must not be cached (reads=%d)", reads.Load())
	}
	failing.Store(false)
	if active, err := cfg.Active(t.Context()); !active || err != nil {
		t.Fatal("recovery not observed")
	}
}

func TestRevokeHydraConsentSessionByIDSendsOnlyTheID(t *testing.T) {
	var method, rawQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, rawQuery = r.Method+" "+r.URL.Path, r.URL.RawQuery
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	hydraConfig := harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{AdminURL: server.URL})
	if err := RevokeHydraConsentSessionByID(t.Context(), hydraConfig, " a+b/c "); err != nil {
		t.Fatal(err)
	}
	if method != "DELETE /admin/oauth2/auth/sessions/consent" || rawQuery != "consent_request_id=a%2Bb%2Fc" {
		t.Fatalf("request = %s ?%s", method, rawQuery)
	}
	if err := RevokeHydraConsentSessionByID(t.Context(), hydraConfig, " "); err == nil {
		t.Fatal("an empty consent request ID must be refused")
	}
}

func TestDeviceRateReservationIsAllOrNothing(t *testing.T) {
	env := newDeviceTestEnv(t)
	counters := []deviceRateCounter{
		{Key: "test:a", Limit: 3, Window: time.Minute},
		{Key: "test:b", Limit: 1, Window: time.Hour},
	}
	first, err := env.store.reserve(t.Context(), counters)
	if err != nil || first.LimitedIndex != -1 || first.Counts[0] != 1 || first.Counts[1] != 1 {
		t.Fatalf("first reservation = %+v, %v", first, err)
	}
	if ttl := env.redis.TTL("test:b"); ttl != time.Hour {
		t.Fatalf("per-key window not applied: %s", ttl)
	}
	second, err := env.store.reserve(t.Context(), counters)
	if err != nil || second.LimitedIndex != 1 {
		t.Fatalf("second reservation = %+v, %v", second, err)
	}
	if got, _ := env.redis.Get("test:a"); got != "1" {
		t.Fatalf("a refused reservation consumed counter a: %s", got)
	}
	if err := env.store.release(t.Context(), counters); err != nil {
		t.Fatal(err)
	}
	if env.redis.Exists("test:a") || env.redis.Exists("test:b") {
		t.Fatal("release must delete counters that reach zero")
	}
	if got := env.store.retryAfterSeconds(t.Context(), "missing", 10*time.Minute); got != 600 {
		t.Fatalf("retry-after fallback = %d", got)
	}
}

func TestDeviceClientCacheIsBounded(t *testing.T) {
	now := time.Unix(0, 0)
	cache := newDeviceClientCache(func() time.Time { return now })
	lookups := 0
	notFound := func(context.Context, string) (*HydraOAuthClient, error) {
		lookups++
		return nil, &hydraRequestError{Status: http.StatusNotFound}
	}
	for i := range deviceClientCacheMaxEntries + 10 {
		if client, err := cache.get(t.Context(), notFound, "id-"+strconv.Itoa(i)); client != nil || err != nil {
			t.Fatalf("404 lookup = %v, %v", client, err)
		}
	}
	if len(cache.entries) != deviceClientCacheMaxEntries {
		t.Fatalf("cache grew to %d entries", len(cache.entries))
	}
	failing := func(context.Context, string) (*HydraOAuthClient, error) { return nil, errors.New("down") }
	if _, err := cache.get(t.Context(), failing, "fresh"); err == nil {
		t.Fatal("a lookup error must be returned")
	}
	// Expired entries are evicted to make room.
	now = now.Add(deviceClientCacheTTL)
	if _, err := cache.get(t.Context(), notFound, "after-expiry"); err != nil {
		t.Fatal(err)
	}
	if len(cache.entries) != 1 {
		t.Fatalf("expired entries were not evicted: %d", len(cache.entries))
	}
}

func TestDeviceFlowReaper(t *testing.T) {
	env := newDeviceTestEnv(t)
	reaper := &deviceFlowReaper{store: env.store, hydraConfig: env.hydraConfig, logger: env.logger, grace: time.Minute}

	_, revoked := env.issue(testPublicClientID)
	env.approve(revoked, deviceFlowStateApproved, "crid-revoked")
	_, issued := env.issue(testPublicClientID)
	env.approve(issued, deviceFlowStateIssued, "crid-issued")
	_, noConsent := env.issue(testPublicClientID)
	env.approve(noConsent, deviceFlowStateApproving, "")
	_, denied := env.issue(testPublicClientID)
	env.approve(denied, deviceFlowStateDenied, "crid-denied")
	_, failing := env.issue(testPublicClientID)
	env.approve(failing, deviceFlowStateUnconfirmed, "crid-failing")

	env.advance(testHydraDeviceTTL*time.Second + 61*time.Second)
	// Make one revocation fail: the flow goes back with score now.
	failingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("consent_request_id") == "crid-failing" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		env.hydra.serve(w, r)
	}))
	t.Cleanup(failingServer.Close)
	reaper.hydraConfig = harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{AdminURL: failingServer.URL})
	reaper.runOnce(t.Context())

	revokes := env.hydra.callsTo(http.MethodDelete, "/admin/oauth2/auth/sessions/consent")
	got := map[string]bool{}
	for _, call := range revokes {
		got[call.RawQuery] = true
	}
	if len(revokes) != 2 || !got["consent_request_id=crid-revoked"] || !got["consent_request_id=crid-denied"] {
		t.Fatalf("revocations = %+v", revokes)
	}
	for flowID, wantState := range map[string]string{
		revoked: deviceFlowStateExpired, issued: deviceFlowStateIssued, noConsent: deviceFlowStateExpired,
		denied: deviceFlowStateDenied, failing: deviceFlowStateUnconfirmed,
	} {
		if state := env.field(flowID, "st"); state != wantState {
			t.Errorf("flow state = %s, want %s", state, wantState)
		}
	}
	for _, flowID := range []string{revoked, issued, noConsent, denied} {
		if env.inUnredeemed(flowID) {
			t.Error("a reaped flow is still unredeemed")
		}
	}
	score, err := env.redis.ZScore(env.unredeemedKey(), failing)
	if err != nil || int64(score) != env.now().UnixMilli() {
		t.Fatalf("failed revocation was not requeued with score now: %v %v", score, err)
	}
	// The requeued flow is retried only after another grace period.
	reaper.runOnce(t.Context())
	if !env.inUnredeemed(failing) {
		t.Fatal("requeued flow retried too early")
	}
	env.assertNoSecretLeaks()
}

// TestDeviceFlowReaperRevokesAfterFlowExpired: a revocation that keeps failing
// for longer than the flow HASH lives (expires_in + record_grace) must still
// revoke the recorded consent once Hydra recovers, instead of finding the
// flow gone and dropping it while the consent stays among the user's apps.
func TestDeviceFlowReaperRevokesAfterFlowExpired(t *testing.T) {
	env := newDeviceTestEnv(t)
	reaper := &deviceFlowReaper{store: env.store, hydraConfig: env.hydraConfig, logger: env.logger, grace: time.Minute}
	keys := env.db.Redis.KeyBuilder()

	// Record the consent the way the approval chain does (H9h), then approve.
	_, flowID := env.issue(testPublicClientID)
	env.setFlow(flowID, "st", deviceFlowStateApproving, "anonce", "nonce-1", "cby", "4242")
	if recorded, err := env.store.recordConsent(t.Context(), flowID, "nonce-1", "crid-late", "kratos-4242"); err != nil || !recorded {
		t.Fatalf("recordConsent = %v, %v", recorded, err)
	}
	if code, state, err := env.store.finishApprove(t.Context(), flowID, "nonce-1", deviceFinishApproved, "label", deviceLabelSourceDevice, 3); err != nil || code != deviceDecisionOK || state != deviceFlowStateApproved {
		t.Fatalf("finishApprove = %s %s %v", code, state, err)
	}
	cridKey := keys.BuildOAuth2DeviceConsentRequestKey(flowID)
	if got, _ := env.redis.Get(cridKey); got != "crid-late" {
		t.Fatalf("crid key = %q", got)
	}
	if env.redis.TTL(cridKey) <= env.redis.TTL(env.flowKey(flowID)) {
		t.Fatalf("crid key TTL %s does not outlive the flow's %s", env.redis.TTL(cridKey), env.redis.TTL(env.flowKey(flowID)))
	}

	// A flow that recorded nothing and whose HASH is gone is only dropped.
	if _, err := env.redis.ZAdd(env.unredeemedKey(), 0, "0000000000000000000000000000dead"); err != nil {
		t.Fatal(err)
	}

	// Hydra keeps failing past the flow's lifetime.
	env.hydra.mu.Lock()
	env.hydra.revokeStatus = http.StatusInternalServerError
	env.hydra.mu.Unlock()
	env.advance(testHydraDeviceTTL*time.Second + 61*time.Second)
	reaper.runOnce(t.Context())
	if !env.inUnredeemed(flowID) || env.inUnredeemed("0000000000000000000000000000dead") {
		t.Fatal("the failed flow must be requeued and the empty one dropped")
	}
	if env.redis.TTL(cridKey) != deviceFlowConsentRequestRetention {
		t.Fatalf("a failed revocation did not renew the crid key: TTL %s", env.redis.TTL(cridKey))
	}
	env.redis.FastForward(env.redis.TTL(env.flowKey(flowID)) + time.Second)
	if env.redis.Exists(env.flowKey(flowID)) || !env.redis.Exists(cridKey) {
		t.Fatal("expected the flow HASH expired and the crid key kept")
	}

	// Hydra recovers: the next round revokes by the kept crid and cleans up.
	env.hydra.mu.Lock()
	env.hydra.revokeStatus = 0
	env.hydra.mu.Unlock()
	env.advance(61 * time.Second)
	reaper.runOnce(t.Context())
	revokes := env.hydra.callsTo(http.MethodDelete, "/admin/oauth2/auth/sessions/consent")
	if len(revokes) != 2 || revokes[0].RawQuery != "consent_request_id=crid-late" || revokes[1].RawQuery != "consent_request_id=crid-late" {
		t.Fatalf("revocations = %+v, want the failed one and its retry", revokes)
	}
	if env.inUnredeemed(flowID) || env.redis.Exists(cridKey) || env.redis.Exists(env.flowKey(flowID)) {
		t.Fatal("a revoked flow must leave the set and its crid key, without recreating the flow")
	}
	env.assertNoSecretLeaks()
}

// TestDeviceFlowConsentRequestKeyLeavesWithTheSet checks that every way out
// of the unredeemed set also deletes the flow's crid key.
func TestDeviceFlowConsentRequestKeyLeavesWithTheSet(t *testing.T) {
	env := newDeviceTestEnv(t)
	keys := env.db.Redis.KeyBuilder()
	record := func(nonce string) (string, string) {
		_, flowID := env.issue(testPublicClientID)
		env.setFlow(flowID, "st", deviceFlowStateApproving, "anonce", nonce, "cby", "4242", "att", "1")
		if recorded, err := env.store.recordConsent(t.Context(), flowID, nonce, "crid-"+nonce, "kratos-4242"); err != nil || !recorded {
			t.Fatalf("recordConsent = %v, %v", recorded, err)
		}
		return flowID, keys.BuildOAuth2DeviceConsentRequestKey(flowID)
	}

	reverted, revertedKey := record("revert")
	if _, _, err := env.store.finishApprove(t.Context(), reverted, "revert", deviceFinishRevert, "", "", 3); err != nil {
		t.Fatal(err)
	}
	removed, removedKey := record("removed")
	if err := env.store.removeUnredeemed(t.Context(), removed); err != nil {
		t.Fatal(err)
	}
	issued, issuedKey := record("issued")
	if _, _, err := env.store.finishApprove(t.Context(), issued, "issued", deviceFinishApproved, "label", deviceLabelSourceDevice, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.settle(t.Context(), issued, "hdc_unused", deviceHydraResultOK, "1"); err != nil {
		t.Fatal(err)
	}
	for name, flow := range map[string][2]string{"revert": {reverted, revertedKey}, "removeUnredeemed": {removed, removedKey}, "issued": {issued, issuedKey}} {
		if env.inUnredeemed(flow[0]) || env.redis.Exists(flow[1]) {
			t.Errorf("%s: the flow left the set but kept its crid key (or stayed in the set)", name)
		}
	}
}

func TestStartDeviceFlowReaperLifecycle(t *testing.T) {
	disabled := StartDeviceFlowReaper(t.Context(), DeviceFlowReaperOptions{})
	disabled()

	env := newDeviceTestEnv(t)
	_, flowID := env.issue(testPublicClientID)
	env.approve(flowID, deviceFlowStateApproved, testConsentRequestID)
	if _, err := env.redis.ZAdd(env.unredeemedKey(), 0, flowID); err != nil { // long overdue
		t.Fatal(err)
	}

	options := testDeviceFlowOptions(env.logger)
	options.Timings.ReaperInterval = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(t.Context())
	wait := StartDeviceFlowReaper(ctx, DeviceFlowReaperOptions{
		Config: NewDeviceFlowConfig(options), HydraConfig: env.hydraConfig, DBManager: env.db, Logger: env.logger,
	})
	deadline := time.Now().Add(5 * time.Second)
	for env.inUnredeemed(flowID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	wait()
	if env.inUnredeemed(flowID) {
		t.Fatal("the running reaper never handled the overdue flow")
	}
}
