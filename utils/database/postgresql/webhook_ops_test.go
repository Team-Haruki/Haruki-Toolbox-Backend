package postgresql_test

import (
	"context"
	"testing"

	dbManager "haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/enttest"

	_ "github.com/mattn/go-sqlite3"
)

func TestWebhookEndpointAndSubscriptionRoundTrip(t *testing.T) {
	t.Parallel()

	client := enttest.Open(t, "sqlite3", "file:webhook-ops-test?mode=memory&cache=shared&_fk=1")
	defer func() {
		_ = client.Close()
	}()

	bearer := "token-a"
	if err := client.UpsertWebhookEndpoint(context.Background(), dbManager.WebhookEndpointRecord{
		ID:          "507f1f77bcf86cd799439011",
		Credential:  "cred-a",
		CallbackURL: "https://example.com/webhook",
		Bearer:      &bearer,
		Enabled:     true,
	}); err != nil {
		t.Fatalf("UpsertWebhookEndpoint returned error: %v", err)
	}

	record, err := client.GetWebhookUser(context.Background(), "507f1f77bcf86cd799439011", "cred-a")
	if err != nil {
		t.Fatalf("GetWebhookUser returned error: %v", err)
	}
	if record == nil {
		t.Fatalf("GetWebhookUser returned nil record")
	}
	if record.CallbackURL != "https://example.com/webhook" {
		t.Fatalf("callback_url = %q, want %q", record.CallbackURL, "https://example.com/webhook")
	}
	if record.Bearer == nil || *record.Bearer != "token-a" {
		t.Fatalf("bearer = %v, want token-a", record.Bearer)
	}

	if err := client.AddWebhookPushUser(context.Background(), "123", "jp", "suite", "507f1f77bcf86cd799439011"); err != nil {
		t.Fatalf("AddWebhookPushUser returned error: %v", err)
	}
	if err := client.AddWebhookPushUser(context.Background(), "123", "jp", "suite", "507f1f77bcf86cd799439011"); err != nil {
		t.Fatalf("AddWebhookPushUser duplicate returned error: %v", err)
	}

	callbacks, err := client.GetWebhookPushAPI(context.Background(), 123, "jp", "suite")
	if err != nil {
		t.Fatalf("GetWebhookPushAPI returned error: %v", err)
	}
	if len(callbacks) != 1 {
		t.Fatalf("len(callbacks) = %d, want 1", len(callbacks))
	}
	if callbacks[0].CallbackURL != "https://example.com/webhook" {
		t.Fatalf("callback url = %q, want %q", callbacks[0].CallbackURL, "https://example.com/webhook")
	}
	if callbacks[0].Bearer != "token-a" {
		t.Fatalf("callback bearer = %q, want %q", callbacks[0].Bearer, "token-a")
	}

	if err := client.UpsertWebhookEndpoint(context.Background(), dbManager.WebhookEndpointRecord{
		ID:          "2",
		Credential:  "cred-b",
		CallbackURL: "https://example.com/disabled",
		Enabled:     false,
	}); err != nil {
		t.Fatalf("UpsertWebhookEndpoint disabled returned error: %v", err)
	}
	if err := client.AddWebhookPushUser(context.Background(), "123", "jp", "suite", "2"); err != nil {
		t.Fatalf("AddWebhookPushUser for disabled endpoint returned error: %v", err)
	}
	callbacks, err = client.GetWebhookPushAPI(context.Background(), 123, "jp", "suite")
	if err != nil {
		t.Fatalf("GetWebhookPushAPI after disabled endpoint returned error: %v", err)
	}
	if len(callbacks) != 1 {
		t.Fatalf("len(callbacks) with disabled endpoint = %d, want 1", len(callbacks))
	}

	subscribers, err := client.GetWebhookSubscribers(context.Background(), "507f1f77bcf86cd799439011")
	if err != nil {
		t.Fatalf("GetWebhookSubscribers returned error: %v", err)
	}
	if len(subscribers) != 1 {
		t.Fatalf("len(subscribers) = %d, want 1", len(subscribers))
	}
	if subscribers[0].UID != "123" || subscribers[0].Server != "jp" || subscribers[0].Type != "suite" {
		t.Fatalf("unexpected subscriber payload: %+v", subscribers[0])
	}

	if err := client.RemoveWebhookPushUser(context.Background(), "123", "jp", "suite", "507f1f77bcf86cd799439011"); err != nil {
		t.Fatalf("RemoveWebhookPushUser returned error: %v", err)
	}

	callbacks, err = client.GetWebhookPushAPI(context.Background(), 123, "jp", "suite")
	if err != nil {
		t.Fatalf("GetWebhookPushAPI after delete returned error: %v", err)
	}
	if len(callbacks) != 0 {
		t.Fatalf("len(callbacks) after delete = %d, want 0", len(callbacks))
	}

	if err := client.DeleteAllWebhookData(context.Background()); err != nil {
		t.Fatalf("DeleteAllWebhookData returned error: %v", err)
	}

	record, err = client.GetWebhookUser(context.Background(), "507f1f77bcf86cd799439011", "cred-a")
	if err != nil {
		t.Fatalf("GetWebhookUser after delete returned error: %v", err)
	}
	if record != nil {
		t.Fatalf("expected webhook record to be deleted")
	}
}
