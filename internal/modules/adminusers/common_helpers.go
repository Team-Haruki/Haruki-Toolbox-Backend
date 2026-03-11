package adminusers

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	platformTime "haruki-suite/internal/platform/timeutil"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

func resolveUploadLogTimeRange(fromRaw, toRaw string, now time.Time) (time.Time, time.Time, error) {
	return platformTime.ResolveUploadLogTimeRange(fromRaw, toRaw, now)
}

func sanitizeBanReason(raw *string) (*string, error) {
	if raw == nil {
		return nil, nil
	}

	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil, nil
	}
	if len(trimmed) > maxBanReasonLength {
		return nil, fiber.NewError(fiber.StatusBadRequest, "ban reason exceeds max length")
	}

	reason := trimmed
	return &reason, nil
}

var adminNow = time.Now

func adminNowUTC() time.Time {
	return adminNow().UTC()
}

func applyManagedTargetUserUpdateGuards(query *postgresql.UserUpdate, actorUserID, actorRole, targetUserID string) *postgresql.UserUpdate {
	guarded := query.Where(
		userSchema.IDEQ(strings.TrimSpace(targetUserID)),
		userSchema.IDNEQ(strings.TrimSpace(actorUserID)),
	)
	if adminCoreModule.NormalizeRole(actorRole) != adminCoreModule.RoleSuperAdmin {
		guarded = guarded.Where(userSchema.RoleNEQ(userSchema.RoleSuperAdmin))
	}
	return guarded
}

func resolveManagedTargetUserUpdateMiss(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, actorUserID, actorRole, targetUserID string) error {
	target, err := apiHelper.DBManager.DB.User.Query().
		Where(userSchema.IDEQ(strings.TrimSpace(targetUserID))).
		Select(userSchema.FieldID, userSchema.FieldRole).
		Only(c.Context())
	if err != nil {
		if postgresql.IsNotFound(err) {
			return fiber.NewError(fiber.StatusNotFound, "user not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "failed to query target user")
	}
	if err := adminCoreModule.EnsureAdminCanManageTargetUser(actorUserID, actorRole, target.ID, string(target.Role)); err != nil {
		return err
	}
	return fiber.NewError(fiber.StatusConflict, "target user changed during update")
}
