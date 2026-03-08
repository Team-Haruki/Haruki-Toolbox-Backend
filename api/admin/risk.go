package admin

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/riskevent"
	"haruki-suite/utils/database/postgresql/riskrule"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

const (
	defaultRiskEventPage     = 1
	defaultRiskEventPageSize = 50
	maxRiskEventPageSize     = 200
	defaultRiskEventSort     = "event_time_desc"

	riskEventSortEventTimeDesc = "event_time_desc"
	riskEventSortEventTimeAsc  = "event_time_asc"
	riskEventSortIDDesc        = "id_desc"
	riskEventSortIDAsc         = "id_asc"
)

var validRiskStatuses = []string{"open", "resolved"}
var validRiskSeverities = []string{"low", "medium", "high", "critical"}

type riskEventFilters struct {
	From         time.Time
	To           time.Time
	Status       string
	Severity     string
	ActorUserID  string
	TargetUserID string
	Action       string
	Page         int
	PageSize     int
	Sort         string
}

type riskEventItem struct {
	ID           int            `json:"id"`
	EventTime    time.Time      `json:"eventTime"`
	Status       string         `json:"status"`
	Severity     string         `json:"severity"`
	Source       string         `json:"source"`
	ActorUserID  string         `json:"actorUserId,omitempty"`
	TargetUserID string         `json:"targetUserId,omitempty"`
	IP           string         `json:"ip,omitempty"`
	Action       string         `json:"action,omitempty"`
	Reason       string         `json:"reason,omitempty"`
	ResolvedAt   *time.Time     `json:"resolvedAt,omitempty"`
	ResolvedBy   string         `json:"resolvedBy,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

type riskEventQueryResponse struct {
	GeneratedAt time.Time       `json:"generatedAt"`
	From        time.Time       `json:"from"`
	To          time.Time       `json:"to"`
	Page        int             `json:"page"`
	PageSize    int             `json:"pageSize"`
	Total       int             `json:"total"`
	TotalPages  int             `json:"totalPages"`
	HasMore     bool            `json:"hasMore"`
	Sort        string          `json:"sort"`
	Items       []riskEventItem `json:"items"`
}

type createRiskEventPayload struct {
	Severity     string         `json:"severity"`
	Source       string         `json:"source"`
	ActorUserID  string         `json:"actorUserId,omitempty"`
	TargetUserID string         `json:"targetUserId,omitempty"`
	IP           string         `json:"ip,omitempty"`
	Action       string         `json:"action,omitempty"`
	Reason       string         `json:"reason,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

type resolveRiskEventPayload struct {
	Reason string `json:"reason,omitempty"`
}

type riskRuleItem struct {
	Key         string         `json:"key"`
	Description string         `json:"description,omitempty"`
	Config      map[string]any `json:"config"`
	UpdatedAt   time.Time      `json:"updatedAt"`
	UpdatedBy   string         `json:"updatedBy,omitempty"`
}

type riskRuleUpsertPayload struct {
	Rules []riskRuleItem `json:"rules"`
}

type riskRuleListResponse struct {
	GeneratedAt time.Time      `json:"generatedAt"`
	Items       []riskRuleItem `json:"items"`
}

func parseRiskEventSort(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultRiskEventSort, nil
	}
	switch trimmed {
	case riskEventSortEventTimeDesc, riskEventSortEventTimeAsc, riskEventSortIDDesc, riskEventSortIDAsc:
		return trimmed, nil
	default:
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid sort option")
	}
}

func parseRiskEventFilters(c fiber.Ctx, now time.Time) (*riskEventFilters, error) {
	from, to, err := resolveUploadLogTimeRange(c.Query("from"), c.Query("to"), now)
	if err != nil {
		return nil, err
	}
	status := strings.ToLower(strings.TrimSpace(c.Query("status")))
	if status != "" && !slices.Contains(validRiskStatuses, status) {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid status filter")
	}
	severity := strings.ToLower(strings.TrimSpace(c.Query("severity")))
	if severity != "" && !slices.Contains(validRiskSeverities, severity) {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid severity filter")
	}
	page, err := parsePositiveInt(c.Query("page"), defaultRiskEventPage, "page")
	if err != nil {
		return nil, err
	}
	pageSize, err := parsePositiveInt(c.Query("page_size"), defaultRiskEventPageSize, "page_size")
	if err != nil {
		return nil, err
	}
	if pageSize > maxRiskEventPageSize {
		return nil, fiber.NewError(fiber.StatusBadRequest, "page_size exceeds max allowed size")
	}
	sortValue, err := parseRiskEventSort(c.Query("sort"))
	if err != nil {
		return nil, err
	}

	return &riskEventFilters{
		From:         from,
		To:           to,
		Status:       status,
		Severity:     severity,
		ActorUserID:  strings.TrimSpace(c.Query("actor_user_id")),
		TargetUserID: strings.TrimSpace(c.Query("target_user_id")),
		Action:       strings.TrimSpace(c.Query("action")),
		Page:         page,
		PageSize:     pageSize,
		Sort:         sortValue,
	}, nil
}

