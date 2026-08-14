package admintickets

import (
	"context"
	"errors"
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	ticketsModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/tickets"
	platformMailNotify "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/mailnotify"
	platformPagination "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/pagination"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/ticket"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/ticketmessage"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	"strings"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

func handleAdminListTickets(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		actorUserID, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

		filters, err := parseAdminTicketFilters(c, actorUserID)
		if err != nil {
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid filters")
		}

		baseQuery := applyAdminTicketFilters(apiHelper.DBManager.DB.Ticket.Query(), filters)
		baseQuery, err = scopeAdminTicketQueryForActor(c.Context(), apiHelper.DBManager.DB, baseQuery, actorRole)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to scope tickets")
		}
		total, err := baseQuery.Clone().Count(c.Context())
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to count tickets")
		}
		rows, err := baseQuery.Clone().
			WithMessages(func(q *postgresql.TicketMessageQuery) {
				q.Order(ticketmessage.ByCreatedAt(sql.OrderDesc()), ticketmessage.ByID(sql.OrderDesc())).Limit(1)
			}).
			Order(ticket.ByUpdatedAt(sql.OrderDesc()), ticket.ByID(sql.OrderDesc())).
			Offset((filters.Page - 1) * filters.PageSize).
			Limit(filters.PageSize).
			All(c.Context())
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to query tickets")
		}

		totalPages := platformPagination.CalculateTotalPages(total, filters.PageSize)
		userNameByUserID, err := loadAdminTicketUserNames(c, apiHelper, collectAdminTicketUserIDs(rows))
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to query ticket users")
		}
		items := make([]adminTicketListItem, 0, len(rows))
		for _, row := range rows {
			items = append(items, buildAdminTicketListItem(row, userNameByUserID))
		}
		resp := adminTicketListResponse{
			GeneratedAt: adminNowUTC(),
			Page:        filters.Page,
			PageSize:    filters.PageSize,
			Total:       total,
			TotalPages:  totalPages,
			HasMore:     platformPagination.HasMoreByTotalPages(filters.Page, totalPages),
			Items:       items,
		}
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleAdminGetTicketDetail(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		actorUserID, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

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
		if err := ensureAdminCanManageTicketCreator(c, apiHelper, actorUserID, actorRole, row); err != nil {
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorNotFound(c, "ticket not found")
			}
			return adminCoreModule.RespondFiberOrInternal(c, err, "failed to authorize ticket")
		}

		userIDs := append(collectAdminTicketUserIDs([]*postgresql.Ticket{row}), collectAdminTicketMessageSenderUserIDs(row.Edges.Messages)...)
		userNameByUserID, err := loadAdminTicketUserNames(c, apiHelper, userIDs)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to query ticket users")
		}

		resp := adminTicketDetailResponse{
			Ticket:   buildAdminTicketListItem(row, userNameByUserID),
			Messages: buildAdminTicketMessageItems(row.Edges.Messages, userNameByUserID),
		}
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleAdminAppendTicketMessage(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, notificationConfig ticketsModule.NotificationConfig) fiber.Handler {
	return func(c fiber.Ctx) error {
		actorUserID, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

		publicTicketID := strings.TrimSpace(c.Params("ticket_id"))
		if publicTicketID == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "ticket_id is required")
		}
		var payload adminAppendTicketMessagePayload
		if err := c.Bind().Body(&payload); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}
		message, err := ticketsModule.NormalizeMessage(payload.Message)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "message must be 1-4000 characters")
		}

		row, err := queryAdminTicketByPublicID(c, apiHelper, publicTicketID)
		if err != nil {
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorNotFound(c, "ticket not found")
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to query ticket")
		}
		if err := ensureAdminCanManageTicketCreator(c, apiHelper, actorUserID, actorRole, row); err != nil {
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorNotFound(c, "ticket not found")
			}
			return adminCoreModule.RespondFiberOrInternal(c, err, "failed to authorize ticket")
		}

		savedMessage, err := ticketsModule.NewService(apiHelper.DBManager.DB, adminNow).
			AppendAdminMessage(c.Context(), row, actorUserID, message, payload.Internal)
		if err != nil {
			if errors.Is(err, ticketsModule.ErrPersistTicketStatus) {
				return harukiAPIHelper.ErrorInternal(c, "failed to update ticket status")
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to append ticket message")
		}

		if !payload.Internal {
			// Notify off the request path: the send result was always discarded
			// (log-only). BuildEvent clones every request-derived string, so the
			// event is safe to read after this handler returns.
			event := ticketsModule.BuildEvent(row, actorUserID, message, apiHelper.SMTPClient, notificationConfig)
			event.Ticket.Status = ticket.StatusPendingUser
			platformMailNotify.Dispatch(func(ctx context.Context) {
				ticketsModule.NotifyUserOfAdminReply(ctx, apiHelper.DBManager.DB, event)
			})
		}
		userNameByUserID, err := loadAdminTicketUserNames(c, apiHelper, collectAdminTicketMessageSenderUserIDs([]*postgresql.TicketMessage{savedMessage}))
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to query ticket users")
		}
		items := buildAdminTicketMessageItems([]*postgresql.TicketMessage{savedMessage}, userNameByUserID)
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionTicketMessageAppend, adminAuditTargetTypeTicket, row.TicketID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"internal": payload.Internal,
		})
		return harukiAPIHelper.SuccessResponse(c, "message added", &items[0])
	}
}

