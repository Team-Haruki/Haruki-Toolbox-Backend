package admin

import (
	"context"
	"strings"

	platformAuthHeader "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/authheader"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"

	"github.com/gofiber/fiber/v3"
)

func resolveAdminKratosIdentityID(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID string) (string, error) {
	dbUser, err := apiHelper.DBManager.DB.User.Query().
		Where(userSchema.IDEQ(userID)).
		Select(userSchema.FieldKratosIdentityID).
		Only(ctx)
	if err != nil {
		return "", err
	}
	if dbUser.KratosIdentityID == nil {
		return "", nil
	}
	return strings.TrimSpace(*dbUser.KratosIdentityID), nil
}

func resolveCurrentAdminKratosIdentityID(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID string) (string, error) {
	if identityID, ok := c.Locals("identityID").(string); ok && strings.TrimSpace(identityID) != "" {
		return strings.TrimSpace(identityID), nil
	}
	identityID, err := resolveAdminKratosIdentityID(harukiAPIHelper.WithHTTPRequestMetadata(c.Context(), c.Get("User-Agent"), c.IP()), apiHelper, userID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(identityID) == "" {
		return "", fiber.NewError(fiber.StatusUnauthorized, "invalid user session")
	}
	return identityID, nil
}

func currentAdminOwnsKratosSession(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID string, sessionID string) (bool, error) {
	identityID, err := resolveCurrentAdminKratosIdentityID(c, apiHelper, userID)
	if err != nil {
		return false, err
	}
	sessions, err := apiHelper.SessionHandler.ListKratosSessionsByIdentityID(harukiAPIHelper.WithHTTPRequestMetadata(c.Context(), c.Get("User-Agent"), c.IP()), identityID)
	if err != nil {
		return false, err
	}
	sessionID = strings.TrimSpace(sessionID)
	for _, session := range sessions {
		if strings.TrimSpace(session.ID) == sessionID {
			return true, nil
		}
	}
	return false, nil
}

func resolveCurrentKratosSessionID(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, bearerToken, kratosHeaderToken, cookieHeader string) (string, error) {
	sessionToken := strings.TrimSpace(kratosHeaderToken)
	if sessionToken == "" {
		sessionToken = strings.TrimSpace(bearerToken)
	}
	return apiHelper.SessionHandler.ResolveKratosSessionID(ctx, sessionToken, cookieHeader)
}

func resolveCurrentAuthProxySessionMarker(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) (string, error) {
	if apiHelper == nil || apiHelper.SessionHandler == nil || !apiHelper.SessionHandler.UsesAuthProxy() {
		return "", nil
	}

	trustedHeader := strings.TrimSpace(apiHelper.SessionHandler.AuthProxyTrustedHeader)
	trustedValue := strings.TrimSpace(apiHelper.SessionHandler.AuthProxyTrustedValue)
	if trustedHeader == "" || trustedValue == "" {
		return "", nil
	}
	if !apiHelper.SessionHandler.AuthProxyTrustedValueMatches(c.Get(trustedHeader)) {
		return "", nil
	}

	if sessionID, ok := c.Locals("authProxySessionID").(string); ok && strings.TrimSpace(sessionID) != "" {
		return "authproxy-session:" + strings.TrimSpace(sessionID), nil
	}
	sessionHeader := strings.TrimSpace(apiHelper.SessionHandler.AuthProxySessionHeader)
	if sessionHeader == "" {
		return "", fiber.NewError(fiber.StatusUnauthorized, "invalid user session")
	}
	if sessionID := strings.TrimSpace(c.Get(sessionHeader)); sessionID != "" {
		return "authproxy-session:" + sessionID, nil
	}
	return "", fiber.NewError(fiber.StatusUnauthorized, "invalid user session")
}

func resolveCurrentAdminSessionMarker(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) (string, error) {
	if sessionMarker, err := resolveCurrentAuthProxySessionMarker(c, apiHelper); err != nil || sessionMarker != "" {
		return sessionMarker, err
	}

	if apiHelper == nil || apiHelper.SessionHandler == nil || !apiHelper.SessionHandler.UsesKratosProvider() {
		return "", fiber.NewError(fiber.StatusUnauthorized, "invalid user session")
	}

	authHeader := c.Get("Authorization")
	bearerToken, _ := platformAuthHeader.ExtractBearerToken(authHeader)
	kratosHeaderToken := strings.TrimSpace(c.Get(apiHelper.SessionHandler.KratosSessionHeader))
	cookieHeader := strings.TrimSpace(c.Get("Cookie"))
	if bearerToken == "" && kratosHeaderToken == "" && cookieHeader == "" {
		return "", fiber.NewError(fiber.StatusUnauthorized, "missing token")
	}

	sessionID, err := resolveCurrentKratosSessionID(harukiAPIHelper.WithHTTPRequestMetadata(c.Context(), c.Get("User-Agent"), c.IP()), apiHelper, bearerToken, kratosHeaderToken, cookieHeader)
	if err != nil {
		if harukiAPIHelper.IsIdentityProviderUnavailableError(err) {
			return "", fiber.NewError(fiber.StatusInternalServerError, "identity provider unavailable")
		}
		return "", fiber.NewError(fiber.StatusUnauthorized, "invalid user session")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", fiber.NewError(fiber.StatusUnauthorized, "invalid user session")
	}
	return "kratos:" + sessionID, nil
}

func mapKratosSessionDeleteError(err error) (statusCode int, message string, known bool) {
	switch {
	case err == nil:
		return fiber.StatusOK, "", false
	case harukiAPIHelper.IsKratosSessionNotFoundError(err):
		return fiber.StatusNotFound, "session not found", true
	case harukiAPIHelper.IsKratosInvalidInputError(err):
		return fiber.StatusBadRequest, "invalid session_token_id", true
	default:
		return fiber.StatusInternalServerError, "failed to delete session", false
	}
}

func isAdminSessionIdentityNotFound(err error) bool {
	if postgresql.IsNotFound(err) || harukiAPIHelper.IsKratosIdentityUnmappedError(err) {
		return true
	}
	if fiberErr, ok := err.(*fiber.Error); ok && fiberErr.Code == fiber.StatusUnauthorized {
		return true
	}
	return false
}
