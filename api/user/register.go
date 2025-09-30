package user

import (
	"context"
	"fmt"
	"haruki-suite/utils/cloudflare"
	"haruki-suite/utils/database/postgresql"
	"net/http"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

func RegisterRegisterRoutes(router fiber.Router, redisClient *redis.Client, postgresClient *postgresql.Client) {
	router.Post("/api/user/register", func(c *fiber.Ctx) error {
		var req RegisterPayload
		if err := c.BodyParser(&req); err != nil {
			return UpdatedDataResponse[string](c, 400, "invalid request payload", nil)
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
			return UpdatedDataResponse[string](c, 400, "invalid challenge token", nil)
		}

		redisKey := "email:verify:" + req.Email
		otp, err := redisClient.Get(context.Background(), redisKey).Result()
		if err != nil || otp != req.OneTimePassword {
			return UpdatedDataResponse[string](c, 400, "invalid or expired verification code", nil)
		}

		passwordHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			return UpdatedDataResponse[string](c, 500, "failed to hash password", nil)
		}

		uid := fmt.Sprintf("%010d", time.Now().UnixNano()%1e10)

		user, err := postgresClient.User.Create().
			SetUserID(uid).
			SetName(req.Name).
			SetEmail(req.Email).
			SetPasswordHash(string(passwordHash)).
			SetNillableAvatarPath(nil).
			Save(context.Background())
		if err != nil {
			return UpdatedDataResponse[string](c, 500, "failed to create user", nil)
		}

		emailInfo, err := postgresClient.EmailInfo.Create().
			SetEmail(req.Email).
			SetVerified(true).
			SetUser(user).
			Save(context.Background())
		if err != nil {
			return UpdatedDataResponse[string](c, 500, "failed to create email info", nil)
		}

		redisClient.Del(context.Background(), redisKey)

		signedToken, err := IssueSession(uid)

		ud := UserData{
			Name:                        user.Name,
			UserID:                      uid,
			AvatarPath:                  nil,
			EmailInfo:                   EmailInfo{Email: emailInfo.Email, Verified: emailInfo.Verified},
			SocialPlatformInfo:          nil,
			AuthorizeSocialPlatformInfo: nil,
			GameAccountBindings:         nil,
			SessionToken:                signedToken,
		}
		resp := RegisterOrLoginSuccessResponse{Status: http.StatusOK, Message: "register success", UserData: ud}
		return ResponseWithStruct(c, http.StatusOK, &resp)
	})
}
