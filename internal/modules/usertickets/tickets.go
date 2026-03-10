package usertickets

import (
	"crypto/rand"
	"encoding/hex"
	userCoreModule "haruki-suite/internal/modules/usercore"
	platformPagination "haruki-suite/internal/platform/pagination"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/ticket"
	"haruki-suite/utils/database/postgresql/ticketmessage"
	"math"
	"strings"
	"time"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

const (
	defaultUserTicketPage     = 1
	defaultUserTicketPageSize = 20
	maxUserTicketPageSize     = 100
	maxUserTicketCategoryLen  = 64
)

type userTicketListItem struct {
	TicketID        string     `json:"ticketId"`
	Subject         string     `json:"subject"`
	Category        string     `json:"category,omitempty"`
	Priority        string     `json:"priority"`
	Status          string     `json:"status"`
	AssigneeAdminID string     `json:"assigneeAdminId,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	ClosedAt        *time.Time `json:"closedAt,omitempty"`
}

type userTicketListResponse struct {
	GeneratedAt time.Time            `json:"generatedAt"`
	Page        int                  `json:"page"`
	PageSize    int                  `json:"pageSize"`
	Total       int                  `json:"total"`
	TotalPages  int                  `json:"totalPages"`
	HasMore     bool                 `json:"hasMore"`
	Items       []userTicketListItem `json:"items"`
}

type userTicketMessageItem struct {
	ID         int       `json:"id"`
	SenderRole string    `json:"senderRole"`
	Message    string    `json:"message"`
	CreatedAt  time.Time `json:"createdAt"`
}

type userTicketDetailResponse struct {
	Ticket   userTicketListItem      `json:"ticket"`
	Messages []userTicketMessageItem `json:"messages"`
}

type createUserTicketPayload struct {
	Subject  string         `json:"subject"`
	Category string         `json:"category,omitempty"`
	Priority string         `json:"priority,omitempty"`
	Message  string         `json:"message"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type createUserTicketResponse struct {
	TicketID string `json:"ticketId"`
}

type appendUserTicketMessagePayload struct {
	Message string `json:"message"`
}

func generateTicketPublicID() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "TK-" + time.Now().UTC().Format("20060102150405") + "-" + hex.EncodeToString(b), nil
}

func parseUserTicketPriority(raw string) (ticket.Priority, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return ticket.PriorityNormal, nil
	}
	switch ticket.Priority(trimmed) {
	case ticket.PriorityLow, ticket.PriorityNormal, ticket.PriorityHigh, ticket.PriorityUrgent:
		return ticket.Priority(trimmed), nil
	default:
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid priority")
	}
}

