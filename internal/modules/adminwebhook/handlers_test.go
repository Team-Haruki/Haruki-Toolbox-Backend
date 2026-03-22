package adminwebhook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	webhookModule "haruki-suite/internal/modules/webhook"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database"
	"haruki-suite/utils/database/postgresql/enttest"

	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"
)

func newAdminWebhookTestHelper(t *testing.T) *harukiAPIHelper.HarukiToolboxRouterHelpers {
	t.Helper()

	client := enttest.Open(t, "sqlite3", "file:admin-webhook-test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() {
		_ = client.Close()
	})

	return &harukiAPIHelper.HarukiToolboxRouterHelpers{
		DBManager:        &database.HarukiToolboxDBManager{DB: client},
		WebhookJWTSecret: "test-webhook-secret",
	}
}

func newAdminWebhookTestApp() *fiber.App {
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", "admin-1")
		c.Locals("userRole", "super_admin")
		return c.Next()
	})
	return app
}

func TestAdminWebhookSettingsHandlers(t *testing.T) {
	helper := newAdminWebhookTestHelper(t)
	app := newAdminWebhookTestApp()
	app.Get("/settings", handleGetAdminWebhookSettings(helper))
	app.Put("/settings", handleUpdateAdminWebhookSettings(helper))

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}

	req = httptest.NewRequest(http.MethodPut, "/settings", strings.NewReader(`{"enabled":false,"jwtSecret":"secret-1"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}
	if helper.GetWebhookEnabled() {
		t.Fatalf("webhook enabled should be false after update")
	}
	if helper.GetWebhookJWTSecret() != "secret-1" {
		t.Fatalf("webhook secret = %q, want %q", helper.GetWebhookJWTSecret(), "secret-1")
	}
}

func TestAdminWebhookCRUDHandlers(t *testing.T) {
	helper := newAdminWebhookTestHelper(t)
	app := newAdminWebhookTestApp()
	app.Get("/webhooks", handleListAdminWebhooks(helper))
	app.Post("/webhooks", handleCreateAdminWebhook(helper))
	app.Put("/webhooks/:webhook_id", handleUpdateAdminWebhook(helper))
	app.Delete("/webhooks/:webhook_id", handleDeleteAdminWebhook(helper))
	app.Get("/webhooks/:webhook_id/subscribers", handleListAdminWebhookSubscribers(helper))

	createReq := httptest.NewRequest(http.MethodPost, "/webhooks", strings.NewReader(`{"callbackUrl":"https://93.184.216.34/callback","bearer":"bearer-1"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createResp, err := app.Test(createReq)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if createResp.StatusCode != fiber.StatusOK {
		t.Fatalf("create status code = %d, want %d", createResp.StatusCode, fiber.StatusOK)
	}

	var createBody struct {
		UpdatedData adminWebhookMutationResponse `json:"updatedData"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&createBody); err != nil {
		t.Fatalf("decode create response returned error: %v", err)
	}
	if createBody.UpdatedData.Webhook.ID != "1" {
		t.Fatalf("created webhook id = %q, want %q", createBody.UpdatedData.Webhook.ID, "1")
	}
	if createBody.UpdatedData.Webhook.Credential == "" {
		t.Fatalf("expected generated credential")
	}
	if !createBody.UpdatedData.Webhook.Enabled {
		t.Fatalf("created webhook should default to enabled")
	}
	if createBody.UpdatedData.Token == "" {
		t.Fatalf("expected token to be returned on create")
	}
	if createBody.UpdatedData.TokenHeaderName != webhookModule.TokenHeaderName {
		t.Fatalf("token header name = %q, want %q", createBody.UpdatedData.TokenHeaderName, webhookModule.TokenHeaderName)
	}

	if err := helper.DBManager.DB.AddWebhookPushUser(context.Background(), "123", "jp", "suite", "1"); err != nil {
		t.Fatalf("AddWebhookPushUser returned error: %v", err)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/webhooks", nil)
	listResp, err := app.Test(listReq)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if listResp.StatusCode != fiber.StatusOK {
		t.Fatalf("list status code = %d, want %d", listResp.StatusCode, fiber.StatusOK)
	}

	subsReq := httptest.NewRequest(http.MethodGet, "/webhooks/1/subscribers", nil)
	subsResp, err := app.Test(subsReq)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if subsResp.StatusCode != fiber.StatusOK {
		t.Fatalf("subscribers status code = %d, want %d", subsResp.StatusCode, fiber.StatusOK)
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/webhooks/1", strings.NewReader(`{"callbackUrl":"https://93.184.216.34/updated","credential":"cred-2","enabled":false,"clearBearer":true}`))
	updateReq.Header.Set("Content-Type", "application/json")
	updateResp, err := app.Test(updateReq)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if updateResp.StatusCode != fiber.StatusOK {
		t.Fatalf("update status code = %d, want %d", updateResp.StatusCode, fiber.StatusOK)
	}
	var updateBody struct {
		UpdatedData adminWebhookMutationResponse `json:"updatedData"`
	}
	if err := json.NewDecoder(updateResp.Body).Decode(&updateBody); err != nil {
		t.Fatalf("decode update response returned error: %v", err)
	}
	if updateBody.UpdatedData.Token == "" {
		t.Fatalf("expected token to be returned on update")
	}

	row, err := helper.DBManager.DB.WebhookEndpoint.Get(context.Background(), "1")
	if err != nil {
		t.Fatalf("query webhook endpoint returned error: %v", err)
	}
	if row.Credential != "cred-2" {
		t.Fatalf("credential = %q, want %q", row.Credential, "cred-2")
	}
	if row.CallbackURL != "https://93.184.216.34/updated" {
		t.Fatalf("callback url = %q, want %q", row.CallbackURL, "https://93.184.216.34/updated")
	}
	if row.Enabled {
		t.Fatalf("expected webhook to be disabled after update")
	}
	if row.Bearer != nil {
		t.Fatalf("expected bearer to be cleared")
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/webhooks/1", nil)
	deleteResp, err := app.Test(deleteReq)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if deleteResp.StatusCode != fiber.StatusOK {
		t.Fatalf("delete status code = %d, want %d", deleteResp.StatusCode, fiber.StatusOK)
	}
}

func TestAdminWebhookCRUDHandlersPreserveCallbackPlaceholders(t *testing.T) {
	helper := newAdminWebhookTestHelper(t)
	app := newAdminWebhookTestApp()
	app.Post("/webhooks", handleCreateAdminWebhook(helper))
	app.Put("/webhooks/:webhook_id", handleUpdateAdminWebhook(helper))

	createReq := httptest.NewRequest(http.MethodPost, "/webhooks", strings.NewReader(`{"id":"placeholder-test","credential":"cred-1","callbackUrl":"https://93.184.216.34/callback/{server}/{data_type}/{user_id}"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createResp, err := app.Test(createReq)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if createResp.StatusCode != fiber.StatusOK {
		t.Fatalf("create status code = %d, want %d", createResp.StatusCode, fiber.StatusOK)
	}

	row, err := helper.DBManager.DB.WebhookEndpoint.Get(context.Background(), "placeholder-test")
	if err != nil {
		t.Fatalf("query created webhook endpoint returned error: %v", err)
	}
	if row.CallbackURL != "https://93.184.216.34/callback/{server}/{data_type}/{user_id}" {
		t.Fatalf("callback url = %q, want placeholders to be preserved", row.CallbackURL)
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/webhooks/placeholder-test", strings.NewReader(`{"callbackUrl":"https://93.184.216.34/updated/{server}/{data_type}/{user_id}"}`))
	updateReq.Header.Set("Content-Type", "application/json")
	updateResp, err := app.Test(updateReq)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if updateResp.StatusCode != fiber.StatusOK {
		t.Fatalf("update status code = %d, want %d", updateResp.StatusCode, fiber.StatusOK)
	}

	row, err = helper.DBManager.DB.WebhookEndpoint.Get(context.Background(), "placeholder-test")
	if err != nil {
		t.Fatalf("query updated webhook endpoint returned error: %v", err)
	}
	if row.CallbackURL != "https://93.184.216.34/updated/{server}/{data_type}/{user_id}" {
		t.Fatalf("callback url = %q, want placeholders to be preserved after update", row.CallbackURL)
	}
}
