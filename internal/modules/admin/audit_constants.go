package admin

const (
	adminAuditTargetTypeUser   = "user"
	adminAuditTargetTypeRoute  = "route"
	adminAuditTargetTypeConfig = "config"
)

const (
	adminAuditActionAccess = "admin.access"

	adminAuditActionConfigPublicAPIKeysUpdate = "admin.config.public_api_keys.update"
	adminAuditActionConfigRuntimeUpdate       = "admin.config.runtime.update"
	adminAuditActionMeSessionsDelete          = "admin.me.sessions.delete"
	adminAuditActionMeReauth                  = "admin.me.reauth"
)
