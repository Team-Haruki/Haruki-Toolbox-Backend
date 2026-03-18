package api

import (
	"context"
	"haruki-suite/utils/database/postgresql"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
)

type KratosSessionInfo struct {
	ID        string
	Active    bool
	ExpiresAt *time.Time
}

type SessionHandler struct {
	RedisClient *redis.Client

	SessionProvider string

	AuthProxyEnabled             bool
	AuthProxyTrustedHeader       string
	AuthProxyTrustedValue        string
	AuthProxySubjectHeader       string
	AuthProxyNameHeader          string
	AuthProxyEmailHeader         string
	AuthProxyEmailVerifiedHeader string
	AuthProxyUserIDHeader        string

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
