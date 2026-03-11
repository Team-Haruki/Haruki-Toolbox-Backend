package adminusers

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	platformPagination "haruki-suite/internal/platform/pagination"
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

func parseAdminUserActivityFilters(c fiber.Ctx, now time.Time) (*adminUserActivityFilters, error) {
	from, to, err := resolveUploadLogTimeRange(c.Query("from"), c.Query("to"), now)
	if err != nil {
		return nil, err
	}

	systemLogLimit, err := platformPagination.ParsePositiveInt(c.Query("system_log_limit"), defaultAdminUserActivitySystemLogLimit, "system_log_limit")
	if err != nil {
		return nil, err
	}
	uploadLogLimit, err := platformPagination.ParsePositiveInt(c.Query("upload_log_limit"), defaultAdminUserActivityUploadLogLimit, "upload_log_limit")
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
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserActivityQuery, adminAuditTargetTypeUser, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingTargetUserID, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "target_user_id is required")
		}

		actorUserID, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserActivityQuery, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingUserSession, nil))
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

		targetUser, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.IDEQ(targetUserID)).
			Select(userSchema.FieldID, userSchema.FieldRole).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserActivityQuery, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonTargetUserNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserActivityQuery, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryTargetUserFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query target user")
		}

		if err := adminCoreModule.EnsureAdminCanManageTargetUser(actorUserID, actorRole, targetUser.ID, string(targetUser.Role)); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserActivityQuery, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPermissionDenied, map[string]any{
				"actorRole":  actorRole,
				"targetRole": adminCoreModule.NormalizeRole(string(targetUser.Role)),
			}))
			return adminCoreModule.RespondFiberOrForbidden(c, err, "insufficient permissions")
		}

		filters, err := parseAdminUserActivityFilters(c, adminNow())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserActivityQuery, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidQueryFilters, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid query filters")
		}

		systemLogBase := apiHelper.DBManager.DB.SystemLog.Query().Where(
			systemlog.EventTimeGTE(filters.From),
			systemlog.EventTimeLTE(filters.To),
			systemlog.Or(
				systemlog.ActorUserIDEQ(targetUser.ID),
				systemlog.And(
					systemlog.TargetTypeEQ(adminAuditTargetTypeUser),
					systemlog.TargetIDEQ(targetUser.ID),
				),
			),
		)

		systemLogTotal, err := systemLogBase.Clone().Count(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserActivityQuery, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonCountSystemLogsFailed, nil))
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
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserActivityQuery, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQuerySystemLogsFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query system logs")
		}

		uploadLogBase := apiHelper.DBManager.DB.UploadLog.Query().Where(
			uploadlog.ToolboxUserIDEQ(targetUser.ID),
			uploadlog.UploadTimeGTE(filters.From),
			uploadlog.UploadTimeLTE(filters.To),
		)

		uploadLogTotal, err := uploadLogBase.Clone().Count(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserActivityQuery, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonCountUploadLogsFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to count upload logs")
		}

		uploadSuccess, err := uploadLogBase.Clone().Where(uploadlog.SuccessEQ(true)).Count(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserActivityQuery, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonCountUploadSuccessFailed, nil))
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
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserActivityQuery, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryUploadLogsFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query upload logs")
		}

		resp := adminUserActivityResponse{
			GeneratedAt:    adminNowUTC(),
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
			SystemLogs: adminCoreModule.BuildSystemLogItems(systemLogRows),
			UploadLogs: adminCoreModule.BuildUploadLogItems(uploadLogRows),
		}

		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserActivityQuery, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"from":           resp.From.Format(time.RFC3339),
			"to":             resp.To.Format(time.RFC3339),
			"systemLogTotal": systemLogTotal,
			"uploadLogTotal": uploadLogTotal,
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}
