package user

import (
	"fmt"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/cloudflare"
	harukiRedis "haruki-suite/utils/database/redis"
	"strings"
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

		remoteIP := extractRemoteIP(c)

		vresp, err := cloudflare.ValidateTurnstile(req.ChallengeToken, remoteIP)
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
			return harukiAPIHelper.ErrorInternal(c, "failed to hash password")
		}

		uid := fmt.Sprintf("%010d", time.Now().UnixNano()%1e10)

		newUser, err := apiHelper.DBManager.DB.User.Create().
			SetID(uid).
			SetName(req.Name).
			SetEmail(req.Email).
			SetPasswordHash(string(passwordHash)).
			SetNillableAvatarPath(nil).
			Save(ctx)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to create user")
		}

		emailInfoRecord, err := apiHelper.DBManager.DB.EmailInfo.Create().
			SetEmail(req.Email).
			SetVerified(true).
			SetUser(newUser).
			Save(ctx)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to create email info")
		}

		redisKey := harukiRedis.BuildEmailVerifyKey(req.Email)
		_ = apiHelper.DBManager.Redis.DeleteCache(ctx, redisKey)

		signedToken, _ := apiHelper.SessionHandler.IssueSession(uid)

		ud := harukiAPIHelper.HarukiToolboxUserData{
			Name:                        &newUser.Name,
			UserID:                      &uid,
			AvatarPath:                  nil,
			AllowCNMysekai:              &newUser.AllowCnMysekai,
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

func extractRemoteIP(c fiber.Ctx) string {
	xff := c.Get("X-Forwarded-For")
	if xff != "" {
		if idx := strings.IndexByte(xff, ','); idx >= 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	return c.IP()
}

func verifyEmailOTP(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, email, otp string) error {
	ctx := c.Context()
	redisKey := harukiRedis.BuildEmailVerifyKey(email)
	var storedOTP string
	exists, err := apiHelper.DBManager.Redis.GetCache(ctx, redisKey, &storedOTP)
	if err != nil {
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
		return harukiAPIHelper.ErrorInternal(c, "database error")
	}
	if userExists {
		return harukiAPIHelper.ErrorBadRequest(c, "email already in use")
	}

	emailVerifiedExists, err := apiHelper.DBManager.DB.EmailInfo.Query().Where(emailinfo.EmailEQ(email), emailinfo.Verified(true)).Exist(ctx)
	if err != nil {
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
