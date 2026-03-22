package adminoauth

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	platformTime "haruki-suite/internal/platform/timeutil"
	"strings"
	"time"
)

const (
	contentTypeApplicationJSON           = "application/json"
	contentTypeApplicationFormURLEncoded = "application/x-www-form-urlencoded"
)

const (
	adminAuditTargetIDAll           = "all"
	adminAuditTargetTypeOAuthClient = "oauth_client"
)

const (
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
)

const (
	adminFailureReasonAggregateActionSummaryFailed    = "aggregate_action_summary_failed"
	adminFailureReasonAggregateActorTypeSummaryFailed = "aggregate_actor_type_summary_failed"
	adminFailureReasonAggregateReasonSummaryFailed    = "aggregate_reason_summary_failed"
	adminFailureReasonAggregateResultSummaryFailed    = "aggregate_result_summary_failed"
	adminFailureReasonClientHasDependencies           = "client_has_dependencies"
	adminFailureReasonClientIdConflict                = "client_id_conflict"
	adminFailureReasonClientNotFound                  = "client_not_found"
	adminFailureReasonCommitTransactionFailed         = "commit_transaction_failed"
	adminFailureReasonCountActiveAuthorizationsFailed = "count_active_authorizations_failed"
	adminFailureReasonCountActiveTokensFailed         = "count_active_tokens_failed"
	adminFailureReasonCountAuditLogsFailed            = "count_audit_logs_failed"
	adminFailureReasonCountAuthorizationsFailed       = "count_authorizations_failed"
	adminFailureReasonCountSuccessAuditLogsFailed     = "count_success_audit_logs_failed"
	adminFailureReasonCountTokensFailed               = "count_tokens_failed"
	adminFailureReasonCreateClientFailed              = "create_client_failed"
	adminFailureReasonDeleteAuthorizationsFailed      = "delete_authorizations_failed"
	adminFailureReasonDeleteClientFailed              = "delete_client_failed"
	adminFailureReasonDeleteTokensFailed              = "delete_tokens_failed"
	adminFailureReasonGenerateClientSecretFailed      = "generate_client_secret_failed"
	adminFailureReasonInvalidHours                    = "invalid_hours"
	adminFailureReasonInvalidIncludeInactive          = "invalid_include_inactive"
	adminFailureReasonInvalidQueryFilters             = "invalid_query_filters"
	adminFailureReasonInvalidRequestPayload           = "invalid_request_payload"
	adminFailureReasonMissingClientID                 = "missing_client_id"
	adminFailureReasonMissingUserSession              = "missing_user_session"
	adminFailureReasonNothingToRevoke                 = "nothing_to_revoke"
	adminFailureReasonQueryAuditLogsFailed            = "query_audit_logs_failed"
	adminFailureReasonQueryAuthorizationTrendsFailed  = "query_authorization_trends_failed"
	adminFailureReasonQueryAuthorizationsFailed       = "query_authorizations_failed"
	adminFailureReasonQueryClientFailed               = "query_client_failed"
	adminFailureReasonQueryClientsFailed              = "query_clients_failed"
	adminFailureReasonQueryTargetUserFailed           = "query_target_user_failed"
	adminFailureReasonQueryTokenStatsFailed           = "query_token_stats_failed"
	adminFailureReasonQueryTokenTrendsFailed          = "query_token_trends_failed"
	adminFailureReasonQueryUsageStatsFailed           = "query_usage_stats_failed"
	adminFailureReasonRestoreClientFailed             = "restore_client_failed"
	adminFailureReasonRevokeAuthorizationsFailed      = "revoke_authorizations_failed"
	adminFailureReasonRevokeTokensFailed              = "revoke_tokens_failed"
	adminFailureReasonStartTransactionFailed          = "start_transaction_failed"
	adminFailureReasonTargetUserNotFound              = "target_user_not_found"
	adminFailureReasonUpdateClientFailed              = "update_client_failed"
	adminFailureReasonUpdateClientSecretFailed        = "update_client_secret_failed"
)

func resolveUploadLogTimeRange(fromRaw, toRaw string, now time.Time) (time.Time, time.Time, error) {
	return platformTime.ResolveUploadLogTimeRange(fromRaw, toRaw, now)
}

func parseAdminOAuthIncludeRevoked(raw string) (bool, error) {
	includeRevoked, err := adminCoreModule.ParseOptionalBoolField(raw, "include_revoked")
	if err != nil {
		return false, err
	}
	if includeRevoked == nil {
		return false, nil
	}
	return *includeRevoked, nil
}

func looksLikeJSONBody(body []byte) bool {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return false
	}
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

func looksLikeFormBody(body []byte) bool {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return false
	}
	return strings.Contains(trimmed, "=")
}

var adminNow = time.Now

func adminNowUTC() time.Time {
	return adminNow().UTC()
}
