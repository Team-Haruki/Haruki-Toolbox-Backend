package adminstats

import (
	"context"

	"time"

	"entgo.io/ent/dialect/sql"
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/uploadlog"

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
	actorIDs ...string,
) (*postgresql.UploadLogQuery, error) {
	if adminCoreModule.NormalizeRole(actorRole) == adminCoreModule.RoleSuperAdmin {
		return query, nil
	}
	return query.Where(func(s *sql.Selector) {
		users := sql.Table("users")
		allowed := sql.Select(users.C("id")).From(users).Where(sql.NEQ(users.C("role"), adminCoreModule.RoleSuperAdmin))
		s.Where(sql.In(s.C(uploadlog.FieldToolboxUserID), allowed))
		s.Where(sql.EQ(s.C(uploadlog.FieldIdentityVerified), true))
		s.Where(sql.Or(sql.IsNull(s.C(uploadlog.FieldActorUserID)), sql.In(s.C(uploadlog.FieldActorUserID), allowed)))
		if len(actorIDs) > 0 {
			s.Where(sql.NEQ(s.C(uploadlog.FieldToolboxUserID), actorIDs[0]))
			s.Where(sql.Or(sql.IsNull(s.C(uploadlog.FieldActorUserID)), sql.NEQ(s.C(uploadlog.FieldActorUserID), actorIDs[0])))
		}
	}), nil
}

var adminNow = time.Now

func adminNowUTC() time.Time {
	return adminNow().UTC()
}
