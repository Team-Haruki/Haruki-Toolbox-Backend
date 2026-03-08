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

const defaultAdminUserDetailActivityWindowHours = 24

type adminUserDetailActivitySummary struct {
	WindowHours    int        `json:"windowHours"`
	From           time.Time  `json:"from"`
	To             time.Time  `json:"to"`
	SystemLogTotal int        `json:"systemLogTotal"`
	UploadLogTotal int        `json:"uploadLogTotal"`
	UploadSuccess  int        `json:"uploadSuccess"`
	UploadFailure  int        `json:"uploadFailure"`
	LastSystemLog  *time.Time `json:"lastSystemLog,omitempty"`
	LastUploadLog  *time.Time `json:"lastUploadLog,omitempty"`
}

type adminUserDetailResponse struct {
	UserData        harukiAPIHelper.HarukiToolboxUserData `json:"userData"`
	Banned          bool                                  `json:"banned"`
	BanReason       *string                               `json:"banReason,omitempty"`
	CreatedAt       *time.Time                            `json:"createdAt,omitempty"`
	ActivitySummary *adminUserDetailActivitySummary       `json:"activitySummary,omitempty"`
}

type adminForceLogoutResponse struct {
	UserID          string `json:"userId"`
	ClearedSessions bool   `json:"clearedSessions"`
}

func queryAdminUserDetailActivitySummary(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, targetUserID string, now time.Time) (*adminUserDetailActivitySummary, error) {
	now = now.UTC()
	from := now.Add(-defaultAdminUserDetailActivityWindowHours * time.Hour)

	systemLogBase := apiHelper.DBManager.DB.SystemLog.Query().Where(
		systemlog.EventTimeGTE(from),
		systemlog.EventTimeLTE(now),
		systemlog.Or(
			systemlog.ActorUserIDEQ(targetUserID),
			systemlog.And(
				systemlog.TargetTypeEQ("user"),
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

		activitySummary, err := queryAdminUserDetailActivitySummary(c, apiHelper, targetUserID, time.Now())
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
			writeAdminAuditLog(c, apiHelper, "admin.user.force_logout", "user", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_target_user_id", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "target_user_id is required")
		}

		actorUserID, actorRole, err := currentAdminActor(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.force_logout", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_user_session", nil))
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
				writeAdminAuditLog(c, apiHelper, "admin.user.force_logout", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("target_user_not_found", nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			writeAdminAuditLog(c, apiHelper, "admin.user.force_logout", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_target_user_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query target user")
		}

		if err := ensureAdminCanManageTargetUser(actorUserID, actorRole, targetUser.ID, string(targetUser.Role)); err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.force_logout", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("permission_denied", map[string]any{
				"actorRole":  actorRole,
				"targetRole": normalizeRole(string(targetUser.Role)),
			}))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorForbidden(c, "insufficient permissions")
		}

		if apiHelper.DBManager == nil || apiHelper.DBManager.Redis == nil || apiHelper.DBManager.Redis.Redis == nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.force_logout", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("session_store_unavailable", nil))
			return harukiAPIHelper.ErrorInternal(c, "session store unavailable")
		}

		if err := harukiAPIHelper.ClearUserSessions(apiHelper.DBManager.Redis.Redis, targetUser.ID); err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.force_logout", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("clear_sessions_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to clear user sessions")
		}

		resp := adminForceLogoutResponse{
			UserID:          targetUser.ID,
			ClearedSessions: true,
		}
		writeAdminAuditLog(c, apiHelper, "admin.user.force_logout", "user", targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, nil)
		return harukiAPIHelper.SuccessResponse(c, "user sessions cleared", &resp)
	}
}