func handleAdminUpdateTicketStatus(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		actorUserID, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

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
		if err := ensureAdminCanManageTicketCreator(c, apiHelper, actorUserID, actorRole, row); err != nil {
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorNotFound(c, "ticket not found")
			}
			return adminCoreModule.RespondFiberOrInternal(c, err, "failed to authorize ticket")
		}

		updated, err := ticketsModule.NewService(apiHelper.DBManager.DB, adminNow).
			UpdateStatus(c.Context(), row, statusValue)
		if err != nil {
			if errors.Is(err, ticketsModule.ErrAppendSystemMessage) {
				return harukiAPIHelper.ErrorInternal(c, "failed to append ticket system message")
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to update ticket status")
		}
		updated, err = apiHelper.DBManager.DB.Ticket.Query().
			Where(ticket.IDEQ(updated.ID)).
			WithMessages(func(q *postgresql.TicketMessageQuery) {
				q.Order(ticketmessage.ByCreatedAt(sql.OrderDesc()), ticketmessage.ByID(sql.OrderDesc())).Limit(1)
			}).
			Only(c.Context())
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to query updated ticket")
		}

		userNameByUserID, err := loadAdminTicketUserNames(c, apiHelper, collectAdminTicketUserIDs([]*postgresql.Ticket{updated}))
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to query ticket users")
		}
		resp := buildAdminTicketListItem(updated, userNameByUserID)
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionTicketStatusUpdate, adminAuditTargetTypeTicket, updated.TicketID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"status": statusValue,
		})
		return harukiAPIHelper.SuccessResponse(c, "ticket status updated", &resp)
	}
}

func handleAdminAssignTicket(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		actorUserID, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

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
		if err := ensureAdminCanManageTicketCreator(c, apiHelper, actorUserID, actorRole, row); err != nil {
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorNotFound(c, "ticket not found")
			}
			return adminCoreModule.RespondFiberOrInternal(c, err, "failed to authorize ticket")
		}

		assignee := ""
		if payload.AssigneeAdminID != nil {
			assignee = strings.TrimSpace(*payload.AssigneeAdminID)
		}
		previousAssignee := ""
		if row.AssigneeAdminID != nil {
			previousAssignee = strings.TrimSpace(*row.AssigneeAdminID)
		}
		if assignee != "" {
			assigneeUser, err := apiHelper.DBManager.DB.User.Query().
				Where(userSchema.IDEQ(assignee)).
				Select(userSchema.FieldID, userSchema.FieldRole, userSchema.FieldBanned).
				Only(c.Context())
			if err != nil {
				if postgresql.IsNotFound(err) {
					return harukiAPIHelper.ErrorNotFound(c, "assignee admin not found")
				}
				return harukiAPIHelper.ErrorInternal(c, "failed to query assignee admin")
			}
			normalizedRole := adminCoreModule.NormalizeRole(string(assigneeUser.Role))
			if normalizedRole != adminCoreModule.RoleAdmin && normalizedRole != adminCoreModule.RoleSuperAdmin {
				return harukiAPIHelper.ErrorBadRequest(c, "assignee must be admin or super_admin")
			}
			if assigneeUser.Banned {
				return harukiAPIHelper.ErrorBadRequest(c, "assignee admin is banned")
			}
			if err := ensureAdminCanAssignToTarget(actorUserID, actorRole, assigneeUser); err != nil {
				return adminCoreModule.RespondFiberOrForbidden(c, err, "insufficient permissions")
			}
		}
		assigneeNameByUserID := map[string]string{}
		if previousAssignee != assignee {
			assigneeNameByUserID, err = loadAdminTicketUserNames(c, apiHelper, []string{previousAssignee, assignee})
			if err != nil {
				return harukiAPIHelper.ErrorInternal(c, "failed to query ticket users")
			}
		}

		tx, err := apiHelper.DBManager.DB.Tx(c.Context())
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to assign ticket")
		}

		update := tx.Ticket.UpdateOneID(row.ID)
		if assignee == "" {
			update.ClearAssigneeAdminID()
		} else {
			update.SetAssigneeAdminID(assignee)
		}
		updated, err := update.Save(c.Context())
		if err != nil {
			_ = tx.Rollback()
			return harukiAPIHelper.ErrorInternal(c, "failed to assign ticket")
		}
		if previousAssignee != assignee {
			if err := ticketsModule.AppendSystemMessage(c.Context(), tx, row.ID, buildAdminTicketAssigneeEventMessage(previousAssignee, assignee, assigneeNameByUserID)); err != nil {
				_ = tx.Rollback()
				return harukiAPIHelper.ErrorInternal(c, "failed to append ticket system message")
			}
		}
		if err := tx.Commit(); err != nil {
			_ = tx.Rollback()
			return harukiAPIHelper.ErrorInternal(c, "failed to assign ticket")
		}
		updated, err = apiHelper.DBManager.DB.Ticket.Query().
			Where(ticket.IDEQ(updated.ID)).
			WithMessages(func(q *postgresql.TicketMessageQuery) {
				q.Order(ticketmessage.ByCreatedAt(sql.OrderDesc()), ticketmessage.ByID(sql.OrderDesc())).Limit(1)
			}).
			Only(c.Context())
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to query updated ticket")
		}
		userNameByUserID, err := loadAdminTicketUserNames(c, apiHelper, collectAdminTicketUserIDs([]*postgresql.Ticket{updated}))
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to query ticket users")
		}
		resp := buildAdminTicketListItem(updated, userNameByUserID)
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionTicketAssign, adminAuditTargetTypeTicket, updated.TicketID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"assigneeAdminID": assignee,
		})
		return harukiAPIHelper.SuccessResponse(c, "ticket assignment updated", &resp)
	}
}
