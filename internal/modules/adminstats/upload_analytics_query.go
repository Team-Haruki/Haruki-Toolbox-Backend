package adminstats

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	core "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
)

// Identifiers only come from this allowlist; all user values are SQL parameters.
var analyticsDimensions = map[string]bool{"upload_method": true, "protocol_version": true, "client_name": true, "client_version": true, "client_channel": true, "platform": true, "os_version": true, "os_arch": true, "app_arch": true, "server": true, "data_type": true, "oauth_client_id": true}

func analyticsWhere(f *uploadLogQueryFilters, actorID, role string) (string, []any) {
	clauses := []string{"COALESCE(l.received_at,l.upload_time) >= $1", "COALESCE(l.received_at,l.upload_time) < $2"}
	args := []any{f.From, f.To}
	if core.NormalizeRole(role) != core.RoleSuperAdmin {
		args = append(args, actorID)
		clauses = append(clauses, fmt.Sprintf("l.identity_verified = TRUE AND EXISTS (SELECT 1 FROM users u WHERE u.id=l.toolbox_user_id AND u.role <> 'super_admin' AND u.id <> $%d)", len(args)))
	}
	values := map[string][]string{"game_user_id": f.GameUserIDs, "upload_method": f.UploadMethods, "data_type": f.DataTypes, "server": f.Servers}
	for k, v := range f.Metadata {
		values[k] = v
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if len(values[k]) == 0 {
			continue
		}
		placeholders := make([]string, 0, len(values[k]))
		for _, v := range values[k] {
			args = append(args, v)
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
		}
		clauses = append(clauses, "l."+k+" IN ("+strings.Join(placeholders, ",")+")")
	}
	if f.Success != nil {
		args = append(args, *f.Success)
		clauses = append(clauses, fmt.Sprintf("l.success=$%d", len(args)))
	}
	return strings.Join(clauses, " AND "), args
}

const analyticsMetricsSQL = `COUNT(*), COUNT(*) FILTER (WHERE success),
COUNT(DISTINCT (server,game_user_id)) FILTER (WHERE success AND identity_verified AND game_user_id IS NOT NULL),
COUNT(processing_duration_ms),
percentile_cont(0.5) WITHIN GROUP (ORDER BY processing_duration_ms),
percentile_cont(0.95) WITHIN GROUP (ORDER BY processing_duration_ms),
COUNT(*) FILTER (WHERE client_metadata_format IS NOT NULL OR platform IS NOT NULL OR oauth_client_id IS NOT NULL)`

type uploadAnalyticsMetrics struct {
	AttemptCount       int64    `json:"attemptCount"`
	SuccessCount       int64    `json:"successCount"`
	FailureCount       int64    `json:"failureCount"`
	SuccessRate        *float64 `json:"successRate"`
	ActiveGameAccounts int64    `json:"activeGameAccounts"`
	LatencySampleCount int64    `json:"latencySampleCount"`
	LatencyP50Ms       *float64 `json:"latencyP50Ms"`
	LatencyP95Ms       *float64 `json:"latencyP95Ms"`
	MetadataCount      int64    `json:"metadataCount"`
	MetadataCoverage   *float64 `json:"metadataCoverage"`
}

func (m *uploadAnalyticsMetrics) destinations() []any {
	return []any{&m.AttemptCount, &m.SuccessCount, &m.ActiveGameAccounts, &m.LatencySampleCount, &m.LatencyP50Ms, &m.LatencyP95Ms, &m.MetadataCount}
}
func (m *uploadAnalyticsMetrics) finish() {
	m.FailureCount = m.AttemptCount - m.SuccessCount
	if m.AttemptCount > 0 {
		rate := float64(m.SuccessCount) / float64(m.AttemptCount)
		coverage := float64(m.MetadataCount) / float64(m.AttemptCount)
		m.SuccessRate = &rate
		m.MetadataCoverage = &coverage
	}
}

type uploadAnalyticsGroup struct {
	Dimensions map[string]*string `json:"dimensions"`
	uploadAnalyticsMetrics
}

func queryAnalyticsGroups(ctx context.Context, tx *sql.Tx, where string, args []any, dimensions []string, interval string) ([]uploadAnalyticsGroup, error) {
	columns := make([]string, 0, len(dimensions)+1)
	for _, d := range dimensions {
		columns = append(columns, "l."+d)
	}
	names := append([]string(nil), dimensions...)
	if interval != "" {
		columns = append(columns, "to_char(date_trunc('"+interval+"',COALESCE(l.received_at,l.upload_time) AT TIME ZONE 'UTC'),'YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"')")
		names = append(names, "bucket")
	}
	groupColumns := strings.Join(columns, ",")
	rows, err := tx.QueryContext(ctx, "SELECT "+groupColumns+","+analyticsMetricsSQL+" FROM upload_logs l WHERE "+where+" GROUP BY "+groupColumns+" ORDER BY "+groupColumns+" LIMIT 5001", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []uploadAnalyticsGroup{}
	for rows.Next() {
		g := uploadAnalyticsGroup{Dimensions: map[string]*string{}}
		vals := make([]*string, len(names))
		dest := make([]any, 0, len(names)+7)
		for i := range vals {
			dest = append(dest, &vals[i])
		}
		dest = append(dest, g.destinations()...)
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		for i, name := range names {
			g.Dimensions[name] = vals[i]
		}
		g.finish()
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

type uploadMigrationMetrics struct {
	V2Success              int64    `json:"v2Success"`
	V3Success              int64    `json:"v3Success"`
	UnknownProtocolSuccess int64    `json:"unknownProtocolSuccess"`
	V3Share                *float64 `json:"v3Share"`
	V2OnlyAccounts         int64    `json:"v2OnlyAccounts"`
}

func queryUploadMigration(ctx context.Context, tx *sql.Tx, where string, args []any) (uploadMigrationMetrics, error) {
	var m uploadMigrationMetrics
	q := `WITH scoped AS (SELECT * FROM upload_logs l WHERE ` + where + ` AND upload_method='haruki_proxy' AND success),
 accounts AS (SELECT server,game_user_id FROM scoped WHERE identity_verified AND game_user_id IS NOT NULL GROUP BY server,game_user_id HAVING bool_or(protocol_version='2') AND NOT bool_or(COALESCE(protocol_version='3',FALSE)))
 SELECT COUNT(*) FILTER (WHERE protocol_version='2'), COUNT(*) FILTER (WHERE protocol_version='3'), COUNT(*) FILTER (WHERE protocol_version IS NULL), (SELECT COUNT(*) FROM accounts) FROM scoped`
	err := tx.QueryRowContext(ctx, q, args...).Scan(&m.V2Success, &m.V3Success, &m.UnknownProtocolSuccess, &m.V2OnlyAccounts)
	if sum := m.V2Success + m.V3Success; sum > 0 {
		share := float64(m.V3Success) / float64(sum)
		m.V3Share = &share
	}
	return m, err
}