func parseUserTicketStatus(raw string) (ticket.Status, error) {
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

func normalizeUserTicketCategory(raw string) (string, error) {
	category := strings.TrimSpace(raw)
	if len(category) > maxUserTicketCategoryLen {
		return "", fiber.NewError(fiber.StatusBadRequest, "category must be 0-64 characters")
	}
	return category, nil
}

func buildUserTicketListItem(row *postgresql.Ticket) userTicketListItem {
	item := userTicketListItem{
		TicketID:  row.TicketID,
		Subject:   row.Subject,
		Priority:  string(row.Priority),
		Status:    string(row.Status),
		CreatedAt: row.CreatedAt.UTC(),
		UpdatedAt: row.UpdatedAt.UTC(),
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

func buildUserTicketMessageItems(rows []*postgresql.TicketMessage) []userTicketMessageItem {
	items := make([]userTicketMessageItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, userTicketMessageItem{
			ID:         row.ID,
			SenderRole: string(row.SenderRole),
			Message:    row.Message,
			CreatedAt:  row.CreatedAt.UTC(),
		})
	}
	return items
}

func handleCreateOwnTicket(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		var createdTicketID string
		defer func() {
			userCoreModule.WriteUserAuditLog(c, apiHelper, "user.ticket.create", result, userID, map[string]any{
				"reason":   reason,
				"ticketId": createdTicketID,
			})
		}()

		var payload createUserTicketPayload
		if err := c.Bind().Body(&payload); err != nil {
			reason = "invalid_payload"
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		subject := strings.TrimSpace(payload.Subject)
		if subject == "" || len(subject) > 200 {
			reason = "invalid_subject"
			return harukiAPIHelper.ErrorBadRequest(c, "subject must be 1-200 characters")
		}
		message := strings.TrimSpace(payload.Message)
		if message == "" || len(message) > 4000 {
			reason = "invalid_message"
			return harukiAPIHelper.ErrorBadRequest(c, "message must be 1-4000 characters")
		}
		priority, err := parseUserTicketPriority(payload.Priority)
		if err != nil {
			reason = "invalid_priority"
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid priority")
		}
		category, err := normalizeUserTicketCategory(payload.Category)
		if err != nil {
			reason = "invalid_category"
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid category")
		}

		ticketID, err := generateTicketPublicID()
		if err != nil {
			reason = "generate_ticket_id_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to generate ticket id")
		}

		tx, err := apiHelper.DBManager.DB.Tx(c.Context())
		if err != nil {
			reason = "start_transaction_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to create ticket")
		}

		builder := tx.Ticket.Create().
			SetTicketID(ticketID).
			SetCreatorUserID(userID).
			SetSubject(subject).
			SetPriority(priority).
			SetStatus(ticket.StatusOpen)
		if category != "" {
			builder.SetCategory(category)
		}
		if payload.Metadata != nil {
			builder.SetMetadata(payload.Metadata)
		}
		createdTicket, err := builder.Save(c.Context())
		if err != nil {
			_ = tx.Rollback()
			reason = "create_ticket_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to create ticket")
		}

		if _, err := tx.TicketMessage.Create().
			SetTicketID(createdTicket.ID).
			SetSenderUserID(userID).
			SetSenderRole(ticketmessage.SenderRoleUser).
			SetInternal(false).
			SetMessage(message).
			Save(c.Context()); err != nil {
			_ = tx.Rollback()
			reason = "create_ticket_message_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to create ticket")
		}

		if err := tx.Commit(); err != nil {
			_ = tx.Rollback()
			reason = "commit_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to create ticket")
		}

		createdTicketID = ticketID
		result = harukiAPIHelper.SystemLogResultSuccess
		reason = "ok"
		resp := createUserTicketResponse{TicketID: ticketID}
		return harukiAPIHelper.SuccessResponse(c, "ticket created", &resp)
	}
}

func handleListOwnTickets(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		statusFilter, err := parseUserTicketStatus(c.Query("status"))
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid status")
		}
		page, err := platformPagination.ParsePositiveInt(c.Query("page"), defaultUserTicketPage, "page")
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid page")
		}
		pageSize, err := platformPagination.ParsePositiveInt(c.Query("page_size"), defaultUserTicketPageSize, "page_size")
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid page_size")
		}
		if pageSize > maxUserTicketPageSize {
			return harukiAPIHelper.ErrorBadRequest(c, "page_size exceeds max allowed size")
		}

		baseQuery := apiHelper.DBManager.DB.Ticket.Query().Where(ticket.CreatorUserIDEQ(userID))
		if statusFilter != "" {
			baseQuery = baseQuery.Where(ticket.StatusEQ(statusFilter))
		}

		total, err := baseQuery.Clone().Count(c.Context())
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to count tickets")
		}
		rows, err := baseQuery.Clone().
			Order(ticket.ByUpdatedAt(sql.OrderDesc()), ticket.ByID(sql.OrderDesc())).
			Offset((page - 1) * pageSize).
			Limit(pageSize).
			All(c.Context())
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to query tickets")
		}

		totalPages := 0
		if total > 0 {
			totalPages = int(math.Ceil(float64(total) / float64(pageSize)))
		}
		items := make([]userTicketListItem, 0, len(rows))
		for _, row := range rows {
			items = append(items, buildUserTicketListItem(row))
		}

		resp := userTicketListResponse{
			GeneratedAt: time.Now().UTC(),
			Page:        page,
			PageSize:    pageSize,
			Total:       total,
			TotalPages:  totalPages,
			HasMore:     page < totalPages,
			Items:       items,
		}
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func queryOwnTicketByPublicID(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID, publicTicketID string) (*postgresql.Ticket, error) {
	return apiHelper.DBManager.DB.Ticket.Query().
		Where(
			ticket.TicketIDEQ(publicTicketID),
			ticket.CreatorUserIDEQ(userID),
		).
		Only(c.Context())
}

