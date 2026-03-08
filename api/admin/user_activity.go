package admin

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/systemlog"
	"haruki-suite/utils/database/postgresql/uploadlog"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"strings"
	"time"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

const (
	defaultAdminUserActivitySystemLogLimit = 50
	defaultAdminUserActivityUploadLogLimit = 50
	maxAdminUserActivityItemLimit          = 200
)

type adminUserActivityFilters struct {
	From           time.Time
	To             time.Time
	SystemLogLimit int
	UploadLogLimit int
}

type adminUserActivitySummary struct {
	SystemLogTotal int `json:"systemLogTotal"`
	UploadLogTotal int `json:"uploadLogTotal"`
	UploadSuccess  int `json:"uploadSuccess"`
	UploadFailure  int `json:"uploadFailure"`
}

type adminUserActivityResponse struct {
	GeneratedAt    time.Time                `json:"generatedAt"`
	UserID         string                   `json:"userId"`
	From           time.Time                `json:"from"`
	To             time.Time                `json:"to"`
	SystemLogLimit int                      `json:"systemLogLimit"`
	UploadLogLimit int                      `json:"uploadLogLimit"`
	Summary        adminUserActivitySummary `json:"summary"`
	SystemLogs     []systemLogListItem      `json:"systemLogs"`
	UploadLogs     []uploadLogListItem      `json:"uploadLogs"`
}

func parseAdminUserActivityFilters(c fiber.Ctx, now time.Time) (*adminUserActivityFilters, error) {
	from, to, err := resolveUploadLogTimeRange(c.Query("from"), c.Query("to"), now)
	if err != nil {
		return nil, err
	}

	systemLogLimit, err := parsePositiveInt(c.Query("system_log_limit"), defaultAdminUserActivitySystemLogLimit, "system_log_limit")
	if err != nil {
		return nil, err
	}
	uploadLogLimit, err := parsePositiveInt(c.Query("upload_log_limit"), defaultAdminUserActivityUploadLogLimit, "upload_log_limit")
	if err != nil {
		return nil, err
	}

	if systemLogLimit > maxAdminUserActivityItemLimit {
		return nil, fiber.NewError(fiber.StatusBadRequest, "system_log_limit exceeds max allowed size")
	}
	if uploadLogLimit > maxAdminUserActivityItemLimit {
		return nil, fiber.NewError(fiber.StatusBadRequest, "upload_log_limit exceeds max allowed size")
	}

	return &adminUserActivityFilters{
		From:           from,
		To:             to,
		SystemLogLimit: systemLogLimit,
		UploadLogLimit: uploadLogLimit,
	}, nil
}

func handleGetUserActivity(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		targetUserID := strings.TrimSpace(c.Params("target_user_id"))
		if targetUserID == "" {
			writeAdminAuditLog(c, apiHelper, "admin.user.activity.query", "user", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_target_user_id", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "target_user_id is required")
		}

		actorUserID, actorRole, err := currentAdminActor(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.activity.query", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_user_session", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorUnauthorized(c, "missing user session")
		}

		targetUser, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.IDEQ(targetUserID)).
			Select(userSchema.FieldID, userSchema.FieldRole).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				writeAdminAuditLog(c, apiHelper, "admin.user.activity.query", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("target_user_not_found", nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			writeAdminAuditLog(c, apiHelper, "admin.user.activity.query", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_target_user_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query target user")
		}

		if err := ensureAdminCanManageTargetUser(actorUserID, actorRole, targetUser.ID, string(targetUser.Role)); err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.activity.query", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("permission_denied", map[string]any{
				"actorRole":  actorRole,
				"targetRole": normalizeRole(string(targetUser.Role)),
			}))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorForbidden(c, "insufficient permissions")
		}

		filters, err := parseAdminUserActivityFilters(c, time.Now())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.activity.query", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_query_filters", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid query filters")
		}

		systemLogBase := apiHelper.DBManager.DB.SystemLog.Query().Where(
			systemlog.EventTimeGTE(filters.From),
			systemlog.EventTimeLTE(filters.To),
			systemlog.Or(
				systemlog.ActorUserIDEQ(targetUser.ID),
				systemlog.And(
					systemlog.TargetTypeEQ("user"),
					systemlog.TargetIDEQ(targetUser.ID),
				),
			),
		)

		systemLogTotal, err := systemLogBase.Clone().Count(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.activity.query", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("count_system_logs_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to count system logs")
		}

		systemLogRows, err := systemLogBase.Clone().
			Order(
				systemlog.ByEventTime(sql.OrderDesc()),
				systemlog.ByID(sql.OrderDesc()),
			).
			Limit(filters.SystemLogLimit).
			All(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.activity.query", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_system_logs_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query system logs")
		}

		uploadLogBase := apiHelper.DBManager.DB.UploadLog.Query().Where(
			uploadlog.ToolboxUserIDEQ(targetUser.ID),
			uploadlog.UploadTimeGTE(filters.From),
			uploadlog.UploadTimeLTE(filters.To),
		)

		uploadLogTotal, err := uploadLogBase.Clone().Count(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.activity.query", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("count_upload_logs_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to count upload logs")
		}

		uploadSuccess, err := uploadLogBase.Clone().Where(uploadlog.SuccessEQ(true)).Count(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.activity.query", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("count_upload_success_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to count upload success logs")
		}
		uploadFailure := uploadLogTotal - uploadSuccess
		if uploadFailure < 0 {
			uploadFailure = 0
		}

		uploadLogRows, err := uploadLogBase.Clone().
			Order(
				uploadlog.ByUploadTime(sql.OrderDesc()),
				uploadlog.ByID(sql.OrderDesc()),
			).
			Limit(filters.UploadLogLimit).
			All(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.activity.query", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_upload_logs_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query upload logs")
		}

		resp := adminUserActivityResponse{
			GeneratedAt:    time.Now().UTC(),
			UserID:         targetUser.ID,
			From:           filters.From.UTC(),
			To:             filters.To.UTC(),
			SystemLogLimit: filters.SystemLogLimit,
			UploadLogLimit: filters.UploadLogLimit,
			Summary: adminUserActivitySummary{
				SystemLogTotal: systemLogTotal,
				UploadLogTotal: uploadLogTotal,
				UploadSuccess:  uploadSuccess,
				UploadFailure:  uploadFailure,
			},
			SystemLogs: buildSystemLogItems(systemLogRows),
			UploadLogs: buildUploadLogItems(uploadLogRows),
		}

		writeAdminAuditLog(c, apiHelper, "admin.user.activity.query", "user", targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"from":           resp.From.Format(time.RFC3339),
			"to":             resp.To.Format(time.RFC3339),
			"systemLogTotal": systemLogTotal,
			"uploadLogTotal": uploadLogTotal,
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}
