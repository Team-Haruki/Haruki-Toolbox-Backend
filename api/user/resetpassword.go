package user

import (
	"fmt"
	"haruki-suite/config"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/cloudflare"
	"haruki-suite/utils/database/postgresql/user"
	harukiRedis "haruki-suite/utils/database/redis"
	"haruki-suite/utils/smtp"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

func handleSendResetPassword(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		var payload harukiAPIHelper.SendResetPasswordPayload
		if err := c.Bind().Body(&payload); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "Invalid payload")
		}

		xForwardedFor := c.Get("X-Forwarded-For")
		clientIP := ""
		if xForwardedFor != "" {
			parts := strings.Split(xForwardedFor, ",")
			clientIP = strings.TrimSpace(parts[0])
		}

		resp, err := cloudflare.ValidateTurnstile(payload.ChallengeToken, clientIP)
		if err != nil || !resp.Success {
			return harukiAPIHelper.ErrorBadRequest(c, "captcha verify failed")
		}

		resetSecret := uuid.NewString()
		resetURL := fmt.Sprintf("%s/user/reset-password/%s?email=%s", config.Cfg.UserSystem.FrontendURL, resetSecret, payload.Email)
		redisKey := harukiRedis.BuildResetPasswordKey(payload.Email)
		if err := apiHelper.DBManager.Redis.SetCache(ctx, redisKey, resetSecret, 30*time.Minute); err != nil {
			return harukiAPIHelper.ErrorInternal(c, "Failed to store secret")
		}

		body := strings.ReplaceAll(smtp.ResetPasswordTemplate, "{{LINK}}", resetURL)
		if err := apiHelper.SMTPClient.Send([]string{payload.Email}, "您的重设密码请求 | Haruki工具箱", body, "Haruki工具箱 | 星云科技"); err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to send email")
		}

		return harukiAPIHelper.SuccessResponse[string](c, "Reset password email sent", nil)
	}
}

func handleResetPassword(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		var payload harukiAPIHelper.ResetPasswordPayload
		if err := c.Bind().Body(&payload); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "Invalid payload")
		}
		redisKey := harukiRedis.BuildResetPasswordKey(payload.Email)
		var secret string
		found, err := apiHelper.DBManager.Redis.GetCache(ctx, redisKey, &secret)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "Failed to retrieve secret")
		}
		if !found {
			return harukiAPIHelper.ErrorBadRequest(c, "Reset code expired or invalid")
		}
		if secret != payload.OneTimeSecret {
			return harukiAPIHelper.ErrorBadRequest(c, "Incorrect reset code")
		}

		u, err := apiHelper.DBManager.DB.User.
			Query().
			Where(user.EmailEQ(payload.Email)).
			Only(ctx)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "Failed to locate user")
		}

		hashed, err := bcrypt.GenerateFromPassword([]byte(payload.Password), bcrypt.DefaultCost)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "Failed to hash password")
		}

		_, err = apiHelper.DBManager.DB.User.
			Update().
			Where(user.EmailEQ(payload.Email)).
			SetPasswordHash(string(hashed)).
			Save(ctx)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "Failed to update password")
		}

		if err := harukiAPIHelper.ClearUserSessions(apiHelper.DBManager.Redis.Redis, u.ID); err != nil {
			return harukiAPIHelper.ErrorInternal(c, "Failed to clear user sessions")
		}

		_ = apiHelper.DBManager.Redis.DeleteCache(ctx, redisKey)
		return harukiAPIHelper.SuccessResponse[string](c, "Password reset successfully", nil)
	}
}

func registerResetPasswordRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	a := apiHelper.Router.Group("/api/user")

	a.Post("/reset-password/send", handleSendResetPassword(apiHelper))
	a.Post("/reset-password", handleResetPassword(apiHelper))
}
