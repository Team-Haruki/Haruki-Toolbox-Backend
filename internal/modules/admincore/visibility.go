package admincore

import (
	"context"

	"entgo.io/ent/dialect/sql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/systemlog"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
)

// HasSuperAdminReference checks current roles for nullable user ID columns.
func HasSuperAdminReference(s *sql.Selector, columns ...string) *sql.Predicate {
	users := sql.Table(userSchema.Table).As("protected_users")
	references := make([]*sql.Predicate, 0, len(columns))
	for _, column := range columns {
		references = append(references, sql.ColumnsEQ(users.C(userSchema.FieldID), s.C(column)))
	}
	return sql.Exists(sql.Select(users.C(userSchema.FieldID)).From(users).
		Where(sql.And(sql.EQ(users.C(userSchema.FieldRole), userSchema.RoleSuperAdmin), sql.Or(references...))))
}

// ScopeSystemLogsForAdminActor keeps audit-log counts, aggregations, exports,
// and details on the same role boundary. A plain admin must not inspect either
// a super_admin actor or any log row targeting a super_admin user.
func ScopeSystemLogsForAdminActor(
	ctx context.Context,
	db *postgresql.Client,
	query *postgresql.SystemLogQuery,
	actorRole string,
) (*postgresql.SystemLogQuery, error) {
	if NormalizeRole(actorRole) == RoleSuperAdmin {
		return query, nil
	}

	superAdminIDs, err := db.User.Query().
		Where(userSchema.RoleEQ(userSchema.RoleSuperAdmin)).
		IDs(ctx)
	if err != nil {
		return nil, err
	}

	scoped := query.Where(systemlog.Or(
		systemlog.ActorRoleIsNil(),
		systemlog.ActorRoleNEQ(RoleSuperAdmin),
	))
	if len(superAdminIDs) > 0 {
		scoped = scoped.Where(systemlog.Or(
			systemlog.ActorUserIDIsNil(),
			systemlog.ActorUserIDNotIn(superAdminIDs...),
		))
		// Spell out the inverse instead of NOT(A AND B): SQL's three-valued
		// NULL logic would otherwise drop rows with no target.
		scoped = scoped.Where(systemlog.Or(
			systemlog.TargetTypeIsNil(),
			systemlog.TargetTypeNEQ("user"),
			systemlog.TargetIDIsNil(),
			systemlog.TargetIDNotIn(superAdminIDs...),
		))
	}
	return scoped, nil
}
