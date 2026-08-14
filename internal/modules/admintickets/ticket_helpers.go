package admintickets

import (
	"context"
	"fmt"

	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	ticketsModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/tickets"
	platformPagination "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/pagination"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/ticket"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	"strings"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"
)

type adminTicketQuickFilter string

const (
	adminTicketQuickFilterPendingAdmin adminTicketQuickFilter = "pending_admin"
	adminTicketQuickFilterPendingUser  adminTicketQuickFilter = "pending_user"
	adminTicketQuickFilterUnassigned   adminTicketQuickFilter = "unassigned"
	adminTicketQuickFilterMine         adminTicketQuickFilter = "mine"
	adminTicketQuickFilterHighOrUrgent adminTicketQuickFilter = "high_or_urgent"
)

func buildAdminTicketListItem(row *postgresql.Ticket, creatorNameByUserID map[string]string) adminTicketListItem {
	item := adminTicketListItem{
		TicketID:      row.TicketID,
		CreatorUserID: row.CreatorUserID,
		Subject:       row.Subject,
		Priority:      string(row.Priority),
		Status:        string(row.Status),
		CreatedAt:     row.CreatedAt.UTC(),
		UpdatedAt:     row.UpdatedAt.UTC(),
	}
	if creatorNameByUserID != nil {
		item.CreatorUserName = strings.TrimSpace(creatorNameByUserID[row.CreatorUserID])
	}
	if row.Category != nil {
		item.Category = *row.Category
	}
	if row.AssigneeAdminID != nil {
		item.AssigneeAdminID = *row.AssigneeAdminID
		if creatorNameByUserID != nil {
			item.AssigneeAdminName = strings.TrimSpace(creatorNameByUserID[*row.AssigneeAdminID])
		}
	}
	if row.ClosedAt != nil {
		closed := row.ClosedAt.UTC()
		item.ClosedAt = &closed
	}
	applyAdminTicketLatestMessageSummary(&item, row.Edges.Messages)
	return item
}

