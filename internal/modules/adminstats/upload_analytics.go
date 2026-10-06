package adminstats

import (
	"context"
	"database/sql"
	"strconv"
	"time"

	core "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	platform "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/upload"
	"github.com/gofiber/fiber/v3"
)

type uploadIngressDay struct {
	Day      string `json:"day"`
	Protocol string `json:"protocol"`
	Result   string `json:"result"`
	Count    int64  `json:"count"`
}
type uploadIngressResponse struct {
	Available     bool               `json:"available"`
	Complete      bool               `json:"complete"`
	Granularity   string             `json:"granularity"`
	RetentionDays int                `json:"retentionDays"`
	Note          string             `json:"note"`
	Items         []uploadIngressDay `json:"items"`
}
type uploadAnalyticsResponse struct {
	From           time.Time              `json:"from"`
	To             time.Time              `json:"to"`
	GroupBy        []string               `json:"groupBy"`
	Interval       string                 `json:"interval,omitempty"`
	Summary        uploadAnalyticsMetrics `json:"summary"`
	Groups         []uploadAnalyticsGroup `json:"groups"`
	ByFailureStage map[string]int64       `json:"byFailureStage"`
	Migration      uploadMigrationMetrics `json:"migration"`
	Ingress        *uploadIngressResponse `json:"ingress,omitempty"`
}

func parseAnalyticsDimensions(c fiber.Ctx) ([]string, string, error) {
	dims := parseCSVValues(c.Query("group_by"))
	if len(dims) == 0 {
		dims = []string{"upload_method"}
	}
	if len(dims) > 2 {
		return nil, "", fiber.NewError(400, "at most two group_by dimensions")
	}
	for _, d := range dims {
		if !analyticsDimensions[d] {
			return nil, "", fiber.NewError(400, "invalid group_by")
		}
	}
	interval := c.Query("interval")
	if interval != "" && interval != "hour" && interval != "day" {
		return nil, "", fiber.NewError(400, "invalid interval")
	}
	return dims, interval, nil
}

func handleUploadAnalytics(helper *api.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		actor, role, err := core.CurrentAdminActor(c)
		if err != nil {
			return core.RespondFiberOrUnauthorized(c, err, "missing user session")
		}
		filters, err := parseUploadLogQueryFilters(c, adminNow())
		if err != nil {
			return respondFiberOrBadRequest(c, err, "invalid filters")
		}
		dims, interval, err := parseAnalyticsDimensions(c)
		if err != nil {
			return respondFiberOrBadRequest(c, err, "invalid grouping")
		}
		ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
		defer cancel()
		db := helper.DBManager.DB.SQLDB()
		if db == nil {
			return api.ErrorInternal(c, "statistics database unavailable")
		}
		tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
		if err != nil {
			return api.ErrorInternal(c, "statistics unavailable")
		}
		defer func() { _ = tx.Rollback() }()
		where, args := analyticsWhere(filters, actor, role)
		response := uploadAnalyticsResponse{From: filters.From, To: filters.To, GroupBy: dims, Interval: interval, ByFailureStage: map[string]int64{}}
		if err := tx.QueryRowContext(ctx, "SELECT "+analyticsMetricsSQL+" FROM upload_logs l WHERE "+where, args...).Scan(response.Summary.destinations()...); err != nil {
			return api.ErrorInternal(c, "failed to aggregate uploads")
		}
		response.Summary.finish()
		response.Groups, err = queryAnalyticsGroups(ctx, tx, where, args, dims, interval)
		if err != nil {
			return api.ErrorInternal(c, "failed to group uploads")
		}
		if len(response.Groups) > 5000 {
			return api.ErrorBadRequest(c, "too many groups; narrow the time range or filters")
		}
		response.Migration, err = queryUploadMigration(ctx, tx, where, args)
		if err != nil {
			return api.ErrorInternal(c, "failed to aggregate migration")
		}
		rows, err := tx.QueryContext(ctx, "SELECT COALESCE(failure_stage,'unknown'),COUNT(*) FROM upload_logs l WHERE "+where+" AND NOT success GROUP BY failure_stage", args...)
		if err != nil {
			return api.ErrorInternal(c, "failed to aggregate failures")
		}
		for rows.Next() {
			var stage string
			var count int64
			if err = rows.Scan(&stage, &count); err != nil {
				break
			}
			response.ByFailureStage[stage] = count
		}
		rowErr := rows.Err()
		_ = rows.Close()
		if err != nil || rowErr != nil {
			return api.ErrorInternal(c, "failed to read failures")
		}
		if err = tx.Commit(); err != nil {
			return api.ErrorInternal(c, "failed to complete statistics")
		}
		if core.NormalizeRole(role) == core.RoleSuperAdmin {
			response.Ingress = queryProxyIngress(ctx, helper, filters.From, filters.To)
		}
		return api.Responses.SuccessResponse(c, "success", &response)
	}
}

func queryProxyIngress(ctx context.Context, helper *api.HarukiToolboxRouterHelpers, from, to time.Time) *uploadIngressResponse {
	out := &uploadIngressResponse{Granularity: "utc_day", RetentionDays: 35, Note: "Best-effort global HarukiProxy ingress totals for intersecting UTC days; not scoped by business filters and not guaranteed complete.", Items: []uploadIngressDay{}}
	if helper.DBManager.Redis == nil || helper.DBManager.Redis.Redis == nil {
		return out
	}
	keys := []string{}
	now := adminNow().UTC()
	for day := from.UTC().Truncate(24 * time.Hour); day.Before(to); day = day.AddDate(0, 0, 1) {
		if day.Before(now.Add(-platform.IngressRetention)) {
			continue
		}
		for _, protocol := range []string{"2", "3"} {
			for _, result := range platform.IngressResults() {
				keys = append(keys, platform.IngressKey(day, protocol, result))
				out.Items = append(out.Items, uploadIngressDay{Day: day.Format("2006-01-02"), Protocol: protocol, Result: result})
			}
		}
	}
	if len(keys) == 0 {
		return out
	}
	readCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	values, err := helper.DBManager.Redis.Redis.MGet(readCtx, keys...).Result()
	if err != nil {
		out.Items = nil
		return out
	}
	for i, v := range values {
		if v == nil {
			continue
		}
		raw, ok := v.(string)
		if !ok {
			out.Items = nil
			return out
		}
		count, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || count < 0 {
			out.Items = nil
			return out
		}
		out.Items[i].Count = count
	}
	out.Available = true
	if failure := platform.IngressLastFailure(); !failure.Before(from) && failure.Before(to) {
		out.Available = false
		out.Items = nil
	}
	// Complete remains false: process restarts and other instances can lose counters.
	return out
}
