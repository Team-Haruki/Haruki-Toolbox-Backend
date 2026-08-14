package adminrisk

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	platformPagination "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/pagination"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/riskevent"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

func parseRiskEventSort(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultRiskEventSort, nil
	}
	switch trimmed {
	case riskEventSortEventTimeDesc, riskEventSortEventTimeAsc, riskEventSortIDDesc, riskEventSortIDAsc:
		return trimmed, nil
	default:
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid sort option")
	}
}

func parseRiskEventFilters(c fiber.Ctx, now time.Time) (*riskEventFilters, error) {
	from, to, err := resolveRiskEventTimeRange(c.Query("from"), c.Query("to"), now)
	if err != nil {
		return nil, err
	}
	status := strings.ToLower(strings.TrimSpace(c.Query("status")))
	if status != "" && !slices.Contains(validRiskStatuses, status) {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid status filter")
	}
	severity := strings.ToLower(strings.TrimSpace(c.Query("severity")))
	if severity != "" && !slices.Contains(validRiskSeverities, severity) {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid severity filter")
	}
	page, pageSize, err := platformPagination.ParsePageAndPageSize(c, defaultRiskEventPage, defaultRiskEventPageSize, maxRiskEventPageSize)
	if err != nil {
		return nil, err
	}
	sortValue, err := parseRiskEventSort(c.Query("sort"))
	if err != nil {
		return nil, err
	}

	return &riskEventFilters{
		From:         from,
		To:           to,
		Status:       status,
		Severity:     severity,
		ActorUserID:  strings.TrimSpace(c.Query("actor_user_id")),
		TargetUserID: strings.TrimSpace(c.Query("target_user_id")),
		Action:       strings.TrimSpace(c.Query("action")),
		Page:         page,
		PageSize:     pageSize,
		Sort:         sortValue,
	}, nil
}

func applyRiskEventFilters(query *postgresql.RiskEventQuery, filters *riskEventFilters) *postgresql.RiskEventQuery {
	q := query.Where(
		riskevent.EventTimeGTE(filters.From),
		riskevent.EventTimeLTE(filters.To),
	)
	if filters.Status != "" {
		q = q.Where(riskevent.StatusEQ(riskevent.Status(filters.Status)))
	}
	if filters.Severity != "" {
		q = q.Where(riskevent.SeverityEQ(riskevent.Severity(filters.Severity)))
	}
	if filters.ActorUserID != "" {
		q = q.Where(riskevent.ActorUserIDEQ(filters.ActorUserID))
	}
	if filters.TargetUserID != "" {
		q = q.Where(riskevent.TargetUserIDEQ(filters.TargetUserID))
	}
	if filters.Action != "" {
		q = q.Where(riskevent.ActionContainsFold(filters.Action))
	}
	return q
}

// scopeRiskEventsForAdminActor keeps count and pagination on the same role
// boundary as the returned rows. A plain admin cannot inspect an event that
// refers to a super admin as either its actor or target.
func scopeRiskEventsForAdminActor(
	ctx context.Context,
	db *postgresql.Client,
	query *postgresql.RiskEventQuery,
	actorRole string,
) (*postgresql.RiskEventQuery, error) {
	if adminCoreModule.NormalizeRole(actorRole) == adminCoreModule.RoleSuperAdmin {
		return query, nil
	}

	superAdminIDs, err := db.User.Query().
		Where(userSchema.RoleEQ(userSchema.RoleSuperAdmin)).
		IDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("query super admin IDs: %w", err)
	}
	if len(superAdminIDs) == 0 {
		return query, nil
	}

	return query.Where(
		riskevent.Or(
			riskevent.ActorUserIDIsNil(),
			riskevent.ActorUserIDNotIn(superAdminIDs...),
		),
		riskevent.Or(
			riskevent.TargetUserIDIsNil(),
			riskevent.TargetUserIDNotIn(superAdminIDs...),
		),
	), nil
}

func ensureRiskEventUserReferencesManageable(
	ctx context.Context,
	db *postgresql.Client,
	adminUserID string,
	adminRole string,
	eventActorUserID string,
	targetUserID string,
) error {
	if err := ensureRiskEventUserReferenceManageable(ctx, db, adminUserID, adminRole, eventActorUserID, true); err != nil {
		return err
	}
	return ensureRiskEventUserReferenceManageable(ctx, db, adminUserID, adminRole, targetUserID, false)
}

func ensureRiskEventUserReferenceManageable(
	ctx context.Context,
	db *postgresql.Client,
	adminUserID string,
	adminRole string,
	referencedUserID string,
	allowSelf bool,
) error {
	referencedUserID = strings.TrimSpace(referencedUserID)
	if referencedUserID == "" || allowSelf && referencedUserID == adminUserID {
		return nil
	}
	if referencedUserID == adminUserID {
		return adminCoreModule.EnsureAdminCanManageTargetUser(adminUserID, adminRole, referencedUserID, adminRole)
	}

	referencedUser, err := db.User.Query().
		Where(userSchema.IDEQ(referencedUserID)).
		Select(userSchema.FieldID, userSchema.FieldRole).
		Only(ctx)
	if err != nil {
		// Risk events may legitimately retain references to deleted or external
		// users, so preserve that existing compatibility behavior.
		if postgresql.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("query referenced user %q: %w", referencedUserID, err)
	}

	return adminCoreModule.EnsureAdminCanManageTargetUser(
		adminUserID,
		adminRole,
		referencedUser.ID,
		string(referencedUser.Role),
	)
}

func optionalRiskEventUserID(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func applyRiskEventSort(query *postgresql.RiskEventQuery, sortValue string) *postgresql.RiskEventQuery {
	switch sortValue {
	case riskEventSortEventTimeAsc:
		return query.Order(riskevent.ByEventTime(sql.OrderAsc()), riskevent.ByID(sql.OrderAsc()))
	case riskEventSortIDDesc:
		return query.Order(riskevent.ByID(sql.OrderDesc()))
	case riskEventSortIDAsc:
		return query.Order(riskevent.ByID(sql.OrderAsc()))
	default:
		return query.Order(riskevent.ByEventTime(sql.OrderDesc()), riskevent.ByID(sql.OrderDesc()))
	}
}

func buildRiskEventItems(rows []*postgresql.RiskEvent) []riskEventItem {
	items := make([]riskEventItem, 0, len(rows))
	for _, row := range rows {
		item := riskEventItem{
			ID:        row.ID,
			EventTime: row.EventTime.UTC(),
			Status:    string(row.Status),
			Severity:  string(row.Severity),
			Source:    row.Source,
			Metadata:  row.Metadata,
		}
		if row.ActorUserID != nil {
			item.ActorUserID = *row.ActorUserID
		}
		if row.TargetUserID != nil {
			item.TargetUserID = *row.TargetUserID
		}
		if row.IP != nil {
			item.IP = *row.IP
		}
		if row.Action != nil {
			item.Action = *row.Action
		}
		if row.Reason != nil {
			item.Reason = *row.Reason
		}
		if row.ResolvedAt != nil {
			resolvedAt := row.ResolvedAt.UTC()
			item.ResolvedAt = &resolvedAt
		}
		if row.ResolvedBy != nil {
			item.ResolvedBy = *row.ResolvedBy
		}
		items = append(items, item)
	}
	return items
}
