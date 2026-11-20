package postgresql_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	dbManager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/webhooksubscription"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	_ "github.com/mattn/go-sqlite3"
)

func TestWebhookPostgresSubscriptionLocksEndpointFirst(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	client := dbManager.NewClient(dbManager.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer client.Close()
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT "webhook_endpoints"\."id" FROM "webhook_endpoints" WHERE .* FOR UPDATE`).
		WithArgs("gone").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectRollback()
	if err := client.AddWebhookPushUser(t.Context(), "123", "jp", "suite", "gone"); !dbManager.IsNotFound(err) {
		t.Fatalf("expected missing locked endpoint: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

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

	subscribers, _, err := client.GetWebhookSubscribers(context.Background(), "507f1f77bcf86cd799439011", 0, dbManager.DefaultWebhookSubscriberPage)
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

func TestOAuth2ClientWebhookEndpointRoundTrip(t *testing.T) {
	t.Parallel()

	client := enttest.Open(t, "sqlite3", "file:oauth2-webhook-ops-test?mode=memory&cache=shared&_fk=1")
	defer func() {
		_ = client.Close()
	}()

	if _, err := client.User.Create().
		SetID("u1").
		SetName("User One").
		SetEmail("u1@example.com").
		Save(context.Background()); err != nil {
		t.Fatalf("create user returned error: %v", err)
	}
	if _, err := client.GameAccountBinding.Create().
		SetServer("jp").
		SetGameUserID("123").
		SetVerified(true).
		SetUserID("u1").
		Save(context.Background()); err != nil {
		t.Fatalf("create binding returned error: %v", err)
	}

	bearer := "oauth-token"
	if _, err := client.OAuth2ClientWebhookEndpoint.Create().
		SetID("wh-1").
		SetClientID("client-a").
		SetCallbackURL("https://example.com/oauth-webhook/{user_id}").
		SetBearer(bearer).
		Save(context.Background()); err != nil {
		t.Fatalf("create oauth2 webhook returned error: %v", err)
	}
	if _, err := client.OAuth2ClientWebhookEndpoint.Create().
		SetID("wh-2").
		SetClientID("client-b").
		SetCallbackURL("https://example.com/disabled").
		SetEnabled(false).
		Save(context.Background()); err != nil {
		t.Fatalf("create disabled oauth2 webhook returned error: %v", err)
	}

	owner, err := client.GetOAuth2WebhookOwnerForGameAccount(context.Background(), 123, "jp")
	if err != nil {
		t.Fatalf("GetOAuth2WebhookOwnerForGameAccount returned error: %v", err)
	}
	if owner == nil || owner.UserID != "u1" || owner.Banned {
		t.Fatalf("unexpected owner: %+v", owner)
	}

	callbacks, err := client.GetOAuth2ClientWebhookCallbacks(context.Background(), []string{"client-a", "client-a", "client-b"})
	if err != nil {
		t.Fatalf("GetOAuth2ClientWebhookCallbacks returned error: %v", err)
	}
	if len(callbacks) != 1 {
		t.Fatalf("len(callbacks) = %d, want 1", len(callbacks))
	}
	if callbacks[0].ClientID != "client-a" {
		t.Fatalf("client id = %q, want client-a", callbacks[0].ClientID)
	}
	if callbacks[0].CallbackURL != "https://example.com/oauth-webhook/{user_id}" {
		t.Fatalf("callback url = %q", callbacks[0].CallbackURL)
	}
	if callbacks[0].Bearer != "oauth-token" {
		t.Fatalf("bearer = %q, want oauth-token", callbacks[0].Bearer)
	}
}

func TestWebhookSubscriptionQuotaIsAtomicAndAllowsRepair(t *testing.T) {
	db := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer db.Close()
	ctx := t.Context()
	const endpoint = "quota-fixture"
	db.WebhookEndpoint.Create().SetID(endpoint).SetCredential("fixture").SetCallbackURL("https://example.test/").ExecX(ctx)
	legacy := db.WebhookSubscription.Create().SetWebhookID(endpoint).SetUserID("+00001").SetServer("jp").SetDataType("suite").SaveX(ctx)
	for from := 2; from < dbManager.MaxWebhookSubscriptions; from += 100 {
		var builders []*dbManager.WebhookSubscriptionCreate
		for id := from; id < min(from+100, dbManager.MaxWebhookSubscriptions); id++ {
			builders = append(builders, db.WebhookSubscription.Create().SetWebhookID(endpoint).SetUserID(strconv.Itoa(id)).SetServer("jp").SetDataType("suite"))
		}
		db.WebhookSubscription.CreateBulk(builders...).ExecX(ctx)
	}
	results := make(chan error, 8)
	for i := range cap(results) {
		go func() { results <- db.AddWebhookPushUser(ctx, strconv.Itoa(9000+i), "jp", "suite", endpoint) }()
	}
	accepted := 0
	for range cap(results) {
		if err := <-results; err == nil {
			accepted++
		} else if !errors.Is(err, dbManager.ErrWebhookSubscriptionLimit) {
			t.Error(err)
		}
	}
	if total := db.WebhookSubscription.Query().Where(webhooksubscription.WebhookIDEQ(endpoint)).CountX(ctx); accepted != 1 || total != dbManager.MaxWebhookSubscriptions {
		t.Fatalf("concurrent subscriptions accepted=%d total=%d", accepted, total)
	}
	if err := db.AddWebhookPushUser(ctx, "00001", "jp", "suite", endpoint); err != nil {
		t.Fatalf("same subscription must remain idempotent at capacity: %v", err)
	}
	repaired := db.WebhookSubscription.GetX(ctx, legacy.ID)
	if repaired.UserID != "1" || !repaired.CreatedAt.Equal(legacy.CreatedAt) {
		t.Fatal("legacy retry did not canonicalize the existing subscription")
	}
	if total := db.WebhookSubscription.Query().Where(webhooksubscription.WebhookIDEQ(endpoint)).CountX(ctx); total != dbManager.MaxWebhookSubscriptions {
		t.Fatalf("legacy retry changed the subscription count to %d", total)
	}
	if callbacks, err := db.GetWebhookPushAPI(ctx, 1, "jp", "suite"); err != nil || len(callbacks) != 1 {
		t.Fatalf("repaired subscription is unavailable to delivery: %v, %v", callbacks, err)
	}
	if err := db.RemoveWebhookPushUser(ctx, "00001", "jp", "suite", endpoint); err != nil {
		t.Fatal(err)
	}
	if err := db.AddWebhookPushUser(ctx, "10000", "jp", "suite", endpoint); err != nil {
		t.Fatalf("deletion did not release a slot: %v", err)
	}
}