func applyRiskEventFilters(query *postgresql.RiskEventQuery, filters *riskEventFilters) *postgresql.RiskEventQuery {
	q := query.Where(
		riskevent.EventTimeGTE(filters.From),
		riskevent.EventTimeLTE(filters.To),
	)
	if filters.Status != "" {
		q = q.Where(riskevent.StatusEQ(riskevent.Status(filters.Status)))
	}
	if filters.Severity != "" {
		q = q.Where(riskevent.SeverityEQ(riskevent.Severity(filters.Severity)))
	}
	if filters.ActorUserID != "" {
		q = q.Where(riskevent.ActorUserIDEQ(filters.ActorUserID))
	}
	if filters.TargetUserID != "" {
		q = q.Where(riskevent.TargetUserIDEQ(filters.TargetUserID))
	}
	if filters.Action != "" {
		q = q.Where(riskevent.ActionContainsFold(filters.Action))
	}
	return q
}

func applyRiskEventSort(query *postgresql.RiskEventQuery, sortValue string) *postgresql.RiskEventQuery {
	switch sortValue {
	case riskEventSortEventTimeAsc:
		return query.Order(riskevent.ByEventTime(sql.OrderAsc()), riskevent.ByID(sql.OrderAsc()))
	case riskEventSortIDDesc:
		return query.Order(riskevent.ByID(sql.OrderDesc()))
	case riskEventSortIDAsc:
		return query.Order(riskevent.ByID(sql.OrderAsc()))
	default:
		return query.Order(riskevent.ByEventTime(sql.OrderDesc()), riskevent.ByID(sql.OrderDesc()))
	}
}

func buildRiskEventItems(rows []*postgresql.RiskEvent) []riskEventItem {
	items := make([]riskEventItem, 0, len(rows))
	for _, row := range rows {
		item := riskEventItem{
			ID:        row.ID,
			EventTime: row.EventTime.UTC(),
			Status:    string(row.Status),
			Severity:  string(row.Severity),
			Source:    row.Source,
			Metadata:  row.Metadata,
		}
		if row.ActorUserID != nil {
			item.ActorUserID = *row.ActorUserID
		}
		if row.TargetUserID != nil {
			item.TargetUserID = *row.TargetUserID
		}
		if row.IP != nil {
			item.IP = *row.IP
		}
		if row.Action != nil {
			item.Action = *row.Action
		}
		if row.Reason != nil {
			item.Reason = *row.Reason
		}
		if row.ResolvedAt != nil {
			resolvedAt := row.ResolvedAt.UTC()
			item.ResolvedAt = &resolvedAt
		}
		if row.ResolvedBy != nil {
			item.ResolvedBy = *row.ResolvedBy
		}
		items = append(items, item)
	}
	return items
}

func handleListRiskEvents(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		filters, err := parseRiskEventFilters(c, time.Now())
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid filters")
		}

		baseQuery := applyRiskEventFilters(apiHelper.DBManager.DB.RiskEvent.Query(), filters)
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

		totalPages := 0
		if total > 0 {
			totalPages = int(math.Ceil(float64(total) / float64(filters.PageSize)))
		}
		resp := riskEventQueryResponse{
			GeneratedAt: time.Now().UTC(),
			From:        filters.From.UTC(),
			To:          filters.To.UTC(),
			Page:        filters.Page,
			PageSize:    filters.PageSize,
			Total:       total,
			TotalPages:  totalPages,
			HasMore:     filters.Page < totalPages,
			Sort:        filters.Sort,
			Items:       buildRiskEventItems(rows),
		}
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleCreateRiskEvent(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		actorUserID, _, err := currentAdminActor(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorUnauthorized(c, "missing user session")
		}

		var payload createRiskEventPayload
		if err := c.Bind().Body(&payload); err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.risk.event.create", "risk_event", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_request_payload", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}
		severity := strings.ToLower(strings.TrimSpace(payload.Severity))
		if severity == "" {
			severity = "medium"
		}
		if !slices.Contains(validRiskSeverities, severity) {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid severity")
		}
		source := strings.TrimSpace(payload.Source)
		if source == "" {
			source = "manual"
		}
		builder := apiHelper.DBManager.DB.RiskEvent.Create().
			SetEventTime(time.Now().UTC()).
			SetStatus(riskevent.StatusOpen).
			SetSeverity(riskevent.Severity(severity)).
			SetSource(source)
		if v := strings.TrimSpace(payload.ActorUserID); v != "" {
			builder.SetActorUserID(v)
		}
		if v := strings.TrimSpace(payload.TargetUserID); v != "" {
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
			writeAdminAuditLog(c, apiHelper, "admin.risk.event.create", "risk_event", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("create_event_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to create risk event")
		}

		writeAdminAuditLog(c, apiHelper, "admin.risk.event.create", "risk_event", strconv.Itoa(row.ID), harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"actorUserID": actorUserID,
			"severity":    severity,
		})
		items := buildRiskEventItems([]*postgresql.RiskEvent{row})
		return harukiAPIHelper.SuccessResponse(c, "risk event created", &items[0])
	}
}

