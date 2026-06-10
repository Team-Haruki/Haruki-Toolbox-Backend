package api

import (
	"strings"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"

	"net/http"

	"github.com/redis/go-redis/v9"
)

const (
	sessionRedisOperationTimeout = 2 * time.Second
	clearUserSessionsTimeout     = 10 * time.Second

	sessionProviderKratos = "kratos"

	defaultAuthProxyTrustedHeader       = "X-Auth-Proxy-Secret"
	defaultAuthProxySubjectHeader       = "X-Kratos-Identity-Id"
	defaultAuthProxyNameHeader          = "X-User-Name"
	defaultAuthProxyEmailHeader         = "X-User-Email"
	defaultAuthProxyEmailVerifiedHeader = "X-User-Email-Verified"
	defaultAuthProxyUserIDHeader        = "X-User-Id"

	defaultKratosSessionHeader = "X-Session-Token"
	defaultKratosSessionCookie = "ory_kratos_session"
	defaultKratosTimeout       = 10 * time.Second

	kratosProvisionUserIDAttempts          = 3
	kratosProvisionUserIDTimeModulo  int64 = 10000
	kratosProvisionUserIDRandomRange int64 = 1000000
)

func normalizeSessionProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", sessionProviderKratos, "local", "auto", "hybrid":
		return sessionProviderKratos
	default:
		return sessionProviderKratos
	}
}

func NewSessionHandler(redisClient *redis.Client, _ string) *SessionHandler {
	return &SessionHandler{
		RedisClient:                  redisClient,
		SessionProvider:              sessionProviderKratos,
		AuthProxyTrustedHeader:       defaultAuthProxyTrustedHeader,
		AuthProxySubjectHeader:       defaultAuthProxySubjectHeader,
		AuthProxyNameHeader:          defaultAuthProxyNameHeader,
		AuthProxyEmailHeader:         defaultAuthProxyEmailHeader,
		AuthProxyEmailVerifiedHeader: defaultAuthProxyEmailVerifiedHeader,
		AuthProxyUserIDHeader:        defaultAuthProxyUserIDHeader,
		KratosSessionHeader:          defaultKratosSessionHeader,
		KratosSessionCookie:          defaultKratosSessionCookie,
		KratosAutoLinkByEmail:        true,
		KratosAutoProvisionUser:      true,
		KratosRequestTimeout:         defaultKratosTimeout,
		KratosIdentityResolver:       nil,
	}
}

func (s *SessionHandler) ConfigureIdentityProvider(
	provider string,
	kratosPublicURL string,
	kratosAdminURL string,
	kratosSessionHeader string,
	kratosSessionCookie string,
	kratosAutoLinkByEmail bool,
	kratosAutoProvisionUser bool,
	kratosRequestTimeout time.Duration,
	dbClient *postgresql.Client,
) {
	s.SessionProvider = normalizeSessionProvider(provider)
	s.KratosPublicURL = strings.TrimSpace(kratosPublicURL)
	s.KratosAdminURL = strings.TrimSpace(kratosAdminURL)
	s.KratosSessionHeader = strings.TrimSpace(kratosSessionHeader)
	if s.KratosSessionHeader == "" {
		s.KratosSessionHeader = defaultKratosSessionHeader
	}
	s.KratosSessionCookie = strings.TrimSpace(kratosSessionCookie)
	if s.KratosSessionCookie == "" {
		s.KratosSessionCookie = defaultKratosSessionCookie
	}
	s.KratosAutoLinkByEmail = kratosAutoLinkByEmail
	s.KratosAutoProvisionUser = kratosAutoProvisionUser
	if kratosRequestTimeout <= 0 {
		kratosRequestTimeout = defaultKratosTimeout
	}
	s.KratosRequestTimeout = kratosRequestTimeout
	s.DBClient = dbClient
	s.KratosHTTPClient = nil
}

func (s *SessionHandler) ConfigureAuthProxy(
	enabled bool,
	trustedHeader string,
	trustedValue string,
	subjectHeader string,
	nameHeader string,
	emailHeader string,
	emailVerifiedHeader string,
	userIDHeader string,
) {
	s.AuthProxyEnabled = enabled
	s.AuthProxyTrustedHeader = strings.TrimSpace(trustedHeader)
	if s.AuthProxyTrustedHeader == "" {
		s.AuthProxyTrustedHeader = defaultAuthProxyTrustedHeader
	}
	s.AuthProxyTrustedValue = strings.TrimSpace(trustedValue)
	s.AuthProxySubjectHeader = strings.TrimSpace(subjectHeader)
	if s.AuthProxySubjectHeader == "" {
		s.AuthProxySubjectHeader = defaultAuthProxySubjectHeader
	}
	s.AuthProxyNameHeader = strings.TrimSpace(nameHeader)
	if s.AuthProxyNameHeader == "" {
		s.AuthProxyNameHeader = defaultAuthProxyNameHeader
	}
	s.AuthProxyEmailHeader = strings.TrimSpace(emailHeader)
	if s.AuthProxyEmailHeader == "" {
		s.AuthProxyEmailHeader = defaultAuthProxyEmailHeader
	}
	s.AuthProxyEmailVerifiedHeader = strings.TrimSpace(emailVerifiedHeader)
	if s.AuthProxyEmailVerifiedHeader == "" {
		s.AuthProxyEmailVerifiedHeader = defaultAuthProxyEmailVerifiedHeader
	}
	s.AuthProxyUserIDHeader = strings.TrimSpace(userIDHeader)
	if s.AuthProxyUserIDHeader == "" {
		s.AuthProxyUserIDHeader = defaultAuthProxyUserIDHeader
	}
}

func (s *SessionHandler) ConfigureAuthProxySessionHeader(sessionHeader string) {
	if s == nil {
		return
	}
	s.AuthProxySessionHeader = strings.TrimSpace(sessionHeader)
}

func (s *SessionHandler) UsesAuthProxy() bool {
	return s != nil && s.AuthProxyEnabled && strings.TrimSpace(s.AuthProxyTrustedHeader) != "" && strings.TrimSpace(s.AuthProxyTrustedValue) != ""
}

func (s *SessionHandler) UsesManagedBrowserAuth() bool {
	return s != nil && (s.UsesKratosProvider() || s.UsesAuthProxy())
}

func (s *SessionHandler) hasKratosProviderConfigured() bool {
	return strings.TrimSpace(s.KratosPublicURL) != ""
}

func (s *SessionHandler) kratosHTTPClient() *http.Client {
	if s.KratosHTTPClient != nil {
		return s.KratosHTTPClient
	}
	timeout := s.KratosRequestTimeout
	if timeout <= 0 {
		timeout = defaultKratosTimeout
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &kratosRequestMetadataTransport{
			base: http.DefaultTransport,
		},
	}
}

func (s *SessionHandler) UsesKratosProvider() bool {
	provider := normalizeSessionProvider(s.SessionProvider)
	return provider == sessionProviderKratos && s.hasKratosProviderConfigured()
}
