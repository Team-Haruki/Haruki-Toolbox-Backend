package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
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
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

const (
	sessionRedisOperationTimeout = 2 * time.Second
	clearUserSessionsTimeout     = 10 * time.Second

	sessionProviderLocal  = "local"
	sessionProviderKratos = "kratos"
	sessionProviderAuto   = "auto"

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

func IsKratosInvalidCredentialsError(err error) bool {
	return errors.Is(err, errKratosInvalidCredentials)
}

func IsKratosIdentityConflictError(err error) bool {
	return errors.Is(err, errKratosIdentityConflict)
}

func IsKratosInvalidInputError(err error) bool {
	return errors.Is(err, errKratosInvalidInput)
}

type kratosSessionWhoamiResponse struct {
	Active   bool                 `json:"active"`
	Identity kratosIdentityRecord `json:"identity"`
}

type kratosIdentityRecord struct {
	ID                  string                   `json:"id"`
	Traits              map[string]any           `json:"traits"`
	VerifiableAddresses []kratosVerifiableRecord `json:"verifiable_addresses"`
}

type kratosVerifiableRecord struct {
	Value string `json:"value"`
}

type kratosFlowResponse struct {
	ID string `json:"id"`
}

type kratosAuthSubmitResponse struct {
	SessionToken string `json:"session_token"`
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
	case sessionProviderKratos:
		return sessionProviderKratos
	case sessionProviderAuto, "hybrid":
		return sessionProviderAuto
	case sessionProviderLocal, "":
		fallthrough
	default:
		return sessionProviderLocal
	}
}

func NewSessionHandler(redisClient *redis.Client, sessionSignKey string) *SessionHandler {
	return &SessionHandler{
		RedisClient:             redisClient,
		SessionSignKey:          sessionSignKey,
		SessionProvider:         sessionProviderLocal,
		KratosSessionHeader:     defaultKratosSessionHeader,
		KratosSessionCookie:     defaultKratosSessionCookie,
		KratosAutoLinkByEmail:   true,
		KratosAutoProvisionUser: true,
		KratosRequestTimeout:    defaultKratosTimeout,
		KratosIdentityResolver:  nil,
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
	return &http.Client{Timeout: timeout}
}

func (s *SessionHandler) UsesKratosProvider() bool {
	provider := normalizeSessionProvider(s.SessionProvider)
	return (provider == sessionProviderKratos || provider == sessionProviderAuto) && s.hasKratosProviderConfigured()
}

func (s *SessionHandler) ResolveUserIDFromKratosSession(ctx context.Context, sessionToken string, cookieHeader string) (string, error) {
	return s.verifyKratosSession(ctx, sessionToken, cookieHeader)
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

func (s *SessionHandler) IssueSession(userID string) (string, error) {
	sessionToken := uuid.NewString()
	ctx, cancel := context.WithTimeout(context.Background(), sessionRedisOperationTimeout)
	defer cancel()

	err := s.RedisClient.Set(ctx, userID+":"+sessionToken, "1", 7*24*time.Hour).Err()
	if err != nil {
		return "", err
	}
	claims := SessionClaims{
		UserID:       userID,
		SessionToken: sessionToken,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(7 * 24 * time.Hour)),
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
	authHeader := c.Get("Authorization")
	bearerToken, hasBearerToken := platformAuthHeader.ExtractBearerToken(authHeader)
	kratosHeaderToken := strings.TrimSpace(c.Get(s.KratosSessionHeader))
	cookieHeader := strings.TrimSpace(c.Get("Cookie"))
	if !hasBearerToken && kratosHeaderToken == "" && cookieHeader == "" {
		return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "missing token", nil)
	}

	applyResolvedUserID := func(userID string) error {
		userID = strings.TrimSpace(userID)
		if userID == "" {
			return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "invalid user session", nil)
		}
		toolboxUserID := strings.TrimSpace(c.Params("toolbox_user_id"))
		if toolboxUserID != "" && toolboxUserID != userID {
			return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "user ID mismatch", nil)
		}
		c.Locals("userID", userID)
		return c.Next()
	}

	provider := normalizeSessionProvider(s.SessionProvider)
	switch provider {
	case sessionProviderKratos:
		kratosToken := firstNonEmpty(kratosHeaderToken, bearerToken)
		userID, err := s.verifyKratosSession(c.Context(), kratosToken, cookieHeader)
		if err != nil {
			return respondSessionVerifyError(c, err)
		}
		return applyResolvedUserID(userID)
	case sessionProviderAuto:
		if hasBearerToken {
			userID, err := s.verifyLocalSession(c.Context(), bearerToken)
			if err == nil {
				return applyResolvedUserID(userID)
			}
			if errors.Is(err, errSessionStoreUnavailable) {
				return respondSessionVerifyError(c, err)
			}
		}
		if !s.hasKratosProviderConfigured() {
			return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "invalid token", nil)
		}
		kratosToken := firstNonEmpty(kratosHeaderToken, bearerToken)
		userID, err := s.verifyKratosSession(c.Context(), kratosToken, cookieHeader)
		if err != nil {
			return respondSessionVerifyError(c, err)
		}
		return applyResolvedUserID(userID)
	default:
		if !hasBearerToken {
			return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "missing token", nil)
		}
		userID, err := s.verifyLocalSession(c.Context(), bearerToken)
		if err != nil {
			return respondSessionVerifyError(c, err)
		}
		return applyResolvedUserID(userID)
	}
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

