package adminoauth

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	oauth2Module "haruki-suite/internal/modules/oauth2"
	harukiAPIHelper "haruki-suite/utils/api"
)

func RegisterAdminOAuthClientRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	adminGroup := adminCoreModule.AdminRootGroup(apiHelper)
	oauthClients := adminGroup.Group("/oauth-clients", adminCoreModule.RequireAdmin(apiHelper))
	if oauth2Module.HydraOAuthManagementEnabled() {
		oauthClients.Post("", adminCoreModule.RequireSuperAdmin(apiHelper), handleCreateHydraOAuthClient(apiHelper))
		oauthClients.Get("", handleListHydraOAuthClients(apiHelper))
		oauthClients.Get("/:client_id/authorizations", handleListHydraOAuthClientAuthorizations(apiHelper))
		oauthClients.Get("/:client_id/statistics", handleGetHydraOAuthClientStatistics(apiHelper))
		oauthClients.Get("/:client_id/audit-logs", handleListHydraOAuthClientAuditLogs(apiHelper))
		oauthClients.Get("/:client_id/audit-summary", handleGetHydraOAuthClientAuditSummary(apiHelper))
		oauthClients.Post("/:client_id/revoke", adminCoreModule.RequireSuperAdmin(apiHelper), handleRevokeHydraOAuthClient(apiHelper))
		oauthClients.Post("/:client_id/restore", adminCoreModule.RequireSuperAdmin(apiHelper), handleRestoreHydraOAuthClient(apiHelper))
		oauthClients.Put("/:client_id", adminCoreModule.RequireSuperAdmin(apiHelper), handleUpdateHydraOAuthClient(apiHelper))
		oauthClients.Put("/:client_id/active", adminCoreModule.RequireSuperAdmin(apiHelper), handleUpdateHydraOAuthClientActive(apiHelper))
		oauthClients.Post("/:client_id/rotate-secret", adminCoreModule.RequireSuperAdmin(apiHelper), handleRotateHydraOAuthClientSecret(apiHelper))
		oauthClients.Delete("/:client_id", adminCoreModule.RequireSuperAdmin(apiHelper), handleDeleteHydraOAuthClient(apiHelper))
		return
	}
}
