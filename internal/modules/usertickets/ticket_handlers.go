package usertickets

import (
	"context"
	"errors"
	ticketsModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/tickets"
	userCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	platformMailNotify "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/mailnotify"
	platformPagination "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/pagination"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/ticket"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/ticketmessage"
	"math"
	"strings"
	"time"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

func handleCreateOwnTicket(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, notificationConfig ticketsModule.NotificationConfig) fiber.Handler {
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
		// Notify off the request path: the send result was always discarded
		// (log-only), so the response should not wait on the SMTP conversation.
		// BuildEvent clones every request-derived string, so the event is safe
		// to read after this handler returns.
		event := ticketsModule.BuildEvent(createdTicket, userID, message, apiHelper.SMTPClient, notificationConfig)
		platformMailNotify.Dispatch(func(ctx context.Context) {
			ticketsModule.NotifyAdminsOfNewTicket(ctx, apiHelper.DBManager.DB, event)
		})
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

func handleAppendOwnTicketMessage(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, notificationConfig ticketsModule.NotificationConfig) fiber.Handler {
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

		createdMessage, err := ticketsModule.NewService(apiHelper.DBManager.DB, time.Now).
			AppendUserMessage(c.Context(), row, userID, message)
		if err != nil {
			if errors.Is(err, ticketsModule.ErrTicketClosed) {
				return harukiAPIHelper.ErrorBadRequest(c, "ticket is closed")
			}
			if errors.Is(err, ticketsModule.ErrPersistTicketStatus) {
				return harukiAPIHelper.ErrorInternal(c, "failed to update ticket status")
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to append ticket message")
		}

		event := ticketsModule.BuildEvent(row, userID, message, apiHelper.SMTPClient, notificationConfig)
		event.Ticket.Status = ticket.StatusPendingAdmin
		platformMailNotify.Dispatch(func(ctx context.Context) {
			ticketsModule.NotifyAdminsOfUserReply(ctx, apiHelper.DBManager.DB, event)
		})
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

		updated, err := ticketsModule.NewService(apiHelper.DBManager.DB, time.Now).Close(c.Context(), row)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to close ticket")
		}
		resp := buildUserTicketListItem(updated)
		return harukiAPIHelper.SuccessResponse(c, "ticket closed", &resp)
	}
}
