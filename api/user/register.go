package user

import (
	"context"
	"fmt"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/cloudflare"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"golang.org/x/crypto/bcrypt"
)

func registerRegisterRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	apiHelper.Router.Post("/api/user/register", func(c *fiber.Ctx) error {
		var req harukiAPIHelper.RegisterPayload
		if err := c.BodyParser(&req); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid request payload", nil)
		}

		xff := c.Get("X-Forwarded-For")
		remoteIP := ""
		if xff != "" {
			if idx := strings.IndexByte(xff, ','); idx >= 0 {
				remoteIP = strings.TrimSpace(xff[:idx])
			} else {
				remoteIP = strings.TrimSpace(xff)
			}
		} else {
			remoteIP = c.IP()
		}

		vresp, err := cloudflare.ValidateTurnstile(req.ChallengeToken, remoteIP)
		if err != nil || vresp == nil || !vresp.Success {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid challenge token", nil)
		}

		redisKey := "email:verify:" + req.Email
		var otp string
		exists, err := apiHelper.DBManager.Redis.GetCache(context.Background(), redisKey, &otp)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "redis error", nil)
		}
		if !exists || otp != req.OneTimePassword {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid or expired verification code", nil)
		}

		passwordHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to hash password", nil)
		}

		uid := fmt.Sprintf("%010d", time.Now().UnixNano()%1e10)

		user, err := apiHelper.DBManager.DB.User.Create().
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
			SetUser(user).
			Save(context.Background())
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to create email info", nil)
		}

		apiHelper.DBManager.Redis.DeleteCache(context.Background(), redisKey)

		signedToken, err := apiHelper.SessionHandler.IssueSession(uid)

		ud := harukiAPIHelper.HarukiToolboxUserData{
			Name:                        &user.Name,
			UserID:                      &uid,
			AvatarPath:                  nil,
			AllowCNMysekai:              &user.AllowCnMysekai,
			EmailInfo:                   &harukiAPIHelper.EmailInfo{Email: emailInfo.Email, Verified: emailInfo.Verified},
			SocialPlatformInfo:          nil,
			AuthorizeSocialPlatformInfo: nil,
			GameAccountBindings:         nil,
			SessionToken:                &signedToken,
		}
		resp := harukiAPIHelper.RegisterOrLoginSuccessResponse{Status: fiber.StatusOK, Message: "register success", UserData: ud}
		return harukiAPIHelper.ResponseWithStruct(c, fiber.StatusOK, &resp)
	})
}
