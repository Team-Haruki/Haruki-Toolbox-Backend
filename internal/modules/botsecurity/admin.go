package botsecurity

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	platformPagination "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/pagination"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/botsecurityalert"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

const (
	defaultAlertPage     = 1
	defaultAlertPageSize = 20
	maxAlertPageSize     = 100
	maxNoteRunes         = 1000
	ownerLookupTimeout   = 2 * time.Second

	adminAuditActionAlertUpdate   = "admin.bot_security_alert.update"
	adminAuditTargetTypeAlert     = "bot_security_alert"
	adminFailureReasonInvalidBody = "invalid_request_payload"
	adminFailureReasonNotFound    = "alert_not_found"
	adminFailureReasonSaveFailed  = "update_failed"
)

var validAlertStatuses = map[string]botsecurityalert.Status{
	string(botsecurityalert.StatusOpen):     botsecurityalert.StatusOpen,
	string(botsecurityalert.StatusResolved): botsecurityalert.StatusResolved,
	string(botsecurityalert.StatusIgnored):  botsecurityalert.StatusIgnored,
}

// AdminRouteOptions configures RegisterAdminRoutes.
type AdminRouteOptions struct {
	// OwnerLookup defaults to the HarukiBot database on apiHelper.DBManager.
	OwnerLookup OwnerLookup
	// Logger defaults to the global logger named BotSecurity.
	Logger *harukiLogger.Logger
	// Now defaults to time.Now.
	Now func() time.Time
}

type adminHandlers struct {
	apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers
	owners    OwnerLookup
	logger    *harukiLogger.Logger
	now       func() time.Time
}

// RegisterAdminRoutes registers the alert list, update and summary under
// /api/admin/bot-security for any admin role.
func RegisterAdminRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, adminGroup fiber.Router, options AdminRouteOptions) {
	h := &adminHandlers{apiHelper: apiHelper, owners: options.OwnerLookup, logger: options.Logger, now: options.Now}
	if h.owners == nil {
		h.owners = helperBotDBOwnerLookup(apiHelper)
	}
	if h.logger == nil {
		h.logger = harukiLogger.NewLoggerFromGlobal("BotSecurity")
	}
	if h.now == nil {
		h.now = time.Now
	}

	group := adminGroup.Group("/bot-security", adminCoreModule.RequireAdmin(apiHelper))
	group.Get("/alerts", h.list)
	group.Patch("/alerts/:alert_id", h.update)
	group.Get("/summary", h.summary)
}

type alertFilters struct {
	Status   string
	Kind     string
	BotID    string
	From     *time.Time
	To       *time.Time
	Page     int
	PageSize int
}

// firstQuery returns the first non-empty query value among names, so both
// the camelCase names of this API and the snake_case names other admin lists
// use are accepted.
func firstQuery(c fiber.Ctx, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(c.Query(name)); value != "" {
			return value
		}
	}
	return ""
}

func parseOptionalRFC3339(raw, name string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, name+" must be an RFC 3339 timestamp")
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func parseAlertFilters(c fiber.Ctx) (alertFilters, error) {
	var filters alertFilters
	filters.Status = strings.ToLower(strings.TrimSpace(c.Query("status")))
	if _, ok := validAlertStatuses[filters.Status]; filters.Status != "" && !ok {
		return filters, fiber.NewError(fiber.StatusBadRequest, "invalid status filter")
	}
	filters.Kind = strings.TrimSpace(c.Query("kind"))
	if filters.Kind != "" && !validKind(filters.Kind) {
		return filters, fiber.NewError(fiber.StatusBadRequest, "invalid kind filter")
	}
	filters.BotID = firstQuery(c, "botId", "bot_id")
	if !validIdentifier(filters.BotID, maxBotIDLength) {
		return filters, fiber.NewError(fiber.StatusBadRequest, "invalid botId filter")
	}
	var err error
	if filters.From, err = parseOptionalRFC3339(strings.TrimSpace(c.Query("from")), "from"); err != nil {
		return filters, err
	}
	if filters.To, err = parseOptionalRFC3339(strings.TrimSpace(c.Query("to")), "to"); err != nil {
		return filters, err
	}
	if filters.From != nil && filters.To != nil && filters.From.After(*filters.To) {
		return filters, fiber.NewError(fiber.StatusBadRequest, "from must not be after to")
	}
	if filters.Page, err = platformPagination.ParsePositiveInt(c.Query("page"), defaultAlertPage, "page"); err != nil {
		return filters, err
	}
	if filters.PageSize, err = platformPagination.ParsePositiveInt(firstQuery(c, "pageSize", "page_size"), defaultAlertPageSize, "pageSize"); err != nil {
		return filters, err
	}
	if filters.PageSize > maxAlertPageSize {
		return filters, fiber.NewError(fiber.StatusBadRequest, "pageSize exceeds max allowed size")
	}
	return filters, nil
}

