package usertickets

import (
	userCoreModule "haruki-suite/internal/modules/usercore"
	platformPagination "haruki-suite/internal/platform/pagination"
	platformTicketNotifications "haruki-suite/internal/platform/ticketnotifications"
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

		subject, err := normalizeUserTicketSubject(payload.Subject)
		if err != nil {
			reason = "invalid_subject"
			return respondUserTicketBadRequest(c, err, "invalid subject")
		}
		message, err := normalizeUserTicketMessage(payload.Message)
		if err != nil {
			reason = "invalid_message"
			return respondUserTicketBadRequest(c, err, "invalid message")
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
		platformTicketNotifications.NotifyAdminsOfNewTicket(c.Context(), apiHelper.DBManager.DB, platformTicketNotifications.BuildEvent(createdTicket, userID, message, apiHelper.SMTPClient))
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
		message, err := normalizeUserTicketMessage(payload.Message)
		if err != nil {
			return respondUserTicketBadRequest(c, err, "invalid message")
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

		event := platformTicketNotifications.BuildEvent(row, userID, message, apiHelper.SMTPClient)
		event.Ticket.Status = ticket.StatusPendingAdmin
		platformTicketNotifications.NotifyAdminsOfUserReply(c.Context(), apiHelper.DBManager.DB, event)
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

		update := row.Update().SetStatus(ticket.StatusClosed)
		if row.ClosedAt == nil {
			update.SetClosedAt(time.Now().UTC())
		}
		updated, err := update.Save(c.Context())
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
