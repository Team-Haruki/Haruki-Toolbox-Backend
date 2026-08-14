package adminrisk

import (
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	platformPagination "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/pagination"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/riskevent"
	"slices"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func handleListRiskEvents(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		_, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}
		filters, err := parseRiskEventFilters(c, adminNow())
		if err != nil {
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid filters")
		}

		baseQuery := applyRiskEventFilters(apiHelper.DBManager.DB.RiskEvent.Query(), filters)
		baseQuery, err = scopeRiskEventsForAdminActor(c.Context(), apiHelper.DBManager.DB, baseQuery, actorRole)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to scope risk events")
		}
		total, err := baseQuery.Clone().Count(c.Context())
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to count risk events")
		}
		rows, err := applyRiskEventSort(baseQuery.Clone(), filters.Sort).
			Offset((filters.Page - 1) * filters.PageSize).
			Limit(filters.PageSize).
			All(c.Context())
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to query risk events")
		}

		totalPages := platformPagination.CalculateTotalPages(total, filters.PageSize)
		resp := riskEventQueryResponse{
			GeneratedAt: adminNowUTC(),
			From:        filters.From.UTC(),
			To:          filters.To.UTC(),
			Page:        filters.Page,
			PageSize:    filters.PageSize,
			Total:       total,
			TotalPages:  totalPages,
			HasMore:     platformPagination.HasMoreByTotalPages(filters.Page, totalPages),
			Sort:        filters.Sort,
			Items:       buildRiskEventItems(rows),
		}
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleCreateRiskEvent(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		actorUserID, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

		var payload createRiskEventPayload
		if err := c.Bind().Body(&payload); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionRiskEventCreate, adminAuditTargetTypeRiskEvent, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}
		severity := strings.ToLower(strings.TrimSpace(payload.Severity))
		if severity == "" {
			severity = "medium"
		}
		if !slices.Contains(validRiskSeverities, severity) {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid severity")
		}
		payload.ActorUserID = strings.TrimSpace(payload.ActorUserID)
		payload.TargetUserID = strings.TrimSpace(payload.TargetUserID)
		if err := ensureRiskEventUserReferencesManageable(
			c.Context(),
			apiHelper.DBManager.DB,
			actorUserID,
			actorRole,
			payload.ActorUserID,
			payload.TargetUserID,
		); err != nil {
			return adminCoreModule.RespondFiberOrInternal(c, err, "failed to validate risk event user references")
		}
		source := strings.TrimSpace(payload.Source)
		if source == "" {
			source = "manual"
		}
		builder := apiHelper.DBManager.DB.RiskEvent.Create().
			SetEventTime(adminNowUTC()).
			SetStatus(riskevent.StatusOpen).
			SetSeverity(riskevent.Severity(severity)).
			SetSource(source)
		if v := payload.ActorUserID; v != "" {
			builder.SetActorUserID(v)
		}
		if v := payload.TargetUserID; v != "" {
			builder.SetTargetUserID(v)
		}
		if v := strings.TrimSpace(payload.IP); v != "" {
			builder.SetIP(v)
		}
		if v := strings.TrimSpace(payload.Action); v != "" {
			builder.SetAction(v)
		}
		if v := strings.TrimSpace(payload.Reason); v != "" {
			builder.SetReason(v)
		}
		if payload.Metadata != nil {
			builder.SetMetadata(payload.Metadata)
		}
		row, err := builder.Save(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionRiskEventCreate, adminAuditTargetTypeRiskEvent, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonCreateEventFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to create risk event")
		}

		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionRiskEventCreate, adminAuditTargetTypeRiskEvent, strconv.Itoa(row.ID), harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"actorUserID": actorUserID,
			"severity":    severity,
		})
		items := buildRiskEventItems([]*postgresql.RiskEvent{row})
		return harukiAPIHelper.SuccessResponse(c, "risk event created", &items[0])
	}
}

func handleResolveRiskEvent(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		actorUserID, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}
		idValue := strings.TrimSpace(c.Params("event_id"))
		id, err := strconv.Atoi(idValue)
		if err != nil || id <= 0 {
			return harukiAPIHelper.ErrorBadRequest(c, "event_id must be a positive integer")
		}

		var payload resolveRiskEventPayload
		if len(c.Body()) > 0 {
			if err := c.Bind().Body(&payload); err != nil {
				return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
			}
		}

		row, err := apiHelper.DBManager.DB.RiskEvent.Query().Where(riskevent.IDEQ(id)).Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorNotFound(c, "risk event not found")
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to query risk event")
		}
		if err := ensureRiskEventUserReferencesManageable(
			c.Context(),
			apiHelper.DBManager.DB,
			actorUserID,
			actorRole,
			optionalRiskEventUserID(row.ActorUserID),
			optionalRiskEventUserID(row.TargetUserID),
		); err != nil {
			return adminCoreModule.RespondFiberOrInternal(c, err, "failed to validate risk event user references")
		}

		metadata := row.Metadata
		if metadata == nil {
			metadata = map[string]any{}
		}
		if reason := strings.TrimSpace(payload.Reason); reason != "" {
			metadata["resolutionReason"] = reason
		}
		resolvedAt := adminNowUTC()

		updated, err := row.Update().
			SetStatus(riskevent.StatusResolved).
			SetResolvedAt(resolvedAt).
			SetResolvedBy(actorUserID).
			SetMetadata(metadata).
			Save(c.Context())
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to resolve risk event")
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionRiskEventResolve, adminAuditTargetTypeRiskEvent, strconv.Itoa(updated.ID), harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"status": string(updated.Status),
		})
		items := buildRiskEventItems([]*postgresql.RiskEvent{updated})
		return harukiAPIHelper.SuccessResponse(c, "risk event resolved", &items[0])
	}
}
