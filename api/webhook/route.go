package webhook

import (
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	harukiMongo "haruki-suite/utils/database/mongo"
	harukiLogger "haruki-suite/utils/logger"

	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
)

func ValidateWebhookUser(secretKey string, manager *harukiMongo.MongoDBManager) fiber.Handler {
	return func(c fiber.Ctx) error {
		if secretKey == "" {
			harukiLogger.Errorf("Webhook secret key is not configured")
			return harukiAPIHelper.ErrorInternal(c, "Internal server error")
		}

		ctx := c.Context()
		jwtToken := c.Get("X-Haruki-Suite-Webhook-Token")
		if jwtToken == "" {
			return harukiAPIHelper.ErrorUnauthorized(c, "Missing X-Haruki-Suite-Webhook-Token header")
		}

		token, err := jwt.Parse(jwtToken, func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fiber.ErrForbidden
			}
			return []byte(secretKey), nil
		})
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

		user, err := manager.GetWebhookUser(ctx, _id, credential)
		if err != nil {
			harukiLogger.Errorf("Failed to get webhook user: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "Database error")
		}
		if user == nil {
			return harukiAPIHelper.ErrorForbidden(c, "Webhook user not found or credential mismatch")
		}

		c.Locals("webhook_id", _id)
		return c.Next()
	}
}

func handleGetSubscribers(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		webhookID := c.Locals("webhook_id").(string)
		users, err := apiHelper.DBManager.Mongo.GetWebhookSubscribers(ctx, webhookID)
		if err != nil {
			harukiLogger.Errorf("Failed to get subscribers for webhook %s: %v", webhookID, err)
			return harukiAPIHelper.ErrorInternal(c, "Failed to get subscribers")
		}
		return harukiAPIHelper.ResponseWithStruct(c, fiber.StatusOK, &users)
	}
}

func handlePutWebhookUser(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID := c.Params("user_id")
		webhookID := c.Locals("webhook_id").(string)

		serverStr := c.Params("server")
		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
		}

		dataTypeStr := c.Params("data_type")
		dataType, err := harukiUtils.ParseUploadDataType(dataTypeStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
		}

		err = apiHelper.DBManager.Mongo.AddWebhookPushUser(ctx, userID, string(server), string(dataType), webhookID)
		if err != nil {
			harukiLogger.Errorf("Failed to add webhook push user: %v", err)
			return harukiAPIHelper.ErrorInternal(c, err.Error())
		}
		return harukiAPIHelper.SuccessResponse[string](c, "Registered webhook push user successfully.", nil)
	}
}

func handleDeleteWebhookUser(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID := c.Params("user_id")
		webhookID := c.Locals("webhook_id").(string)

		serverStr := c.Params("server")
		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
		}

		dataTypeStr := c.Params("data_type")
		dataType, err := harukiUtils.ParseUploadDataType(dataTypeStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
		}

		err = apiHelper.DBManager.Mongo.RemoveWebhookPushUser(ctx, userID, string(server), string(dataType), webhookID)
		if err != nil {
			harukiLogger.Errorf("Failed to remove webhook push user: %v", err)
			return harukiAPIHelper.ErrorInternal(c, err.Error())
		}
		return harukiAPIHelper.SuccessResponse[string](c, "Unregistered webhook push user successfully.", nil)
	}
}

func RegisterWebhookRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	api := apiHelper.Router.Group("/webhook", ValidateWebhookUser(apiHelper.WebhookJWTSecret, apiHelper.DBManager.Mongo))

	api.Get("/subscribers", handleGetSubscribers(apiHelper))
	api.RouteChain("/:server/:data_type/:user_id").
		Put(handlePutWebhookUser(apiHelper)).
		Delete(handleDeleteWebhookUser(apiHelper))
}
