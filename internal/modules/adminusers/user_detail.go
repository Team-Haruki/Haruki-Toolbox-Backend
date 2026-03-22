package adminusers

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
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

func queryAdminUserDetailActivitySummary(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, targetUserID string, now time.Time) (*adminUserDetailActivitySummary, error) {
	now = now.UTC()
	from := now.Add(-defaultAdminUserDetailActivityWindowHours * time.Hour)

	systemLogBase := apiHelper.DBManager.DB.SystemLog.Query().Where(
		systemlog.EventTimeGTE(from),
		systemlog.EventTimeLTE(now),
		systemlog.Or(
			systemlog.ActorUserIDEQ(targetUserID),
			systemlog.And(
				systemlog.TargetTypeEQ(adminAuditTargetTypeUser),
				systemlog.TargetIDEQ(targetUserID),
			),
		),
	)

	systemLogTotal, err := systemLogBase.Clone().Count(c.Context())
	if err != nil {
		return nil, err
	}

	uploadLogBase := apiHelper.DBManager.DB.UploadLog.Query().Where(
		uploadlog.ToolboxUserIDEQ(targetUserID),
		uploadlog.UploadTimeGTE(from),
		uploadlog.UploadTimeLTE(now),
	)

	uploadLogTotal, err := uploadLogBase.Clone().Count(c.Context())
	if err != nil {
		return nil, err
	}

	uploadSuccess, err := uploadLogBase.Clone().Where(uploadlog.SuccessEQ(true)).Count(c.Context())
	if err != nil {
		return nil, err
	}
	uploadFailure := uploadLogTotal - uploadSuccess
	if uploadFailure < 0 {
		uploadFailure = 0
	}

	summary := &adminUserDetailActivitySummary{
		WindowHours:    defaultAdminUserDetailActivityWindowHours,
		From:           from,
		To:             now,
		SystemLogTotal: systemLogTotal,
		UploadLogTotal: uploadLogTotal,
		UploadSuccess:  uploadSuccess,
		UploadFailure:  uploadFailure,
	}

	latestSystemLog, err := systemLogBase.Clone().Order(
		systemlog.ByEventTime(sql.OrderDesc()),
		systemlog.ByID(sql.OrderDesc()),
	).First(c.Context())
	if err != nil {
		if !postgresql.IsNotFound(err) {
			return nil, err
		}
	} else {
		lastSystemLog := latestSystemLog.EventTime.UTC()
		summary.LastSystemLog = &lastSystemLog
	}

	latestUploadLog, err := uploadLogBase.Clone().Order(
		uploadlog.ByUploadTime(sql.OrderDesc()),
		uploadlog.ByID(sql.OrderDesc()),
	).First(c.Context())
	if err != nil {
		if !postgresql.IsNotFound(err) {
			return nil, err
		}
	} else {
		lastUploadLog := latestUploadLog.UploadTime.UTC()
		summary.LastUploadLog = &lastUploadLog
	}

	return summary, nil
}

func handleGetUserDetail(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		targetUserID := strings.TrimSpace(c.Params("target_user_id"))
		if targetUserID == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "target_user_id is required")
		}

		dbUser, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.IDEQ(targetUserID)).
			WithSocialPlatformInfo().
			WithAuthorizedSocialPlatforms().
			WithGameAccountBindings().
			WithIosScriptCode().
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to query user detail")
		}

		activitySummary, err := queryAdminUserDetailActivitySummary(c, apiHelper, targetUserID, adminNow())
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to query user activity summary")
		}

		var createdAt *time.Time
		if dbUser.CreatedAt != nil {
			createdAtUTC := dbUser.CreatedAt.UTC()
			createdAt = &createdAtUTC
		}

		resp := adminUserDetailResponse{
			UserData:        harukiAPIHelper.BuildUserDataFromDBUser(dbUser, nil),
			Banned:          dbUser.Banned,
			AllowCNMysekai:  dbUser.AllowCnMysekai,
			BanReason:       dbUser.BanReason,
			CreatedAt:       createdAt,
			ActivitySummary: activitySummary,
		}
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleForceLogoutUser(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		targetUserID := strings.TrimSpace(c.Params("target_user_id"))
		if targetUserID == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserForceLogout, adminAuditTargetTypeUser, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingTargetUserID, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "target_user_id is required")
		}

		actorUserID, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserForceLogout, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingUserSession, nil))
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

		targetUser, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.IDEQ(targetUserID)).
			Select(userSchema.FieldID, userSchema.FieldRole, userSchema.FieldKratosIdentityID).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserForceLogout, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonTargetUserNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserForceLogout, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryTargetUserFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query target user")
		}

		if err := adminCoreModule.EnsureAdminCanManageTargetUser(actorUserID, actorRole, targetUser.ID, string(targetUser.Role)); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserForceLogout, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPermissionDenied, map[string]any{
				"actorRole":  actorRole,
				"targetRole": adminCoreModule.NormalizeRole(string(targetUser.Role)),
			}))
			return adminCoreModule.RespondFiberOrForbidden(c, err, "insufficient permissions")
		}

		if apiHelper.DBManager == nil || apiHelper.DBManager.Redis == nil || apiHelper.DBManager.Redis.Redis == nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserForceLogout, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonSessionStoreUnavailable, nil))
			return harukiAPIHelper.ErrorInternal(c, "session store unavailable")
		}

		sessionClearFailed := false
		kratosIdentityID := ""
		if targetUser.KratosIdentityID != nil {
			kratosIdentityID = strings.TrimSpace(*targetUser.KratosIdentityID)
		}
		if apiHelper != nil && apiHelper.SessionHandler != nil && apiHelper.SessionHandler.UsesKratosProvider() && kratosIdentityID != "" {
			if err := apiHelper.SessionHandler.RevokeKratosSessionsByIdentityID(c.Context(), kratosIdentityID); err != nil {
				sessionClearFailed = true
			}
		}
		if err := harukiAPIHelper.ClearUserSessions(apiHelper.RedisClient(), targetUser.ID); err != nil {
			sessionClearFailed = true
		}

		resp := adminForceLogoutResponse{
			UserID:          targetUser.ID,
			ClearedSessions: !sessionClearFailed,
		}
		if sessionClearFailed {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserForceLogout, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
				"sessionClearFailed": true,
			})
			return harukiAPIHelper.SuccessResponse(c, "user sessions cleared partially", &resp)
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserForceLogout, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, nil)
		return harukiAPIHelper.SuccessResponse(c, "user sessions cleared", &resp)
	}
}
