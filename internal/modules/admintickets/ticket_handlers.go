package admintickets

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	platformPagination "haruki-suite/internal/platform/pagination"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/ticket"
	"haruki-suite/utils/database/postgresql/ticketmessage"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"strings"
	"unicode/utf8"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

func handleAdminListTickets(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		filters, err := parseAdminTicketFilters(c)
		if err != nil {
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid filters")
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

		totalPages := platformPagination.CalculateTotalPages(total, filters.PageSize)
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
		actorUserID, _, err := adminCoreModule.CurrentAdminActor(c)
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
		message := strings.TrimSpace(payload.Message)
		messageLength := utf8.RuneCountInString(message)
		if messageLength == 0 || messageLength > maxAdminTicketMessageLength {
			return harukiAPIHelper.ErrorBadRequest(c, "message must be 1-4000 characters")
		}

		row, err := queryAdminTicketByPublicID(c, apiHelper, publicTicketID)
		if err != nil {
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorNotFound(c, "ticket not found")
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to query ticket")
		}

		tx, err := apiHelper.DBManager.DB.Tx(c.Context())
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to append ticket message")
		}

		savedMessage, err := tx.TicketMessage.Create().
			SetTicketID(row.ID).
			SetSenderUserID(actorUserID).
			SetSenderRole(ticketmessage.SenderRoleAdmin).
			SetInternal(payload.Internal).
			SetMessage(message).
			Save(c.Context())
		if err != nil {
			_ = tx.Rollback()
			return harukiAPIHelper.ErrorInternal(c, "failed to append ticket message")
		}

		if !payload.Internal && row.Status != ticket.StatusClosed {
			update := tx.Ticket.UpdateOneID(row.ID).SetStatus(ticket.StatusPendingUser)
			if row.ClosedAt != nil {
				update.ClearClosedAt()
			}
			if _, err := update.Save(c.Context()); err != nil {
				_ = tx.Rollback()
				return harukiAPIHelper.ErrorInternal(c, "failed to update ticket status")
			}
		}

		if err := tx.Commit(); err != nil {
			_ = tx.Rollback()
			return harukiAPIHelper.ErrorInternal(c, "failed to append ticket message")
		}

		items := buildAdminTicketMessageItems([]*postgresql.TicketMessage{savedMessage})
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionTicketMessageAppend, adminAuditTargetTypeTicket, row.TicketID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
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
			if row.ClosedAt == nil {
				update.SetClosedAt(adminNowUTC())
			}
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
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionTicketStatusUpdate, adminAuditTargetTypeTicket, updated.TicketID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
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
			assigneeUser, err := apiHelper.DBManager.DB.User.Query().
				Where(userSchema.IDEQ(assignee)).
				Select(userSchema.FieldRole, userSchema.FieldBanned).
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
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionTicketAssign, adminAuditTargetTypeTicket, updated.TicketID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"assigneeAdminID": assignee,
		})
		return harukiAPIHelper.SuccessResponse(c, "ticket assignment updated", &resp)
	}
}
