package adminstats

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	core "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	"github.com/gofiber/fiber/v3"
	_ "github.com/lib/pq"
)

func TestAnalyticsFiltersAndDimensions(t *testing.T) {
	for _, query := range []string{"group_by=client_version,platform,server", "group_by=evil", "interval=minute", "platform=" + strings.Repeat("x", 17), "client_version=" + strings.Repeat("x", 129), "from=2026-01-01T00:00:00Z&to=2026-03-01T00:00:00Z"} {
		app := fiber.New()
		app.Get("/", func(c fiber.Ctx) error {
			if _, err := parseUploadLogQueryFilters(c, time.Now()); err != nil {
				return c.SendStatus(400)
			}
			if _, _, err := parseAnalyticsDimensions(c); err != nil {
				return c.SendStatus(400)
			}
			return c.SendStatus(200)
		})
		resp, err := app.Test(httptest.NewRequest("GET", "/?"+query, nil))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Fatalf("accepted %s", query)
		}
	}
	f := &uploadLogQueryFilters{From: time.Now(), To: time.Now().Add(time.Hour), Metadata: map[string][]string{"client_version": {"x' OR TRUE --"}}}
	where, args := analyticsWhere(f, "actor", core.RoleAdmin)
	if strings.Contains(where, "OR TRUE") || !strings.Contains(where, "identity_verified = TRUE") || !strings.Contains(where, "u.role <> 'super_admin'") || !strings.Contains(where, "u.id <>") {
		t.Fatal(where)
	}
	value, err := args[10].(driver.Valuer).Value()
	if err != nil || !strings.Contains(value.(string), "x' OR TRUE --") {
		t.Fatal(args)
	}
}

// Opt-in against any PostgreSQL test DB. All fixtures are transaction-local TEMP
// tables and the transaction is rolled back, so no permanent schema is changed.
func TestUploadAnalyticsPostgres(t *testing.T) {
	dsn := os.Getenv("HARUKI_TEST_UPLOAD_ANALYTICS_DSN")
	if dsn == "" {
		t.Skip("set HARUKI_TEST_UPLOAD_ANALYTICS_DSN for PostgreSQL integration")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `CREATE TEMP TABLE users(id text,role text) ON COMMIT DROP;
 CREATE TEMP TABLE upload_logs(server text,game_user_id text,toolbox_user_id text,data_type text,upload_method text,success boolean,upload_time timestamptz,received_at timestamptz,identity_verified boolean,client_metadata_format text,platform text,protocol_version text,processing_duration_ms bigint,oauth_client_id text,failure_stage text) ON COMMIT DROP;
 ALTER TABLE upload_logs ADD COLUMN client_version text, ADD COLUMN client_channel text, ADD COLUMN os_version text, ADD COLUMN os_arch text, ADD COLUMN app_arch text, ADD COLUMN client_name text, ADD COLUMN actor_user_id text;
 INSERT INTO users VALUES ('u','user'),('s','super_admin'),('a','admin');`)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	insert := func(owner, game, protocol, platform string, success, verified bool, ms int, day int) {
		_, err := tx.ExecContext(ctx, `INSERT INTO upload_logs(server,game_user_id,toolbox_user_id,data_type,upload_method,success,upload_time,received_at,identity_verified,client_metadata_format,platform,protocol_version,processing_duration_ms,oauth_client_id,failure_stage) VALUES ('jp',$1,$2,'suite','haruki_proxy',$3,$4,$4,$5,'structured',$6,$7,$8,NULL,NULL)`, game, owner, success, start.Add(time.Duration(day)*24*time.Hour), verified, platform, protocol, ms)
		if err != nil {
			t.Fatal(err)
		}
	}
	insert("u", "1", "2", "windows", true, true, 10, 0)
	insert("u", "1", "3", "android", true, true, 30, 1)
	insert("u", "2", "2", "windows", true, true, 50, 0)
	insert("s", "3", "3", "ios", true, true, 90, 0)
	insert("", "4", "3", "android", false, false, 100, 0)
	insert("a", "5", "3", "android", true, true, 200, 0)
	f := &uploadLogQueryFilters{From: start, To: start.Add(48 * time.Hour)}
	for _, tc := range []struct {
		role                       string
		attempts, accounts, v2Only int64
		p50                        float64
	}{
		{core.RoleSuperAdmin, 6, 4, 1, 70}, {core.RoleAdmin, 3, 2, 1, 30},
	} {
		where, args := analyticsWhere(f, "a", tc.role)
		var m uploadAnalyticsMetrics
		if err := tx.QueryRowContext(ctx, "SELECT "+analyticsMetricsSQL+" FROM upload_logs l WHERE "+where, args...).Scan(m.destinations()...); err != nil {
			t.Fatal(err)
		}
		m.finish()
		if m.AttemptCount != tc.attempts || m.ActiveGameAccounts != tc.accounts || m.LatencyP50Ms == nil || *m.LatencyP50Ms != tc.p50 {
			t.Fatalf("%s metrics: %+v", tc.role, m)
		}
		migration, err := queryUploadMigration(ctx, tx, where, args)
		if err != nil || migration.V2OnlyAccounts != tc.v2Only {
			t.Fatalf("migration %+v %v", migration, err)
		}
		groups, err := queryAnalyticsGroups(ctx, tx, where, args, []string{"platform"}, "day")
		if err != nil {
			t.Fatal(err)
		}
		var sum int64
		for _, g := range groups {
			sum += g.AttemptCount
			if g.Dimensions["bucket"] == nil {
				t.Fatal("missing UTC bucket")
			}
		}
		if sum != tc.attempts {
			t.Fatalf("group sum %d", sum)
		}
	}
}
