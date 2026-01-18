package user

import (
	"crypto/rand"
	"fmt"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/cloudflare"
	harukiRedis "haruki-suite/utils/database/redis"
	harukiLogger "haruki-suite/utils/logger"
	"math/big"
	"time"

	"github.com/gofiber/fiber/v3"
	"golang.org/x/crypto/bcrypt"

	"haruki-suite/utils/database/postgresql/emailinfo"
	"haruki-suite/utils/database/postgresql/user"
)

func handleRegister(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		var req harukiAPIHelper.RegisterPayload
		if err := c.Bind().Body(&req); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}
		vresp, err := cloudflare.ValidateTurnstile(req.ChallengeToken, c.IP())
		if err != nil || vresp == nil || !vresp.Success {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid challenge token")
		}
		if err := verifyEmailOTP(c, apiHelper, req.Email, req.OneTimePassword); err != nil {
			return err
		}
		if err := checkEmailAvailability(c, apiHelper, req.Email); err != nil {
			return err
		}
		passwordHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			harukiLogger.Errorf("Failed to hash password: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to hash password")
		}
		tsSuffix := time.Now().UnixMicro() % 10000
		randNum, _ := rand.Int(rand.Reader, big.NewInt(1000000))
		uid := fmt.Sprintf("%04d%06d", tsSuffix, randNum.Int64())
		newUser, err := apiHelper.DBManager.DB.User.Create().
			SetID(uid).
			SetName(req.Name).
			SetEmail(req.Email).
			SetPasswordHash(string(passwordHash)).
			SetNillableAvatarPath(nil).
			Save(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to create user: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to create user")
		}
		emailInfoRecord, err := apiHelper.DBManager.DB.EmailInfo.Create().
			SetEmail(req.Email).
			SetVerified(true).
			SetUser(newUser).
			Save(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to create email info: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to create email info")
		}
		uploadCode, err := generateUploadCode()
		if err != nil {
			harukiLogger.Errorf("Failed to generate upload code: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to generate upload code")
		}
		_, err = apiHelper.DBManager.DB.IOSScriptCode.Create().
			SetUserID(uid).
			SetUploadCode(uploadCode).
			Save(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to create iOS script code: %v", err)
			// Non-fatal, continue with registration
		}
		redisKey := harukiRedis.BuildEmailVerifyKey(req.Email)
		_ = apiHelper.DBManager.Redis.DeleteCache(ctx, redisKey)
		signedToken, err := apiHelper.SessionHandler.IssueSession(uid)
		if err != nil {
			harukiLogger.Errorf("Failed to issue session for user %s: %v", uid, err)
			return harukiAPIHelper.ErrorInternal(c, "Failed to create session")
		}
		ud := harukiAPIHelper.HarukiToolboxUserData{
			Name:                        &newUser.Name,
			UserID:                      &uid,
			AvatarPath:                  nil,
			AllowCNMysekai:              &newUser.AllowCnMysekai,
			IOSUploadCode:               &uploadCode,
			EmailInfo:                   &harukiAPIHelper.EmailInfo{Email: emailInfoRecord.Email, Verified: emailInfoRecord.Verified},
			SocialPlatformInfo:          nil,
			AuthorizeSocialPlatformInfo: nil,
			GameAccountBindings:         nil,
			SessionToken:                &signedToken,
		}
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

	emailVerifiedExists, err := apiHelper.DBManager.DB.EmailInfo.Query().Where(emailinfo.EmailEQ(email), emailinfo.Verified(true)).Exist(ctx)
	if err != nil {
		harukiLogger.Errorf("Failed to query email info: %v", err)
		return harukiAPIHelper.ErrorInternal(c, "database error")
	}
	if emailVerifiedExists {
		return harukiAPIHelper.ErrorBadRequest(c, "email already verified")
	}
	return nil
}

func registerRegisterRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	apiHelper.Router.Post("/api/user/register", handleRegister(apiHelper))
}
