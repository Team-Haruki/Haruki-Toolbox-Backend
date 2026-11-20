package usertickets

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"math"
	"strings"
	"time"

	ticketsModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/tickets"
	userCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	platformMailNotify "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/mailnotify"
	platformPagination "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/pagination"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/ticket"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/ticketmessage"

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

		if encoding := c.Get("Content-Encoding"); encoding != "" && !strings.EqualFold(encoding, "identity") {
			reason = "invalid_payload"
			return c.SendStatus(fiber.StatusUnsupportedMediaType)
		}
		if len(c.Request().Body()) > 64<<10 {
			reason = "invalid_payload"
			return c.SendStatus(fiber.StatusRequestEntityTooLarge)
		}
		var payload createUserTicketPayload
		if err := c.Bind().Body(&payload); err != nil {
			reason = "invalid_payload"
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}
		if payload.Metadata != nil {
			encoded, err := json.Marshal(payload.Metadata)
			if err != nil || len(encoded) > 16<<10 {
				reason = "invalid_metadata"
				return harukiAPIHelper.ErrorBadRequest(c, "ticket metadata exceeds 16 KiB")
			}
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
				return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid priority")
		}
		category, err := normalizeUserTicketCategory(payload.Category)
		if err != nil {
			reason = "invalid_category"
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid category")
		}

		ticketID, err := generateTicketPublicID()
		if err != nil {
			reason = "generate_ticket_id_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to generate ticket id")
		}

		notification, admissionError := reserveTicketNotification(c, apiHelper, userID)
		if admissionError != nil {
			reason = "notification_admission_denied"
			return respondUserTicketBadRequest(c, admissionError, "ticket notification unavailable")
		}
		defer notification.Cancel()

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
		if notification != nil {
			event := ticketsModule.BuildEvent(createdTicket, userID, message, apiHelper.SMTPClient, notificationConfig)
			notification.Send(func(ctx context.Context) {
				ticketsModule.NotifyAdminsOfNewTicket(ctx, apiHelper.DBManager.DB, event)
			})
		}
		resp := createUserTicketResponse{TicketID: ticketID}
		return harukiAPIHelper.Responses.SuccessResponse(c, "ticket created", &resp)
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
				return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid status")
		}
		page, err := platformPagination.ParsePositiveInt(c.Query("page"), defaultUserTicketPage, "page")
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid page")
		}
		pageSize, err := platformPagination.ParsePositiveInt(c.Query("page_size"), defaultUserTicketPageSize, "page_size")
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
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
			Offset((page-1)*pageSize).
			Limit(pageSize).
			Select(ticket.FieldID, ticket.FieldTicketID, ticket.FieldSubject, ticket.FieldPriority,
				ticket.FieldStatus, ticket.FieldCategory, ticket.FieldAssigneeAdminID,
				ticket.FieldCreatedAt, ticket.FieldUpdatedAt, ticket.FieldClosedAt).
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
		return harukiAPIHelper.Responses.SuccessResponse(c, "success", &resp)
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

		beforeID, err := platformPagination.ParsePositiveInt(c.Query("before_message_id"), 0, "before_message_id")
		if err != nil {
			return respondUserTicketBadRequest(c, err, "invalid message cursor")
		}
		row, err := queryOwnTicketByPublicID(c, apiHelper, userID, publicTicketID)
		if err != nil {
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorNotFound(c, "ticket not found")
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to query ticket detail")
		}

		messages, nextBeforeID, err := ticketsModule.ReadMessagePage(c.Context(), row.QueryMessages().Where(ticketmessage.InternalEQ(false)), beforeID)
		if postgresql.IsNotFound(err) {
			return harukiAPIHelper.ErrorNotFound(c, "ticket message not found")
		}
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to query ticket messages")
		}
		resp := userTicketDetailResponse{
			Ticket:              buildUserTicketListItem(row),
			Messages:            buildUserTicketMessageItems(messages),
			NextBeforeMessageID: nextBeforeID,
		}
		return harukiAPIHelper.Responses.SuccessResponse(c, "success", &resp)
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

		notification, admissionError := reserveTicketNotification(c, apiHelper, userID)
		if admissionError != nil {
			return respondUserTicketBadRequest(c, admissionError, "ticket notification unavailable")
		}
		defer notification.Cancel()

		createdMessage, err := ticketsModule.NewService(apiHelper.DBManager.DB, time.Now).
			AppendUserMessage(c.Context(), row, userID, message)
		if err != nil {
			if errors.Is(err, ticketsModule.ErrTicketMessageLimit) {
				return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, fiber.StatusConflict, err.Error(), nil)
			}
			if errors.Is(err, ticketsModule.ErrTicketClosed) {
				return harukiAPIHelper.ErrorBadRequest(c, "ticket is closed")
			}
			if errors.Is(err, ticketsModule.ErrPersistTicketStatus) {
				return harukiAPIHelper.ErrorInternal(c, "failed to update ticket status")
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to append ticket message")
		}

		if notification != nil {
			event := ticketsModule.BuildEvent(row, userID, message, apiHelper.SMTPClient, notificationConfig)
			event.Ticket.Status = ticket.StatusPendingAdmin
			notification.Send(func(ctx context.Context) {
				ticketsModule.NotifyAdminsOfUserReply(ctx, apiHelper.DBManager.DB, event)
			})
		}
		items := buildUserTicketMessageItems([]*postgresql.TicketMessage{createdMessage})
		return harukiAPIHelper.Responses.SuccessResponse(c, "message added", &items[0])
	}
}

func reserveTicketNotification(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID string) (*platformMailNotify.Reservation, *fiber.Error) {
	if apiHelper.SMTPClient == nil {
		return nil, nil
	}
	reservation := platformMailNotify.ReserveTicket()
	if reservation == nil {
		return nil, fiber.ErrServiceUnavailable
	}
	cache := apiHelper.DBManager.Redis
	if cache == nil || cache.Redis == nil {
		reservation.Cancel()
		return nil, fiber.ErrServiceUnavailable
	}
	count, err := cache.IncrementWithTTL(c.Context(), cache.KeyBuilder().BuildTicketNotificationRateKey(userID), ticketNotificationWindow)
	if err != nil {
		reservation.Cancel()
		return nil, fiber.ErrServiceUnavailable
	}
	if count > ticketNotificationsPerUser {
		reservation.Cancel()
		c.Set("Retry-After", "600")
		return nil, fiber.ErrTooManyRequests
	}
	return reservation, nil
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
		return harukiAPIHelper.Responses.SuccessResponse(c, "ticket closed", &resp)
	}
}
