package api

import (
	"context"
	"fmt"
	"haruki-suite/config"
	harukiLogger "haruki-suite/utils/logger"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
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

func UpdatedDataResponse[T any](c fiber.Ctx, status int, message string, data *T) error {
	return c.Status(status).JSON(NewResponse(status, message, data))
}

func ResponseWithStruct[T any](c fiber.Ctx, status int, data T) error {
	return c.Status(status).JSON(data)
}

// ====================== Error Response Functions ======================

// ErrorBadRequest returns a 400 Bad Request response
func ErrorBadRequest(c fiber.Ctx, message string) error {
	return UpdatedDataResponse[string](c, fiber.StatusBadRequest, message, nil)
}

// ErrorUnauthorized returns a 401 Unauthorized response
func ErrorUnauthorized(c fiber.Ctx, message string) error {
	return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, message, nil)
}

// ErrorForbidden returns a 403 Forbidden response
func ErrorForbidden(c fiber.Ctx, message string) error {
	return UpdatedDataResponse[string](c, fiber.StatusForbidden, message, nil)
}

// ErrorNotFound returns a 404 Not Found response
func ErrorNotFound(c fiber.Ctx, message string) error {
	return UpdatedDataResponse[string](c, fiber.StatusNotFound, message, nil)
}

// ErrorInternal returns a 500 Internal Server Error response
func ErrorInternal(c fiber.Ctx, message string) error {
	return UpdatedDataResponse[string](c, fiber.StatusInternalServerError, message, nil)
}

// SuccessResponse returns a 200 OK response with optional data
func SuccessResponse[T any](c fiber.Ctx, message string, data *T) error {
	return UpdatedDataResponse(c, fiber.StatusOK, message, data)
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

func (s *SessionHandler) VerifySessionToken(c fiber.Ctx) error {
	auth := c.Get("Authorization")
	if auth == "" {
		return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "missing token", nil)
	}
	tokenStr := auth
	if strings.HasPrefix(tokenStr, "Bearer ") {
		tokenStr = strings.TrimPrefix(tokenStr, "Bearer ")
	}

	parsed, err := jwt.ParseWithClaims(tokenStr, &SessionClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(config.Cfg.UserSystem.SessionSignToken), nil
	})
	if err != nil || !parsed.Valid {
		harukiLogger.Warnf("Invalid session token: %v", err)
		return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "invalid token", nil)
	}
	claims, ok := parsed.Claims.(*SessionClaims)
	if !ok {
		harukiLogger.Warnf("Invalid session claims")
		return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "invalid claims", nil)
	}

	key := claims.UserID + ":" + claims.SessionToken
	exists, err := s.RedisClient.Exists(context.Background(), key).Result()
	if err != nil {
		harukiLogger.Errorf("Redis error checking session: %v", err)
		return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "invalid session", nil)
	}
	if exists == 0 {
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
			harukiLogger.Errorf("Redis scan error: %v", err)
			return err
		}
		if len(keys) > 0 {
			if err := redisClient.Del(ctx, keys...).Err(); err != nil {
				harukiLogger.Errorf("Redis del error: %v", err)
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
