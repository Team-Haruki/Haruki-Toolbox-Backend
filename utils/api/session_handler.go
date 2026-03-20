package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	platformAuthHeader "haruki-suite/internal/platform/authheader"
	platformIdentity "haruki-suite/internal/platform/identity"
	"haruki-suite/utils/database/postgresql"
	userSchema "haruki-suite/utils/database/postgresql/user"
	harukiLogger "haruki-suite/utils/logger"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/gofiber/fiber/v3"
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

var (
	errSessionUnauthorized         = errors.New("session unauthorized")
	errSessionStoreUnavailable     = errors.New("session store unavailable")
	errIdentityProviderUnavailable = errors.New("identity provider unavailable")
	errUserStoreUnavailable        = errors.New("user store unavailable")
	errKratosIdentityUnmapped      = errors.New("kratos identity unmapped")
	errKratosSessionNotFound       = errors.New("kratos session not found")
	errKratosInvalidCredentials    = errors.New("kratos invalid credentials")
	errKratosIdentityConflict      = errors.New("kratos identity conflict")
	errKratosInvalidInput          = errors.New("kratos invalid input")
)

func IsSessionStoreUnavailableError(err error) bool {
	return errors.Is(err, errSessionStoreUnavailable)
}

func IsIdentityProviderUnavailableError(err error) bool {
	return errors.Is(err, errIdentityProviderUnavailable)
}

func IsKratosIdentityUnmappedError(err error) bool {
	return errors.Is(err, errKratosIdentityUnmapped)
}

func IsKratosSessionNotFoundError(err error) bool {
	return errors.Is(err, errKratosSessionNotFound)
}

func IsKratosInvalidCredentialsError(err error) bool {
	return errors.Is(err, errKratosInvalidCredentials)
}

func IsKratosIdentityConflictError(err error) bool {
	return errors.Is(err, errKratosIdentityConflict)
}

func IsKratosInvalidInputError(err error) bool {
	return errors.Is(err, errKratosInvalidInput)
}

type kratosRequestMetadataKey struct{}

type kratosRequestMetadata struct {
	UserAgent string
	ClientIP  string
}

func WithHTTPRequestMetadata(ctx context.Context, userAgent string, clientIP string) context.Context {
	return context.WithValue(ctx, kratosRequestMetadataKey{}, kratosRequestMetadata{
		UserAgent: strings.TrimSpace(userAgent),
		ClientIP:  strings.TrimSpace(clientIP),
	})
}

type kratosRequestMetadataTransport struct {
	base http.RoundTripper
}

func (t *kratosRequestMetadataTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	clone := req.Clone(req.Context())
	if metadata, ok := clone.Context().Value(kratosRequestMetadataKey{}).(kratosRequestMetadata); ok {
		if metadata.UserAgent != "" && strings.TrimSpace(clone.Header.Get("User-Agent")) == "" {
			clone.Header.Set("User-Agent", metadata.UserAgent)
		}
		if metadata.ClientIP != "" {
			if strings.TrimSpace(clone.Header.Get("X-Forwarded-For")) == "" {
				clone.Header.Set("X-Forwarded-For", metadata.ClientIP)
			}
			if strings.TrimSpace(clone.Header.Get("X-Real-IP")) == "" {
				clone.Header.Set("X-Real-IP", metadata.ClientIP)
			}
		}
	}
	return base.RoundTrip(clone)
}

type resolvedKratosSession struct {
	UserID        string
	IdentityID    string
	DisplayName   *string
	EmailVerified *bool
}

type kratosSessionWhoamiResponse struct {
	ID       string               `json:"id"`
	Active   bool                 `json:"active"`
	Identity kratosIdentityRecord `json:"identity"`
}

type kratosAdminSessionRecord struct {
	ID        string     `json:"id"`
	Active    bool       `json:"active"`
	ExpiresAt *time.Time `json:"expires_at"`
}

type kratosIdentityRecord struct {
	ID                  string                   `json:"id"`
	Traits              map[string]any           `json:"traits"`
	VerifiableAddresses []kratosVerifiableRecord `json:"verifiable_addresses"`
}

type kratosVerifiableRecord struct {
	Value    string `json:"value"`
	Verified bool   `json:"verified"`
	Status   string `json:"status"`
}

type kratosFlowResponse struct {
	ID string `json:"id"`
}

type kratosAuthSubmitResponse struct {
	SessionToken string `json:"session_token"`
}

type kratosRecoveryFlowSubmitResponse struct {
	State        string                   `json:"state"`
	ContinueWith []kratosContinueWithItem `json:"continue_with"`
}

type kratosContinueWithItem struct {
	Action          string `json:"action"`
	OrySessionToken string `json:"ory_session_token"`
}

type kratosErrorPayload struct {
	Error *struct {
		Reason string `json:"reason"`
		Text   string `json:"text"`
	} `json:"error"`
	UI *struct {
		Messages []struct {
			Text string `json:"text"`
		} `json:"messages"`
	} `json:"ui"`
}

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

func (s *SessionHandler) resolveUserIDByKratosIdentityID(ctx context.Context, identityID string) (string, error) {
	if s.DBClient == nil {
		return "", fmt.Errorf("%w: database client is nil", errUserStoreUnavailable)
	}
	identityID = strings.TrimSpace(identityID)
	if identityID == "" {
		return "", fmt.Errorf("%w: empty identity id", errKratosInvalidInput)
	}
	matchedUser, err := s.DBClient.User.Query().
		Where(userSchema.KratosIdentityIDEQ(identityID)).
		Select(userSchema.FieldID).
		Only(ctx)
	if err != nil {
		if postgresql.IsNotFound(err) {
			return "", fmt.Errorf("%w: identity is not linked", errKratosIdentityUnmapped)
		}
		return "", fmt.Errorf("%w: query kratos identity map: %v", errUserStoreUnavailable, err)
	}
	return strings.TrimSpace(matchedUser.ID), nil
}

func parseAuthProxyBooleanHeader(raw string) *bool {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return nil
	}
	return &parsed
}

func extractKratosIdentityName(identity kratosIdentityRecord) string {
	return extractNameFromTraitValue(identity.Traits)
}

func extractNameFromTraitValue(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		if direct, ok := typed["name"].(string); ok {
			if name := strings.TrimSpace(direct); name != "" {
				return name
			}
		}
		for _, child := range typed {
			if name := extractNameFromTraitValue(child); name != "" {
				return name
			}
		}
	case []any:
		for _, child := range typed {
			if name := extractNameFromTraitValue(child); name != "" {
				return name
			}
		}
	}
	return ""
}

func (s *SessionHandler) syncResolvedUserProfile(ctx context.Context, userID string, identityID string, email string, displayName *string) {
	if s == nil || s.DBClient == nil {
		return
	}

	userID = strings.TrimSpace(userID)
	if userID == "" {
		return
	}
	identityID = strings.TrimSpace(identityID)
	email = platformIdentity.NormalizeEmail(email)

	currentUser, err := s.DBClient.User.Query().
		Where(userSchema.IDEQ(userID)).
		Select(userSchema.FieldID, userSchema.FieldName, userSchema.FieldEmail, userSchema.FieldKratosIdentityID).
		Only(ctx)
	if err != nil {
		if !postgresql.IsNotFound(err) {
			harukiLogger.Warnf("Failed to query resolved user profile for sync: user=%s err=%v", userID, err)
		}
		return
	}

	update := s.DBClient.User.Update().Where(userSchema.IDEQ(userID))
	needsUpdate := false

	if identityID != "" {
		currentIdentityID := ""
		if currentUser.KratosIdentityID != nil {
			currentIdentityID = strings.TrimSpace(*currentUser.KratosIdentityID)
		}
		if currentIdentityID != identityID {
			update.SetKratosIdentityID(identityID)
			needsUpdate = true
		}
	}

	if email != "" && !strings.EqualFold(strings.TrimSpace(currentUser.Email), email) {
		update.SetEmail(email)
		needsUpdate = true
	}

	if displayName != nil {
		trimmedName := strings.TrimSpace(*displayName)
		if trimmedName != "" && strings.TrimSpace(currentUser.Name) != trimmedName {
			update.SetName(trimmedName)
			needsUpdate = true
		}
	}

	if !needsUpdate {
		return
	}

	if _, err := update.Save(ctx); err != nil {
		harukiLogger.Warnf("Failed to sync resolved user profile: user=%s identity=%s err=%v", userID, identityID, err)
	}
}

