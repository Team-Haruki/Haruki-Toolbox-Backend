package user

import (
	"crypto/rand"
	"fmt"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/cloudflare"
	"haruki-suite/utils/database/postgresql"
	harukiRedis "haruki-suite/utils/database/redis"
	harukiLogger "haruki-suite/utils/logger"
	"math/big"
	"time"

	"github.com/gofiber/fiber/v3"
	"golang.org/x/crypto/bcrypt"

	"haruki-suite/utils/database/postgresql/user"
)

func handleRegister(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		logRegister := func(result string, targetUserID string, reason string) {
			targetType := "user"
			var targetIDPtr *string
			if targetUserID != "" {
				targetID := targetUserID
				targetIDPtr = &targetID
			}
			entry := harukiAPIHelper.BuildSystemLogEntryFromFiber(c, "user.register", result, &targetType, targetIDPtr, map[string]any{
				"reason": reason,
			})
			if targetUserID != "" {
				entry.ActorUserID = &targetUserID
				role := "user"
				entry.ActorRole = &role
				entry.ActorType = harukiAPIHelper.SystemLogActorTypeUser
			}
			_ = harukiAPIHelper.WriteSystemLog(ctx, apiHelper, entry)
		}

		var req harukiAPIHelper.RegisterPayload
		if err := c.Bind().Body(&req); err != nil {
			logRegister(harukiAPIHelper.SystemLogResultFailure, "", "invalid_payload")
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}
		vresp, err := cloudflare.ValidateTurnstile(req.ChallengeToken, c.IP())
		if err != nil || vresp == nil || !vresp.Success {
			logRegister(harukiAPIHelper.SystemLogResultFailure, "", "invalid_challenge")
			return harukiAPIHelper.ErrorBadRequest(c, "invalid challenge token")
		}
		if err := verifyEmailOTP(c, apiHelper, req.Email, req.OneTimePassword); err != nil {
			logRegister(harukiAPIHelper.SystemLogResultFailure, "", "invalid_email_otp")
			return err
		}
		if err := checkEmailAvailability(c, apiHelper, req.Email); err != nil {
			logRegister(harukiAPIHelper.SystemLogResultFailure, "", "email_unavailable")
			return err
		}
		if len(req.Password) < 8 {
			logRegister(harukiAPIHelper.SystemLogResultFailure, "", "password_too_short")
			return harukiAPIHelper.ErrorBadRequest(c, "password must be at least 8 characters")
		}
		if len([]byte(req.Password)) > 72 {
			logRegister(harukiAPIHelper.SystemLogResultFailure, "", "password_too_long")
			return harukiAPIHelper.ErrorBadRequest(c, "password is too long (max 72 bytes)")
		}
		passwordHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			harukiLogger.Errorf("Failed to hash password: %v", err)
			logRegister(harukiAPIHelper.SystemLogResultFailure, "", "password_hash_failed")
			return harukiAPIHelper.ErrorInternal(c, "failed to hash password")
		}
		var uid string
		var newUser *postgresql.User
		for attempt := range 3 {
			tsSuffix := time.Now().UnixMicro() % 10000
			randNum, _ := rand.Int(rand.Reader, big.NewInt(1000000))
			uid = fmt.Sprintf("%04d%06d", tsSuffix, randNum.Int64())
			newUser, err = apiHelper.DBManager.DB.User.Create().
				SetID(uid).
				SetName(req.Name).
				SetEmail(req.Email).
				SetPasswordHash(string(passwordHash)).
				SetNillableAvatarPath(nil).
				SetCreatedAt(time.Now().UTC()).
				Save(ctx)
			if err == nil {
				break
			}
			if !postgresql.IsConstraintError(err) {
				break
			}
			harukiLogger.Warnf("UID collision on attempt %d, retrying...", attempt+1)
		}
		if err != nil {
			harukiLogger.Errorf("Failed to create user after retries: %v", err)
			logRegister(harukiAPIHelper.SystemLogResultFailure, "", "create_user_failed")
			return harukiAPIHelper.ErrorInternal(c, "failed to create user")
		}
		uploadCode, err := generateUploadCode()
		if err != nil {
			harukiLogger.Errorf("Failed to generate upload code: %v", err)
			logRegister(harukiAPIHelper.SystemLogResultFailure, uid, "generate_upload_code_failed")
			return harukiAPIHelper.ErrorInternal(c, "failed to generate upload code")
		}
		_, err = apiHelper.DBManager.DB.IOSScriptCode.Create().
			SetUserID(uid).
			SetUploadCode(uploadCode).
			Save(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to create iOS script code: %v", err)
		}
		redisKey := harukiRedis.BuildEmailVerifyKey(req.Email)
		_ = apiHelper.DBManager.Redis.DeleteCache(ctx, redisKey)
		signedToken, err := apiHelper.SessionHandler.IssueSession(uid)
		if err != nil {
			harukiLogger.Errorf("Failed to issue session for user %s: %v", uid, err)
			logRegister(harukiAPIHelper.SystemLogResultFailure, uid, "issue_session_failed")
			return harukiAPIHelper.ErrorInternal(c, "Failed to create session")
		}
		role := string(newUser.Role)
		ud := harukiAPIHelper.HarukiToolboxUserData{
			Name:                        &newUser.Name,
			UserID:                      &uid,
			Role:                        &role,
			AvatarPath:                  nil,
			AllowCNMysekai:              &newUser.AllowCnMysekai,
			IOSUploadCode:               &uploadCode,
			EmailInfo:                   &harukiAPIHelper.EmailInfo{Email: req.Email, Verified: true},
			SocialPlatformInfo:          nil,
			AuthorizeSocialPlatformInfo: nil,
			GameAccountBindings:         nil,
			SessionToken:                &signedToken,
		}
		logRegister(harukiAPIHelper.SystemLogResultSuccess, uid, "ok")
		resp := harukiAPIHelper.RegisterOrLoginSuccessResponse{Status: fiber.StatusOK, Message: "register success", UserData: ud}
		return harukiAPIHelper.ResponseWithStruct(c, fiber.StatusOK, &resp)
	}
}

func verifyEmailOTP(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, email, otp string) error {
	ctx := c.Context()
	redisKey := harukiRedis.BuildEmailVerifyKey(email)
	var storedOTP string
	exists, err := apiHelper.DBManager.Redis.GetCache(ctx, redisKey, &storedOTP)
	if err != nil {
		harukiLogger.Errorf("Failed to get redis cache: %v", err)
		return harukiAPIHelper.ErrorInternal(c, "redis error")
	}
	if !exists || storedOTP != otp {
		return harukiAPIHelper.ErrorBadRequest(c, "invalid or expired verification code")
	}
	return nil
}

func checkEmailAvailability(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, email string) error {
	ctx := c.Context()
	userExists, err := apiHelper.DBManager.DB.User.Query().Where(user.EmailEQ(email)).Exist(ctx)
	if err != nil {
		harukiLogger.Errorf("Failed to query user: %v", err)
		return harukiAPIHelper.ErrorInternal(c, "database error")
	}
	if userExists {
		return harukiAPIHelper.ErrorBadRequest(c, "email already in use")
	}
	return nil
}

func registerRegisterRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	apiHelper.Router.Post("/api/user/register", handleRegister(apiHelper))
}
