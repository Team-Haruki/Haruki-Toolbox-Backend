package adminsyslog

import (
	"bytes"
	"encoding/csv"
	json "encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/systemlog"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"
)

func TestParseSystemLogActorTypesFilter(t *testing.T) {
	values, err := parseSystemLogActorTypesFilter("admin,user")
	if err != nil {
		t.Fatalf("parseSystemLogActorTypesFilter returned error: %v", err)
	}
	if len(values) != 2 || values[0] != "admin" || values[1] != "user" {
		t.Fatalf("unexpected actor types: %#v", values)
	}

	if _, err := parseSystemLogActorTypesFilter("admin,owner"); err == nil {
		t.Fatalf("expected invalid actor_type filter to fail")
	}
}

func TestParseSystemLogResultFilter(t *testing.T) {
	result, err := parseSystemLogResultFilter("success")
	if err != nil {
		t.Fatalf("parseSystemLogResultFilter returned error: %v", err)
	}
	if result != "success" {
		t.Fatalf("result = %q, want success", result)
	}

	if _, err := parseSystemLogResultFilter("unknown"); err == nil {
		t.Fatalf("expected invalid result filter to fail")
	}
}

func TestParseSystemLogSort(t *testing.T) {
	sortValue, err := parseSystemLogSort("")
	if err != nil {
		t.Fatalf("parseSystemLogSort returned error: %v", err)
	}
	if sortValue != defaultSystemLogSort {
		t.Fatalf("sortValue = %q, want %q", sortValue, defaultSystemLogSort)
	}

	if _, err := parseSystemLogSort("invalid"); err == nil {
		t.Fatalf("expected invalid sort to fail")
	}
}

func TestParseSystemLogQueryFilters(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		_, err := parseSystemLogQueryFilters(c, time.Date(2026, time.March, 8, 12, 0, 0, 0, time.UTC))
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.SendStatus(fiberErr.Code)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}
		return c.SendStatus(fiber.StatusNoContent)
	})

	t.Run("valid filters", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?from=2026-03-08T00:00:00Z&to=2026-03-08T12:00:00Z&actor_type=admin,user&actor_user_id=1001&target_type=user&target_id=2002&action=admin.user.ban&result=success&page=2&page_size=20&sort=event_time_desc", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
		}
	})

	t.Run("invalid actor type", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?actor_type=owner", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})

	t.Run("invalid result", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?result=ok", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})

	t.Run("invalid page size", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?page_size=1000", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})
}

func TestSystemLogFailureReasonSummaryGroupsInDatabase(t *testing.T) {
	db := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer db.Close()
	stamp := time.Now().UTC().Add(-time.Minute)
	for _, reason := range []any{"permission_denied", "\tpermission_denied\u00a0", " invalid_payload ", "", 123, nil} {
		db.SystemLog.Create().SetEventTime(stamp).SetAction("fixture").SetResult(systemlog.ResultFailure).
			SetMetadata(map[string]any{"reason": reason}).ExecX(t.Context())
	}
	db.SystemLog.Create().SetEventTime(stamp).SetAction("fixture").SetResult(systemlog.ResultSuccess).
		SetMetadata(map[string]any{"reason": "permission_denied"}).ExecX(t.Context())
	got, err := summarizeSystemLogFailureReasons(t.Context(), db.SystemLog.Query())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}
	if got[0].Key != unknownSystemLogFailureReason || got[0].Count != 3 {
		t.Fatalf("got[0] = %#v, want key=%q,count=3", got[0], unknownSystemLogFailureReason)
	}
	if got[1].Key != "permission_denied" || got[1].Count != 2 {
		t.Fatalf("got[1] = %#v, want key=permission_denied,count=2", got[1])
	}
	if got[2].Key != "invalid_payload" || got[2].Count != 1 {
		t.Fatalf("got[2] = %#v, want key=invalid_payload,count=1", got[2])
	}
	for i := range maxFailureReasonGroups + 2 {
		db.SystemLog.Create().SetEventTime(stamp).SetAction("fixture").SetResult(systemlog.ResultFailure).
			SetMetadata(map[string]any{"reason": fmt.Sprintf("legacy-%04d-%s", i, strings.Repeat("x", 256))}).ExecX(t.Context())
	}
	got, err = summarizeSystemLogFailureReasons(t.Context(), db.SystemLog.Query())
	if err != nil || len(got) != maxFailureReasonGroups || got[0].Key != unknownSystemLogFailureReason || len(got[3].Key) != 128 {
		t.Fatalf("unexpected bounded summary: groups=%d err=%v", len(got), err)
	}
	db.SystemLog.Create().SetEventTime(stamp).SetAction("fixture").SetActorRole("super_admin").SetResult(systemlog.ResultFailure).
		SetMetadata(map[string]any{"reason": "protected"}).ExecX(t.Context())
	helper := &api.HarukiToolboxRouterHelpers{DBManager: &database.HarukiToolboxDBManager{DB: db}}
	app := fiber.New()
	app.Get("/summary", func(c fiber.Ctx) error {
		c.Locals("userID", "fixture-admin")
		c.Locals("userRole", "admin")
		return c.Next()
	}, handleGetSystemLogSummary(helper))
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/summary", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		UpdatedData systemLogSummaryResponse `json:"updatedData"`
	}
	err = json.UnmarshalRead(resp.Body, &body)
	result := body.UpdatedData
	if err != nil || resp.StatusCode != http.StatusOK || len(result.ByReason) != maxFailureReasonGroups || result.OtherFailureReasonCount != 5 || result.Failure != maxFailureReasonGroups+8 {
		t.Fatalf("summary status=%d groups=%d other=%d failures=%d err=%v", resp.StatusCode, len(result.ByReason), result.OtherFailureReasonCount, result.Failure, err)
	}
}

func TestPostgresReasonSummaryUsesBoundedJSONAggregation(t *testing.T) {
	selector := sql.Dialect(dialect.Postgres).Select().From(sql.Table(systemlog.Table))
	selector.Select(groupFailureReasons(selector))
	query, _ := selector.Query()
	for _, fragment := range []string{"jsonb_typeof(", "->> 'reason'", "GROUP BY", "ORDER BY count DESC", "LIMIT 128"} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("PostgreSQL query lost %q: %s", fragment, query)
		}
	}
}

func TestAuditCSVPreservesFormulaLikeCellsAsText(t *testing.T) {
	data, err := buildSystemLogCSV([]systemLogListItem{{TargetID: "  =1", UserAgent: "browser, test"}})
	if err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil || len(records) != 2 || records[1][7] != "'  =1" || records[1][10] != "browser, test" {
		t.Fatalf("unexpected CSV records: %v, %v", records, err)
	}
}
