package admin

const (
	adminAuditTargetTypeUser        = "user"
	adminAuditTargetTypeRoute       = "route"
	adminAuditTargetTypeOAuthClient = "oauth_client"
	adminAuditTargetTypeConfig      = "config"
	adminAuditTargetTypeGameAccount = "game_account"
	adminAuditTargetTypeRiskEvent   = "risk_event"
	adminAuditTargetTypeRiskRule    = "risk_rule"
	adminAuditTargetTypeTicket      = "ticket"
)

const (
	adminAuditActionAccess = "admin.access"

	adminAuditActionUserBan           = "admin.user.ban"
	adminAuditActionUserUnban         = "admin.user.unban"
	adminAuditActionUserForceLogout   = "admin.user.force_logout"
	adminAuditActionUserSoftDelete    = "admin.user.soft_delete"
	adminAuditActionUserRestore       = "admin.user.restore"
	adminAuditActionUserResetPass     = "admin.user.reset_password"
	adminAuditActionUserRoleUpdate    = "admin.user.role.update"
	adminAuditActionUserBatchPrefix   = "admin.user.batch."
	adminAuditActionUserBatchRole     = "admin.user.batch.role.update"
	adminAuditActionUserBatchAllowCN  = "admin.user.batch.allow_cn_mysekai.update"
	adminAuditActionUserActivityQuery = "admin.user.activity.query"
	adminAuditActionUserOAuthList     = "admin.user.oauth.list"
	adminAuditActionUserOAuthRevoke   = "admin.user.oauth.revoke"

	adminAuditActionUserGameBindingsList  = "admin.user.game_account_bindings.list"
	adminAuditActionUserGameBindingUpsert = "admin.user.game_account_binding.upsert"
	adminAuditActionUserGameBindingDelete = "admin.user.game_account_binding.delete"
	adminAuditActionUserEmailUpdate       = "admin.user.email.update"
	adminAuditActionUserAllowCNUpdate     = "admin.user.allow_cn_mysekai.update"
	adminAuditActionUserSocialGet         = "admin.user.social_platform.get"
	adminAuditActionUserSocialUpsert      = "admin.user.social_platform.upsert"
	adminAuditActionUserSocialClear       = "admin.user.social_platform.clear"
	adminAuditActionUserAuthorizedList    = "admin.user.authorized_social_platforms.list"
	adminAuditActionUserAuthorizedUpsert  = "admin.user.authorized_social_platform.upsert"
	adminAuditActionUserAuthorizedDelete  = "admin.user.authorized_social_platform.delete"
	adminAuditActionUserIOSCodeRegenerate = "admin.user.ios_upload_code.regenerate"
	adminAuditActionUserIOSCodeClear      = "admin.user.ios_upload_code.clear"
	adminAuditActionMeSessionsList        = "admin.me.sessions.list"
	adminAuditActionMeSessionsDelete      = "admin.me.sessions.delete"
	adminAuditActionMeReauth              = "admin.me.reauth"

	adminAuditActionOAuthClientList               = "admin.oauth_client.list"
	adminAuditActionOAuthClientCreate             = "admin.oauth_client.create"
	adminAuditActionOAuthClientActiveUpdate       = "admin.oauth_client.active.update"
	adminAuditActionOAuthClientUpdate             = "admin.oauth_client.update"
	adminAuditActionOAuthClientRotateSecret       = "admin.oauth_client.rotate_secret"
	adminAuditActionOAuthClientDelete             = "admin.oauth_client.delete"
	adminAuditActionOAuthClientStatisticsQuery    = "admin.oauth_client.statistics.query"
	adminAuditActionOAuthClientAuthorizationsList = "admin.oauth_client.authorizations.list"
	adminAuditActionOAuthClientRevoke             = "admin.oauth_client.revoke"
	adminAuditActionOAuthClientRestore            = "admin.oauth_client.restore"
	adminAuditActionOAuthClientAuditLogsQuery     = "admin.oauth_client.audit_logs.query"
	adminAuditActionOAuthClientAuditSummaryQuery  = "admin.oauth_client.audit_summary.query"

	adminAuditActionConfigPublicAPIKeysUpdate = "admin.config.public_api_keys.update"
	adminAuditActionConfigRuntimeUpdate       = "admin.config.runtime.update"

	adminAuditActionGameAccountGlobalList          = "admin.game_account_binding.global.list"
	adminAuditActionGameAccountGlobalDelete        = "admin.game_account_binding.global.delete"
	adminAuditActionGameAccountGlobalReassign      = "admin.game_account_binding.global.reassign"
	adminAuditActionGameAccountGlobalBatchDelete   = "admin.game_account_binding.global.batch_delete"
	adminAuditActionGameAccountGlobalBatchReassign = "admin.game_account_binding.global.batch_reassign"

	adminAuditActionRiskEventCreate  = "admin.risk.event.create"
	adminAuditActionRiskEventResolve = "admin.risk.event.resolve"
	adminAuditActionRiskRulesUpsert  = "admin.risk.rules.upsert"

	adminAuditActionTicketMessageAppend = "admin.ticket.message.append"
	adminAuditActionTicketStatusUpdate  = "admin.ticket.status.update"
	adminAuditActionTicketAssign        = "admin.ticket.assign"
)

const (
	adminBatchActionBan         = "ban"
	adminBatchActionUnban       = "unban"
	adminBatchActionForceLogout = "force_logout"
	adminBatchActionRoleUpdate  = "role_update"
	adminBatchActionAllowCN     = "allow_cn_mysekai_update"
)
