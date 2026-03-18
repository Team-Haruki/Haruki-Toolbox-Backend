package adminoauth

import (
	platformPagination "haruki-suite/internal/platform/pagination"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/systemlog"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

type adminOAuthClientAuditFilters struct {
	From        time.Time
	To          time.Time
	ActorTypes  []string
	ActorUserID string
	Action      string
	Result      string
	Page        int
	PageSize    int
	Sort        string
}

type adminOAuthClientAuditAppliedFilters struct {
	ActorTypes  []string `json:"actorTypes,omitempty"`
	ActorUserID string   `json:"actorUserId,omitempty"`
	Action      string   `json:"action,omitempty"`
	Result      string   `json:"result,omitempty"`
}

type adminOAuthClientAuditLogsResponse struct {
	GeneratedAt time.Time                           `json:"generatedAt"`
	ClientID    string                              `json:"clientId"`
	ClientName  string                              `json:"clientName"`
	From        time.Time                           `json:"from"`
	To          time.Time                           `json:"to"`
	Page        int                                 `json:"page"`
	PageSize    int                                 `json:"pageSize"`
	Total       int                                 `json:"total"`
	TotalPages  int                                 `json:"totalPages"`
	HasMore     bool                                `json:"hasMore"`
	Sort        string                              `json:"sort"`
	Filters     adminOAuthClientAuditAppliedFilters `json:"filters"`
	Items       []systemLogListItem                 `json:"items"`
}

type adminOAuthClientAuditSummaryResponse struct {
	GeneratedAt time.Time       `json:"generatedAt"`
	ClientID    string          `json:"clientId"`
	ClientName  string          `json:"clientName"`
	From        time.Time       `json:"from"`
	To          time.Time       `json:"to"`
	Total       int             `json:"total"`
	Success     int             `json:"success"`
	Failure     int             `json:"failure"`
	ByAction    []categoryCount `json:"byAction"`
	ByActorType []categoryCount `json:"byActorType"`
	ByResult    []categoryCount `json:"byResult"`
	ByReason    []categoryCount `json:"byReason"`
}

func parseAdminOAuthClientAuditFilters(c fiber.Ctx, now time.Time) (*adminOAuthClientAuditFilters, error) {
	from, to, err := resolveUploadLogTimeRange(c.Query("from"), c.Query("to"), now)
	if err != nil {
		return nil, err
	}
	actorTypes, err := parseSystemLogActorTypesFilter(c.Query("actor_type"))
	if err != nil {
		return nil, err
	}
	result, err := parseSystemLogResultFilter(c.Query("result"))
	if err != nil {
		return nil, err
	}
	page, pageSize, err := platformPagination.ParsePageAndPageSize(c, defaultSystemLogPage, defaultSystemLogPageSize, maxSystemLogPageSize)
	if err != nil {
		return nil, err
	}
	sortValue, err := parseSystemLogSort(c.Query("sort"))
	if err != nil {
		return nil, err
	}
	return &adminOAuthClientAuditFilters{
		From:        from,
		To:          to,
		ActorTypes:  actorTypes,
		ActorUserID: strings.TrimSpace(c.Query("actor_user_id")),
		Action:      strings.TrimSpace(c.Query("action")),
		Result:      result,
		Page:        page,
		PageSize:    pageSize,
		Sort:        sortValue,
	}, nil
}

func applyAdminOAuthClientAuditFilters(query *postgresql.SystemLogQuery, clientID string, filters *adminOAuthClientAuditFilters) *postgresql.SystemLogQuery {
	q := query.Where(systemlog.EventTimeGTE(filters.From), systemlog.EventTimeLTE(filters.To), systemlog.TargetTypeEQ(adminAuditTargetTypeOAuthClient), systemlog.TargetIDEQ(clientID))
	if len(filters.ActorTypes) > 0 {
		types := make([]systemlog.ActorType, 0, len(filters.ActorTypes))
		for _, actorType := range filters.ActorTypes {
			types = append(types, systemlog.ActorType(actorType))
		}
		q = q.Where(systemlog.ActorTypeIn(types...))
	}
	if filters.ActorUserID != "" {
		q = q.Where(systemlog.ActorUserIDEQ(filters.ActorUserID))
	}
	if filters.Action != "" {
		q = q.Where(systemlog.ActionContainsFold(filters.Action))
	}
	if filters.Result != "" {
		q = q.Where(systemlog.ResultEQ(systemlog.Result(filters.Result)))
	}
	return q
}