func applyAlertFilters(query *postgresql.BotSecurityAlertQuery, filters alertFilters) *postgresql.BotSecurityAlertQuery {
	if filters.Status != "" {
		query = query.Where(botsecurityalert.StatusEQ(validAlertStatuses[filters.Status]))
	}
	if filters.Kind != "" {
		query = query.Where(botsecurityalert.KindEQ(filters.Kind))
	}
	if filters.BotID != "" {
		query = query.Where(botsecurityalert.BotIDEQ(filters.BotID))
	}
	if filters.From != nil {
		query = query.Where(botsecurityalert.AlertTimeGTE(*filters.From))
	}
	if filters.To != nil {
		query = query.Where(botsecurityalert.AlertTimeLTE(*filters.To))
	}
	return query
}

func (h *adminHandlers) db() *postgresql.Client {
	if h.apiHelper == nil || h.apiHelper.DBManager == nil {
		return nil
	}
	return h.apiHelper.DBManager.DB
}

func (h *adminHandlers) list(c fiber.Ctx) error {
	filters, err := parseAlertFilters(c)
	if err != nil {
		return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid filters")
	}
	db := h.db()
	if db == nil {
		return harukiAPIHelper.ErrorInternal(c, "database unavailable")
	}
	query := applyAlertFilters(db.BotSecurityAlert.Query(), filters)
	total, err := query.Clone().Count(c.Context())
	if err != nil {
		return harukiAPIHelper.ErrorInternal(c, "failed to count bot security alerts")
	}
	rows, err := query.Clone().
		Order(botsecurityalert.ByAlertTime(sql.OrderDesc()), botsecurityalert.ByID(sql.OrderDesc())).
		Offset((filters.Page - 1) * filters.PageSize).
		Limit(filters.PageSize).
		All(c.Context())
	if err != nil {
		return harukiAPIHelper.ErrorInternal(c, "failed to query bot security alerts")
	}
	resp := alertListResponse{
		Items:    h.buildItems(c.Context(), rows),
		Total:    total,
		Page:     filters.Page,
		PageSize: filters.PageSize,
	}
	return harukiAPIHelper.Responses.SuccessResponse(c, "success", &resp)
}

