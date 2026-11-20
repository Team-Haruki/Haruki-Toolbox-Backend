package adminusers

import (
	"context"
	"strings"
	"time"

	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	platformTime "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/timeutil"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"

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

func visibleAdminTargetUsers(query *postgresql.UserQuery, actorRole string) *postgresql.UserQuery {
	if adminCoreModule.NormalizeRole(actorRole) == adminCoreModule.RoleSuperAdmin {
		return query
	}
	return query.Where(userSchema.RoleNEQ(userSchema.RoleSuperAdmin))
}

func scopeAdminUserQueryForActor(query *postgresql.UserQuery, actorUserID, actorRole string) *postgresql.UserQuery {
	return visibleAdminTargetUsers(query, actorRole).Where(userSchema.IDNEQ(strings.TrimSpace(actorUserID)))
}

// Check visibility before waiting on a reservation that could otherwise
// reveal activity for a hidden target. Callers still authorize after locking.
func lockVisibleAdminTargets(ctx context.Context, db *postgresql.Client, role string, ids ...string) (func(), []string, error) {
	if adminCoreModule.NormalizeRole(role) != adminCoreModule.RoleSuperAdmin {
		visible, err := visibleAdminTargetUsers(db.User.Query(), role).Where(userSchema.IDIn(ids...)).IDs(ctx)
		if err != nil {
			return nil, nil, err
		}
		ids = visible
	}
	release, err := db.LockUserMutations(ctx, ids...)
	return release, ids, err
}

func lockVisibleAdminTarget(ctx context.Context, db *postgresql.Client, role, id string) (func(), error) {
	release, ids, err := lockVisibleAdminTargets(ctx, db, role, id)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		release()
		return nil, fiber.NewError(fiber.StatusNotFound, "user not found")
	}
	return release, nil
}

func resolveManagedTargetUserUpdateMiss(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, actorUserID, actorRole, targetUserID string) error {
	target, err := visibleAdminTargetUsers(apiHelper.DBManager.DB.User.Query(), actorRole).
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
