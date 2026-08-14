package adminoauth

import (
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	oauth2Module "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/oauth2"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/oauth2"

	"github.com/gofiber/fiber/v3"
)

func RegisterAdminOAuthClientRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, adminGroup fiber.Router, hydraConfig *harukiOAuth2.HydraConfig) {
	oauthClients := adminGroup.Group("/oauth-clients", adminCoreModule.RequireAdmin(apiHelper))
	if oauth2Module.HydraOAuthManagementEnabled(hydraConfig) {
		oauthClients.Post("", adminCoreModule.RequireSuperAdmin(apiHelper), handleCreateHydraOAuthClient(apiHelper, hydraConfig))
		oauthClients.Get("", handleListHydraOAuthClients(apiHelper, hydraConfig))
		oauthClients.Get("/:client_id/authorizations", handleListHydraOAuthClientAuthorizations(apiHelper, hydraConfig))
		oauthClients.Get("/:client_id/statistics", handleGetHydraOAuthClientStatistics(apiHelper, hydraConfig))
		oauthClients.Get("/:client_id/audit-logs", handleListHydraOAuthClientAuditLogs(apiHelper, hydraConfig))
		oauthClients.Get("/:client_id/audit-summary", handleGetHydraOAuthClientAuditSummary(apiHelper, hydraConfig))
		oauthClients.Get("/:client_id/webhooks", handleListHydraOAuthClientWebhooks(apiHelper, hydraConfig))
		oauthClients.Post("/:client_id/webhooks", adminCoreModule.RequireSuperAdmin(apiHelper), handleCreateHydraOAuthClientWebhook(apiHelper, hydraConfig))
		oauthClients.Put("/:client_id/webhooks/:webhook_id", adminCoreModule.RequireSuperAdmin(apiHelper), handleUpdateHydraOAuthClientWebhook(apiHelper))
		oauthClients.Delete("/:client_id/webhooks/:webhook_id", adminCoreModule.RequireSuperAdmin(apiHelper), handleDeleteHydraOAuthClientWebhook(apiHelper))
		oauthClients.Post("/:client_id/revoke", adminCoreModule.RequireSuperAdmin(apiHelper), handleRevokeHydraOAuthClient(apiHelper, hydraConfig))
		oauthClients.Post("/:client_id/restore", adminCoreModule.RequireSuperAdmin(apiHelper), handleRestoreHydraOAuthClient(apiHelper, hydraConfig))
		oauthClients.Put("/:client_id", adminCoreModule.RequireSuperAdmin(apiHelper), handleUpdateHydraOAuthClient(apiHelper, hydraConfig))
		oauthClients.Put("/:client_id/active", adminCoreModule.RequireSuperAdmin(apiHelper), handleUpdateHydraOAuthClientActive(apiHelper, hydraConfig))
		oauthClients.Post("/:client_id/rotate-secret", adminCoreModule.RequireSuperAdmin(apiHelper), handleRotateHydraOAuthClientSecret(apiHelper, hydraConfig))
		oauthClients.Delete("/:client_id", adminCoreModule.RequireSuperAdmin(apiHelper), handleDeleteHydraOAuthClient(apiHelper, hydraConfig))
		return
	}
}
