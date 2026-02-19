package user

import (
	"crypto/rand"
	"fmt"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/cloudflare"
	"haruki-suite/utils/database/postgresql/emailinfo"
	"haruki-suite/utils/database/postgresql/user"
	harukiRedis "haruki-suite/utils/database/redis"
	harukiLogger "haruki-suite/utils/logger"
	"haruki-suite/utils/smtp"
	"math/big"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
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

func SendEmailHandler(c fiber.Ctx, email, challengeToken string, helper *harukiAPIHelper.HarukiToolboxRouterHelpers) error {
	ctx := c.Context()
	xForwardedFor := c.Get("X-Forwarded-For")
	clientIP := ""
	if xForwardedFor != "" {
		parts := strings.Split(xForwardedFor, ",")
		clientIP = strings.TrimSpace(parts[0])
	}
	resp, err := cloudflare.ValidateTurnstile(challengeToken, clientIP)
	if err != nil || !resp.Success {
		return harukiAPIHelper.ErrorBadRequest(c, "captcha verify failed")
	}
	code := GenerateCode(false)
	redisKey := harukiRedis.BuildEmailVerifyKey(email)
	if err := helper.DBManager.Redis.SetCache(ctx, redisKey, code, 5*time.Minute); err != nil {
		harukiLogger.Errorf("Failed to set redis cache: %v", err)
		return harukiAPIHelper.ErrorInternal(c, "failed to save code")
	}
	body := strings.ReplaceAll(smtp.VerificationCodeTemplate, "{{CODE}}", code)
	if err := helper.SMTPClient.Send([]string{email}, "您的验证码 | Haruki工具箱", body, "Haruki工具箱 | 星云科技"); err != nil {
		harukiLogger.Errorf("Failed to send email: %v", err)
		return harukiAPIHelper.ErrorInternal(c, "failed to send email")
	}
	return harukiAPIHelper.SuccessResponse[string](c, "verification code sent", nil)
}

func VerifyEmailHandler(c fiber.Ctx, email, oneTimePassword string, helper *harukiAPIHelper.HarukiToolboxRouterHelpers) (bool, error) {
	ctx := c.Context()
	attemptKey := harukiRedis.BuildOTPAttemptKey(email)
	var attemptCount int
	found, err := helper.DBManager.Redis.GetCache(ctx, attemptKey, &attemptCount)
	if err != nil {
		harukiLogger.Errorf("Failed to get OTP attempt count: %v", err)
	}
	if found && attemptCount >= 5 {
		return false, harukiAPIHelper.ErrorBadRequest(c, "Too many verification attempts. Please request a new code.")
	}
	redisKey := harukiRedis.BuildEmailVerifyKey(email)
	var code string
	found, err = helper.DBManager.Redis.GetCache(ctx, redisKey, &code)
	if err != nil {
		harukiLogger.Errorf("Failed to get redis cache: %v", err)
		return false, harukiAPIHelper.ErrorInternal(c, "Verification service unavailable")
	}
	if !found {
		return false, harukiAPIHelper.ErrorBadRequest(c, "verification code expired or not found")
	}
	if oneTimePassword != code {
		newCount := attemptCount + 1
		_ = helper.DBManager.Redis.SetCache(ctx, attemptKey, newCount, 5*time.Minute)
		return false, harukiAPIHelper.ErrorBadRequest(c, "invalid verification code")
	}
	_ = helper.DBManager.Redis.DeleteCache(ctx, redisKey)
	_ = helper.DBManager.Redis.DeleteCache(ctx, attemptKey)
	return true, nil
}

func handleSendEmail(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		var req harukiAPIHelper.SendEmailPayload
		if err := c.Bind().Body(&req); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}
		exists, err := apiHelper.DBManager.DB.EmailInfo.Query().Where(emailinfo.EmailEQ(req.Email)).Exist(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to query email info: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to query database")
		}
		if exists {
			return harukiAPIHelper.ErrorBadRequest(c, "email already exists")
		}
		return SendEmailHandler(c, req.Email, req.ChallengeToken, apiHelper)
	}
}

func handleVerifyEmail(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		var req harukiAPIHelper.VerifyEmailPayload
		if err := c.Bind().Body(&req); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}
		ok, err := VerifyEmailHandler(c, req.Email, req.OneTimePassword, apiHelper)
		if err != nil {
			return err
		}
		if !ok {
			return harukiAPIHelper.ErrorBadRequest(c, "verification failed")
		}
		userID := c.Locals("userID").(string)
		if _, err := apiHelper.DBManager.DB.User.
			Update().
			Where(user.IDEQ(userID)).
			SetEmail(req.Email).
			Save(ctx); err != nil {
			harukiLogger.Errorf("Failed to update user email: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to update user email")
		}
		if _, err := apiHelper.DBManager.DB.EmailInfo.
			Update().
			Where(emailinfo.HasUserWith(user.IDEQ(userID))).
			SetEmail(req.Email).
			SetVerified(true).
			Save(ctx); err != nil {
			harukiLogger.Errorf("Failed to update email info: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to update email info")
		}

		ud := harukiAPIHelper.HarukiToolboxUserData{
			EmailInfo: &harukiAPIHelper.EmailInfo{
				Email:    req.Email,
				Verified: true,
			},
		}
		_ = harukiAPIHelper.ClearUserSessions(apiHelper.DBManager.Redis.Redis, userID)
		return harukiAPIHelper.SuccessResponse(c, "email verified", &ud)
	}
}

func registerEmailRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	email := apiHelper.Router.Group("/api/email")

	email.Post("/send", handleSendEmail(apiHelper))
	email.Post("/verify", apiHelper.SessionHandler.VerifySessionToken, checkUserNotBanned(apiHelper), handleVerifyEmail(apiHelper))
}