func (h *adminHandlers) update(c fiber.Ctx) error {
	actorUserID, _, err := adminCoreModule.CurrentAdminActor(c)
	if err != nil {
		return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
	}
	idValue := strings.TrimSpace(c.Params("alert_id"))
	id, err := strconv.Atoi(idValue)
	if err != nil || id <= 0 {
		return harukiAPIHelper.ErrorBadRequest(c, "alert_id must be a positive integer")
	}

	var payload updateAlertPayload
	if err := c.Bind().Body(&payload); err != nil {
		h.audit(c, idValue, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidBody, nil))
		return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
	}
	status, ok := validAlertStatuses[strings.ToLower(strings.TrimSpace(payload.Status))]
	if !ok {
		h.audit(c, idValue, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidBody, nil))
		return harukiAPIHelper.ErrorBadRequest(c, "status must be open, resolved or ignored")
	}
	var note *string
	if payload.Note != nil {
		trimmed := strings.TrimSpace(*payload.Note)
		if utf8.RuneCountInString(trimmed) > maxNoteRunes || strings.ContainsRune(trimmed, 0) {
			h.audit(c, idValue, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidBody, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "note must be at most 1000 characters")
		}
		note = &trimmed
	}

	db := h.db()
	if db == nil {
		return harukiAPIHelper.ErrorInternal(c, "database unavailable")
	}
	row, err := db.BotSecurityAlert.Get(c.Context(), id)
	if err != nil {
		if postgresql.IsNotFound(err) {
			h.audit(c, idValue, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonNotFound, nil))
			return harukiAPIHelper.ErrorNotFound(c, "bot security alert not found")
		}
		return harukiAPIHelper.ErrorInternal(c, "failed to query bot security alert")
	}

	builder := row.Update().SetStatus(status)
	switch {
	case status == botsecurityalert.StatusOpen:
		builder.ClearHandledByUserID().ClearHandledAt()
	case status != row.Status:
		// Moving out of open, or between resolved and ignored, records who
		// made the call. Re-saving the same status only edits the note.
		builder.SetHandledByUserID(actorUserID).SetHandledAt(h.now().UTC())
	}
	if note != nil {
		builder.SetNote(*note)
	}
	updated, err := builder.Save(c.Context())
	if err != nil {
		h.audit(c, idValue, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonSaveFailed, nil))
		return harukiAPIHelper.ErrorInternal(c, "failed to update bot security alert")
	}
	h.audit(c, idValue, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
		"fromStatus":  string(row.Status),
		"toStatus":    string(updated.Status),
		"noteChanged": note != nil && *note != row.Note,
	})
	items := h.buildItems(c.Context(), []*postgresql.BotSecurityAlert{updated})
	return harukiAPIHelper.Responses.SuccessResponse(c, "bot security alert updated", &items[0])
}

func (h *adminHandlers) audit(c fiber.Ctx, targetID, result string, metadata map[string]any) {
	if h.apiHelper == nil || h.apiHelper.DBManager == nil || h.apiHelper.DBManager.DB == nil {
		return
	}
	adminCoreModule.WriteAdminAuditLog(c, h.apiHelper, adminAuditActionAlertUpdate, adminAuditTargetTypeAlert, targetID, result, metadata)
}

func (h *adminHandlers) summary(c fiber.Ctx) error {
	db := h.db()
	if db == nil {
		return harukiAPIHelper.ErrorInternal(c, "database unavailable")
	}
	ctx := c.Context()
	now := h.now().UTC()

	var byKind []kindCount
	if err := db.BotSecurityAlert.Query().
		Where(botsecurityalert.StatusEQ(botsecurityalert.StatusOpen)).
		GroupBy(botsecurityalert.FieldKind).
		Aggregate(postgresql.Count()).
		Scan(ctx, &byKind); err != nil {
		return harukiAPIHelper.ErrorInternal(c, "failed to summarize bot security alerts")
	}
	sort.Slice(byKind, func(i, j int) bool {
		if byKind[i].Count != byKind[j].Count {
			return byKind[i].Count > byKind[j].Count
		}
		return byKind[i].Kind < byKind[j].Kind
	})
	resp := alertSummaryResponse{ByKind: make([]kindCount, 0, len(byKind))}
	for _, entry := range byKind {
		resp.Open += entry.Count
		resp.ByKind = append(resp.ByKind, entry)
	}

	var err error
	if resp.Last24h, err = db.BotSecurityAlert.Query().Where(botsecurityalert.AlertTimeGTE(now.Add(-24 * time.Hour))).Count(ctx); err != nil {
		return harukiAPIHelper.ErrorInternal(c, "failed to summarize bot security alerts")
	}
	if resp.Last7d, err = db.BotSecurityAlert.Query().Where(botsecurityalert.AlertTimeGTE(now.Add(-7 * 24 * time.Hour))).Count(ctx); err != nil {
		return harukiAPIHelper.ErrorInternal(c, "failed to summarize bot security alerts")
	}
	return harukiAPIHelper.Responses.SuccessResponse(c, "success", &resp)
}