func handleResolveRiskEvent(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		actorUserID, _, err := currentAdminActor(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorUnauthorized(c, "missing user session")
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

		metadata := row.Metadata
		if metadata == nil {
			metadata = map[string]any{}
		}
		if reason := strings.TrimSpace(payload.Reason); reason != "" {
			metadata["resolutionReason"] = reason
		}
		resolvedAt := time.Now().UTC()

		updated, err := row.Update().
			SetStatus(riskevent.StatusResolved).
			SetResolvedAt(resolvedAt).
			SetResolvedBy(actorUserID).
			SetMetadata(metadata).
			Save(c.Context())
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to resolve risk event")
		}
		writeAdminAuditLog(c, apiHelper, "admin.risk.event.resolve", "risk_event", strconv.Itoa(updated.ID), harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"status": string(updated.Status),
		})
		items := buildRiskEventItems([]*postgresql.RiskEvent{updated})
		return harukiAPIHelper.SuccessResponse(c, "risk event resolved", &items[0])
	}
}

func normalizeRiskRuleItem(row *postgresql.RiskRule) riskRuleItem {
	item := riskRuleItem{
		Key:       row.RuleKey,
		Config:    row.Config,
		UpdatedAt: row.UpdatedAt.UTC(),
	}
	if row.Description != nil {
		item.Description = *row.Description
	}
	if row.UpdatedBy != nil {
		item.UpdatedBy = *row.UpdatedBy
	}
	return item
}

func handleListRiskRules(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		rows, err := apiHelper.DBManager.DB.RiskRule.Query().
			Order(riskrule.ByRuleKey(sql.OrderAsc())).
			All(c.Context())
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to query risk rules")
		}

		items := make([]riskRuleItem, 0, len(rows))
		for _, row := range rows {
			items = append(items, normalizeRiskRuleItem(row))
		}
		resp := riskRuleListResponse{
			GeneratedAt: time.Now().UTC(),
			Items:       items,
		}
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleUpsertRiskRules(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		actorUserID, _, err := currentAdminActor(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorUnauthorized(c, "missing user session")
		}

		var payload riskRuleUpsertPayload
		if err := c.Bind().Body(&payload); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}
		if len(payload.Rules) == 0 {
			return harukiAPIHelper.ErrorBadRequest(c, "rules is required")
		}
		if len(payload.Rules) > 100 {
			return harukiAPIHelper.ErrorBadRequest(c, "too many rules in one request")
		}

		updated := make([]riskRuleItem, 0, len(payload.Rules))
		for _, item := range payload.Rules {
			key := strings.TrimSpace(item.Key)
			if key == "" {
				return harukiAPIHelper.ErrorBadRequest(c, "rule key is required")
			}
			configValue := item.Config
			if configValue == nil {
				configValue = map[string]any{}
			}
			existing, err := apiHelper.DBManager.DB.RiskRule.Query().
				Where(riskrule.RuleKeyEQ(key)).
				Only(c.Context())
			if err != nil && !postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorInternal(c, "failed to upsert risk rule")
			}

			description := strings.TrimSpace(item.Description)
			if existing == nil || postgresql.IsNotFound(err) {
				builder := apiHelper.DBManager.DB.RiskRule.Create().
					SetRuleKey(key).
					SetConfig(configValue).
					SetUpdatedBy(actorUserID)
				if description != "" {
					builder.SetDescription(description)
				}
				row, err := builder.Save(c.Context())
				if err != nil {
					return harukiAPIHelper.ErrorInternal(c, "failed to upsert risk rule")
				}
				updated = append(updated, normalizeRiskRuleItem(row))
				continue
			}

			update := existing.Update().
				SetConfig(configValue).
				SetUpdatedBy(actorUserID)
			if description != "" {
				update.SetDescription(description)
			} else {
				update.ClearDescription()
			}
			row, err := update.Save(c.Context())
			if err != nil {
				return harukiAPIHelper.ErrorInternal(c, "failed to upsert risk rule")
			}
			updated = append(updated, normalizeRiskRuleItem(row))
		}

		writeAdminAuditLog(c, apiHelper, "admin.risk.rules.upsert", "risk_rule", "", harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"count": len(updated),
		})
		resp := riskRuleListResponse{GeneratedAt: time.Now().UTC(), Items: updated}
		return harukiAPIHelper.SuccessResponse(c, "risk rules updated", &resp)
	}
}

func registerAdminRiskRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, adminGroup fiber.Router) {
	risk := adminGroup.Group("/risk", RequireAdmin(apiHelper))

	events := risk.Group("/events")
	events.Get("/", handleListRiskEvents(apiHelper))
	events.Post("/", handleCreateRiskEvent(apiHelper))
	events.Post("/:event_id/resolve", handleResolveRiskEvent(apiHelper))

	rules := risk.Group("/rules")
	rules.Get("/", handleListRiskRules(apiHelper))
	rules.Put("/", RequireSuperAdmin(apiHelper), handleUpsertRiskRules(apiHelper))
}
