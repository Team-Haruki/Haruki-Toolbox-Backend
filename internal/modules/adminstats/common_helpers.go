package adminstats

import (
	"context"

	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/uploadlog"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	"time"

	"github.com/gofiber/fiber/v3"
)

func respondFiberOrBadRequest(c fiber.Ctx, err error, fallbackMessage string) error {
	if fiberErr, ok := err.(*fiber.Error); ok {
		return c.Status(fiberErr.Code).JSON(fiber.Map{
			"status":  fiberErr.Code,
			"message": fiberErr.Message,
		})
	}
	return harukiAPIHelper.ErrorBadRequest(c, fallbackMessage)
}

func scopeUploadLogsForAdminActor(
	ctx context.Context,
	db *postgresql.Client,
	query *postgresql.UploadLogQuery,
	actorRole string,
) (*postgresql.UploadLogQuery, error) {
	if adminCoreModule.NormalizeRole(actorRole) == adminCoreModule.RoleSuperAdmin {
		return query, nil
	}
	superAdminIDs, err := db.User.Query().Where(userSchema.RoleEQ(userSchema.RoleSuperAdmin)).IDs(ctx)
	if err != nil {
		return nil, err
	}
	if len(superAdminIDs) == 0 {
		return query, nil
	}
	return query.Where(uploadlog.Or(
		uploadlog.ToolboxUserIDIsNil(),
		uploadlog.ToolboxUserIDNotIn(superAdminIDs...),
	)), nil
}

var adminNow = time.Now

func adminNowUTC() time.Time {
	return adminNow().UTC()
}
