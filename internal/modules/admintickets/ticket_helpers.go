package admintickets

import (
	platformPagination "haruki-suite/internal/platform/pagination"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/ticket"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"strings"

	"github.com/gofiber/fiber/v3"
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
	}
	if row.ClosedAt != nil {
		closed := row.ClosedAt.UTC()
		item.ClosedAt = &closed
	}
	return item
}

func loadAdminTicketCreatorNames(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, creatorUserIDs []string) (map[string]string, error) {
	if len(creatorUserIDs) == 0 {
		return map[string]string{}, nil
	}

	seen := make(map[string]struct{}, len(creatorUserIDs))
	uniqueIDs := make([]string, 0, len(creatorUserIDs))
	for _, rawID := range creatorUserIDs {
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

func buildAdminTicketMessageItems(rows []*postgresql.TicketMessage) []adminTicketMessageItem {
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
		}
		items = append(items, item)
	}
	return items
}

func parseAdminTicketStatus(raw string) (ticket.Status, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return "", nil
	}
	switch ticket.Status(trimmed) {
	case ticket.StatusOpen, ticket.StatusPendingAdmin, ticket.StatusPendingUser, ticket.StatusResolved, ticket.StatusClosed:
		return ticket.Status(trimmed), nil
	default:
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid status")
	}
}

func parseAdminTicketPriority(raw string) (ticket.Priority, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return "", nil
	}
	switch ticket.Priority(trimmed) {
	case ticket.PriorityLow, ticket.PriorityNormal, ticket.PriorityHigh, ticket.PriorityUrgent:
		return ticket.Priority(trimmed), nil
	default:
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid priority")
	}
}

func parseAdminTicketFilters(c fiber.Ctx) (*adminTicketFilters, error) {
	status, err := parseAdminTicketStatus(c.Query("status"))
	if err != nil {
		return nil, err
	}
	priority, err := parseAdminTicketPriority(c.Query("priority"))
	if err != nil {
		return nil, err
	}
	page, pageSize, err := platformPagination.ParsePageAndPageSize(c, defaultAdminTicketPage, defaultAdminTicketPageSize, maxAdminTicketPageSize)
	if err != nil {
		return nil, err
	}
	return &adminTicketFilters{
		Query:           strings.TrimSpace(c.Query("q")),
		Status:          status,
		Priority:        priority,
		CreatorUserID:   strings.TrimSpace(c.Query("creator_user_id")),
		AssigneeAdminID: strings.TrimSpace(c.Query("assignee_admin_id")),
		Page:            page,
		PageSize:        pageSize,
	}, nil
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
	}
	if filters.CreatorUserID != "" {
		q = q.Where(ticket.CreatorUserIDEQ(filters.CreatorUserID))
	}
	if filters.AssigneeAdminID != "" {
		q = q.Where(ticket.AssigneeAdminIDEQ(filters.AssigneeAdminID))
	}
	return q
}

func queryAdminTicketByPublicID(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, publicTicketID string) (*postgresql.Ticket, error) {
	return apiHelper.DBManager.DB.Ticket.Query().
		Where(ticket.TicketIDEQ(publicTicketID)).
		Only(c.Context())
}
