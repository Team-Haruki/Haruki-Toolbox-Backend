package api

import (
	"context"
	"haruki-suite/config"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// ====================== Response Functions ======================

func NewResponse[T any](status int, message string, data *T) *GenericResponse[T] {
	return &GenericResponse[T]{
		Status:      status,
		Message:     message,
		UpdatedData: data,
	}
}

func UpdatedDataResponse[T any](c *fiber.Ctx, status int, message string, data *T) error {
	return c.Status(status).JSON(NewResponse(status, message, data))
}

func ResponseWithStruct[T any](c *fiber.Ctx, status int, data T) error {
	return c.Status(status).JSON(data)
}

// ====================== Session Helper Functions ======================

func NewSessionHandler(redisClient *redis.Client, sessionSignKey string) *SessionHandler {
	return &SessionHandler{
		RedisClient:    redisClient,
		SessionSignKey: sessionSignKey,
	}
}

func (s *SessionHandler) IssueSession(userID string) (string, error) {
	sessionToken := uuid.NewString()
	err := s.RedisClient.Set(context.Background(), userID+":"+sessionToken, "1", 12*time.Hour).Err()
	if err != nil {
		return "", err
	}
	claims := SessionClaims{
		UserID:       userID,
		SessionToken: sessionToken,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(12 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(s.SessionSignKey))
	if err != nil {
		return "", err
	}
	return signed, nil
}

func (s *SessionHandler) VerifySessionToken(c *fiber.Ctx) error {
	auth := c.Get("Authorization")
	if auth == "" {
		return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "missing token", nil)
	}
	tokenStr := auth
	parsed, err := jwt.ParseWithClaims(tokenStr, &SessionClaims{}, func(t *jwt.Token) (interface{}, error) {
		return []byte(config.Cfg.UserSystem.SessionSignToken), nil
	})
	if err != nil || !parsed.Valid {
		return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "invalid token", nil)
	}
	claims, ok := parsed.Claims.(*SessionClaims)
	if !ok {
		return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "invalid claims", nil)
	}

	key := claims.UserID + ":" + claims.SessionToken
	exists, err := s.RedisClient.Exists(context.Background(), key).Result()
	if err != nil || exists == 0 {
		return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "invalid session", nil)
	}

	toolboxUserID := c.Params("toolbox_user_id")
	if toolboxUserID != "" && toolboxUserID != claims.UserID {
		return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "user ID mismatch", nil)
	}

	c.Locals("userID", claims.UserID)
	return c.Next()
}

// ====================== Other Helper Functions ======================

func ArrayContains(arr []string, s string) bool {
	for _, v := range arr {
		if v == s {
			return true
		}
	}
	return false
}

func StringContains(s, substr string) bool {
	return strings.Contains(s, substr)
}

func ClearUserSessions(redisClient *redis.Client, userID string) error {
	ctx := context.Background()
	var cursor uint64
	prefix := userID + ":"
	for {
		keys, newCursor, err := redisClient.Scan(ctx, cursor, prefix+"*", 100).Result()
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			if err := redisClient.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}
		cursor = newCursor
		if cursor == 0 {
			break
		}
	}
	return nil
}