func loadAdminTicketUserNames(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userIDs []string) (map[string]string, error) {
	if len(userIDs) == 0 {
		return map[string]string{}, nil
	}

	seen := make(map[string]struct{}, len(userIDs))
	uniqueIDs := make([]string, 0, len(userIDs))
	for _, rawID := range userIDs {
		userID := strings.TrimSpace(rawID)
		if userID == "" {
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		uniqueIDs = append(uniqueIDs, userID)
	}
	if len(uniqueIDs) == 0 {
		return map[string]string{}, nil
	}

	users, err := apiHelper.DBManager.DB.User.Query().
		Where(userSchema.IDIn(uniqueIDs...)).
		Select(userSchema.FieldID, userSchema.FieldName).
		All(c.Context())
	if err != nil {
		return nil, err
	}

	nameByUserID := make(map[string]string, len(users))
	for _, row := range users {
		nameByUserID[row.ID] = row.Name
	}
	return nameByUserID, nil
}

func latestAdminTicketMessage(rows []*postgresql.TicketMessage) *postgresql.TicketMessage {
	var latest *postgresql.TicketMessage
	for _, row := range rows {
		if row == nil {
			continue
		}
		if latest == nil || row.CreatedAt.After(latest.CreatedAt) || (row.CreatedAt.Equal(latest.CreatedAt) && row.ID > latest.ID) {
			latest = row
		}
	}
	return latest
}

func normalizeAdminTicketMessagePreview(raw string) string {
	trimmed := strings.TrimSpace(strings.Join(strings.Fields(raw), " "))
	if trimmed == "" {
		return ""
	}
	if utf8.RuneCountInString(trimmed) <= maxAdminTicketPreviewLength {
		return trimmed
	}
	runes := []rune(trimmed)
	if maxAdminTicketPreviewLength <= 1 {
		return string(runes[:maxAdminTicketPreviewLength])
	}
	return string(runes[:maxAdminTicketPreviewLength-1]) + "…"
}

func applyAdminTicketLatestMessageSummary(item *adminTicketListItem, rows []*postgresql.TicketMessage) {
	if item == nil {
		return
	}
	latest := latestAdminTicketMessage(rows)
	if latest == nil {
		return
	}
	item.LastMessageSenderRole = string(latest.SenderRole)
	item.LastMessagePreview = normalizeAdminTicketMessagePreview(latest.Message)
	internal := latest.Internal
	item.LastMessageInternal = &internal
	lastMessageAt := latest.CreatedAt.UTC()
	item.LastMessageAt = &lastMessageAt
}

func buildAdminTicketStatusEventMessage(previous ticket.Status, next ticket.Status) string {
	return ticketsModule.BuildStatusEventMessage(previous, next)
}

func formatAdminTicketAssigneeLabel(assigneeAdminID string, nameByUserID map[string]string) string {
	assigneeAdminID = strings.TrimSpace(assigneeAdminID)
	if assigneeAdminID == "" {
		return "Unassigned"
	}
	if nameByUserID != nil {
		if name := strings.TrimSpace(nameByUserID[assigneeAdminID]); name != "" {
			return fmt.Sprintf("%s (%s)", name, assigneeAdminID)
		}
	}
	return assigneeAdminID
}

func buildAdminTicketAssigneeEventMessage(previous string, next string, nameByUserID map[string]string) string {
	return fmt.Sprintf("Assignee changed: %s -> %s", formatAdminTicketAssigneeLabel(previous, nameByUserID), formatAdminTicketAssigneeLabel(next, nameByUserID))
}

func buildAdminTicketMessageItems(rows []*postgresql.TicketMessage, userNameByUserID map[string]string) []adminTicketMessageItem {
	items := make([]adminTicketMessageItem, 0, len(rows))
	for _, row := range rows {
		item := adminTicketMessageItem{
			ID:         row.ID,
			SenderRole: string(row.SenderRole),
			Message:    row.Message,
			Internal:   row.Internal,
			CreatedAt:  row.CreatedAt.UTC(),
		}
		if row.SenderUserID != nil {
			item.SenderUserID = *row.SenderUserID
			if userNameByUserID != nil {
				item.SenderUserName = strings.TrimSpace(userNameByUserID[*row.SenderUserID])
			}
		}
		items = append(items, item)
	}
	return items
}

func parseAdminTicketStatus(raw string) (ticket.Status, error) {
	value, err := ticketsModule.ParseStatus(raw)
	if err != nil {
		return "", fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	return value, nil
}

func parseAdminTicketPriority(raw string) (ticket.Priority, error) {
	value, err := ticketsModule.ParsePriority(raw, "")
	if err != nil {
		return "", fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	return value, nil
}

func parseAdminTicketQuickFilter(raw string) (adminTicketQuickFilter, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	switch adminTicketQuickFilter(trimmed) {
	case "", "all":
		return "", nil
	case adminTicketQuickFilterPendingAdmin, adminTicketQuickFilterPendingUser, adminTicketQuickFilterUnassigned, adminTicketQuickFilterMine, adminTicketQuickFilterHighOrUrgent:
		return adminTicketQuickFilter(trimmed), nil
	default:
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid quick_filter")
	}
}

func applyAdminTicketQuickFilter(filters *adminTicketFilters, actorUserID string) error {
	if filters == nil {
		return nil
	}

	switch filters.QuickFilter {
	case "":
		return nil
	case adminTicketQuickFilterPendingAdmin:
		if filters.Status == "" {
			filters.Status = ticket.StatusPendingAdmin
		}
	case adminTicketQuickFilterPendingUser:
		if filters.Status == "" {
			filters.Status = ticket.StatusPendingUser
		}
	case adminTicketQuickFilterUnassigned:
		if filters.AssigneeAdminID == "" {
			filters.RequireUnassigned = true
		}
	case adminTicketQuickFilterMine:
		if filters.AssigneeAdminID != "" {
			return nil
		}
		actorUserID = strings.TrimSpace(actorUserID)
		if actorUserID == "" {
			return fiber.NewError(fiber.StatusUnauthorized, "missing user session")
		}
		filters.AssigneeAdminID = actorUserID
	case adminTicketQuickFilterHighOrUrgent:
		if filters.Priority == "" {
			filters.PriorityValues = []ticket.Priority{ticket.PriorityHigh, ticket.PriorityUrgent}
		}
	}

	return nil
}

func parseAdminTicketFilters(c fiber.Ctx, actorUserID string) (*adminTicketFilters, error) {
	status, err := parseAdminTicketStatus(c.Query("status"))
	if err != nil {
		return nil, err
	}
	priority, err := parseAdminTicketPriority(c.Query("priority"))
	if err != nil {
		return nil, err
	}
	quickFilter, err := parseAdminTicketQuickFilter(c.Query("quick_filter"))
	if err != nil {
		return nil, err
	}
	page, pageSize, err := platformPagination.ParsePageAndPageSize(c, defaultAdminTicketPage, defaultAdminTicketPageSize, maxAdminTicketPageSize)
	if err != nil {
		return nil, err
	}
	filters := &adminTicketFilters{
		Query:             strings.TrimSpace(c.Query("q")),
		QuickFilter:       quickFilter,
		Status:            status,
		Priority:          priority,
		CreatorUserID:     strings.TrimSpace(c.Query("creator_user_id")),
		AssigneeAdminID:   strings.TrimSpace(c.Query("assignee_admin_id")),
		RequireUnassigned: false,
		Page:              page,
		PageSize:          pageSize,
	}
	if err := applyAdminTicketQuickFilter(filters, actorUserID); err != nil {
		return nil, err
	}
	return filters, nil
}

func applyAdminTicketFilters(query *postgresql.TicketQuery, filters *adminTicketFilters) *postgresql.TicketQuery {
	q := query
	if filters.Query != "" {
		q = q.Where(ticket.Or(
			ticket.SubjectContainsFold(filters.Query),
			ticket.TicketIDContainsFold(filters.Query),
		))
	}
	if filters.Status != "" {
		q = q.Where(ticket.StatusEQ(filters.Status))
	}
	if filters.Priority != "" {
		q = q.Where(ticket.PriorityEQ(filters.Priority))
	} else if len(filters.PriorityValues) > 0 {
		q = q.Where(ticket.PriorityIn(filters.PriorityValues...))
	}
	if filters.CreatorUserID != "" {
		q = q.Where(ticket.CreatorUserIDEQ(filters.CreatorUserID))
	}
	if filters.RequireUnassigned {
		q = q.Where(ticket.AssigneeAdminIDIsNil())
	} else if filters.AssigneeAdminID != "" {
		q = q.Where(ticket.AssigneeAdminIDEQ(filters.AssigneeAdminID))
	}
	return q
}

// scopeAdminTicketQueryForActor applies the same role boundary before both
// Count and pagination. Tickets do not have an Ent edge to their creator, so
// the creator IDs must be resolved from the users table first.
func scopeAdminTicketQueryForActor(
	ctx context.Context,
	db *postgresql.Client,
	query *postgresql.TicketQuery,
	actorRole string,
) (*postgresql.TicketQuery, error) {
	if adminCoreModule.NormalizeRole(actorRole) == adminCoreModule.RoleSuperAdmin {
		return query, nil
	}

	superAdminIDs, err := db.User.Query().
		Where(userSchema.RoleEQ(userSchema.RoleSuperAdmin)).
		IDs(ctx)
	if err != nil {
		return nil, err
	}
	if len(superAdminIDs) == 0 {
		return query, nil
	}
	return query.Where(ticket.CreatorUserIDNotIn(superAdminIDs...)), nil
}

func collectAdminTicketUserIDs(rows []*postgresql.Ticket) []string {
	userIDs := make([]string, 0, len(rows)*2)
	for _, row := range rows {
		if row == nil {
			continue
		}
		userIDs = append(userIDs, row.CreatorUserID)
		if row.AssigneeAdminID != nil {
			userIDs = append(userIDs, *row.AssigneeAdminID)
		}
	}
	return userIDs
}

func collectAdminTicketMessageSenderUserIDs(rows []*postgresql.TicketMessage) []string {
	userIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		if row == nil || row.SenderUserID == nil {
			continue
		}
		userIDs = append(userIDs, *row.SenderUserID)
	}
	return userIDs
}

func queryAdminTicketByPublicID(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, publicTicketID string) (*postgresql.Ticket, error) {
	return apiHelper.DBManager.DB.Ticket.Query().
		Where(ticket.TicketIDEQ(publicTicketID)).
		Only(c.Context())
}

func ensureAdminCanManageTicketCreator(
	c fiber.Ctx,
	apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	actorUserID string,
	actorRole string,
	row *postgresql.Ticket,
) error {
	creator, err := apiHelper.DBManager.DB.User.Query().
		Where(userSchema.IDEQ(row.CreatorUserID)).
		Select(userSchema.FieldID, userSchema.FieldRole).
		Only(c.Context())
	if err != nil {
		return err
	}
	return adminCoreModule.EnsureAdminCanManageTargetUser(actorUserID, actorRole, creator.ID, string(creator.Role))
}

func ensureAdminCanAssignToTarget(actorUserID, actorRole string, target *postgresql.User) error {
	if target == nil {
		return fiber.NewError(fiber.StatusInternalServerError, "assignee admin is missing")
	}

	// Self-assignment is an ordinary ticket workflow operation, not an
	// administrative mutation of the current user's account. For every other
	// assignee, reuse the canonical role-hierarchy check.
	if strings.TrimSpace(actorUserID) == strings.TrimSpace(target.ID) {
		return nil
	}
	return adminCoreModule.EnsureAdminCanManageTargetUser(actorUserID, actorRole, target.ID, string(target.Role))
}
