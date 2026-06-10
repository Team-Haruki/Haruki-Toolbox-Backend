package harukibotneo

import (
	"fmt"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/smtp"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func handleSendMail(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		if !apiHelper.BotRegistrationEnabled {
			return harukiAPIHelper.ErrorForbidden(c, "registration is currently disabled")
		}
		if apiHelper.DBManager.BotDB == nil {
			harukiLogger.Errorf("bot database is not configured")
			return harukiAPIHelper.ErrorInternal(c, "registration service unavailable")
		}

		var payload sendMailPayload
		if err := c.Bind().JSON(&payload); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}
		if payload.QQNumber <= 0 {
			return harukiAPIHelper.ErrorBadRequest(c, "missing qq_number")
		}

		qqStr := strconv.FormatInt(payload.QQNumber, 10)
		ctx := c.Context()

		// Rate limit
		clientIP := c.IP()
		limited, limitKey, limitMsg, rlErr := checkSendMailRateLimit(c, apiHelper, clientIP, qqStr)
		if rlErr != nil {
			return harukiAPIHelper.ErrorInternal(c, "registration service unavailable")
		}
		if limited {
			return respondRateLimited(c, limitKey, limitMsg, apiHelper, sendMailRateLimitWindow)
		}

		// Generate and store code
		code, err := generateCode()
		if err != nil {
			harukiLogger.Errorf("Failed to generate verification code: %v", err)
			releaseSendMailRateLimit(c, apiHelper, clientIP, qqStr)
			return harukiAPIHelper.ErrorInternal(c, "failed to generate verification code")
		}
		redisKey := harukiRedis.BuildBotVerifyCodeKey(qqStr)
		if err := apiHelper.DBManager.Redis.SetCache(ctx, redisKey, code, verifyCodeTTL); err != nil {
			harukiLogger.Errorf("Failed to store verification code: %v", err)
			releaseSendMailRateLimit(c, apiHelper, clientIP, qqStr)
			return harukiAPIHelper.ErrorInternal(c, "failed to save verification code")
		}

		// Send email
		email := fmt.Sprintf("%s@qq.com", qqStr)
		body := strings.ReplaceAll(smtp.VerificationCodeTemplate, "{{CODE}}", code)
		if err := apiHelper.SMTPClient.Send([]string{email}, "您的验证码 | Haruki Bot", body, "Haruki Bot | 星云科技"); err != nil {
			if delErr := apiHelper.DBManager.Redis.DeleteCache(ctx, redisKey); delErr != nil {
				harukiLogger.Warnf("Failed to rollback verification code for QQ %s: %v", qqStr, delErr)
			}
			releaseSendMailRateLimit(c, apiHelper, clientIP, qqStr)
			harukiLogger.Errorf("Failed to send email to %s: %v", email, err)
			return harukiAPIHelper.ErrorInternal(c, "failed to send verification email")
		}

		return harukiAPIHelper.SuccessResponse[string](c, "verification code sent", nil)
	}
}
