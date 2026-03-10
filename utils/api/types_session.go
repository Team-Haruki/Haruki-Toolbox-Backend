package api

import (
	"context"
	"haruki-suite/utils/database/postgresql"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
)

type SessionClaims struct {
	UserID       string `json:"userId"`
	SessionToken string `json:"sessionToken"`
	jwt.RegisteredClaims
}

type KratosSessionInfo struct {
	ID        string
	Active    bool
	ExpiresAt *time.Time
}

type SessionHandler struct {
	RedisClient *redis.Client

	SessionSignKey  string
	SessionProvider string

	KratosPublicURL         string
	KratosAdminURL          string
	KratosSessionHeader     string
	KratosSessionCookie     string
	KratosAutoLinkByEmail   bool
	KratosAutoProvisionUser bool
	KratosRequestTimeout    time.Duration
	KratosHTTPClient        *http.Client

	DBClient               *postgresql.Client
	KratosIdentityResolver func(ctx context.Context, identityID string, email string) (string, error)
}
