package adminoauth

import (
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	oauth2Module "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/oauth2"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
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
		oauthClients.Get("/:client_id/webhooks", handleListHydraOAuthClientWebhooks(apiHelper))
		oauthClients.Post("/:client_id/webhooks", adminCoreModule.RequireSuperAdmin(apiHelper), handleCreateHydraOAuthClientWebhook(apiHelper))
		oauthClients.Put("/:client_id/webhooks/:webhook_id", adminCoreModule.RequireSuperAdmin(apiHelper), handleUpdateHydraOAuthClientWebhook(apiHelper))
		oauthClients.Delete("/:client_id/webhooks/:webhook_id", adminCoreModule.RequireSuperAdmin(apiHelper), handleDeleteHydraOAuthClientWebhook(apiHelper))
		oauthClients.Post("/:client_id/revoke", adminCoreModule.RequireSuperAdmin(apiHelper), handleRevokeHydraOAuthClient(apiHelper))
		oauthClients.Post("/:client_id/restore", adminCoreModule.RequireSuperAdmin(apiHelper), handleRestoreHydraOAuthClient(apiHelper))
		oauthClients.Put("/:client_id", adminCoreModule.RequireSuperAdmin(apiHelper), handleUpdateHydraOAuthClient(apiHelper))
		oauthClients.Put("/:client_id/active", adminCoreModule.RequireSuperAdmin(apiHelper), handleUpdateHydraOAuthClientActive(apiHelper))
		oauthClients.Post("/:client_id/rotate-secret", adminCoreModule.RequireSuperAdmin(apiHelper), handleRotateHydraOAuthClientSecret(apiHelper))
		oauthClients.Delete("/:client_id", adminCoreModule.RequireSuperAdmin(apiHelper), handleDeleteHydraOAuthClient(apiHelper))
		return
	}
}
