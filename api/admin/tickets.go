package admin

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/ticket"
	"haruki-suite/utils/database/postgresql/ticketmessage"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"math"
	"strings"
	"time"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

const (
	defaultAdminTicketPage     = 1
	defaultAdminTicketPageSize = 50
	maxAdminTicketPageSize     = 200
)

type adminTicketFilters struct {
	Query           string
	Status          ticket.Status
	Priority        ticket.Priority
	CreatorUserID   string
	AssigneeAdminID string
	Page            int
	PageSize        int
}

type adminTicketListResponse struct {
	GeneratedAt time.Time             `json:"generatedAt"`
	Page        int                   `json:"page"`
	PageSize    int                   `json:"pageSize"`
	Total       int                   `json:"total"`
	TotalPages  int                   `json:"totalPages"`
	HasMore     bool                  `json:"hasMore"`
	Items       []adminTicketListItem `json:"items"`
}

type adminAppendTicketMessagePayload struct {
	Message  string `json:"message"`
	Internal bool   `json:"internal"`
}

type adminUpdateTicketStatusPayload struct {
	Status string `json:"status"`
}

type adminAssignTicketPayload struct {
	AssigneeAdminID *string `json:"assigneeAdminId"`
}

type adminTicketListItem struct {
	TicketID        string     `json:"ticketId"`
	CreatorUserID   string     `json:"creatorUserId"`
	CreatorUserName string     `json:"creatorUserName,omitempty"`
	Subject         string     `json:"subject"`
	Category        string     `json:"category,omitempty"`
	Priority        string     `json:"priority"`
	Status          string     `json:"status"`
	AssigneeAdminID string     `json:"assigneeAdminId,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	ClosedAt        *time.Time `json:"closedAt,omitempty"`
}

type adminTicketMessageItem struct {
	ID           int       `json:"id"`
	SenderUserID string    `json:"senderUserId,omitempty"`
	SenderRole   string    `json:"senderRole"`
	Message      string    `json:"message"`
	Internal     bool      `json:"internal"`
	CreatedAt    time.Time `json:"createdAt"`
}

type adminTicketDetailResponse struct {
	Ticket   adminTicketListItem      `json:"ticket"`
	Messages []adminTicketMessageItem `json:"messages"`
}

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
	page, err := parsePositiveInt(c.Query("page"), defaultAdminTicketPage, "page")
	if err != nil {
		return nil, err
	}
	pageSize, err := parsePositiveInt(c.Query("page_size"), defaultAdminTicketPageSize, "page_size")
	if err != nil {
		return nil, err
	}
	if pageSize > maxAdminTicketPageSize {
		return nil, fiber.NewError(fiber.StatusBadRequest, "page_size exceeds max allowed size")
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

func handleAdminListTickets(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		filters, err := parseAdminTicketFilters(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid filters")
		}

		baseQuery := applyAdminTicketFilters(apiHelper.DBManager.DB.Ticket.Query(), filters)
		total, err := baseQuery.Clone().Count(c.Context())
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to count tickets")
		}
		rows, err := baseQuery.Clone().
			Order(ticket.ByUpdatedAt(sql.OrderDesc()), ticket.ByID(sql.OrderDesc())).
			Offset((filters.Page - 1) * filters.PageSize).
			Limit(filters.PageSize).
			All(c.Context())
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to query tickets")
		}

		totalPages := 0
		if total > 0 {
			totalPages = int(math.Ceil(float64(total) / float64(filters.PageSize)))
		}
		creatorUserIDs := make([]string, 0, len(rows))
		for _, row := range rows {
			creatorUserIDs = append(creatorUserIDs, row.CreatorUserID)
		}
		creatorNameByUserID, err := loadAdminTicketCreatorNames(c, apiHelper, creatorUserIDs)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to query ticket creators")
		}
		items := make([]adminTicketListItem, 0, len(rows))
		for _, row := range rows {
			items = append(items, buildAdminTicketListItem(row, creatorNameByUserID))
		}
		resp := adminTicketListResponse{
			GeneratedAt: time.Now().UTC(),
			Page:        filters.Page,
			PageSize:    filters.PageSize,
			Total:       total,
			TotalPages:  totalPages,
			HasMore:     filters.Page < totalPages,
			Items:       items,
		}
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleAdminGetTicketDetail(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		publicTicketID := strings.TrimSpace(c.Params("ticket_id"))
		if publicTicketID == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "ticket_id is required")
		}
		row, err := apiHelper.DBManager.DB.Ticket.Query().
			Where(ticket.TicketIDEQ(publicTicketID)).
			WithMessages(func(q *postgresql.TicketMessageQuery) {
				q.Order(ticketmessage.ByCreatedAt(sql.OrderAsc()), ticketmessage.ByID(sql.OrderAsc()))
			}).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorNotFound(c, "ticket not found")
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to query ticket detail")
		}

		creatorNameByUserID, err := loadAdminTicketCreatorNames(c, apiHelper, []string{row.CreatorUserID})
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to query ticket creator")
		}

		resp := adminTicketDetailResponse{
			Ticket:   buildAdminTicketListItem(row, creatorNameByUserID),
			Messages: buildAdminTicketMessageItems(row.Edges.Messages),
		}
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleAdminAppendTicketMessage(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		actorUserID, _, err := currentAdminActor(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorUnauthorized(c, "missing user session")
		}

		publicTicketID := strings.TrimSpace(c.Params("ticket_id"))
		if publicTicketID == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "ticket_id is required")
		}
		var payload adminAppendTicketMessagePayload
		if err := c.Bind().Body(&payload); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}
		message := strings.TrimSpace(payload.Message)
		if message == "" || len(message) > 4000 {
			return harukiAPIHelper.ErrorBadRequest(c, "message must be 1-4000 characters")
		}

		row, err := queryAdminTicketByPublicID(c, apiHelper, publicTicketID)
		if err != nil {
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorNotFound(c, "ticket not found")
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to query ticket")
		}

		createdMessage, err := apiHelper.DBManager.DB.TicketMessage.Create().
			SetTicketID(row.ID).
			SetSenderUserID(actorUserID).
			SetSenderRole(ticketmessage.SenderRoleAdmin).
			SetInternal(payload.Internal).
			SetMessage(message).
			Save(c.Context())
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to append ticket message")
		}

		if !payload.Internal && row.Status != ticket.StatusClosed {
			update := row.Update().SetStatus(ticket.StatusPendingUser)
			if row.ClosedAt != nil {
				update.ClearClosedAt()
			}
			_, _ = update.Save(c.Context())
		}

		items := buildAdminTicketMessageItems([]*postgresql.TicketMessage{createdMessage})
		writeAdminAuditLog(c, apiHelper, "admin.ticket.message.append", "ticket", row.TicketID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"internal": payload.Internal,
		})
		return harukiAPIHelper.SuccessResponse(c, "message added", &items[0])
	}
}

func handleAdminUpdateTicketStatus(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		publicTicketID := strings.TrimSpace(c.Params("ticket_id"))
		if publicTicketID == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "ticket_id is required")
		}
		var payload adminUpdateTicketStatusPayload
		if err := c.Bind().Body(&payload); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}
		statusValue, err := parseAdminTicketStatus(payload.Status)
		if err != nil || statusValue == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid status")
		}

		row, err := queryAdminTicketByPublicID(c, apiHelper, publicTicketID)
		if err != nil {
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorNotFound(c, "ticket not found")
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to query ticket")
		}

		update := row.Update().SetStatus(statusValue)
		if statusValue == ticket.StatusClosed {
			now := time.Now().UTC()
			update.SetClosedAt(now)
		} else {
			update.ClearClosedAt()
		}
		updated, err := update.Save(c.Context())
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to update ticket status")
		}

		creatorNameByUserID, err := loadAdminTicketCreatorNames(c, apiHelper, []string{updated.CreatorUserID})
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to query ticket creator")
		}
		resp := buildAdminTicketListItem(updated, creatorNameByUserID)
		writeAdminAuditLog(c, apiHelper, "admin.ticket.status.update", "ticket", updated.TicketID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"status": statusValue,
		})
		return harukiAPIHelper.SuccessResponse(c, "ticket status updated", &resp)
	}
}

func handleAdminAssignTicket(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		publicTicketID := strings.TrimSpace(c.Params("ticket_id"))
		if publicTicketID == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "ticket_id is required")
		}
		var payload adminAssignTicketPayload
		if err := c.Bind().Body(&payload); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		row, err := queryAdminTicketByPublicID(c, apiHelper, publicTicketID)
		if err != nil {
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorNotFound(c, "ticket not found")
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to query ticket")
		}

		update := row.Update()
		assignee := ""
		if payload.AssigneeAdminID != nil {
			assignee = strings.TrimSpace(*payload.AssigneeAdminID)
		}
		if assignee == "" {
			update.ClearAssigneeAdminID()
		} else {
			update.SetAssigneeAdminID(assignee)
		}
		updated, err := update.Save(c.Context())
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to assign ticket")
		}
		creatorNameByUserID, err := loadAdminTicketCreatorNames(c, apiHelper, []string{updated.CreatorUserID})
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to query ticket creator")
		}
		resp := buildAdminTicketListItem(updated, creatorNameByUserID)
		writeAdminAuditLog(c, apiHelper, "admin.ticket.assign", "ticket", updated.TicketID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"assigneeAdminID": assignee,
		})
		return harukiAPIHelper.SuccessResponse(c, "ticket assignment updated", &resp)
	}
}

func registerAdminTicketRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, adminGroup fiber.Router) {
	tickets := adminGroup.Group("/tickets", RequireAdmin(apiHelper))
	tickets.Get("/", handleAdminListTickets(apiHelper))
	tickets.Get("/:ticket_id", handleAdminGetTicketDetail(apiHelper))
	tickets.Post("/:ticket_id/messages", handleAdminAppendTicketMessage(apiHelper))
	tickets.Put("/:ticket_id/status", handleAdminUpdateTicketStatus(apiHelper))
	tickets.Put("/:ticket_id/assign", handleAdminAssignTicket(apiHelper))
}
