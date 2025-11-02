package user

import (
	"context"
	"crypto/rand"
	"fmt"
	harukiAPIHelper "haruki-suite/utils/api"
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

func SendEmailHandler(c *fiber.Ctx, email, challengeToken string, helper *harukiAPIHelper.HarukiToolboxRouterHelpers) error {
	xForwardedFor := c.Get("X-Forwarded-For")
	clientIP := ""
	if xForwardedFor != "" {
		parts := strings.Split(xForwardedFor, ",")
		clientIP = strings.TrimSpace(parts[0])
	}

	resp, err := cloudflare.ValidateTurnstile(challengeToken, clientIP)
	if err != nil || !resp.Success {
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "captcha verify failed", nil)
	}

	code := GenerateCode(false)
	if err := helper.DBManager.Redis.SetCache(context.Background(), "email:verify:"+email, code, 5*time.Minute); err != nil {
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to save code", nil)
	}

	body := strings.ReplaceAll(smtp.VerificationCodeTemplate, "{{CODE}}", code)
	if err := helper.SMTPClient.Send([]string{email}, "您的验证码 | Haruki工具箱", body, "Haruki工具箱 | 星云科技"); err != nil {
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to send email", nil)
	}

	return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusOK, "verification code sent", nil)
}

func VerifyEmailHandler(c *fiber.Ctx, email, oneTimePassword string, helper *harukiAPIHelper.HarukiToolboxRouterHelpers) (bool, error) {
	ctx := context.Background()
	var code string
	found, err := helper.DBManager.Redis.GetCache(ctx, "email:verify:"+email, &code)
	if err != nil {
		return false, harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to check redis", nil)
	}
	if !found {
		return false, harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "verification code expired or not found", nil)
	}

	if oneTimePassword != code {
		return false, harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid verification code", nil)
	}

	_ = helper.DBManager.Redis.DeleteCache(ctx, "email:verify:"+email)
	return true, nil
}

func handleSendEmail(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var req harukiAPIHelper.SendEmailPayload
		if err := c.BodyParser(&req); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid request body", nil)
		}
		ctx := context.Background()
		exists, err := apiHelper.DBManager.DB.EmailInfo.Query().Where(emailinfo.EmailEQ(req.Email)).Exist(ctx)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to query database", nil)
		}
		if exists {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "email already exists", nil)
		}
		return SendEmailHandler(c, req.Email, req.ChallengeToken, apiHelper)
	}
}

func handleVerifyEmail(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var req harukiAPIHelper.VerifyEmailPayload
		if err := c.BodyParser(&req); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid request body", nil)
		}
		ok, err := VerifyEmailHandler(c, req.Email, req.OneTimePassword, apiHelper)
		if err != nil {
			return err
		}
		if !ok {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "verification failed", nil)
		}
		userID := c.Locals("userID").(string)
		ctx := context.Background()
		if _, err := apiHelper.DBManager.DB.User.
			Update().
			Where(user.IDEQ(userID)).
			SetEmail(req.Email).
			Save(ctx); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to update user email", nil)
		}
		if _, err := apiHelper.DBManager.DB.EmailInfo.
			Update().
			Where(emailinfo.HasUserWith(user.IDEQ(userID))).
			SetEmail(req.Email).
			SetVerified(true).
			Save(ctx); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to update email info", nil)
		}

		ud := harukiAPIHelper.HarukiToolboxUserData{
			EmailInfo: &harukiAPIHelper.EmailInfo{
				Email:    req.Email,
				Verified: true,
			},
		}
		_ = harukiAPIHelper.ClearUserSessions(apiHelper.DBManager.Redis.Redis, userID)
		return harukiAPIHelper.UpdatedDataResponse(c, fiber.StatusOK, "email verified", &ud)
	}
}

func registerEmailRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	email := apiHelper.Router.Group("/api/email")

	email.Post("/send", handleSendEmail(apiHelper))
	email.Post("/verify", apiHelper.SessionHandler.VerifySessionToken, handleVerifyEmail(apiHelper))
}
