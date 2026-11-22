package usersponsor

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/authheader"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/jsoncodec"
	"github.com/gofiber/fiber/v3"
)

const oauthStateTTL = 10 * time.Minute

var errOAuthExchange = errors.New("afdian authorization could not be verified")

func newOAuthHTTPClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

// Afdian's code exchange returns the verified user_id directly. No browser
// user ID, access token or unsigned profile is accepted as evidence of ownership.
// https://guide.afdian.com/creator/oauth2
func exchangeCode(ctx context.Context, client *http.Client, cfg OAuthConfig, code string) (string, error) {
	body := url.Values{"grant_type": {"authorization_code"}, "client_id": {cfg.ClientID},
		"client_secret": {cfg.ClientSecret}, "redirect_uri": {cfg.RedirectURI}, "code": {code}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://afdian.com/api/oauth2/access_token", strings.NewReader(body.Encode()))
	if err != nil {
		return "", errOAuthExchange
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return "", errOAuthExchange
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errOAuthExchange
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return "", errOAuthExchange
	}
	var result struct {
		EC   int `json:"ec"`
		Data struct {
			UserID string `json:"user_id"`
		} `json:"data"`
	}
	if jsoncodec.Unmarshal(raw, &result) != nil || result.EC != 200 {
		return "", errOAuthExchange
	}
	id := strings.TrimSpace(result.Data.UserID)
	if id == "" || len(id) > 128 || strings.ContainsAny(id, "\x00\r\n\t ") {
		return "", errOAuthExchange
	}
	return id, nil
}

func (h *handlers) oauthOwner(c fiber.Ctx) (string, error) {
	uid, err := usercore.CurrentUserID(c)
	if err != nil {
		return "", err
	}
	session, _ := c.Locals("authProxySessionID").(string)
	if session == "" {
		token, _ := authheader.ExtractBearerToken(c.Get("Authorization"))
		if value := c.Get(h.api.SessionHandler.KratosSessionHeader); value != "" {
			token = value
		}
		session, err = h.api.SessionHandler.ResolveKratosSessionID(c.Context(), token, c.Get("Cookie"))
	}
	if err != nil || strings.TrimSpace(session) == "" {
		return "", errors.New("session unavailable")
	}
	sum := sha256.Sum256([]byte(uid + "\x00" + session))
	return hex.EncodeToString(sum[:]), nil
}

func (h *handlers) rateLimit(c fiber.Ctx) error {
	manager := h.api.DBManager.Redis
	if manager == nil || manager.Redis == nil {
		return fiber.NewError(503, "authorization temporarily unavailable")
	}
	uid, _ := usercore.CurrentUserID(c)
	count, err := manager.IncrementWithTTL(c.Context(), manager.KeyBuilder().BuildAfdianOAuthRateKey(uid), time.Minute)
	if err != nil {
		return fiber.NewError(503, "authorization temporarily unavailable")
	}
	if count > 10 {
		return fiber.NewError(429, "too many authorization attempts")
	}
	return nil
}

func (h *handlers) authorize(c fiber.Ctx) error {
	if !h.oauth.Enabled() {
		return fiber.NewError(503, "afdian authorization is not configured")
	}
	if err := h.rateLimit(c); err != nil {
		return err
	}
	owner, err := h.oauthOwner(c)
	if err != nil {
		return api.ErrorUnauthorized(c, "session unavailable")
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return api.ErrorInternal(c, "failed to begin authorization")
	}
	state := base64.RawURLEncoding.EncodeToString(random)
	manager := h.api.DBManager.Redis
	if err := manager.SetCache(c.Context(), manager.KeyBuilder().BuildAfdianOAuthStateKey(state), owner, oauthStateTTL); err != nil {
		return fiber.NewError(503, "authorization temporarily unavailable")
	}
	query := url.Values{"response_type": {"code"}, "scope": {"basic"}, "client_id": {h.oauth.ClientID}, "redirect_uri": {h.oauth.RedirectURI}, "state": {state}}
	return api.Responses.SuccessResponse(c, "success", &struct {
		URL string `json:"authorizeUrl"`
	}{"https://afdian.com/oauth2/authorize?" + query.Encode()})
}

func (h *handlers) callback(c fiber.Ctx) error {
	if !h.oauth.Enabled() {
		return fiber.NewError(503, "afdian authorization is not configured")
	}
	if err := h.rateLimit(c); err != nil {
		return err
	}
	var payload struct {
		Code  string `json:"code"`
		State string `json:"state"`
	}
	if c.Bind().Body(&payload) != nil || len(payload.Code) == 0 || len(payload.Code) > 2048 || len(payload.State) != 43 {
		return api.ErrorBadRequest(c, "invalid authorization callback")
	}
	owner, err := h.oauthOwner(c)
	if err != nil {
		return api.ErrorUnauthorized(c, "session unavailable")
	}
	manager := h.api.DBManager.Redis
	key := manager.KeyBuilder().BuildAfdianOAuthStateKey(payload.State)
	var stored string
	found, err := manager.GetCache(c.Context(), key, &stored)
	if err != nil {
		return fiber.NewError(503, "authorization temporarily unavailable")
	}
	if !found || subtle.ConstantTimeCompare([]byte(stored), []byte(owner)) != 1 {
		return api.ErrorBadRequest(c, "authorization expired or session changed")
	}
	consumed, err := manager.DeleteCacheIfValueMatches(c.Context(), key, owner)
	if err != nil {
		return fiber.NewError(503, "authorization temporarily unavailable")
	}
	if !consumed {
		return api.ErrorBadRequest(c, "authorization already used")
	}
	// Consume before the outbound request: even concurrent callbacks can redeem once.
	afdianID, err := exchangeCode(c.Context(), h.client, h.oauth, payload.Code)
	if err != nil {
		return fiber.NewError(502, "afdian authorization could not be verified")
	}
	uid, _ := usercore.CurrentUserID(c)
	err = h.bindIdentity(c.Context(), uid, afdianID, "oauth2")
	if errors.Is(err, errBindingConflict) {
		return fiber.NewError(409, "afdian account is already bound; unlink before changing accounts")
	}
	if err != nil {
		return api.ErrorInternal(c, "failed to save afdian binding")
	}
	usercore.WriteUserAuditLog(c, h.api, "user.afdian.bind", api.SystemLogResultSuccess, uid, map[string]any{"verificationMethod": "oauth2"})
	return h.status(c)
}

func (h *handlers) unlink(c fiber.Ctx) error {
	uid, _ := usercore.CurrentUserID(c)
	if err := h.api.DBManager.DB.User.UpdateOneID(uid).ClearAfdianUserID().ClearAfdianBoundAt().ClearAfdianVerificationMethod().Exec(c.Context()); err != nil {
		return api.ErrorInternal(c, "failed to unlink afdian account")
	}
	usercore.WriteUserAuditLog(c, h.api, "user.afdian.unlink", api.SystemLogResultSuccess, uid, nil)
	return h.status(c)
}
