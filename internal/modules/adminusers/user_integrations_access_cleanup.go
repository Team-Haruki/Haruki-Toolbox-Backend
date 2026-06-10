package adminusers

import (
	"context"
	oauth2Module "haruki-suite/internal/modules/oauth2"
	harukiAPIHelper "haruki-suite/utils/api"
	harukiLogger "haruki-suite/utils/logger"
	"strconv"
	"strings"
)

func clearManagedBindingPublicCaches(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, server, gameUserID string) {
	if apiHelper == nil || apiHelper.DBManager == nil || apiHelper.DBManager.Redis == nil {
		return
	}
	parsedUserID, err := strconv.ParseInt(strings.TrimSpace(gameUserID), 10, 64)
	if err != nil {
		harukiLogger.Warnf("Failed to parse managed binding game user id for cache clear: server=%s gameUserID=%s err=%v", server, gameUserID, err)
		return
	}
	if err := apiHelper.DBManager.Redis.ClearPublicGameDataCaches(ctx, server, parsedUserID); err != nil {
		harukiLogger.Warnf("Failed to clear managed binding public caches: server=%s gameUserID=%s err=%v", server, gameUserID, err)
	}
}

func clearManagedUserSessions(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, targetUserID string, kratosIdentityID *string) (sessionClearFailed bool) {
	if apiHelper != nil && apiHelper.SessionHandler != nil && apiHelper.SessionHandler.UsesKratosProvider() &&
		kratosIdentityID != nil && strings.TrimSpace(*kratosIdentityID) != "" {
		if err := apiHelper.SessionHandler.RevokeKratosSessionsByIdentityID(ctx, strings.TrimSpace(*kratosIdentityID)); err != nil {
			sessionClearFailed = true
		}
	}

	if err := harukiAPIHelper.ClearUserSessionsWithContext(ctx, apiHelper.RedisClient(), targetUserID); err != nil {
		sessionClearFailed = true
	}
	return sessionClearFailed
}

func revokeManagedUserOAuthTokens(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, targetUserID string, kratosIdentityID *string) (oauthRevokeFailed bool) {
	if oauth2Module.HydraOAuthManagementEnabled() {
		subjects := oauth2Module.HydraSubjectsForUser(targetUserID, kratosIdentityID)
		if err := oauth2Module.RevokeHydraConsentSessionsForSubjects(ctx, subjects, ""); err != nil {
			oauthRevokeFailed = true
		}
		return oauthRevokeFailed
	}
	return true
}

func cleanupManagedUserAccessAfterBan(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, targetUserID string, kratosIdentityID *string) (sessionClearFailed bool, oauthRevokeFailed bool) {
	sessionClearFailed = clearManagedUserSessions(ctx, apiHelper, targetUserID, kratosIdentityID)
	oauthRevokeFailed = revokeManagedUserOAuthTokens(ctx, apiHelper, targetUserID, kratosIdentityID)
	return sessionClearFailed, oauthRevokeFailed
}

func resolveManagedUserBanFinalizeOutcome(sessionClearFailed, oauthRevokeFailed bool) (message string, success bool) {
	if sessionClearFailed && oauthRevokeFailed {
		return "user banned, but failed to clear user sessions and revoke oauth tokens", false
	}
	if sessionClearFailed {
		return "user banned, but failed to clear user sessions", true
	}
	if oauthRevokeFailed {
		return "user banned, but failed to revoke oauth tokens", true
	}
	return "user banned", true
}