func (s *SessionHandler) verifyAuthProxySession(ctx context.Context, c fiber.Ctx) (string, string, *string, *bool, bool, error) {
	if !s.UsesAuthProxy() {
		return "", "", nil, nil, false, nil
	}
	if strings.TrimSpace(c.Get(s.AuthProxyTrustedHeader)) != strings.TrimSpace(s.AuthProxyTrustedValue) {
		return "", "", nil, nil, false, nil
	}

	identityID := strings.TrimSpace(c.Get(s.AuthProxySubjectHeader))
	displayName := strings.TrimSpace(c.Get(s.AuthProxyNameHeader))
	var displayNamePtr *string
	if displayName != "" {
		displayNamePtr = &displayName
	}
	email := platformIdentity.NormalizeEmail(c.Get(s.AuthProxyEmailHeader))
	emailVerified := parseAuthProxyBooleanHeader(c.Get(s.AuthProxyEmailVerifiedHeader))
	userID := strings.TrimSpace(c.Get(s.AuthProxyUserIDHeader))
	if userID == "" {
		if identityID == "" {
			return "", "", displayNamePtr, emailVerified, true, fmt.Errorf("%w: missing auth proxy subject header", errSessionUnauthorized)
		}
		resolvedUserID, err := s.resolveKratosIdentity(ctx, identityID, email)
		if err != nil {
			return "", "", displayNamePtr, emailVerified, true, err
		}
		userID = resolvedUserID
	}
	if identityID == "" {
		return "", "", displayNamePtr, emailVerified, true, nil
	}
	s.syncResolvedUserProfile(ctx, userID, identityID, email, displayNamePtr)
	return userID, identityID, displayNamePtr, emailVerified, true, nil
}

func (s *SessionHandler) ResolveUserIDFromKratosSession(ctx context.Context, sessionToken string, cookieHeader string) (string, error) {
	resolved, err := s.resolveKratosSession(ctx, sessionToken, cookieHeader)
	if err != nil {
		return "", err
	}
	return resolved.UserID, nil
}

func (s *SessionHandler) LoginWithKratosPassword(ctx context.Context, identifier string, password string) (string, error) {
	flowID, err := s.initKratosSelfServiceFlow(ctx, "/self-service/login/api")
	if err != nil {
		return "", err
	}

	payload := map[string]any{
		"method":     "password",
		"identifier": strings.TrimSpace(identifier),
		"password":   password,
	}
	return s.submitKratosSelfServiceFlow(ctx, "/self-service/login", flowID, payload)
}

func (s *SessionHandler) VerifyKratosPassword(ctx context.Context, identifier string, password string) error {
	sessionToken, err := s.LoginWithKratosPassword(ctx, identifier, password)
	if err != nil {
		return err
	}
	if err := s.RevokeKratosSessionByToken(ctx, sessionToken); err != nil {
		harukiLogger.Warnf("Failed to revoke temporary Kratos verification session: %v", err)
		return fmt.Errorf("%w: revoke temporary verification session failed: %v", errIdentityProviderUnavailable, err)
	}
	return nil
}

func (s *SessionHandler) VerifyKratosPasswordByIdentityID(ctx context.Context, identityID string, password string) error {
	identityID = strings.TrimSpace(identityID)
	if identityID == "" {
		return fmt.Errorf("%w: empty identity id", errKratosInvalidInput)
	}

	identity, err := s.fetchKratosIdentityByID(ctx, identityID)
	if err != nil {
		return err
	}
	identifier := platformIdentity.NormalizeEmail(extractKratosIdentityEmail(*identity))
	if identifier == "" {
		return fmt.Errorf("%w: identity email is empty", errKratosIdentityUnmapped)
	}
	return s.VerifyKratosPassword(ctx, identifier, password)
}

func (s *SessionHandler) RegisterWithKratosPassword(ctx context.Context, email string, password string, extraTraits map[string]any) (string, error) {
	flowID, err := s.initKratosSelfServiceFlow(ctx, "/self-service/registration/api")
	if err != nil {
		return "", err
	}

	traits := map[string]any{
		"email": platformIdentity.NormalizeEmail(email),
	}
	for k, v := range extraTraits {
		if strings.TrimSpace(k) == "" || k == "email" {
			continue
		}
		traits[k] = v
	}

	payload := map[string]any{
		"method":   "password",
		"password": password,
		"traits":   traits,
	}
	return s.submitKratosSelfServiceFlow(ctx, "/self-service/registration", flowID, payload)
}

func (s *SessionHandler) StartKratosRecoveryByEmail(ctx context.Context, email string) error {
	email = platformIdentity.NormalizeEmail(email)
	if email == "" {
		return fmt.Errorf("%w: empty email", errKratosInvalidInput)
	}
	return s.startKratosRecoveryByEmailWithMethod(ctx, email, "code")
}

func (s *SessionHandler) startKratosRecoveryByEmailWithMethod(ctx context.Context, email string, method string) error {
	flowID, err := s.initKratosSelfServiceFlow(ctx, "/self-service/recovery/api")
	if err != nil {
		return err
	}
	return s.submitKratosRecoveryFlow(ctx, flowID, method, email)
}

func (s *SessionHandler) UpdateKratosEmailByIdentityID(ctx context.Context, identityID string, email string) error {
	identityID = strings.TrimSpace(identityID)
	email = platformIdentity.NormalizeEmail(email)
	if identityID == "" {
		return fmt.Errorf("%w: empty identity id", errKratosInvalidInput)
	}
	if email == "" {
		return fmt.Errorf("%w: empty email", errKratosInvalidInput)
	}
	if strings.TrimSpace(s.KratosAdminURL) == "" {
		return fmt.Errorf("%w: kratos admin url is not configured", errIdentityProviderUnavailable)
	}

	if err := s.updateKratosEmailViaPatch(ctx, identityID, email); err == nil {
		return nil
	} else if !shouldFallbackFromKratosPatch(err) {
		return err
	}
	if err := s.updateKratosEmailViaPut(ctx, identityID, email); err == nil {
		return nil
	} else {
		return err
	}
}

