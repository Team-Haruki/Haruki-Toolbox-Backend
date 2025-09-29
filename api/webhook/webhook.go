package webhook

import (
	harukiRootApi "haruki-suite/api"
	harukiUtils "haruki-suite/utils"
	harukiMongo "haruki-suite/utils/database/mongo"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
)

func ValidateWebhookUser(secretKey string, manager *harukiMongo.MongoDBManager) fiber.Handler {
	return func(c *fiber.Ctx) error {
		jwtToken := c.Get("X-Haruki-Suite-Webhook-Token")
		if jwtToken == "" {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: "Missing X-Haruki-Suite-Webhook-Token header",
				Status:  harukiRootApi.IntPtr(fiber.StatusUnauthorized),
			})
		}

		token, err := jwt.Parse(jwtToken, func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fiber.ErrForbidden
			}
			return []byte(secretKey), nil
		})
		if err != nil || !token.Valid {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: "Invalid or expired JWT",
				Status:  harukiRootApi.IntPtr(fiber.StatusForbidden),
			})
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: "Invalid token claims",
				Status:  harukiRootApi.IntPtr(fiber.StatusForbidden),
			})
		}

		_id, okID := claims["_id"].(string)
		credential, okCred := claims["credential"].(string)
		if !okID || !okCred {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: "Invalid token payload",
				Status:  harukiRootApi.IntPtr(fiber.StatusForbidden),
			})
		}

		user, err := manager.GetWebhookUser(c.Context(), _id, credential)
		if err != nil || user == nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: "Webhook user not found or credential mismatch",
				Status:  harukiRootApi.IntPtr(fiber.StatusForbidden),
			})
		}

		c.Locals("webhook_id", _id)
		return c.Next()
	}
}

func RegisterRoutes(app *fiber.App, manager *harukiMongo.MongoDBManager, secret string) {
	api := app.Group("/webhook")

	api.Get("/subscribers", ValidateWebhookUser(secret, manager), func(c *fiber.Ctx) error {
		webhookID := c.Locals("webhook_id").(string)
		users, err := manager.GetWebhookSubscribers(c.Context(), webhookID)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: "Failed to fetch subscribers",
				Status:  harukiRootApi.IntPtr(fiber.StatusInternalServerError),
			})
		}
		return c.JSON(users)
	})

	api.Put("/:server/:data_type/:user_id", ValidateWebhookUser(secret, manager), func(c *fiber.Ctx) error {
		userID := c.Params("user_id")
		webhookID := c.Locals("webhook_id").(string)

		serverStr := c.Params("server")
		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
				Message: err.Error(),
			})
		}

		dataTypeStr := c.Params("data_type")
		dataType, err := harukiUtils.ParseUploadDataType(dataTypeStr)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
				Message: err.Error(),
			})
		}

		err = manager.AddWebhookPushUser(c.Context(), userID, string(server), string(dataType), webhookID)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: "Failed to register webhook push user",
				Status:  harukiRootApi.IntPtr(fiber.StatusInternalServerError),
			})
		}
		return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{Message: "Registered webhook push user successfully."})
	})

	api.Delete("/:server/:data_type/:user_id", ValidateWebhookUser(secret, manager), func(c *fiber.Ctx) error {
		userID := c.Params("user_id")
		webhookID := c.Locals("webhook_id").(string)

		serverStr := c.Params("server")
		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
				Message: err.Error(),
			})
		}

		dataTypeStr := c.Params("data_type")
		dataType, err := harukiUtils.ParseUploadDataType(dataTypeStr)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
				Message: err.Error(),
			})
		}

		err = manager.RemoveWebhookPushUser(c.Context(), userID, string(server), string(dataType), webhookID)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: "Failed to unregister webhook push user",
				Status:  harukiRootApi.IntPtr(fiber.StatusInternalServerError),
			})
		}
		return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{Message: "Unregistered webhook push user successfully."})
	})
}
