package harukibotneo

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/neopg"
	botUser "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/neopg/user"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	"golang.org/x/crypto/bcrypt"
)

func handleRegister(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		if !apiHelper.BotRegistrationEnabled {
			return harukiAPIHelper.ErrorForbidden(c, "registration is currently disabled")
		}
		if apiHelper.DBManager.BotDB == nil {
			harukiLogger.Errorf("bot database is not configured")
			return harukiAPIHelper.ErrorInternal(c, "registration service unavailable")
		}

		var payload registerPayload
		if err := c.Bind().JSON(&payload); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}
		if payload.QQNumber <= 0 {
			return harukiAPIHelper.ErrorBadRequest(c, "missing qq_number")
		}
		if strings.TrimSpace(payload.VerificationCode) == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "missing verification_code")
		}

		qqStr := strconv.FormatInt(payload.QQNumber, 10)
		ctx := c.Context()

		// Register rate limit
		rlKey := harukiRedis.BuildBotRegisterRateLimitTargetKey(qqStr)
		count, err := apiHelper.DBManager.Redis.IncrementWithTTL(ctx, rlKey, registerRateLimitWindow)
		if err != nil {
			harukiLogger.Errorf("Failed to check register rate limit: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "registration service unavailable")
		}
		if count > int64(registerTargetLimit) {
			return respondRateLimited(c, rlKey, "too many registration attempts", apiHelper, registerRateLimitWindow)
		}

		// Verify code
		codeKey := harukiRedis.BuildBotVerifyCodeKey(qqStr)
		var storedCode string
		found, err := apiHelper.DBManager.Redis.GetCache(ctx, codeKey, &storedCode)
		if err != nil {
			harukiLogger.Errorf("Failed to get verification code: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "registration service unavailable")
		}
		if !found {
			return harukiAPIHelper.ErrorBadRequest(c, "verification code not found or expired")
		}
		if subtle.ConstantTimeCompare([]byte(payload.VerificationCode), []byte(storedCode)) != 1 {
			// Track attempts
			attemptKey := harukiRedis.BuildBotVerifyAttemptKey(qqStr)
			var attemptCount int
			if af, _ := apiHelper.DBManager.Redis.GetCache(ctx, attemptKey, &attemptCount); af && attemptCount >= maxVerifyAttempts {
				_ = apiHelper.DBManager.Redis.DeleteCache(ctx, codeKey)
				_ = apiHelper.DBManager.Redis.DeleteCache(ctx, attemptKey)
				return harukiAPIHelper.ErrorBadRequest(c, "too many verification attempts, please request a new code")
			}
			_ = apiHelper.DBManager.Redis.SetCache(ctx, attemptKey, attemptCount+1, verifyCodeTTL)
			return harukiAPIHelper.ErrorBadRequest(c, "verification code is invalid")
		}

		// Consume code
		consumed, err := apiHelper.DBManager.Redis.DeleteCacheIfValueMatches(ctx, codeKey, storedCode)
		if err != nil || !consumed {
			return harukiAPIHelper.ErrorBadRequest(c, "verification code not found or expired")
		}

		// Generate new credential
		credentialRaw := make([]byte, credentialBytes)
		if _, err := rand.Read(credentialRaw); err != nil {
			harukiLogger.Errorf("Failed to generate credential: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to generate credential")
		}
		credentialPlain := base64.URLEncoding.EncodeToString(credentialRaw)

		hashedCredential, err := bcrypt.GenerateFromPassword([]byte(credentialPlain), bcrypt.DefaultCost)
		if err != nil {
			harukiLogger.Errorf("Failed to hash credential: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to process credential")
		}

		// Check if already registered — update credential if so, otherwise create new
		existing, err := apiHelper.DBManager.BotDB.User.Query().
			Where(botUser.OwnerUserIDEQ(payload.QQNumber)).
			Only(ctx)
		if err != nil && !neopg.IsNotFound(err) {
			harukiLogger.Errorf("Failed to check bot registration: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "registration service unavailable")
		}

		var botIDStr string
		var statusCode int
		var message string

		if existing != nil {
			// Update existing credential
			_, err = existing.Update().
				SetCredential(string(hashedCredential)).
				Save(ctx)
			if err != nil {
				harukiLogger.Errorf("Failed to update bot credential: %v", err)
				return harukiAPIHelper.ErrorInternal(c, "failed to update credential")
			}
			botIDStr = strconv.Itoa(existing.BotID)
			statusCode = fiber.StatusOK
			message = "credential reset successful"
		} else {
			// Generate bot_id for new registration
			botID, err := generateUniqueBotID(ctx, apiHelper.DBManager.BotDB)
			if err != nil {
				harukiLogger.Errorf("Failed to generate bot_id: %v", err)
				return harukiAPIHelper.ErrorInternal(c, "failed to generate bot ID")
			}
			_, err = apiHelper.DBManager.BotDB.User.Create().
				SetOwnerUserID(payload.QQNumber).
				SetBotID(botID).
				SetCredential(string(hashedCredential)).
				Save(ctx)
			if err != nil {
				harukiLogger.Errorf("Failed to create bot registration: %v", err)
				return harukiAPIHelper.ErrorInternal(c, "failed to create registration")
			}
			botIDStr = strconv.Itoa(botID)
			statusCode = fiber.StatusCreated
			message = "registration successful"
		}

		// Sign credential as JWT
		credentialJWT, err := signCredentialJWT(apiHelper.BotCredentialSignToken, botIDStr, credentialPlain)
		if err != nil {
			harukiLogger.Errorf("Failed to sign credential JWT: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to sign credential")
		}

		// Cleanup Redis
		attemptKey := harukiRedis.BuildBotVerifyAttemptKey(qqStr)
		_ = apiHelper.DBManager.Redis.DeleteCache(ctx, attemptKey)

		result := registrationResultData{
			BotID:      botIDStr,
			Credential: credentialJWT,
		}
		return harukiAPIHelper.UpdatedDataResponse(c, statusCode, message, &result)
	}
}
