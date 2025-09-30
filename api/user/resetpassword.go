package user

import (
	"context"
	"errors"
	"fmt"
	"haruki-suite/config"
	"haruki-suite/utils/cloudflare"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/user"
	"haruki-suite/utils/smtp"
	"net/http"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

func RegisterSendResetPasswordRoute(app *fiber.App, rdb *redis.Client, smtpClient *smtp.SMTPClient, postgresqlClient *postgresql.Client) {
	a := app.Group("/api/user")

	a.Post("/reset-password/send", func(c *fiber.Ctx) error {
		var payload SendResetPasswordPayload
		if err := c.BodyParser(&payload); err != nil {
			return UpdatedDataResponse[string](c, http.StatusBadRequest, "Invalid payload", nil)
		}

		xForwardedFor := c.Get("X-Forwarded-For")
		clientIP := ""
		if xForwardedFor != "" {
			parts := strings.Split(xForwardedFor, ",")
			clientIP = strings.TrimSpace(parts[0])
		}

		resp, err := cloudflare.ValidateTurnstile(payload.ChallengeToken, clientIP)
		if err != nil || !resp.Success {
			return UpdatedDataResponse[string](c, 400, "captcha verify failed", nil)
		}

		resetSecret := uuid.NewString()
		resetURL := fmt.Sprintf("%s/user/reset-password/%s?email=%s", config.Cfg.UserSystem.FrontendURL, resetSecret, payload.Email)
		key := "resetpw:" + payload.Email
		ctx := context.Background()
		if err := rdb.Set(ctx, key, resetSecret, 30*time.Minute).Err(); err != nil {
			return UpdatedDataResponse[string](c, http.StatusInternalServerError, "Failed to store secret", nil)
		}

		body := strings.ReplaceAll(smtp.ResetPasswordTemplate, "{{LINK}}", resetURL)
		if err := smtpClient.Send([]string{payload.Email}, "您的重设密码请求 | Haruki工具箱", body, "Haruki工具箱 | 星云科技"); err != nil {
			return UpdatedDataResponse[string](c, http.StatusInternalServerError, "failed to send email", nil)
		}

		return UpdatedDataResponse[string](c, http.StatusOK, "Reset password email sent", nil)
	})

	a.Post("/reset-password", func(c *fiber.Ctx) error {
		var payload ResetPasswordPayload
		if err := c.BodyParser(&payload); err != nil {
			return UpdatedDataResponse[string](c, http.StatusBadRequest, "Invalid payload", nil)
		}
		key := "resetpw:" + payload.Email
		ctx := context.Background()
		secret, err := rdb.Get(ctx, key).Result()
		if errors.Is(err, redis.Nil) {
			return UpdatedDataResponse[string](c, http.StatusUnauthorized, "Reset code expired or invalid", nil)
		} else if err != nil {
			return UpdatedDataResponse[string](c, http.StatusInternalServerError, "Failed to retrieve secret", nil)
		}
		if secret != payload.OneTimeSecret {
			return UpdatedDataResponse[string](c, http.StatusUnauthorized, "Incorrect reset code", nil)
		}

		hashed, err := bcrypt.GenerateFromPassword([]byte(payload.Password), bcrypt.DefaultCost)
		if err != nil {
			return UpdatedDataResponse[string](c, http.StatusInternalServerError, "Failed to hash password", nil)
		}

		_, err = postgresqlClient.User.
			Update().
			Where(user.EmailEQ(payload.Email)).
			SetPasswordHash(string(hashed)).
			Save(ctx)
		if err != nil {
			return UpdatedDataResponse[string](c, http.StatusInternalServerError, "Failed to update password", nil)
		}

		rdb.Del(ctx, key).Err()
		return UpdatedDataResponse[string](c, http.StatusOK, "Password reset successfully", nil)
	})
}
