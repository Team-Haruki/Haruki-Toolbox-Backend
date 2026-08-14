package adminsyslog

import (
	"context"

	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	platformTime "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/timeutil"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"sort"
	"time"

	"github.com/gofiber/fiber/v3"
)

// currentActorIsSuperAdmin reports whether the authenticated admin is a super_admin.
func currentActorIsSuperAdmin(c fiber.Ctx) (bool, error) {
	_, actorRole, err := adminCoreModule.CurrentAdminActor(c)
	if err != nil {
		return false, err
	}
	return adminCoreModule.NormalizeRole(actorRole) == adminCoreModule.RoleSuperAdmin, nil
}

// scopeSystemLogsForActor hides super_admin-actor rows from non-super-admins, so a
// plain admin cannot read a super_admin's audit history (IP/UA/action timeline).
// Rows with no actor_role (anonymous/user/system) stay visible.
func scopeSystemLogsForActor(ctx context.Context, db *postgresql.Client, query *postgresql.SystemLogQuery, isSuperAdmin bool) (*postgresql.SystemLogQuery, error) {
	actorRole := adminCoreModule.RoleAdmin
	if isSuperAdmin {
		actorRole = adminCoreModule.RoleSuperAdmin
	}
	return adminCoreModule.ScopeSystemLogsForAdminActor(ctx, db, query, actorRole)
}

type categoryCount struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type groupedFieldCount struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

func normalizeCategoryCounts(rows []groupedFieldCount) []categoryCount {
	out := make([]categoryCount, 0, len(rows))
	for _, row := range rows {
		out = append(out, categoryCount(row))
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Key < out[j].Key
		}
		return out[i].Count > out[j].Count
	})

	return out
}

func resolveUploadLogTimeRange(fromRaw, toRaw string, now time.Time) (time.Time, time.Time, error) {
	return platformTime.ResolveUploadLogTimeRange(fromRaw, toRaw, now)
}

func respondFiberOrBadRequest(c fiber.Ctx, err error, fallbackMessage string) error {
	return adminCoreModule.RespondFiberOrBadRequest(c, err, fallbackMessage)
}

var adminNow = time.Now

func adminNowUTC() time.Time {
	return adminNow().UTC()
}

func requireAdmin(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return adminCoreModule.RequireAdmin(apiHelper)
}
