package adminwebhook

import (
	"crypto/rand"
	"encoding/hex"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/webhookendpoint"
	"strconv"
	"strings"
	"time"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

const (
	adminWebhookTargetType       = "webhook"
	adminWebhookSettingsTargetID = "settings"
	adminWebhookTargetIDAll      = "all"
)

const (
	adminWebhookActionList           = "admin.webhook.list"
	adminWebhookActionCreate         = "admin.webhook.create"
	adminWebhookActionUpdate         = "admin.webhook.update"
	adminWebhookActionDelete         = "admin.webhook.delete"
	adminWebhookActionSubscribers    = "admin.webhook.subscribers.list"
	adminWebhookActionGetSettings    = "admin.webhook.settings.get"
	adminWebhookActionUpdateSettings = "admin.webhook.settings.update"
)

const (
	adminWebhookFailureReasonInvalidRequestPayload      = "invalid_request_payload"
	adminWebhookFailureReasonInvalidWebhookID           = "invalid_webhook_id"
	adminWebhookFailureReasonInvalidCallbackURL         = "invalid_callback_url"
	adminWebhookFailureReasonInvalidCredential          = "invalid_credential"
	adminWebhookFailureReasonWebhookConflict            = "webhook_conflict"
	adminWebhookFailureReasonWebhookNotFound            = "webhook_not_found"
	adminWebhookFailureReasonQueryWebhooksFailed        = "query_webhooks_failed"
	adminWebhookFailureReasonQuerySubscribersFailed     = "query_webhook_subscribers_failed"
	adminWebhookFailureReasonCreateWebhookFailed        = "create_webhook_failed"
	adminWebhookFailureReasonUpdateWebhookFailed        = "update_webhook_failed"
	adminWebhookFailureReasonDeleteWebhookFailed        = "delete_webhook_failed"
	adminWebhookFailureReasonResolveNextWebhookIDFailed = "resolve_next_webhook_id_failed"
	adminWebhookFailureReasonGenerateCredentialFailed   = "generate_credential_failed"
	adminWebhookFailureReasonPersistRuntimeConfigFailed = "persist_runtime_config_failed"
	adminWebhookFailureReasonInvalidWebhookJWTSecret    = "invalid_webhook_jwt_secret"
)

var adminWebhookNow = time.Now

func adminWebhookNowUTC() time.Time {
	return adminWebhookNow().UTC()
}

func sanitizeWebhookID(raw string) (string, error) {
	webhookID := strings.TrimSpace(raw)
	if webhookID == "" {
		return "", fiber.NewError(fiber.StatusBadRequest, "webhook id is required")
	}
	if strings.ContainsAny(webhookID, " \t\r\n/") {
		return "", fiber.NewError(fiber.StatusBadRequest, "webhook id contains invalid characters")
	}
	return webhookID, nil
}

func sanitizeWebhookCredential(raw string) (string, error) {
	credential := strings.TrimSpace(raw)
	if credential == "" {
		return "", fiber.NewError(fiber.StatusBadRequest, "credential is required")
	}
	return credential, nil
}

func sanitizeOptionalBearer(raw *string) *string {
	if raw == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func sanitizeWebhookJWTSecret(raw *string) (*string, error) {
	if raw == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil, fiber.NewError(fiber.StatusBadRequest, "jwtSecret cannot be empty")
	}
	return &trimmed, nil
}

func generateWebhookCredential() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func resolveNextWebhookID(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, c fiber.Ctx) (string, error) {
	rows, err := apiHelper.DBManager.DB.WebhookEndpoint.Query().
		Select(webhookendpoint.FieldID).
		Order(webhookendpoint.ByID(sql.OrderAsc())).
		All(c.Context())
	if err != nil {
		return "", err
	}

	maxID := 0
	for _, row := range rows {
		if row == nil {
			continue
		}
		parsed, parseErr := strconv.Atoi(strings.TrimSpace(row.ID))
		if parseErr != nil {
			continue
		}
		if parsed > maxID {
			maxID = parsed
		}
	}
	return strconv.Itoa(maxID + 1), nil
}

func queryWebhookEndpointOrNotFound(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, c fiber.Ctx, webhookID string) (*postgresql.WebhookEndpoint, error) {
	endpoint, err := apiHelper.DBManager.DB.WebhookEndpoint.Query().
		Where(webhookendpoint.IDEQ(webhookID)).
		WithSubscriptions().
		Only(c.Context())
	if postgresql.IsNotFound(err) {
		return nil, fiber.NewError(fiber.StatusNotFound, "webhook not found")
	}
	return endpoint, err
}