// buildItems maps rows to API items, resolving owners from the bot database
// and handler names from the toolbox users in one query each. Neither lookup
// can fail the response: on error the affected fields stay null / empty and
// one warning is logged.
func (h *adminHandlers) buildItems(ctx context.Context, rows []*postgresql.BotSecurityAlert) []alertItem {
	owners := h.resolveOwners(ctx, rows)
	names := h.resolveHandlerNames(ctx, rows)

	items := make([]alertItem, 0, len(rows))
	for _, row := range rows {
		item := alertItem{
			ID:            row.ID,
			Kind:          row.Kind,
			BotID:         row.BotID,
			SourceIP:      row.SourceIP,
			BuildID:       row.BuildID,
			ClientVersion: row.ClientVersion,
			Reason:        row.Reason,
			Enforced:      row.Enforced,
			Count:         row.Count,
			Threshold:     row.Threshold,
			WindowSeconds: row.WindowSeconds,
			Node:          row.Node,
			AlertTime:     row.AlertTime.UTC(),
			ReceivedAt:    row.ReceivedAt.UTC(),
			Status:        string(row.Status),
			Note:          row.Note,
		}
		if row.BotID != nil {
			if id, ok := numericBotID(*row.BotID); ok {
				if owner, found := owners[id]; found {
					qq := strconv.FormatInt(owner, 10)
					item.OwnerQQ = &qq
				}
			}
		}
		if row.HandledByUserID != nil {
			item.HandledBy = &handledBy{UserID: *row.HandledByUserID, Name: names[*row.HandledByUserID]}
		}
		if row.HandledAt != nil {
			handledAt := row.HandledAt.UTC()
			item.HandledAt = &handledAt
		}
		items = append(items, item)
	}
	return items
}

func (h *adminHandlers) resolveOwners(ctx context.Context, rows []*postgresql.BotSecurityAlert) map[int]int64 {
	seen := make(map[int]struct{})
	botIDs := make([]int, 0, len(rows))
	for _, row := range rows {
		if row.BotID == nil {
			continue
		}
		if id, ok := numericBotID(*row.BotID); ok {
			if _, dup := seen[id]; !dup {
				seen[id] = struct{}{}
				botIDs = append(botIDs, id)
			}
		}
	}
	if len(botIDs) == 0 || h.owners == nil {
		return nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, ownerLookupTimeout)
	defer cancel()
	owners, err := h.owners.OwnerQQs(lookupCtx, botIDs)
	if err != nil {
		if !errors.Is(err, ErrBotDBNotConfigured) {
			h.logger.Warnf("bot_security_admin event=owner_lookup_failed bots=%d error_type=%T", len(botIDs), err)
		}
		return nil
	}
	return owners
}

func (h *adminHandlers) resolveHandlerNames(ctx context.Context, rows []*postgresql.BotSecurityAlert) map[string]string {
	seen := make(map[string]struct{})
	userIDs := make([]string, 0)
	for _, row := range rows {
		if row.HandledByUserID == nil {
			continue
		}
		if _, dup := seen[*row.HandledByUserID]; !dup {
			seen[*row.HandledByUserID] = struct{}{}
			userIDs = append(userIDs, *row.HandledByUserID)
		}
	}
	db := h.db()
	if len(userIDs) == 0 || db == nil {
		return nil
	}
	users, err := db.User.Query().
		Where(userSchema.IDIn(userIDs...)).
		Select(userSchema.FieldID, userSchema.FieldName).
		All(ctx)
	if err != nil {
		h.logger.Warnf("bot_security_admin event=handler_lookup_failed users=%d error_type=%T", len(userIDs), err)
		return nil
	}
	names := make(map[string]string, len(users))
	for _, user := range users {
		names[user.ID] = user.Name
	}
	return names
}
