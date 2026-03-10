package admingamebindings

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	"time"
)

const (
	roleUser       = adminCoreModule.RoleUser
	roleAdmin      = adminCoreModule.RoleAdmin
	roleSuperAdmin = adminCoreModule.RoleSuperAdmin
)

const (
	adminAuditTargetIDAll           = "all"
	adminAuditTargetIDBatch         = "batch"
	adminAuditTargetTypeGameAccount = "game_account"
)

const (
	adminAuditActionGameAccountGlobalList          = "admin.game_account_binding.global.list"
	adminAuditActionGameAccountGlobalDelete        = "admin.game_account_binding.global.delete"
	adminAuditActionGameAccountGlobalReassign      = "admin.game_account_binding.global.reassign"
	adminAuditActionGameAccountGlobalBatchDelete   = "admin.game_account_binding.global.batch_delete"
	adminAuditActionGameAccountGlobalBatchReassign = "admin.game_account_binding.global.batch_reassign"
)

const (
	adminFailureReasonInvalidQueryFilters   = "invalid_query_filters"
	adminFailureReasonCountBindingsFailed   = "count_bindings_failed"
	adminFailureReasonQueryBindingsFailed   = "query_bindings_failed"
	adminFailureReasonInvalidPathParams     = "invalid_path_params"
	adminFailureReasonBindingNotFound       = "binding_not_found"
	adminFailureReasonQueryBindingFailed    = "query_binding_failed"
	adminFailureReasonPermissionDenied      = "permission_denied"
	adminFailureReasonDeleteBindingFailed   = "delete_binding_failed"
	adminFailureReasonInvalidRequestPayload = "invalid_request_payload"
	adminFailureReasonInvalidTargetUser     = "invalid_target_user"
	adminFailureReasonReassignBindingFailed = "reassign_binding_failed"
	adminFailureReasonInvalidItems          = "invalid_items"
)

const (
	adminBatchResultCodeDeleteBindingFailed   = "delete_binding_failed"
	adminBatchResultCodeInvalidTargetUser     = "invalid_target_user"
	adminBatchResultCodeReassignBindingFailed = "reassign_binding_failed"
)

func normalizeRole(raw string) string {
	return adminCoreModule.NormalizeRole(raw)
}

var adminNow = time.Now

func adminNowUTC() time.Time {
	return adminNow().UTC()
}
