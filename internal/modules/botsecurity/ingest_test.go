package botsecurity

import (
	stdjson "encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/botsecurityalert"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	goredis "github.com/redis/go-redis/v9"
)

// cloudAlertPayload is a verbatim copy of alertPayload in Haruki-Cloud
// internal/core/secevent/secevent.go, marshalled with encoding/json as Cloud
// does, so the ingest contract is checked against the real wire shape.
type cloudAlertPayload struct {
	Kind          string `json:"kind"`
	BotID         string `json:"bot_id,omitempty"`
	BuildID       string `json:"build_id,omitempty"`
	ClientVersion string `json:"client_version,omitempty"`
	SourceIP      string `json:"source_ip,omitempty"`
	Reason        string `json:"reason,omitempty"`
	Enforced      bool   `json:"enforced"`
	Count         int64  `json:"count"`
	Threshold     int    `json:"threshold"`
	WindowSeconds int64  `json:"window_seconds"`
	Node          string `json:"node,omitempty"`
	Time          string `json:"time"`
}

func cloudBody(t *testing.T, payload cloudAlertPayload) string {
	t.Helper()
	data, err := stdjson.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func sampleCloudPayload() cloudAlertPayload {
	return cloudAlertPayload{
		Kind:          "auth_failed",
		BotID:         "30042042",
		BuildID:       "build-7f3a",
		ClientVersion: "3.2.1",
		SourceIP:      "203.0.113.7",
		Reason:        "invalid credential",
		Enforced:      true,
		Count:         5,
		Threshold:     5,
		WindowSeconds: 600,
		Node:          "node-a",
		Time:          testNow.Add(-time.Minute).Format(time.RFC3339),
	}
}

type ingestTestEnv struct {
	app    *fiber.App
	helper *harukiAPIHelper.HarukiToolboxRouterHelpers
	logs   *logBuffer
}

func newIngestTestEnv(t *testing.T) ingestTestEnv {
	t.Helper()
	helper := newTestHelper(t)
	app := fiber.New()
	helper.Router = app
	logger, logs := newTestLogger()
	RegisterIngestRoutes(helper, IngestRouteOptions{
		Config: testIngestConfig(t),
		Logger: logger,
		Now:    func() time.Time { return testNow },
	})
	return ingestTestEnv{app: app, helper: helper, logs: logs}
}

func (e ingestTestEnv) post(t *testing.T, body string, authorization ...string) testResponse {
	t.Helper()
	headers := map[string]string{"Content-Type": "application/json"}
	if len(authorization) > 0 {
		headers["Authorization"] = authorization[0]
	} else {
		headers["Authorization"] = "Bearer " + testIngestToken
	}
	return doRequest(t, e.app, http.MethodPost, IngestPath, body, headers)
}

func TestParseIngestConfig(t *testing.T) {
	cfg, err := ParseIngestConfig("  ")
	if err != nil || cfg.Enabled() {
		t.Fatalf("blank value: enabled %v err %v, want disabled", cfg.Enabled(), err)
	}
	sum := strings.Repeat("ab", 32)
	for _, value := range []string{sum, strings.ToUpper(sum), " " + sum + "\n"} {
		cfg, err := ParseIngestConfig(value)
		if err != nil || !cfg.Enabled() {
			t.Fatalf("%q: enabled %v err %v", value, cfg.Enabled(), err)
		}
	}
	for _, value := range []string{sum[:63], sum + "a", "zz" + sum[2:], "secret-token"} {
		_, err := ParseIngestConfig(value)
		if err == nil || !strings.Contains(err.Error(), "bot_security.ingest_token_sha256") || strings.Contains(err.Error(), value) {
			t.Fatalf("%q: err = %v, want a keyed error without the value", value, err)
		}
	}
}

func TestIngestAuthorization(t *testing.T) {
	env := newIngestTestEnv(t)
	body := cloudBody(t, sampleCloudPayload())

	cases := []struct {
		name   string
		header map[string]string
		reason string
	}{
		{"missing", map[string]string{}, "missing"},
		{"wrong token", map[string]string{"Authorization": "Bearer wrong-token"}, "mismatch"},
		{"basic scheme", map[string]string{"Authorization": "Basic Ym90OnNlY3JldA=="}, "malformed"},
		{"bare token", map[string]string{"Authorization": testIngestToken}, "malformed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.header["Content-Type"] = "application/json"
			resp := doRequest(t, env.app, http.MethodPost, IngestPath, body, tc.header)
			if resp.Status != fiber.StatusUnauthorized || strings.TrimSpace(string(resp.Body)) != `{"error":"unauthorized"}` {
				t.Fatalf("status %d body %s, want 401 unauthorized", resp.Status, resp.Body)
			}
			if resp.Header.Get("Cache-Control") != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", resp.Header.Get("Cache-Control"))
			}
			if !strings.Contains(env.logs.String(), "event=unauthorized reason="+tc.reason) {
				t.Fatalf("log lacks reason %s: %s", tc.reason, env.logs.String())
			}
		})
	}

	// Two Authorization headers are refused even when one is right.
	req := doRequestMulti(t, env.app, body, []string{"Bearer " + testIngestToken, "Bearer other"})
	if req != fiber.StatusUnauthorized {
		t.Fatalf("two Authorization headers: status %d, want 401", req)
	}
	if strings.Contains(env.logs.String(), testIngestToken) || strings.Contains(env.logs.String(), "wrong-token") {
		t.Fatalf("a credential reached the log: %s", env.logs.String())
	}
	if n, _ := env.helper.DBManager.DB.BotSecurityAlert.Query().Count(t.Context()); n != 0 {
		t.Fatalf("%d alerts stored by unauthorized calls", n)
	}

	resp := env.post(t, body, "bearer "+testIngestToken)
	if resp.Status != fiber.StatusAccepted {
		t.Fatalf("lower-case scheme: status %d body %s, want 202", resp.Status, resp.Body)
	}
}