func (s *SessionHandler) ListKratosSessionsByIdentityID(ctx context.Context, identityID string) ([]KratosSessionInfo, error) {
	identityID = strings.TrimSpace(identityID)
	if identityID == "" {
		return nil, fmt.Errorf("%w: empty identity id", errKratosInvalidInput)
	}
	if strings.TrimSpace(s.KratosAdminURL) == "" {
		return nil, fmt.Errorf("%w: kratos admin url is not configured", errIdentityProviderUnavailable)
	}

	endpoint, err := buildProviderEndpoint(s.KratosAdminURL, "/admin/identities/"+url.PathEscape(identityID)+"/sessions")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errIdentityProviderUnavailable, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build list sessions request: %v", errIdentityProviderUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := s.kratosHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: list sessions request failed: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: read list sessions response: %v", errIdentityProviderUnavailable, err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		var parsed []kratosAdminSessionRecord
		if err := sonic.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("%w: decode list sessions response: %v", errIdentityProviderUnavailable, err)
		}
		items := make([]KratosSessionInfo, 0, len(parsed))
		for _, row := range parsed {
			sessionID := strings.TrimSpace(row.ID)
			if sessionID == "" {
				continue
			}
			var expiresAt *time.Time
			if row.ExpiresAt != nil {
				expiresAtUTC := row.ExpiresAt.UTC()
				expiresAt = &expiresAtUTC
			}
			items = append(items, KratosSessionInfo{
				ID:        sessionID,
				Active:    row.Active,
				ExpiresAt: expiresAt,
			})
		}
		return items, nil
	case http.StatusNotFound:
		return nil, fmt.Errorf("%w: identity not found", errKratosIdentityUnmapped)
	default:
		if resp.StatusCode >= http.StatusInternalServerError {
			return nil, fmt.Errorf("%w: list sessions failed status=%d body=%s", errIdentityProviderUnavailable, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return nil, fmt.Errorf("%w: list sessions failed status=%d body=%s", errKratosInvalidInput, resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

func (s *SessionHandler) ResolveKratosSessionID(ctx context.Context, sessionToken string, cookieHeader string) (string, error) {
	sessionToken = strings.TrimSpace(sessionToken)
	cookieHeader = strings.TrimSpace(cookieHeader)
	if sessionToken == "" && cookieHeader == "" {
		return "", fmt.Errorf("%w: missing session token", errSessionUnauthorized)
	}

	whoami, err := s.fetchKratosWhoami(ctx, sessionToken, cookieHeader)
	if err != nil {
		return "", err
	}
	if !whoami.Active {
		return "", fmt.Errorf("%w: kratos session is not active", errSessionUnauthorized)
	}

	sessionID := strings.TrimSpace(whoami.ID)
	if sessionID == "" {
		return "", fmt.Errorf("%w: missing kratos session id", errKratosInvalidInput)
	}
	return sessionID, nil
}

func (s *SessionHandler) ResetKratosPasswordByRecoveryCode(ctx context.Context, recoveryCode string, newPassword string) (string, string, error) {
	recoveryCode = strings.TrimSpace(recoveryCode)
	if recoveryCode == "" {
		return "", "", fmt.Errorf("%w: empty recovery code", errKratosInvalidInput)
	}
	if strings.TrimSpace(newPassword) == "" {
		return "", "", fmt.Errorf("%w: empty password", errKratosInvalidInput)
	}

	sessionToken, err := s.verifyKratosRecoveryCode(ctx, recoveryCode)
	if err != nil {
		return "", "", err
	}

	whoami, err := s.fetchKratosWhoami(ctx, sessionToken, "")
	if err != nil {
		return "", "", err
	}
	if !whoami.Active {
		return "", "", fmt.Errorf("%w: kratos session is not active", errSessionUnauthorized)
	}
	identityID := strings.TrimSpace(whoami.Identity.ID)
	if identityID == "" {
		return "", "", fmt.Errorf("%w: empty identity id", errKratosIdentityUnmapped)
	}
	email := platformIdentity.NormalizeEmail(extractKratosIdentityEmail(whoami.Identity))
	userID, err := s.resolveKratosIdentity(ctx, identityID, email)
	if err != nil {
		return "", "", err
	}

	if err := s.UpdateKratosPasswordByIdentityID(ctx, identityID, newPassword); err != nil {
		return "", "", err
	}
	return userID, identityID, nil
}

func (s *SessionHandler) FindKratosIdentityIDByEmail(ctx context.Context, email string) (string, error) {
	email = platformIdentity.NormalizeEmail(email)
	if email == "" {
		return "", fmt.Errorf("%w: empty email", errKratosInvalidInput)
	}
	if strings.TrimSpace(s.KratosAdminURL) == "" {
		return "", fmt.Errorf("%w: kratos admin url is not configured", errIdentityProviderUnavailable)
	}

	targetURL, err := buildProviderEndpoint(s.KratosAdminURL, "/admin/identities")
	if err != nil {
		return "", fmt.Errorf("%w: %v", errIdentityProviderUnavailable, err)
	}
	endpoint, err := url.Parse(targetURL)
	if err != nil {
		return "", fmt.Errorf("%w: invalid admin identities url: %v", errIdentityProviderUnavailable, err)
	}
	query := endpoint.Query()
	query.Set("credentials_identifier", email)
	query.Set("page_size", "2")
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", fmt.Errorf("%w: build list identities request: %v", errIdentityProviderUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := s.kratosHTTPClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: list identities request failed: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("%w: read list identities response: %v", errIdentityProviderUnavailable, err)
	}
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode >= http.StatusInternalServerError {
			return "", fmt.Errorf("%w: list identities failed status=%d body=%s", errIdentityProviderUnavailable, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return "", fmt.Errorf("%w: list identities failed status=%d body=%s", errKratosInvalidInput, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var identities []kratosIdentityRecord
	if err := sonic.Unmarshal(body, &identities); err != nil {
		return "", fmt.Errorf("%w: decode list identities response: %v", errIdentityProviderUnavailable, err)
	}
	if len(identities) == 0 {
		return "", fmt.Errorf("%w: identity not found", errKratosIdentityUnmapped)
	}

	for _, identity := range identities {
		identityEmail := platformIdentity.NormalizeEmail(extractKratosIdentityEmail(identity))
		if identityEmail != email {
			continue
		}
		identityID := strings.TrimSpace(identity.ID)
		if identityID != "" {
			return identityID, nil
		}
	}
	return "", fmt.Errorf("%w: ambiguous or empty identity result for email", errKratosInvalidInput)
}

func (s *SessionHandler) UpdateKratosPasswordByIdentityID(ctx context.Context, identityID string, newPassword string) error {
	identityID = strings.TrimSpace(identityID)
	if identityID == "" {
		return fmt.Errorf("%w: empty identity id", errKratosInvalidInput)
	}
	if strings.TrimSpace(newPassword) == "" {
		return fmt.Errorf("%w: empty password", errKratosInvalidInput)
	}
	if strings.TrimSpace(s.KratosAdminURL) == "" {
		return fmt.Errorf("%w: kratos admin url is not configured", errIdentityProviderUnavailable)
	}

	if err := s.updateKratosPasswordViaPatch(ctx, identityID, newPassword); err == nil {
		return nil
	} else if !shouldFallbackFromKratosPatch(err) {
		return err
	}
	if err := s.updateKratosPasswordViaPut(ctx, identityID, newPassword); err == nil {
		return nil
	} else {
		return err
	}
}

func (s *SessionHandler) RevokeKratosSessionsByIdentityID(ctx context.Context, identityID string) error {
	identityID = strings.TrimSpace(identityID)
	if identityID == "" {
		return fmt.Errorf("%w: empty identity id", errKratosInvalidInput)
	}
	if strings.TrimSpace(s.KratosAdminURL) == "" {
		return fmt.Errorf("%w: kratos admin url is not configured", errIdentityProviderUnavailable)
	}

	endpoint, err := buildProviderEndpoint(s.KratosAdminURL, "/admin/identities/"+url.PathEscape(identityID)+"/sessions")
	if err != nil {
		return fmt.Errorf("%w: %v", errIdentityProviderUnavailable, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return fmt.Errorf("%w: build revoke sessions request: %v", errIdentityProviderUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := s.kratosHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("%w: revoke sessions request failed: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%w: read revoke sessions response: %v", errIdentityProviderUnavailable, err)
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusAccepted, http.StatusNoContent:
		return nil
	case http.StatusNotFound:
		return fmt.Errorf("%w: identity not found", errKratosIdentityUnmapped)
	default:
		if resp.StatusCode >= http.StatusInternalServerError {
			return fmt.Errorf("%w: revoke sessions failed status=%d body=%s", errIdentityProviderUnavailable, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return fmt.Errorf("%w: revoke sessions failed status=%d body=%s", errKratosInvalidInput, resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

func (s *SessionHandler) fetchKratosIdentityByID(ctx context.Context, identityID string) (*kratosIdentityRecord, error) {
	identityID = strings.TrimSpace(identityID)
	if identityID == "" {
		return nil, fmt.Errorf("%w: empty identity id", errKratosInvalidInput)
	}
	if strings.TrimSpace(s.KratosAdminURL) == "" {
		return nil, fmt.Errorf("%w: kratos admin url is not configured", errIdentityProviderUnavailable)
	}

	endpoint, err := buildProviderEndpoint(s.KratosAdminURL, "/admin/identities/"+url.PathEscape(identityID))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errIdentityProviderUnavailable, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build get identity request: %v", errIdentityProviderUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := s.kratosHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: get identity request failed: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: read get identity response: %v", errIdentityProviderUnavailable, err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: identity not found", errKratosIdentityUnmapped)
	}
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode >= http.StatusInternalServerError {
			return nil, fmt.Errorf("%w: get identity failed status=%d body=%s", errIdentityProviderUnavailable, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return nil, fmt.Errorf("%w: get identity failed status=%d body=%s", errKratosInvalidInput, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var identity kratosIdentityRecord
	if err := sonic.Unmarshal(body, &identity); err != nil {
		return nil, fmt.Errorf("%w: decode identity response: %v", errIdentityProviderUnavailable, err)
	}
	if strings.TrimSpace(identity.ID) == "" {
		identity.ID = identityID
	}
	return &identity, nil
}

func (s *SessionHandler) RevokeKratosSessionByToken(ctx context.Context, sessionToken string) error {
	sessionToken = strings.TrimSpace(sessionToken)
	if sessionToken == "" {
		return fmt.Errorf("%w: empty session token", errKratosInvalidInput)
	}
	whoami, err := s.fetchKratosWhoami(ctx, sessionToken, "")
	if err != nil {
		return err
	}
	sessionID := strings.TrimSpace(whoami.ID)
	if sessionID == "" {
		return fmt.Errorf("%w: missing kratos session id", errKratosInvalidInput)
	}
	return s.RevokeKratosSessionByID(ctx, sessionID)
}

func (s *SessionHandler) RevokeKratosSessionByID(ctx context.Context, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("%w: empty session id", errKratosInvalidInput)
	}
	if strings.TrimSpace(s.KratosAdminURL) == "" {
		return fmt.Errorf("%w: kratos admin url is not configured", errIdentityProviderUnavailable)
	}

	endpoint, err := buildProviderEndpoint(s.KratosAdminURL, "/admin/sessions/"+url.PathEscape(sessionID))
	if err != nil {
		return fmt.Errorf("%w: %v", errIdentityProviderUnavailable, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return fmt.Errorf("%w: build revoke session request: %v", errIdentityProviderUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := s.kratosHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("%w: revoke session request failed: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%w: read revoke session response: %v", errIdentityProviderUnavailable, err)
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusAccepted, http.StatusNoContent:
		return nil
	case http.StatusNotFound:
		return fmt.Errorf("%w: session not found", errKratosSessionNotFound)
	default:
		if resp.StatusCode >= http.StatusInternalServerError {
			return fmt.Errorf("%w: revoke session failed status=%d body=%s", errIdentityProviderUnavailable, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return fmt.Errorf("%w: revoke session failed status=%d body=%s", errKratosInvalidInput, resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

func (s *SessionHandler) VerifySessionToken(c fiber.Ctx) error {
	authHeader := c.Get("Authorization")
	bearerToken, hasBearerToken := platformAuthHeader.ExtractBearerToken(authHeader)
	kratosHeaderToken := strings.TrimSpace(c.Get(s.KratosSessionHeader))
	cookieHeader := strings.TrimSpace(c.Get("Cookie"))

	applyResolvedUserIdentity := func(userID string, identityID string, displayName *string, emailVerified *bool) error {
		userID = strings.TrimSpace(userID)
		if userID == "" {
			return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "invalid user session", nil)
		}
		toolboxUserID := strings.TrimSpace(c.Params("toolbox_user_id"))
		if toolboxUserID != "" && toolboxUserID != userID {
			return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "user ID mismatch", nil)
		}
		c.Locals("userID", userID)
		if trimmedIdentityID := strings.TrimSpace(identityID); trimmedIdentityID != "" {
			c.Locals("identityID", trimmedIdentityID)
		}
		if displayName != nil && strings.TrimSpace(*displayName) != "" {
			c.Locals("displayName", strings.TrimSpace(*displayName))
		}
		if emailVerified != nil {
			c.Locals("emailVerified", *emailVerified)
		}
		return c.Next()
	}

	requestCtx := WithHTTPRequestMetadata(c.Context(), c.Get("User-Agent"), c.IP())
	if proxyUserID, proxyIdentityID, proxyDisplayName, proxyEmailVerified, handled, err := s.verifyAuthProxySession(requestCtx, c); handled {
		if err != nil {
			return respondSessionVerifyError(c, err)
		}
		if sessionHeader := strings.TrimSpace(s.AuthProxySessionHeader); sessionHeader != "" {
			if sessionID := strings.TrimSpace(c.Get(sessionHeader)); sessionID != "" {
				c.Locals("authProxySessionID", sessionID)
			}
		}
		return applyResolvedUserIdentity(proxyUserID, proxyIdentityID, proxyDisplayName, proxyEmailVerified)
	}
	if s.UsesAuthProxy() {
		return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "missing auth proxy identity", nil)
	}

	if !hasBearerToken && kratosHeaderToken == "" && cookieHeader == "" {
		return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "missing token", nil)
	}

	if !s.hasKratosProviderConfigured() {
		return respondSessionVerifyError(c, fmt.Errorf("%w: kratos public url is not configured", errIdentityProviderUnavailable))
	}
	kratosToken := firstNonEmpty(kratosHeaderToken, bearerToken)
	resolved, err := s.resolveKratosSession(requestCtx, kratosToken, cookieHeader)
	if err != nil {
		return respondSessionVerifyError(c, err)
	}
	return applyResolvedUserIdentity(resolved.UserID, resolved.IdentityID, resolved.DisplayName, resolved.EmailVerified)
}

func respondSessionVerifyError(c fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, errSessionStoreUnavailable):
		harukiLogger.Errorf("Session store unavailable: %v", err)
		return UpdatedDataResponse[string](c, fiber.StatusServiceUnavailable, "session store unavailable", nil)
	case errors.Is(err, errIdentityProviderUnavailable):
		harukiLogger.Errorf("Identity provider unavailable: %v", err)
		return UpdatedDataResponse[string](c, fiber.StatusServiceUnavailable, "identity provider unavailable", nil)
	case errors.Is(err, errUserStoreUnavailable):
		harukiLogger.Errorf("User store unavailable: %v", err)
		return UpdatedDataResponse[string](c, fiber.StatusServiceUnavailable, "user store unavailable", nil)
	case errors.Is(err, errKratosIdentityUnmapped):
		return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "invalid user session", nil)
	default:
		return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "invalid token", nil)
	}
}

func (s *SessionHandler) verifyKratosSession(ctx context.Context, sessionToken string, cookieHeader string) (string, error) {
	resolved, err := s.resolveKratosSession(ctx, sessionToken, cookieHeader)
	if err != nil {
		return "", err
	}
	return resolved.UserID, nil
}

func (s *SessionHandler) resolveKratosSession(ctx context.Context, sessionToken string, cookieHeader string) (*resolvedKratosSession, error) {
	if !s.hasKratosProviderConfigured() {
		return nil, fmt.Errorf("%w: kratos public url is not configured", errIdentityProviderUnavailable)
	}
	sessionToken = strings.TrimSpace(sessionToken)
	cookieHeader = strings.TrimSpace(cookieHeader)
	if sessionToken == "" && cookieHeader == "" {
		return nil, fmt.Errorf("%w: missing session token", errSessionUnauthorized)
	}

	whoami, err := s.fetchKratosWhoami(ctx, sessionToken, cookieHeader)
	if err != nil {
		return nil, err
	}
	if !whoami.Active {
		return nil, fmt.Errorf("%w: kratos session is not active", errSessionUnauthorized)
	}
	identityID := strings.TrimSpace(whoami.Identity.ID)
	if identityID == "" {
		return nil, fmt.Errorf("%w: kratos identity id is empty", errSessionUnauthorized)
	}
	displayName := strings.TrimSpace(extractKratosIdentityName(whoami.Identity))
	var displayNamePtr *string
	if displayName != "" {
		displayNamePtr = &displayName
	}
	email := platformIdentity.NormalizeEmail(extractKratosIdentityEmail(whoami.Identity))
	emailVerified := extractKratosIdentityEmailVerification(whoami.Identity)
	userID, err := s.resolveKratosIdentity(ctx, identityID, email)
	if err != nil {
		return nil, err
	}
	s.syncResolvedUserProfile(ctx, userID, identityID, email, displayNamePtr)
	return &resolvedKratosSession{
		UserID:        userID,
		IdentityID:    identityID,
		DisplayName:   displayNamePtr,
		EmailVerified: emailVerified,
	}, nil
}

func extractKratosIdentityEmail(identity kratosIdentityRecord) string {
	if direct := extractEmailFromTraitValue(identity.Traits); direct != "" {
		return direct
	}
	for _, address := range identity.VerifiableAddresses {
		if value := strings.TrimSpace(address.Value); value != "" {
			return value
		}
	}
	return ""
}

func extractKratosIdentityEmailVerification(identity kratosIdentityRecord) *bool {
	traitsEmail := platformIdentity.NormalizeEmail(extractEmailFromTraitValue(identity.Traits))
	for _, address := range identity.VerifiableAddresses {
		addressEmail := platformIdentity.NormalizeEmail(address.Value)
		if addressEmail == "" {
			continue
		}
		if traitsEmail != "" && addressEmail != traitsEmail {
			continue
		}

		verified := address.Verified
		if !verified && strings.EqualFold(strings.TrimSpace(address.Status), "completed") {
			verified = true
		}
		return &verified
	}
	return nil
}

func extractEmailFromTraitValue(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		if direct, ok := typed["email"].(string); ok {
			if email := strings.TrimSpace(direct); email != "" {
				return email
			}
		}
		for _, child := range typed {
			if email := extractEmailFromTraitValue(child); email != "" {
				return email
			}
		}
	case []any:
		for _, child := range typed {
			if email := extractEmailFromTraitValue(child); email != "" {
				return email
			}
		}
	case string:
		if email := strings.TrimSpace(typed); strings.Contains(email, "@") {
			return email
		}
	}
	return ""
}

func (s *SessionHandler) resolveKratosIdentity(ctx context.Context, identityID string, email string) (string, error) {
	if s.KratosIdentityResolver != nil {
		return s.KratosIdentityResolver(ctx, identityID, email)
	}
	if s.DBClient == nil {
		return "", fmt.Errorf("%w: database client is nil", errUserStoreUnavailable)
	}

	identityID = strings.TrimSpace(identityID)
	if identityID == "" {
		return "", fmt.Errorf("%w: identity id is empty", errSessionUnauthorized)
	}

	matchedByIdentity, err := s.DBClient.User.Query().
		Where(userSchema.KratosIdentityIDEQ(identityID)).
		Select(userSchema.FieldID).
		Only(ctx)
	if err == nil {
		return matchedByIdentity.ID, nil
	}
	if err != nil && !postgresql.IsNotFound(err) {
		return "", fmt.Errorf("%w: query kratos identity map: %v", errUserStoreUnavailable, err)
	}

	if email == "" {
		return "", fmt.Errorf("%w: identity email is empty", errKratosIdentityUnmapped)
	}

	targetUser, err := s.DBClient.User.Query().
		Where(userSchema.EmailEqualFold(email)).
		Select(userSchema.FieldID, userSchema.FieldKratosIdentityID).
		Only(ctx)
	if err != nil {
		if postgresql.IsNotFound(err) {
			if !s.KratosAutoProvisionUser {
				return "", fmt.Errorf("%w: email is not linked", errKratosIdentityUnmapped)
			}
			provisionedUserID, provisionErr := s.createKratosProvisionedUser(ctx, identityID, email)
			if provisionErr == nil {
				return provisionedUserID, nil
			}
			matchedByIdentity, retryErr := s.DBClient.User.Query().
				Where(userSchema.KratosIdentityIDEQ(identityID)).
				Select(userSchema.FieldID).
				Only(ctx)
			if retryErr == nil {
				return matchedByIdentity.ID, nil
			}
			if retryErr != nil && !postgresql.IsNotFound(retryErr) {
				return "", fmt.Errorf("%w: re-query kratos identity map: %v", errUserStoreUnavailable, retryErr)
			}
			return "", provisionErr
		}
		return "", fmt.Errorf("%w: query user by email: %v", errUserStoreUnavailable, err)
	}

	if targetUser.KratosIdentityID != nil {
		boundIdentityID := strings.TrimSpace(*targetUser.KratosIdentityID)
		if boundIdentityID == identityID {
			return targetUser.ID, nil
		}
		if boundIdentityID != "" {
			return "", fmt.Errorf("%w: email already linked to another identity", errKratosIdentityUnmapped)
		}
	}
	if !s.KratosAutoLinkByEmail {
		return "", fmt.Errorf("%w: auto-link by email is disabled", errKratosIdentityUnmapped)
	}

	_, err = s.DBClient.User.Update().
		Where(userSchema.IDEQ(targetUser.ID)).
		SetKratosIdentityID(identityID).
		Save(ctx)
	if err != nil {
		if postgresql.IsConstraintError(err) {
			return "", fmt.Errorf("%w: identity already linked", errKratosIdentityUnmapped)
		}
		return "", fmt.Errorf("%w: bind identity to user: %v", errUserStoreUnavailable, err)
	}
	return targetUser.ID, nil
}

func (s *SessionHandler) createKratosProvisionedUser(ctx context.Context, identityID string, email string) (string, error) {
	if s.DBClient == nil {
		return "", fmt.Errorf("%w: database client is nil", errUserStoreUnavailable)
	}
	identityID = strings.TrimSpace(identityID)
	email = platformIdentity.NormalizeEmail(email)
	if identityID == "" || email == "" {
		return "", fmt.Errorf("%w: missing identity data", errKratosIdentityUnmapped)
	}

	for range kratosProvisionUserIDAttempts {
		uid, err := generateProvisionedUserID(time.Now().UTC())
		if err != nil {
			return "", fmt.Errorf("%w: generate user id: %v", errUserStoreUnavailable, err)
		}
		uploadCode, err := generateProvisionedUploadCode()
		if err != nil {
			return "", fmt.Errorf("%w: generate upload code: %v", errUserStoreUnavailable, err)
		}

		tx, err := s.DBClient.Tx(ctx)
		if err != nil {
			return "", fmt.Errorf("%w: start transaction: %v", errUserStoreUnavailable, err)
		}

		_, err = tx.User.Create().
			SetID(uid).
			SetName(deriveProvisionedUserName(email)).
			SetEmail(email).
			SetNillableAvatarPath(nil).
			SetKratosIdentityID(identityID).
			SetCreatedAt(time.Now().UTC()).
			Save(ctx)
		if err != nil {
			_ = tx.Rollback()
			if postgresql.IsConstraintError(err) {
				continue
			}
			return "", fmt.Errorf("%w: create user: %v", errUserStoreUnavailable, err)
		}

		if _, err := tx.IOSScriptCode.Create().
			SetUserID(uid).
			SetUploadCode(uploadCode).
			Save(ctx); err != nil {
			_ = tx.Rollback()
			if postgresql.IsConstraintError(err) {
				continue
			}
			return "", fmt.Errorf("%w: create iOS upload code: %v", errUserStoreUnavailable, err)
		}

		if err := tx.Commit(); err != nil {
			_ = tx.Rollback()
			return "", fmt.Errorf("%w: commit transaction: %v", errUserStoreUnavailable, err)
		}
		return uid, nil
	}

	return "", fmt.Errorf("%w: exhausted retries while creating user", errUserStoreUnavailable)
}

func deriveProvisionedUserName(email string) string {
	email = strings.TrimSpace(email)
	if email == "" {
		return "kratos-user"
	}
	parts := strings.SplitN(email, "@", 2)
	candidate := strings.TrimSpace(parts[0])
	if candidate == "" {
		return email
	}
	return candidate
}

func generateProvisionedUploadCode() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", raw), nil
}

func generateProvisionedUserID(now time.Time) (string, error) {
	tsSuffix := now.UnixMicro() % kratosProvisionUserIDTimeModulo
	randomPart, err := rand.Int(rand.Reader, big.NewInt(kratosProvisionUserIDRandomRange))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%04d%06d", tsSuffix, randomPart.Int64()), nil
}

func (s *SessionHandler) fetchKratosWhoami(ctx context.Context, sessionToken string, cookieHeader string) (*kratosSessionWhoamiResponse, error) {
	whoamiURL, err := buildProviderEndpoint(s.KratosPublicURL, "/sessions/whoami")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errIdentityProviderUnavailable, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, whoamiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", errIdentityProviderUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")
	if sessionToken != "" {
		req.Header.Set(s.KratosSessionHeader, sessionToken)
	}
	if cookieHeader != "" {
		req.Header.Set("Cookie", cookieHeader)
	}

	resp, err := s.kratosHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: request whoami: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: read whoami response: %v", errIdentityProviderUnavailable, err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		var parsed kratosSessionWhoamiResponse
		if err := sonic.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("%w: decode whoami payload: %v", errIdentityProviderUnavailable, err)
		}
		return &parsed, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, fmt.Errorf("%w: status %d", errSessionUnauthorized, resp.StatusCode)
	case http.StatusNotFound:
		return nil, fmt.Errorf("%w: whoami endpoint returned 404", errIdentityProviderUnavailable)
	default:
		if resp.StatusCode >= http.StatusInternalServerError {
			return nil, fmt.Errorf("%w: status %d", errIdentityProviderUnavailable, resp.StatusCode)
		}
		return nil, fmt.Errorf("%w: status %d", errSessionUnauthorized, resp.StatusCode)
	}
}

func (s *SessionHandler) initKratosSelfServiceFlow(ctx context.Context, initPath string) (string, error) {
	if !s.hasKratosProviderConfigured() {
		return "", fmt.Errorf("%w: kratos public url is not configured", errIdentityProviderUnavailable)
	}
	targetURL, err := buildProviderEndpoint(s.KratosPublicURL, initPath)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errIdentityProviderUnavailable, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return "", fmt.Errorf("%w: build request: %v", errIdentityProviderUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := s.kratosHTTPClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: init flow request failed: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("%w: read init flow response: %v", errIdentityProviderUnavailable, err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", classifyKratosFlowError(resp.StatusCode, body)
	}

	var parsed kratosFlowResponse
	if err := sonic.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("%w: decode init flow response: %v", errIdentityProviderUnavailable, err)
	}
	flowID := strings.TrimSpace(parsed.ID)
	if flowID == "" {
		return "", fmt.Errorf("%w: empty flow id", errIdentityProviderUnavailable)
	}
	return flowID, nil
}

func (s *SessionHandler) submitKratosSelfServiceFlow(ctx context.Context, submitPath string, flowID string, payload map[string]any) (string, error) {
	if !s.hasKratosProviderConfigured() {
		return "", fmt.Errorf("%w: kratos public url is not configured", errIdentityProviderUnavailable)
	}
	targetURL, err := buildProviderEndpoint(s.KratosPublicURL, submitPath)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errIdentityProviderUnavailable, err)
	}

	endpoint, err := url.Parse(targetURL)
	if err != nil {
		return "", fmt.Errorf("%w: invalid submit url: %v", errIdentityProviderUnavailable, err)
	}
	query := endpoint.Query()
	query.Set("flow", strings.TrimSpace(flowID))
	endpoint.RawQuery = query.Encode()

	encoded, err := sonic.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("%w: encode payload: %v", errIdentityProviderUnavailable, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(encoded))
	if err != nil {
		return "", fmt.Errorf("%w: build submit request: %v", errIdentityProviderUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.kratosHTTPClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: submit flow request failed: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("%w: read submit response: %v", errIdentityProviderUnavailable, err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", classifyKratosFlowError(resp.StatusCode, body)
	}

	var parsed kratosAuthSubmitResponse
	if err := sonic.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("%w: decode submit response: %v", errIdentityProviderUnavailable, err)
	}
	sessionToken := strings.TrimSpace(parsed.SessionToken)
	if sessionToken == "" {
		return "", fmt.Errorf("%w: empty session token in response", errIdentityProviderUnavailable)
	}
	return sessionToken, nil
}

func (s *SessionHandler) submitKratosRecoveryFlow(ctx context.Context, flowID string, method string, email string) error {
	if !s.hasKratosProviderConfigured() {
		return fmt.Errorf("%w: kratos public url is not configured", errIdentityProviderUnavailable)
	}
	targetURL, err := buildProviderEndpoint(s.KratosPublicURL, "/self-service/recovery")
	if err != nil {
		return fmt.Errorf("%w: %v", errIdentityProviderUnavailable, err)
	}
	endpoint, err := url.Parse(targetURL)
	if err != nil {
		return fmt.Errorf("%w: invalid submit url: %v", errIdentityProviderUnavailable, err)
	}
	query := endpoint.Query()
	query.Set("flow", strings.TrimSpace(flowID))
	endpoint.RawQuery = query.Encode()

	encoded, err := sonic.Marshal(map[string]any{
		"method": method,
		"email":  email,
	})
	if err != nil {
		return fmt.Errorf("%w: encode payload: %v", errIdentityProviderUnavailable, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("%w: build submit request: %v", errIdentityProviderUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.kratosHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("%w: submit flow request failed: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%w: read submit response: %v", errIdentityProviderUnavailable, err)
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusSeeOther:
		return nil
	case http.StatusGone:
		return fmt.Errorf("%w: status=%d reason=%s", errKratosInvalidInput, resp.StatusCode, strings.TrimSpace(string(body)))
	default:
		return classifyKratosFlowError(resp.StatusCode, body)
	}
}

func (s *SessionHandler) verifyKratosRecoveryCode(ctx context.Context, recoveryCode string) (string, error) {
	if !s.hasKratosProviderConfigured() {
		return "", fmt.Errorf("%w: kratos public url is not configured", errIdentityProviderUnavailable)
	}
	flowID, err := s.initKratosSelfServiceFlow(ctx, "/self-service/recovery/api")
	if err != nil {
		return "", err
	}

	targetURL, err := buildProviderEndpoint(s.KratosPublicURL, "/self-service/recovery")
	if err != nil {
		return "", fmt.Errorf("%w: %v", errIdentityProviderUnavailable, err)
	}
	endpoint, err := url.Parse(targetURL)
	if err != nil {
		return "", fmt.Errorf("%w: invalid submit url: %v", errIdentityProviderUnavailable, err)
	}
	query := endpoint.Query()
	query.Set("flow", strings.TrimSpace(flowID))
	endpoint.RawQuery = query.Encode()

	encoded, err := sonic.Marshal(map[string]any{
		"method": "code",
		"code":   strings.TrimSpace(recoveryCode),
	})
	if err != nil {
		return "", fmt.Errorf("%w: encode payload: %v", errIdentityProviderUnavailable, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(encoded))
	if err != nil {
		return "", fmt.Errorf("%w: build submit request: %v", errIdentityProviderUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.kratosHTTPClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: submit flow request failed: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("%w: read submit response: %v", errIdentityProviderUnavailable, err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", classifyKratosFlowError(resp.StatusCode, body)
	}

	var parsed kratosRecoveryFlowSubmitResponse
	if err := sonic.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("%w: decode recovery response: %v", errIdentityProviderUnavailable, err)
	}
	sessionToken := strings.TrimSpace(extractSessionTokenFromRecoveryPayload(body, parsed))
	if sessionToken == "" {
		return "", fmt.Errorf("%w: missing session token after recovery (state=%s)", errKratosInvalidInput, strings.TrimSpace(parsed.State))
	}
	return sessionToken, nil
}

func classifyKratosFlowError(statusCode int, body []byte) error {
	bodyText := strings.TrimSpace(string(body))
	var payload kratosErrorPayload
	_ = sonic.Unmarshal(body, &payload)
	reason := strings.TrimSpace(extractKratosErrorReason(payload, bodyText))
	reasonLower := strings.ToLower(reason)

	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: %s", errKratosInvalidCredentials, reason)
	case http.StatusConflict:
		return fmt.Errorf("%w: %s", errKratosIdentityConflict, reason)
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		if strings.Contains(reasonLower, "already exists") ||
			strings.Contains(reasonLower, "exists already") ||
			strings.Contains(reasonLower, "already been registered") {
			return fmt.Errorf("%w: %s", errKratosIdentityConflict, reason)
		}
		if strings.Contains(reasonLower, "identifier") || strings.Contains(reasonLower, "password") || strings.Contains(reasonLower, "trait") {
			return fmt.Errorf("%w: %s", errKratosInvalidInput, reason)
		}
		return fmt.Errorf("%w: %s", errKratosInvalidCredentials, reason)
	default:
		if statusCode >= http.StatusInternalServerError {
			return fmt.Errorf("%w: status=%d reason=%s", errIdentityProviderUnavailable, statusCode, reason)
		}
		return fmt.Errorf("%w: status=%d reason=%s", errKratosInvalidInput, statusCode, reason)
	}
}

func extractKratosErrorReason(payload kratosErrorPayload, fallback string) string {
	if payload.Error != nil {
		if text := strings.TrimSpace(payload.Error.Reason); text != "" {
			return text
		}
		if text := strings.TrimSpace(payload.Error.Text); text != "" {
			return text
		}
	}
	if payload.UI != nil {
		for _, item := range payload.UI.Messages {
			if text := strings.TrimSpace(item.Text); text != "" {
				return text
			}
		}
	}
	if fallback != "" {
		return fallback
	}
	return "kratos flow request failed"
}

func (s *SessionHandler) updateKratosPasswordViaPatch(ctx context.Context, identityID string, newPassword string) error {
	endpoint, err := buildProviderEndpoint(s.KratosAdminURL, "/admin/identities/"+url.PathEscape(identityID))
	if err != nil {
		return fmt.Errorf("%w: %v", errIdentityProviderUnavailable, err)
	}
	patchPayload := []map[string]any{
		{
			"op":    "replace",
			"path":  "/credentials/password/config/password",
			"value": newPassword,
		},
	}
	encoded, err := sonic.Marshal(patchPayload)
	if err != nil {
		return fmt.Errorf("%w: encode patch payload: %v", errIdentityProviderUnavailable, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("%w: build patch request: %v", errIdentityProviderUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.kratosHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("%w: patch request failed: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%w: read patch response: %v", errIdentityProviderUnavailable, err)
	}
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: identity not found", errKratosIdentityUnmapped)
	}
	// PATCH may be unsupported by some Kratos versions/setups; caller may fall back to PUT.
	if resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusUnsupportedMediaType || resp.StatusCode == http.StatusBadRequest {
		return fmt.Errorf("%w: patch strategy unsupported", errKratosInvalidInput)
	}
	if resp.StatusCode >= http.StatusInternalServerError {
		return fmt.Errorf("%w: patch failed status=%d body=%s", errIdentityProviderUnavailable, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return fmt.Errorf("%w: patch failed status=%d body=%s", errKratosInvalidInput, resp.StatusCode, strings.TrimSpace(string(body)))
}

func (s *SessionHandler) updateKratosEmailViaPatch(ctx context.Context, identityID string, email string) error {
	endpoint, err := buildProviderEndpoint(s.KratosAdminURL, "/admin/identities/"+url.PathEscape(identityID))
	if err != nil {
		return fmt.Errorf("%w: %v", errIdentityProviderUnavailable, err)
	}
	patchPayload := []map[string]any{
		{
			"op":    "replace",
			"path":  "/traits/email",
			"value": email,
		},
	}
	encoded, err := sonic.Marshal(patchPayload)
	if err != nil {
		return fmt.Errorf("%w: encode patch payload: %v", errIdentityProviderUnavailable, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("%w: build patch request: %v", errIdentityProviderUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.kratosHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("%w: patch request failed: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%w: read patch response: %v", errIdentityProviderUnavailable, err)
	}
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: identity not found", errKratosIdentityUnmapped)
	}
	if resp.StatusCode == http.StatusConflict {
		return fmt.Errorf("%w: email already in use", errKratosIdentityConflict)
	}
	if resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusUnsupportedMediaType || resp.StatusCode == http.StatusBadRequest {
		return fmt.Errorf("%w: patch strategy unsupported", errKratosInvalidInput)
	}
	if resp.StatusCode >= http.StatusInternalServerError {
		return fmt.Errorf("%w: patch failed status=%d body=%s", errIdentityProviderUnavailable, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return fmt.Errorf("%w: patch failed status=%d body=%s", errKratosInvalidInput, resp.StatusCode, strings.TrimSpace(string(body)))
}

func (s *SessionHandler) updateKratosPasswordViaPut(ctx context.Context, identityID string, newPassword string) error {
	endpoint, err := buildProviderEndpoint(s.KratosAdminURL, "/admin/identities/"+url.PathEscape(identityID))
	if err != nil {
		return fmt.Errorf("%w: %v", errIdentityProviderUnavailable, err)
	}

	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("%w: build get identity request: %v", errIdentityProviderUnavailable, err)
	}
	getReq.Header.Set("Accept", "application/json")

	getResp, err := s.kratosHTTPClient().Do(getReq)
	if err != nil {
		return fmt.Errorf("%w: get identity request failed: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(getResp.Body)
	getBody, err := io.ReadAll(getResp.Body)
	if err != nil {
		return fmt.Errorf("%w: read get identity response: %v", errIdentityProviderUnavailable, err)
	}
	if getResp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: identity not found", errKratosIdentityUnmapped)
	}
	if getResp.StatusCode != http.StatusOK {
		if getResp.StatusCode >= http.StatusInternalServerError {
			return fmt.Errorf("%w: get identity failed status=%d body=%s", errIdentityProviderUnavailable, getResp.StatusCode, strings.TrimSpace(string(getBody)))
		}
		return fmt.Errorf("%w: get identity failed status=%d body=%s", errKratosInvalidInput, getResp.StatusCode, strings.TrimSpace(string(getBody)))
	}

	var identity map[string]any
	if err := sonic.Unmarshal(getBody, &identity); err != nil {
		return fmt.Errorf("%w: decode identity response: %v", errIdentityProviderUnavailable, err)
	}
	applyPasswordIntoKratosIdentity(identity, newPassword)
	encoded, err := sonic.Marshal(identity)
	if err != nil {
		return fmt.Errorf("%w: encode identity update payload: %v", errIdentityProviderUnavailable, err)
	}

	putReq, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("%w: build put identity request: %v", errIdentityProviderUnavailable, err)
	}
	putReq.Header.Set("Accept", "application/json")
	putReq.Header.Set("Content-Type", "application/json")

	putResp, err := s.kratosHTTPClient().Do(putReq)
	if err != nil {
		return fmt.Errorf("%w: put identity request failed: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(putResp.Body)
	putBody, err := io.ReadAll(putResp.Body)
	if err != nil {
		return fmt.Errorf("%w: read put identity response: %v", errIdentityProviderUnavailable, err)
	}
	if putResp.StatusCode == http.StatusOK || putResp.StatusCode == http.StatusNoContent {
		return nil
	}
	if putResp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: identity not found", errKratosIdentityUnmapped)
	}
	if putResp.StatusCode >= http.StatusInternalServerError {
		return fmt.Errorf("%w: put identity failed status=%d body=%s", errIdentityProviderUnavailable, putResp.StatusCode, strings.TrimSpace(string(putBody)))
	}
	return fmt.Errorf("%w: put identity failed status=%d body=%s", errKratosInvalidInput, putResp.StatusCode, strings.TrimSpace(string(putBody)))
}

func (s *SessionHandler) updateKratosEmailViaPut(ctx context.Context, identityID string, email string) error {
	endpoint, err := buildProviderEndpoint(s.KratosAdminURL, "/admin/identities/"+url.PathEscape(identityID))
	if err != nil {
		return fmt.Errorf("%w: %v", errIdentityProviderUnavailable, err)
	}

	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("%w: build get identity request: %v", errIdentityProviderUnavailable, err)
	}
	getReq.Header.Set("Accept", "application/json")

	getResp, err := s.kratosHTTPClient().Do(getReq)
	if err != nil {
		return fmt.Errorf("%w: get identity request failed: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(getResp.Body)
	getBody, err := io.ReadAll(getResp.Body)
	if err != nil {
		return fmt.Errorf("%w: read get identity response: %v", errIdentityProviderUnavailable, err)
	}
	if getResp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: identity not found", errKratosIdentityUnmapped)
	}
	if getResp.StatusCode != http.StatusOK {
		if getResp.StatusCode >= http.StatusInternalServerError {
			return fmt.Errorf("%w: get identity failed status=%d body=%s", errIdentityProviderUnavailable, getResp.StatusCode, strings.TrimSpace(string(getBody)))
		}
		return fmt.Errorf("%w: get identity failed status=%d body=%s", errKratosInvalidInput, getResp.StatusCode, strings.TrimSpace(string(getBody)))
	}

	var identity map[string]any
	if err := sonic.Unmarshal(getBody, &identity); err != nil {
		return fmt.Errorf("%w: decode identity response: %v", errIdentityProviderUnavailable, err)
	}
	applyEmailIntoKratosIdentity(identity, email)
	encoded, err := sonic.Marshal(identity)
	if err != nil {
		return fmt.Errorf("%w: encode identity update payload: %v", errIdentityProviderUnavailable, err)
	}

	putReq, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("%w: build put identity request: %v", errIdentityProviderUnavailable, err)
	}
	putReq.Header.Set("Accept", "application/json")
	putReq.Header.Set("Content-Type", "application/json")

	putResp, err := s.kratosHTTPClient().Do(putReq)
	if err != nil {
		return fmt.Errorf("%w: put identity request failed: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(putResp.Body)
	putBody, err := io.ReadAll(putResp.Body)
	if err != nil {
		return fmt.Errorf("%w: read put identity response: %v", errIdentityProviderUnavailable, err)
	}
	if putResp.StatusCode == http.StatusOK || putResp.StatusCode == http.StatusNoContent {
		return nil
	}
	if putResp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: identity not found", errKratosIdentityUnmapped)
	}
	if putResp.StatusCode == http.StatusConflict {
		return fmt.Errorf("%w: email already in use", errKratosIdentityConflict)
	}
	if putResp.StatusCode >= http.StatusInternalServerError {
		return fmt.Errorf("%w: put identity failed status=%d body=%s", errIdentityProviderUnavailable, putResp.StatusCode, strings.TrimSpace(string(putBody)))
	}
	return fmt.Errorf("%w: put identity failed status=%d body=%s", errKratosInvalidInput, putResp.StatusCode, strings.TrimSpace(string(putBody)))
}

func applyPasswordIntoKratosIdentity(identity map[string]any, password string) {
	credentials, ok := identity["credentials"].(map[string]any)
	if !ok || credentials == nil {
		credentials = map[string]any{}
	}
	passwordCredentials, ok := credentials["password"].(map[string]any)
	if !ok || passwordCredentials == nil {
		passwordCredentials = map[string]any{}
	}
	passwordCredentials["config"] = map[string]any{
		"password": password,
	}
	credentials["password"] = passwordCredentials
	identity["credentials"] = credentials
}

func applyEmailIntoKratosIdentity(identity map[string]any, email string) {
	traits, ok := identity["traits"].(map[string]any)
	if !ok || traits == nil {
		traits = map[string]any{}
	}
	traits["email"] = email
	identity["traits"] = traits
}

func extractSessionTokenFromContinueWith(items []kratosContinueWithItem) string {
	for _, item := range items {
		action := strings.TrimSpace(item.Action)
		if action != "" && action != "set_ory_session_token" {
			continue
		}
		if token := strings.TrimSpace(item.OrySessionToken); token != "" {
			return token
		}
	}
	return ""
}

func extractSessionTokenFromRecoveryPayload(rawBody []byte, parsed kratosRecoveryFlowSubmitResponse) string {
	if token := strings.TrimSpace(extractSessionTokenFromContinueWith(parsed.ContinueWith)); token != "" {
		return token
	}

	var raw any
	if err := sonic.Unmarshal(rawBody, &raw); err != nil {
		return ""
	}
	return extractSessionTokenFromAny(raw)
}

func extractSessionTokenFromAny(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		if token := trimAnyString(typed["ory_session_token"]); token != "" {
			return token
		}
		if token := trimAnyString(typed["session_token"]); token != "" {
			return token
		}

		action := strings.TrimSpace(trimAnyString(typed["action"]))
		if action == "set_ory_session_token" {
			if token := trimAnyString(typed["token"]); token != "" {
				return token
			}
			if nested, ok := typed["set_ory_session_token"]; ok {
				if token := extractSessionTokenFromAny(nested); token != "" {
					return token
				}
			}
		}

		for _, child := range typed {
			if token := extractSessionTokenFromAny(child); token != "" {
				return token
			}
		}
	case []any:
		for _, child := range typed {
			if token := extractSessionTokenFromAny(child); token != "" {
				return token
			}
		}
	}
	return ""
}

func trimAnyString(value any) string {
	raw, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(raw)
}

func shouldFallbackFromKratosPatch(err error) bool {
	return IsKratosInvalidInputError(err)
}

func buildProviderEndpoint(baseURL, endpointPath string) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "", fmt.Errorf("provider base URL is not configured")
	}
	parsedBase, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid provider base URL: %w", err)
	}
	if parsedBase.Scheme == "" || parsedBase.Host == "" {
		return "", fmt.Errorf("invalid provider base URL")
	}

	cleanPath := endpointPath
	if cleanPath == "" {
		cleanPath = "/"
	}
	if !strings.HasPrefix(cleanPath, "/") {
		cleanPath = "/" + cleanPath
	}

	basePath := strings.TrimRight(parsedBase.EscapedPath(), "/")
	if basePath == "" {
		parsedBase.Path = cleanPath
		return parsedBase.String(), nil
	}

	joined := path.Clean(strings.TrimRight(basePath, "/") + cleanPath)
	if !strings.HasPrefix(joined, "/") {
		joined = "/" + joined
	}
	parsedBase.Path = joined
	return parsedBase.String(), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func ClearUserSessions(redisClient *redis.Client, userID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), clearUserSessionsTimeout)
	defer cancel()
	return ClearUserSessionsWithContext(ctx, redisClient, userID)
}

func ClearUserSessionsWithContext(ctx context.Context, redisClient *redis.Client, userID string) error {
	if redisClient == nil {
		return fmt.Errorf("redis client is nil")
	}
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
