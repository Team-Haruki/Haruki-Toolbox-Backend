package adminstats

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/lib/pq"
	"strings"

	core "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
)

// Identifiers only come from this allowlist; all user values are SQL parameters.
var analyticsDimensions = map[string]bool{"upload_method": true, "protocol_version": true, "client_name": true, "client_version": true, "client_channel": true, "platform": true, "os_version": true, "os_arch": true, "app_arch": true, "server": true, "data_type": true, "oauth_client_id": true}

const analyticsWhereSQL = `COALESCE(l.received_at,l.upload_time) >= $1 AND COALESCE(l.received_at,l.upload_time) < $2
AND ($3::boolean OR (l.identity_verified = TRUE AND EXISTS (SELECT 1 FROM users u WHERE u.id=l.toolbox_user_id AND u.role <> 'super_admin' AND u.id <> $4) AND (l.actor_user_id IS NULL OR EXISTS (SELECT 1 FROM users a WHERE a.id=l.actor_user_id AND a.role <> 'super_admin' AND a.id <> $4))))
AND ($5::boolean IS NULL OR l.success=$5)
AND ($6::text[] IS NULL OR l.game_user_id=ANY($6::text[]))
AND ($7::text[] IS NULL OR l.upload_method=ANY($7::text[]))
AND ($8::text[] IS NULL OR l.data_type=ANY($8::text[]))
AND ($9::text[] IS NULL OR l.server=ANY($9::text[]))
AND ($10::text[] IS NULL OR l.protocol_version=ANY($10::text[]))
AND ($11::text[] IS NULL OR l.client_version=ANY($11::text[]))
AND ($12::text[] IS NULL OR l.client_channel=ANY($12::text[]))
AND ($13::text[] IS NULL OR l.platform=ANY($13::text[]))
AND ($14::text[] IS NULL OR l.os_version=ANY($14::text[]))
AND ($15::text[] IS NULL OR l.os_arch=ANY($15::text[]))
AND ($16::text[] IS NULL OR l.app_arch=ANY($16::text[]))
AND ($17::text[] IS NULL OR l.client_name=ANY($17::text[]))
AND ($18::text[] IS NULL OR l.oauth_client_id=ANY($18::text[]))`

func analyticsWhere(f *uploadLogQueryFilters, actorID, role string) (string, []any) {
	// Keep both SQL identifiers and query structure static; all filters are bound values.
	args := []any{f.From, f.To, core.NormalizeRole(role) == core.RoleSuperAdmin, actorID, f.Success}
	args = append(args, pq.Array(f.GameUserIDs))
	args = append(args, pq.Array(f.UploadMethods))
	args = append(args, pq.Array(f.DataTypes))
	args = append(args, pq.Array(f.Servers))
	args = append(args, pq.Array(f.Metadata["protocol_version"]))
	args = append(args, pq.Array(f.Metadata["client_version"]))
	args = append(args, pq.Array(f.Metadata["client_channel"]))
	args = append(args, pq.Array(f.Metadata["platform"]))
	args = append(args, pq.Array(f.Metadata["os_version"]))
	args = append(args, pq.Array(f.Metadata["os_arch"]))
	args = append(args, pq.Array(f.Metadata["app_arch"]))
	args = append(args, pq.Array(f.Metadata["client_name"]))
	args = append(args, pq.Array(f.Metadata["oauth_client_id"]))
	return analyticsWhereSQL, args
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
		if !analyticsDimensions[d] {
			return nil, fmt.Errorf("invalid analytics dimension")
		}
		columns = append(columns, "l."+d)
	}
	names := append([]string(nil), dimensions...)
	if interval != "" {
		if interval != "hour" && interval != "day" {
			return nil, fmt.Errorf("invalid analytics interval")
		}
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

func queryUploadMigration(ctx context.Context, tx *sql.Tx, _ string, args []any) (uploadMigrationMetrics, error) {
	var m uploadMigrationMetrics
	const q = `WITH scoped AS (SELECT * FROM upload_logs l WHERE ` + analyticsWhereSQL + ` AND upload_method='haruki_proxy' AND success),
 accounts AS (SELECT server,game_user_id FROM scoped WHERE identity_verified AND game_user_id IS NOT NULL GROUP BY server,game_user_id HAVING bool_or(protocol_version='2') AND NOT bool_or(COALESCE(protocol_version='3',FALSE)))
 SELECT COUNT(*) FILTER (WHERE protocol_version='2'), COUNT(*) FILTER (WHERE protocol_version='3'), COUNT(*) FILTER (WHERE protocol_version IS NULL), (SELECT COUNT(*) FROM accounts) FROM scoped`
	err := tx.QueryRowContext(ctx, q, args...).Scan(&m.V2Success, &m.V3Success, &m.UnknownProtocolSuccess, &m.V2OnlyAccounts)
	if sum := m.V2Success + m.V3Success; sum > 0 {
		share := float64(m.V3Success) / float64(sum)
		m.V3Share = &share
	}
	return m, err
}