func doRequestMulti(t *testing.T, app *fiber.App, body string, authorizations []string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, IngestPath, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for _, value := range authorizations {
		req.Header.Add("Authorization", value)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestIngestRouteAbsentWhenUnconfigured(t *testing.T) {
	helper := newTestHelper(t)
	app := fiber.New()
	helper.Router = app
	RegisterIngestRoutes(helper, IngestRouteOptions{})
	resp := doRequest(t, app, http.MethodPost, IngestPath, cloudBody(t, sampleCloudPayload()), map[string]string{
		"Authorization": "Bearer " + testIngestToken,
		"Content-Type":  "application/json",
	})
	if resp.Status != fiber.StatusNotFound {
		t.Fatalf("status %d, want 404 without bot_security.ingest_token_sha256", resp.Status)
	}
	RegisterIngestRoutes(nil, IngestRouteOptions{Config: testIngestConfig(t)})
}

func TestIngestStoresCloudPayload(t *testing.T) {
	env := newIngestTestEnv(t)
	payload := sampleCloudPayload()
	resp := env.post(t, cloudBody(t, payload))
	if resp.Status != fiber.StatusAccepted {
		t.Fatalf("status %d body %s, want 202", resp.Status, resp.Body)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", resp.Header.Get("Cache-Control"))
	}
	var raw map[string]any
	if err := stdjson.Unmarshal(resp.Body, &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) != 3 || raw["accepted"] != true || raw["duplicate"] != false || !strings.HasPrefix(string(resp.Body), `{"accepted":true,"id":`) {
		t.Fatalf("response %s, want exactly accepted/id/duplicate", resp.Body)
	}
	got := decodeJSON[ingestResponse](t, resp.Body)

	row, err := env.helper.DBManager.DB.BotSecurityAlert.Get(t.Context(), got.ID)
	if err != nil {
		t.Fatalf("stored alert: %v", err)
	}
	alertTime, _ := time.Parse(time.RFC3339, payload.Time)
	if row.Kind != payload.Kind || row.BotID == nil || *row.BotID != payload.BotID || row.Subject != payload.BotID ||
		row.SourceIP != payload.SourceIP || row.BuildID != payload.BuildID || row.ClientVersion != payload.ClientVersion ||
		row.Reason != payload.Reason || !row.Enforced || row.Count != 5 || row.Threshold != 5 || row.WindowSeconds != 600 ||
		row.Node != payload.Node || !row.AlertTime.Equal(alertTime) || !row.ReceivedAt.Equal(testNow) ||
		row.Status != botsecurityalert.StatusOpen || row.Note != "" || row.HandledByUserID != nil || row.HandledAt != nil {
		t.Fatalf("stored row %+v does not match the payload %+v", row, payload)
	}
}

func TestIngestSubjectWithoutBotID(t *testing.T) {
	env := newIngestTestEnv(t)

	payload := sampleCloudPayload()
	payload.BotID = ""
	payload.Kind = "rate_limited"
	resp := env.post(t, cloudBody(t, payload))
	if resp.Status != fiber.StatusAccepted {
		t.Fatalf("status %d body %s", resp.Status, resp.Body)
	}
	row, err := env.helper.DBManager.DB.BotSecurityAlert.Get(t.Context(), decodeJSON[ingestResponse](t, resp.Body).ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.BotID != nil || row.Subject != payload.SourceIP {
		t.Fatalf("bot_id %v subject %q, want nil and the source IP", row.BotID, row.Subject)
	}

	// Neither bot nor source: Cloud counts it as "global"; node omitted.
	payload = cloudAlertPayload{Kind: "policy_unavailable", Count: 5, Threshold: 5, WindowSeconds: 600, Time: payload.Time}
	resp = env.post(t, cloudBody(t, payload))
	if resp.Status != fiber.StatusAccepted {
		t.Fatalf("status %d body %s", resp.Status, resp.Body)
	}
	row, err = env.helper.DBManager.DB.BotSecurityAlert.Get(t.Context(), decodeJSON[ingestResponse](t, resp.Body).ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Subject != "global" || row.Node != "" || row.SourceIP != "" {
		t.Fatalf("subject %q node %q source %q, want global and empty", row.Subject, row.Node, row.SourceIP)
	}
}

func TestIngestDeduplicates(t *testing.T) {
	env := newIngestTestEnv(t)
	payload := sampleCloudPayload()
	first := decodeJSON[ingestResponse](t, env.post(t, cloudBody(t, payload)).Body)
	if !first.Accepted || first.Duplicate || first.ID <= 0 {
		t.Fatalf("first = %+v", first)
	}

	// The same alert again, even with other informational fields, is a replay.
	replay := payload
	replay.Reason = "different text"
	replay.Count = 6
	resp := env.post(t, cloudBody(t, replay))
	second := decodeJSON[ingestResponse](t, resp.Body)
	if resp.Status != fiber.StatusAccepted || !second.Accepted || !second.Duplicate || second.ID != first.ID {
		t.Fatalf("replay status %d = %+v, want 202 duplicate of %d", resp.Status, second, first.ID)
	}
	// The same instant written with another offset is the same alert.
	offset := payload
	alertTime, _ := time.Parse(time.RFC3339, payload.Time)
	offset.Time = alertTime.In(time.FixedZone("CST", 8*3600)).Format(time.RFC3339)
	if got := decodeJSON[ingestResponse](t, env.post(t, cloudBody(t, offset)).Body); !got.Duplicate || got.ID != first.ID {
		t.Fatalf("offset replay = %+v, want duplicate of %d", got, first.ID)
	}

	// Any change to kind, subject, node or time is a new alert.
	variants := []func(p *cloudAlertPayload){
		func(p *cloudAlertPayload) { p.Kind = "replay_detected" },
		func(p *cloudAlertPayload) { p.BotID = "30042043" },
		func(p *cloudAlertPayload) { p.Node = "node-b" },
		func(p *cloudAlertPayload) { p.Time = alertTime.Add(time.Second).Format(time.RFC3339) },
	}
	ids := map[int]bool{first.ID: true}
	for i, mutate := range variants {
		variant := payload
		mutate(&variant)
		got := decodeJSON[ingestResponse](t, env.post(t, cloudBody(t, variant)).Body)
		if got.Duplicate || ids[got.ID] {
			t.Fatalf("variant %d = %+v, want a new alert", i, got)
		}
		ids[got.ID] = true
	}
	if n, _ := env.helper.DBManager.DB.BotSecurityAlert.Query().Count(t.Context()); n != 1+len(variants) {
		t.Fatalf("stored %d alerts, want %d", n, 1+len(variants))
	}
}

func TestIngestValidation(t *testing.T) {
	env := newIngestTestEnv(t)
	valid := sampleCloudPayload()
	encode := func(mutate func(m map[string]any)) string {
		var m map[string]any
		if err := stdjson.Unmarshal([]byte(cloudBody(t, valid)), &m); err != nil {
			t.Fatal(err)
		}
		mutate(m)
		data, _ := stdjson.Marshal(m)
		return string(data)
	}

	cases := []struct {
		name string
		body string
	}{
		{"empty body", ""},
		{"not json", "kind=auth_failed"},
		{"array", "[]"},
		{"two values", cloudBody(t, valid) + "{}"},
		{"duplicate member", `{"kind":"auth_failed","kind":"x","time":"` + valid.Time + `"}`},
		{"missing kind", encode(func(m map[string]any) { delete(m, "kind") })},
		{"upper-case kind", encode(func(m map[string]any) { m["kind"] = "Auth_Failed" })},
		{"kind with digits", encode(func(m map[string]any) { m["kind"] = "auth2" })},
		{"long kind", encode(func(m map[string]any) { m["kind"] = strings.Repeat("a", 65) })},
		{"kind wrong type", encode(func(m map[string]any) { m["kind"] = 1 })},
		{"long bot_id", encode(func(m map[string]any) { m["bot_id"] = strings.Repeat("1", 65) })},
		{"bot_id with space", encode(func(m map[string]any) { m["bot_id"] = "1 2" })},
		{"long source_ip", encode(func(m map[string]any) { m["source_ip"] = strings.Repeat("1", 65) })},
		{"long node", encode(func(m map[string]any) { m["node"] = strings.Repeat("n", 65) })},
		{"node with NUL", encode(func(m map[string]any) { m["node"] = "node\x00a" })},
		{"negative count", encode(func(m map[string]any) { m["count"] = -1 })},
		{"fractional count", encode(func(m map[string]any) { m["count"] = 1.5 })},
		{"count as string", encode(func(m map[string]any) { m["count"] = "5" })},
		{"enforced as string", encode(func(m map[string]any) { m["enforced"] = "true" })},
		{"missing time", encode(func(m map[string]any) { delete(m, "time") })},
		{"unix time", encode(func(m map[string]any) { m["time"] = 1790000000 })},
		{"non RFC 3339 time", encode(func(m map[string]any) { m["time"] = "2026-10-08 11:59:00" })},
		{"far future time", encode(func(m map[string]any) { m["time"] = testNow.Add(11 * time.Minute).Format(time.RFC3339) })},
		{"ancient time", encode(func(m map[string]any) { m["time"] = "1970-01-01T00:00:00Z" })},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := env.post(t, tc.body)
			if resp.Status != fiber.StatusBadRequest {
				t.Fatalf("status %d body %s, want 400", resp.Status, resp.Body)
			}
			got := decodeJSON[map[string]string](t, resp.Body)
			if got["error"] != "invalid_request" || got["detail"] == "" {
				t.Fatalf("body %s, want invalid_request with a detail", resp.Body)
			}
		})
	}
	if n, _ := env.helper.DBManager.DB.BotSecurityAlert.Query().Count(t.Context()); n != 0 {
		t.Fatalf("%d invalid alerts stored", n)
	}

	big := valid
	big.Reason = strings.Repeat("x", maxIngestBodyBytes)
	if resp := env.post(t, cloudBody(t, big)); resp.Status != fiber.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: status %d, want 413", resp.Status)
	}

	// Slight clock skew is fine; unknown kinds and fields are kept.
	skewed := valid
	skewed.Kind = "brand_new_kind"
	skewed.Time = testNow.Add(5 * time.Minute).Format(time.RFC3339)
	body := strings.TrimSuffix(cloudBody(t, skewed), "}") + `,"future_field":{"x":1}}`
	if resp := env.post(t, body); resp.Status != fiber.StatusAccepted {
		t.Fatalf("unknown kind/field: status %d body %s, want 202", resp.Status, resp.Body)
	}
}

func TestIngestBoundsClientText(t *testing.T) {
	env := newIngestTestEnv(t)
	payload := sampleCloudPayload()
	payload.BuildID = strings.Repeat("b", 200)
	payload.ClientVersion = "v1\x00\x07" + strings.Repeat("é", 100)
	payload.Reason = strings.Repeat("理", 500)
	resp := env.post(t, cloudBody(t, payload))
	if resp.Status != fiber.StatusAccepted {
		t.Fatalf("status %d body %s, want 202 (client text is truncated, not rejected)", resp.Status, resp.Body)
	}
	row, err := env.helper.DBManager.DB.BotSecurityAlert.Get(t.Context(), decodeJSON[ingestResponse](t, resp.Body).ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(row.BuildID) != maxBuildIDBytes {
		t.Fatalf("build_id length %d, want %d", len(row.BuildID), maxBuildIDBytes)
	}
	if strings.ContainsRune(row.ClientVersion, 0) || !strings.HasPrefix(row.ClientVersion, "v1��") ||
		len(row.ClientVersion) > maxClientVersionBytes || !utf8.ValidString(row.ClientVersion) {
		t.Fatalf("client_version %q not sanitized", row.ClientVersion)
	}
	if len(row.Reason) > maxReasonBytes || !utf8.ValidString(row.Reason) || len(row.Reason) < maxReasonBytes-3 {
		t.Fatalf("reason length %d valid %v, want a rune-safe cut at %d bytes", len(row.Reason), utf8.ValidString(row.Reason), maxReasonBytes)
	}
}

func TestIngestRateLimit(t *testing.T) {
	env := newIngestTestEnv(t)
	srv := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: srv.Addr(), MaxRetries: -1, DialTimeout: 200 * time.Millisecond})
	t.Cleanup(func() { _ = client.Close() })
	env.helper.DBManager.Redis = &harukiRedis.HarukiRedisManager{Redis: client}

	window := int64(ingestRateLimitWindow / time.Second)
	key := harukiRedis.BuildBotSecurityIngestRateLimitKey(testNow.Unix()/window, "0.0.0.0")
	srv.Set(key, fmt.Sprint(ingestRateLimitPerIP-1))

	if resp := env.post(t, cloudBody(t, sampleCloudPayload())); resp.Status != fiber.StatusAccepted {
		t.Fatalf("last allowed alert: status %d body %s", resp.Status, resp.Body)
	}
	resp := env.post(t, cloudBody(t, sampleCloudPayload()))
	if resp.Status != fiber.StatusTooManyRequests || strings.TrimSpace(string(resp.Body)) != `{"error":"rate_limited"}` || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("over the limit: status %d body %s retry %q, want 429", resp.Status, resp.Body, resp.Header.Get("Retry-After"))
	}
	// Unauthenticated calls do not consume the caller's budget.
	before, _ := srv.Get(key)
	env.post(t, "{}", "Bearer wrong")
	if after, _ := srv.Get(key); after != before {
		t.Fatalf("unauthorized call counted: %s -> %s", before, after)
	}

	// Redis down: fail open.
	srv.Close()
	if resp := env.post(t, cloudBody(t, sampleCloudPayload())); resp.Status != fiber.StatusAccepted {
		t.Fatalf("redis down: status %d body %s, want 202", resp.Status, resp.Body)
	}
}

func TestIngestStoreUnavailable(t *testing.T) {
	env := newIngestTestEnv(t)
	_ = env.helper.DBManager.DB.Close()
	resp := env.post(t, cloudBody(t, sampleCloudPayload()))
	if resp.Status != fiber.StatusServiceUnavailable || !strings.Contains(string(resp.Body), "temporarily_unavailable") {
		t.Fatalf("status %d body %s, want 503", resp.Status, resp.Body)
	}
	env.helper.DBManager.DB = nil
	if resp := env.post(t, cloudBody(t, sampleCloudPayload())); resp.Status != fiber.StatusServiceUnavailable {
		t.Fatalf("no DB: status %d, want 503", resp.Status)
	}
}