func (s *SessionHandler) verifyLocalSession(ctx context.Context, tokenStr string) (string, error) {
	tokenStr = strings.TrimSpace(tokenStr)
	if tokenStr == "" {
		return "", fmt.Errorf("%w: empty token", errSessionUnauthorized)
	}
	if strings.TrimSpace(s.SessionSignKey) == "" {
		return "", fmt.Errorf("%w: session sign key is not configured", errSessionUnauthorized)
	}

	parsed, err := jwt.ParseWithClaims(tokenStr, &SessionClaims{}, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(s.SessionSignKey), nil
	})
	if err != nil || !parsed.Valid {
		return "", fmt.Errorf("%w: parse token: %v", errSessionUnauthorized, err)
	}
	claims, ok := parsed.Claims.(*SessionClaims)
	if !ok {
		return "", fmt.Errorf("%w: invalid session claims", errSessionUnauthorized)
	}

	key := claims.UserID + ":" + claims.SessionToken
	sessionCtx, cancel := context.WithTimeout(ctx, sessionRedisOperationTimeout)
	defer cancel()
	exists, err := s.RedisClient.Exists(sessionCtx, key).Result()
	if err != nil {
		return "", fmt.Errorf("%w: redis check session: %v", errSessionStoreUnavailable, err)
	}
	if exists == 0 {
		return "", fmt.Errorf("%w: session not found", errSessionUnauthorized)
	}

	return strings.TrimSpace(claims.UserID), nil
}

func (s *SessionHandler) verifyKratosSession(ctx context.Context, sessionToken string, cookieHeader string) (string, error) {
	if !s.hasKratosProviderConfigured() {
		return "", fmt.Errorf("%w: kratos public url is not configured", errIdentityProviderUnavailable)
	}
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
	identityID := strings.TrimSpace(whoami.Identity.ID)
	if identityID == "" {
		return "", fmt.Errorf("%w: kratos identity id is empty", errSessionUnauthorized)
	}
	email := platformIdentity.NormalizeEmail(extractKratosIdentityEmail(whoami.Identity))

	return s.resolveKratosIdentity(ctx, identityID, email)
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

	passwordHash, err := generateProvisionedPasswordHash()
	if err != nil {
		return "", fmt.Errorf("%w: generate password hash: %v", errUserStoreUnavailable, err)
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
			SetPasswordHash(passwordHash).
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

func generateProvisionedPasswordHash() (string, error) {
	randomValue := uuid.NewString() + ":" + time.Now().UTC().Format(time.RFC3339Nano)
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(randomValue), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(passwordHash), nil
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
		if err := json.Unmarshal(body, &parsed); err != nil {
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
	if err := json.Unmarshal(body, &parsed); err != nil {
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

	encoded, err := json.Marshal(payload)
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
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("%w: decode submit response: %v", errIdentityProviderUnavailable, err)
	}
	sessionToken := strings.TrimSpace(parsed.SessionToken)
	if sessionToken == "" {
		return "", fmt.Errorf("%w: empty session token in response", errIdentityProviderUnavailable)
	}
	return sessionToken, nil
}

func classifyKratosFlowError(statusCode int, body []byte) error {
	bodyText := strings.TrimSpace(string(body))
	var payload kratosErrorPayload
	_ = json.Unmarshal(body, &payload)
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
