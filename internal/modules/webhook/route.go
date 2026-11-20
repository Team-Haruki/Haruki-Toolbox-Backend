package webhook

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/pagination"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
)

const webhookRegistrationPerMinute = 120

func ValidateWebhookUser(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		if apiHelper == nil {
			return harukiAPIHelper.ErrorInternal(c, "Internal server error")
		}
		jwtToken := c.Get(TokenHeaderName)
		if jwtToken == "" {
			return harukiAPIHelper.ErrorUnauthorized(c, "Missing "+TokenHeaderName+" header")
		}
		if len(jwtToken) > 8192 {
			return harukiAPIHelper.ErrorForbidden(c, "Invalid or expired JWT")
		}
		ctx := c.Context()
		configUnavailable := false
		token, err := jwt.Parse(jwtToken, func(t *jwt.Token) (any, error) {
			snapshot, err := apiHelper.CurrentRuntimeConfigForAuthentication(ctx)
			if err != nil || snapshot.WebhookJWTSecret == "" || config.IsExampleCredential(snapshot.WebhookJWTSecret) {
				configUnavailable = true
				return nil, fiber.ErrServiceUnavailable
			}
			return []byte(snapshot.WebhookJWTSecret), nil
		}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
		if configUnavailable {
			return harukiAPIHelper.ErrorInternal(c, "webhook authentication unavailable")
		}
		if err != nil || !token.Valid {
			return harukiAPIHelper.ErrorForbidden(c, "Invalid or expired JWT")
		}
		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			return harukiAPIHelper.ErrorForbidden(c, "Invalid token claims")
		}
		_id, okID := claims["_id"].(string)
		credential, okCred := claims["credential"].(string)
		if !okID || !okCred {
			return harukiAPIHelper.ErrorForbidden(c, "Invalid token payload")
		}
		if apiHelper.DBManager == nil || apiHelper.DBManager.DB == nil {
			harukiLogger.Errorf("Webhook PostgreSQL client is not initialized")
			return harukiAPIHelper.ErrorInternal(c, "Database error")
		}
		user, err := apiHelper.DBManager.DB.GetWebhookUser(ctx, _id, credential)
		if err != nil {
			harukiLogger.Errorf("Failed to get webhook user: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "Database error")
		}
		if user == nil {
			return harukiAPIHelper.ErrorForbidden(c, "Webhook user not found or credential mismatch")
		}
		c.Locals("webhook_id", user.ID)
		return c.Next()
	}
}

func handleGetSubscribers(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		webhookID, ok := resolveWebhookIDFromLocals(c)
		if !ok {
			return nil
		}
		beforeID, err := pagination.ParsePositiveInt(c.Query("before_id"), 0, "before_id")
		if err != nil {
			return err
		}
		pageSize, err := pagination.ParsePositiveInt(c.Query("page_size"), postgresql.DefaultWebhookSubscriberPage, "page_size")
		if err != nil {
			return err
		}
		if pageSize > postgresql.MaxWebhookSubscriberPage {
			return harukiAPIHelper.ErrorBadRequest(c, "page_size exceeds max allowed size")
		}
		users, nextBeforeID, err := apiHelper.DBManager.DB.GetWebhookSubscribers(ctx, webhookID, beforeID, pageSize)
		if err != nil {
			harukiLogger.Errorf("Failed to get subscribers for webhook %s: %v", webhookID, err)
			return harukiAPIHelper.ErrorInternal(c, "Failed to get subscribers")
		}
		if nextBeforeID != 0 {
			c.Set("X-Next-Before-ID", strconv.Itoa(nextBeforeID))
			c.Append(fiber.HeaderAccessControlExposeHeaders, "X-Next-Before-ID")
		}
		return harukiAPIHelper.Responses.ResponseWithStruct(c, fiber.StatusOK, &users)
	}
}

func handlePutWebhookUser(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		webhookID, ok := resolveWebhookIDFromLocals(c)
		if !ok {
			return nil
		}
		userID, err := postgresql.NormalizeWebhookGameUserID(c.Params("user_id"))
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
		}
		serverStr := c.Params("server")
		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid server")
		}
		dataTypeStr := c.Params("data_type")
		dataType, err := harukiUtils.ParseUploadDataType(dataTypeStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid data_type")
		}
		cache := apiHelper.DBManager.Redis
		if cache == nil || cache.Redis == nil {
			return c.SendStatus(fiber.StatusServiceUnavailable)
		}
		key := cache.KeyBuilder().BuildWebhookSubscriptionWriteKey(webhookID)
		count, err := cache.IncrementWithTTL(ctx, key, time.Minute)
		if err != nil {
			return c.SendStatus(fiber.StatusServiceUnavailable)
		}
		if count > webhookRegistrationPerMinute {
			c.Set("Retry-After", "60")
			return c.SendStatus(fiber.StatusTooManyRequests)
		}
		err = apiHelper.DBManager.DB.AddWebhookPushUser(ctx, userID, string(server), string(dataType), webhookID)
		if errors.Is(err, postgresql.ErrWebhookSubscriptionLimit) {
			return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, fiber.StatusConflict, err.Error(), nil)
		}
		if err != nil {
			harukiLogger.Errorf("Failed to add webhook push user: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to register webhook push user")
		}
		return harukiAPIHelper.Responses.SuccessResponse[string](c, "Registered webhook push user successfully.", nil)
	}
}

func handleDeleteWebhookUser(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID := c.Params("user_id")
		webhookID, ok := resolveWebhookIDFromLocals(c)
		if !ok {
			return nil
		}
		serverStr := c.Params("server")
		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid server")
		}
		dataTypeStr := c.Params("data_type")
		dataType, err := harukiUtils.ParseUploadDataType(dataTypeStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid data_type")
		}
		err = apiHelper.DBManager.DB.RemoveWebhookPushUser(ctx, userID, string(server), string(dataType), webhookID)
		if err != nil {
			harukiLogger.Errorf("Failed to remove webhook push user: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to unregister webhook push user")
		}
		return harukiAPIHelper.Responses.SuccessResponse[string](c, "Unregistered webhook push user successfully.", nil)
	}
}

func RegisterWebhookRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	for _, prefix := range []string{"/webhook", "/api/webhook"} {
		api := apiHelper.Router.Group(prefix, ValidateWebhookUser(apiHelper))

		api.Get("/subscribers", handleGetSubscribers(apiHelper))
		api.RouteChain("/:server/:data_type/:user_id").
			Put(handlePutWebhookUser(apiHelper)).
			Delete(handleDeleteWebhookUser(apiHelper))
	}
}

func resolveWebhookIDFromLocals(c fiber.Ctx) (string, bool) {
	webhookID, ok := c.Locals("webhook_id").(string)
	if !ok || strings.TrimSpace(webhookID) == "" {
		_ = harukiAPIHelper.ErrorUnauthorized(c, "invalid webhook session")
		return "", false
	}
	return webhookID, true
}
