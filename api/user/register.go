package user

import (
	"context"
	"fmt"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/cloudflare"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"golang.org/x/crypto/bcrypt"

	"haruki-suite/utils/database/postgresql/emailinfo"
	"haruki-suite/utils/database/postgresql/user"
)

func handleRegister(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		var req harukiAPIHelper.RegisterPayload
		if err := c.Bind().Body(&req); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid request payload", nil)
		}

		remoteIP := extractRemoteIP(c)

		vresp, err := cloudflare.ValidateTurnstile(req.ChallengeToken, remoteIP)
		if err != nil || vresp == nil || !vresp.Success {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid challenge token", nil)
		}

		if err := verifyEmailOTP(c, apiHelper, req.Email, req.OneTimePassword); err != nil {
			return err
		}

		if err := checkEmailAvailability(c, apiHelper, req.Email); err != nil {
			return err
		}

		passwordHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to hash password", nil)
		}

		uid := fmt.Sprintf("%010d", time.Now().UnixNano()%1e10)

		newUser, err := apiHelper.DBManager.DB.User.Create().
			SetID(uid).
			SetName(req.Name).
			SetEmail(req.Email).
			SetPasswordHash(string(passwordHash)).
			SetNillableAvatarPath(nil).
			Save(context.Background())
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to create user", nil)
		}

		emailInfo, err := apiHelper.DBManager.DB.EmailInfo.Create().
			SetEmail(req.Email).
			SetVerified(true).
			SetUser(newUser).
			Save(context.Background())
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to create email info", nil)
		}

		redisKey := "email:verify:" + req.Email
		_ = apiHelper.DBManager.Redis.DeleteCache(context.Background(), redisKey)

		signedToken, _ := apiHelper.SessionHandler.IssueSession(uid)

		ud := harukiAPIHelper.HarukiToolboxUserData{
			Name:                        &newUser.Name,
			UserID:                      &uid,
			AvatarPath:                  nil,
			AllowCNMysekai:              &newUser.AllowCnMysekai,
			EmailInfo:                   &harukiAPIHelper.EmailInfo{Email: emailInfo.Email, Verified: emailInfo.Verified},
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
	redisKey := "email:verify:" + email
	var storedOTP string
	exists, err := apiHelper.DBManager.Redis.GetCache(context.Background(), redisKey, &storedOTP)
	if err != nil {
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "redis error", nil)
	}
	if !exists || storedOTP != otp {
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid or expired verification code", nil)
	}
	return nil
}

func checkEmailAvailability(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, email string) error {
	userExists, err := apiHelper.DBManager.DB.User.Query().Where(user.EmailEQ(email)).Exist(context.Background())
	if err != nil {
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "database error", nil)
	}
	if userExists {
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "email already in use", nil)
	}

	emailVerifiedExists, err := apiHelper.DBManager.DB.EmailInfo.Query().Where(emailinfo.EmailEQ(email), emailinfo.Verified(true)).Exist(context.Background())
	if err != nil {
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "database error", nil)
	}
	if emailVerifiedExists {
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "email already verified", nil)
	}
	return nil
}

func registerRegisterRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	apiHelper.Router.Post("/api/user/register", handleRegister(apiHelper))
}