func handleGetOwnTicketDetail(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		publicTicketID := strings.TrimSpace(c.Params("ticket_id"))
		if publicTicketID == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "ticket_id is required")
		}

		row, err := apiHelper.DBManager.DB.Ticket.Query().
			Where(
				ticket.TicketIDEQ(publicTicketID),
				ticket.CreatorUserIDEQ(userID),
			).
			WithMessages(func(q *postgresql.TicketMessageQuery) {
				q.Where(ticketmessage.InternalEQ(false)).Order(ticketmessage.ByCreatedAt(sql.OrderAsc()), ticketmessage.ByID(sql.OrderAsc()))
			}).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorNotFound(c, "ticket not found")
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to query ticket detail")
		}

		resp := userTicketDetailResponse{
			Ticket:   buildUserTicketListItem(row),
			Messages: buildUserTicketMessageItems(row.Edges.Messages),
		}
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleAppendOwnTicketMessage(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		publicTicketID := strings.TrimSpace(c.Params("ticket_id"))
		if publicTicketID == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "ticket_id is required")
		}

		var payload appendUserTicketMessagePayload
		if err := c.Bind().Body(&payload); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}
		message := strings.TrimSpace(payload.Message)
		if message == "" || len(message) > 4000 {
			return harukiAPIHelper.ErrorBadRequest(c, "message must be 1-4000 characters")
		}

		row, err := queryOwnTicketByPublicID(c, apiHelper, userID, publicTicketID)
		if err != nil {
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorNotFound(c, "ticket not found")
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to query ticket")
		}
		if row.Status == ticket.StatusClosed {
			return harukiAPIHelper.ErrorBadRequest(c, "ticket is closed")
		}

		tx, err := apiHelper.DBManager.DB.Tx(c.Context())
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to append ticket message")
		}

		createdMessage, err := tx.TicketMessage.Create().
			SetTicketID(row.ID).
			SetSenderUserID(userID).
			SetSenderRole(ticketmessage.SenderRoleUser).
			SetInternal(false).
			SetMessage(message).
			Save(c.Context())
		if err != nil {
			_ = tx.Rollback()
			return harukiAPIHelper.ErrorInternal(c, "failed to append ticket message")
		}

		update := tx.Ticket.UpdateOneID(row.ID).
			SetStatus(ticket.StatusPendingAdmin)
		if row.ClosedAt != nil {
			update.ClearClosedAt()
		}
		if _, err := update.Save(c.Context()); err != nil {
			_ = tx.Rollback()
			return harukiAPIHelper.ErrorInternal(c, "failed to update ticket status")
		}

		if err := tx.Commit(); err != nil {
			_ = tx.Rollback()
			return harukiAPIHelper.ErrorInternal(c, "failed to append ticket message")
		}

		items := buildUserTicketMessageItems([]*postgresql.TicketMessage{createdMessage})
		return harukiAPIHelper.SuccessResponse(c, "message added", &items[0])
	}
}

func handleCloseOwnTicket(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		publicTicketID := strings.TrimSpace(c.Params("ticket_id"))
		if publicTicketID == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "ticket_id is required")
		}

		row, err := queryOwnTicketByPublicID(c, apiHelper, userID, publicTicketID)
		if err != nil {
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorNotFound(c, "ticket not found")
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to query ticket")
		}

		now := time.Now().UTC()
		updated, err := row.Update().
			SetStatus(ticket.StatusClosed).
			SetClosedAt(now).
			Save(c.Context())
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to close ticket")
		}
		resp := buildUserTicketListItem(updated)
		return harukiAPIHelper.SuccessResponse(c, "ticket closed", &resp)
	}
}

func RegisterUserTicketRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	r := apiHelper.Router.Group("/api/user/:toolbox_user_id/tickets")
	r.Get("/", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(apiHelper), handleListOwnTickets(apiHelper))
	r.Post("/", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(apiHelper), handleCreateOwnTicket(apiHelper))
	r.Get("/:ticket_id", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(apiHelper), handleGetOwnTicketDetail(apiHelper))
	r.Post("/:ticket_id/messages", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(apiHelper), handleAppendOwnTicketMessage(apiHelper))
	r.Post("/:ticket_id/close", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(apiHelper), handleCloseOwnTicket(apiHelper))
}
