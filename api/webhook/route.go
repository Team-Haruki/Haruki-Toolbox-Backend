package webhook

import (
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	harukiMongo "haruki-suite/utils/database/mongo"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
)

func ValidateWebhookUser(secretKey string, manager *harukiMongo.MongoDBManager) fiber.Handler {
	return func(c *fiber.Ctx) error {
		jwtToken := c.Get("X-Haruki-Suite-Webhook-Token")
		if jwtToken == "" {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "Missing X-Haruki-Suite-Webhook-Token header", nil)
		}

		token, err := jwt.Parse(jwtToken, func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fiber.ErrForbidden
			}
			return []byte(secretKey), nil
		})
		if err != nil || !token.Valid {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusForbidden, "Invalid or expired JWT", nil)
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusForbidden, "Invalid token claims", nil)
		}

		_id, okID := claims["_id"].(string)
		credential, okCred := claims["credential"].(string)
		if !okID || !okCred {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusForbidden, "Invalid token payload", nil)
		}

		user, err := manager.GetWebhookUser(c.Context(), _id, credential)
		if err != nil || user == nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusForbidden, "Webhook user not found or credential mismatch", nil)
		}

		c.Locals("webhook_id", _id)
		return c.Next()
	}
}

func RegisterWebhookRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	api := apiHelper.Router.Group("/webhook", ValidateWebhookUser(apiHelper.WebhookJWTSecret, apiHelper.DBManager.Mongo))

	api.Get("/subscribers", func(c *fiber.Ctx) error {
		webhookID := c.Locals("webhook_id").(string)
		users, err := apiHelper.DBManager.Mongo.GetWebhookSubscribers(c.Context(), webhookID)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, err.Error(), nil)
		}
		return harukiAPIHelper.ResponseWithStruct(c, fiber.StatusOK, &users)
	})

	api.Put("/:server/:data_type/:user_id", func(c *fiber.Ctx) error {
		userID := c.Params("user_id")
		webhookID := c.Locals("webhook_id").(string)

		serverStr := c.Params("server")
		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, err.Error(), nil)
		}

		dataTypeStr := c.Params("data_type")
		dataType, err := harukiUtils.ParseUploadDataType(dataTypeStr)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, err.Error(), nil)
		}

		err = apiHelper.DBManager.Mongo.AddWebhookPushUser(c.Context(), userID, string(server), string(dataType), webhookID)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, err.Error(), nil)
		}
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusOK, "Registered webhook push user successfully.", nil)
	})

	api.Delete("/:server/:data_type/:user_id", func(c *fiber.Ctx) error {
		userID := c.Params("user_id")
		webhookID := c.Locals("webhook_id").(string)

		serverStr := c.Params("server")
		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, err.Error(), nil)
		}

		dataTypeStr := c.Params("data_type")
		dataType, err := harukiUtils.ParseUploadDataType(dataTypeStr)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, err.Error(), nil)
		}

		err = apiHelper.DBManager.Mongo.RemoveWebhookPushUser(c.Context(), userID, string(server), string(dataType), webhookID)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, err.Error(), nil)
		}
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusOK, "Unregistered webhook push user successfully.", nil)
	})
}
