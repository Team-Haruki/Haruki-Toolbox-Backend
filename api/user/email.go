package user

import (
	"context"
	"crypto/rand"
	"fmt"
	"haruki-suite/utils/cloudflare"
	"haruki-suite/utils/database/postgresql/emailinfo"
	"haruki-suite/utils/database/postgresql/user"
	"haruki-suite/utils/smtp"
	"math/big"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

func GenerateCode(antiCensor bool) string {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "000000"
	}
	code := fmt.Sprintf("%06d", n.Int64())
	if antiCensor {
		return strings.Join(strings.Split(code, ""), "/")
	}
	return code
}

func SendEmailHandler(c *fiber.Ctx, email, challengeToken string, helper HarukiToolboxUserRouterHelpers) error {
	xForwardedFor := c.Get("X-Forwarded-For")
	clientIP := ""
	if xForwardedFor != "" {
		parts := strings.Split(xForwardedFor, ",")
		clientIP = strings.TrimSpace(parts[0])
	}

	resp, err := cloudflare.ValidateTurnstile(challengeToken, clientIP)
	if err != nil || !resp.Success {
		return UpdatedDataResponse[string](c, fiber.StatusBadRequest, "captcha verify failed", nil)
	}

	code := GenerateCode(false)
	if err := helper.DBManager.Redis.SetCache(context.Background(), "email:verify:"+email, code, 5*time.Minute); err != nil {
		return UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to save code", nil)
	}

	body := strings.ReplaceAll(smtp.VerificationCodeTemplate, "{{CODE}}", code)
	if err := helper.SMTPClient.Send([]string{email}, "您的验证码 | Haruki工具箱", body, "Haruki工具箱 | 星云科技"); err != nil {
		return UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to send email", nil)
	}

	return UpdatedDataResponse[string](c, fiber.StatusOK, "verification code sent", nil)
}

func VerifyEmailHandler(c *fiber.Ctx, email, oneTimePassword string, helper HarukiToolboxUserRouterHelpers) error {
	ctx := context.Background()
	var code string
	found, err := helper.DBManager.Redis.GetCache(ctx, "email:verify:"+email, &code)
	if err != nil || !found {
		return UpdatedDataResponse[string](c, fiber.StatusBadRequest, "verification code expired or not found", nil)
	}

	if oneTimePassword != code {
		return UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid verification code", nil)
	}

	helper.DBManager.Redis.DeleteCache(ctx, "email:verify:"+email)

	return nil
}

func RegisterEmailRoutes(helper HarukiToolboxUserRouterHelpers) {
	email := helper.Router.Group("/api/email")

	email.Post("/send", func(c *fiber.Ctx) error {
		var req SendEmailPayload
		if err := c.BodyParser(&req); err != nil {
			return UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid request body", nil)
		}
		ctx := context.Background()
		exists, err := helper.DBManager.DB.EmailInfo.Query().Where(emailinfo.EmailEQ(req.Email)).Exist(ctx)
		if err != nil {
			return UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to query database", nil)
		}
		if exists {
			return UpdatedDataResponse[string](c, fiber.StatusBadRequest, "email already exists", nil)
		}
		return SendEmailHandler(c, req.Email, req.ChallengeToken, helper)

	})

	email.Post("/verify", helper.SessionHandler.VerifySessionToken, func(c *fiber.Ctx) error {
		var req VerifyEmailPayload
		if err := c.BodyParser(&req); err != nil {
			return UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid request body", nil)
		}
		result := VerifyEmailHandler(c, req.Email, req.OneTimePassword, helper)
		if result != nil {
			return result
		}
		userID := c.Locals("userID").(string)
		ctx := context.Background()
		if _, err := helper.DBManager.DB.User.
			Update().
			Where(user.IDEQ(userID)).
			SetEmail(req.Email).
			Save(ctx); err != nil {
			return UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to update user email", nil)
		}
		if _, err := helper.DBManager.DB.EmailInfo.
			Update().
			Where(emailinfo.HasUserWith(user.IDEQ(userID))).
			SetEmail(req.Email).
			SetVerified(true).
			Save(ctx); err != nil {
			return UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to update email info", nil)
		}

		ud := HarukiToolboxUserData{
			EmailInfo: EmailInfo{
				Email:    req.Email,
				Verified: true,
			},
		}
		return UpdatedDataResponse(c, fiber.StatusOK, "email verified", &ud)
	})
}
