package user

import (
	"context"
	"fmt"
	"haruki-suite/config"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/cloudflare"
	"haruki-suite/utils/database/postgresql/user"
	"haruki-suite/utils/smtp"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

func registerResetPasswordRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	a := apiHelper.Router.Group("/api/user")

	a.Post("/reset-password/send", func(c *fiber.Ctx) error {
		var payload harukiAPIHelper.SendResetPasswordPayload
		if err := c.BodyParser(&payload); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Invalid payload", nil)
		}

		xForwardedFor := c.Get("X-Forwarded-For")
		clientIP := ""
		if xForwardedFor != "" {
			parts := strings.Split(xForwardedFor, ",")
			clientIP = strings.TrimSpace(parts[0])
		}

		resp, err := cloudflare.ValidateTurnstile(payload.ChallengeToken, clientIP)
		if err != nil || !resp.Success {
			return harukiAPIHelper.UpdatedDataResponse[string](c, 400, "captcha verify failed", nil)
		}

		resetSecret := uuid.NewString()
		resetURL := fmt.Sprintf("%s/user/reset-password/%s?email=%s", config.Cfg.UserSystem.FrontendURL, resetSecret, payload.Email)
		key := "resetpw:" + payload.Email
		ctx := context.Background()
		if err := apiHelper.DBManager.Redis.SetCache(ctx, key, resetSecret, 30*time.Minute); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "Failed to store secret", nil)
		}

		body := strings.ReplaceAll(smtp.ResetPasswordTemplate, "{{LINK}}", resetURL)
		if err := apiHelper.SMTPClient.Send([]string{payload.Email}, "您的重设密码请求 | Haruki工具箱", body, "Haruki工具箱 | 星云科技"); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to send email", nil)
		}

		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusOK, "Reset password email sent", nil)
	})

	a.Post("/reset-password", func(c *fiber.Ctx) error {
		var payload harukiAPIHelper.ResetPasswordPayload
		if err := c.BodyParser(&payload); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Invalid payload", nil)
		}
		key := "resetpw:" + payload.Email
		ctx := context.Background()
		var secret string
		found, err := apiHelper.DBManager.Redis.GetCache(ctx, key, &secret)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "Failed to retrieve secret", nil)
		}
		if !found {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "Reset code expired or invalid", nil)
		}
		if secret != payload.OneTimeSecret {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "Incorrect reset code", nil)
		}

		hashed, err := bcrypt.GenerateFromPassword([]byte(payload.Password), bcrypt.DefaultCost)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "Failed to hash password", nil)
		}

		_, err = apiHelper.DBManager.DB.User.
			Update().
			Where(user.EmailEQ(payload.Email)).
			SetPasswordHash(string(hashed)).
			Save(ctx)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "Failed to update password", nil)
		}

		apiHelper.DBManager.Redis.DeleteCache(ctx, key)
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusOK, "Password reset successfully", nil)
	})
}
