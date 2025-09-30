package user

import (
	"context"
	"fmt"
	"haruki-suite/utils/cloudflare"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/emailinfo"
	"haruki-suite/utils/smtp"
	"math/rand"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/redis/go-redis/v9"
)

func GenerateCode() string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return fmt.Sprintf("%06d", r.Intn(1000000))
}

func RegisterEmailRoutes(router fiber.Router, redisClient *redis.Client, smtpClient *smtp.SMTPClient, postgresClient *postgresql.Client) {
	email := router.Group("/api/email")

	email.Post("/send", func(c *fiber.Ctx) error {
		var req SendEmailPayload
		if err := c.BodyParser(&req); err != nil {
			return UpdatedDataResponse[string](c, 400, "invalid request body", nil)
		}

		xForwardedFor := c.Get("X-Forwarded-For")
		clientIP := ""
		if xForwardedFor != "" {
			parts := strings.Split(xForwardedFor, ",")
			clientIP = strings.TrimSpace(parts[0])
		}

		resp, err := cloudflare.ValidateTurnstile(req.ChallengeToken, clientIP)
		if err != nil || !resp.Success {
			return UpdatedDataResponse[string](c, 400, "captcha verify failed", nil)
		}

		code := GenerateCode()
		if err := redisClient.Set(context.Background(), "email:verify:"+req.Email, code, 5*time.Minute).Err(); err != nil {
			return UpdatedDataResponse[string](c, 500, "failed to save code", nil)
		}

		body := strings.ReplaceAll(smtp.VerificationCodeTemplate, "{{CODE}}", code)
		if err := smtpClient.Send([]string{req.Email}, "您的验证码 | Haruki工具箱", body, "Haruki工具箱 | 星云科技"); err != nil {
			return UpdatedDataResponse[string](c, 500, "failed to send email", nil)
		}

		return UpdatedDataResponse[string](c, 200, "verification code sent", nil)
	})

	email.Post("/verify", VerifySessionToken, func(c *fiber.Ctx) error {
		var req VerifyEmailPayload
		if err := c.BodyParser(&req); err != nil {
			return UpdatedDataResponse[string](c, 400, "invalid request body", nil)
		}

		code, err := redisClient.Get(context.Background(), "email:verify:"+req.Email).Result()
		if err != nil {
			return UpdatedDataResponse[string](c, 400, "verification code expired or not found", nil)
		}

		if req.OneTimePassword != code {
			return UpdatedDataResponse[string](c, 400, "invalid verification code", nil)
		}

		redisClient.Del(context.Background(), "email:verify:"+req.Email)

		ctx := context.Background()
		if _, err := postgresClient.EmailInfo.
			Update().
			Where(emailinfo.EmailEQ(req.Email)).
			SetVerified(true).
			Save(ctx); err != nil {
			return UpdatedDataResponse[string](c, 500, "failed to update database", nil)
		}

		ud := UserData{
			EmailInfo: EmailInfo{
				Email:    req.Email,
				Verified: true,
			},
		}
		return UpdatedDataResponse(c, 200, "email verified", &ud)
	})
}
