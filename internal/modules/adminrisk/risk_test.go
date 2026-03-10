package adminrisk

import (
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/riskevent"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func TestParseRiskEventFilters(t *testing.T) {
	now := time.Date(2026, time.March, 8, 12, 0, 0, 0, time.UTC)

	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		_, err := parseRiskEventFilters(c, now)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.SendStatus(fiberErr.Code)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}
		return c.SendStatus(fiber.StatusNoContent)
	})

	t.Run("valid filters", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?status=open&severity=high&actor_user_id=1001&target_user_id=2002&action=login&page=2&page_size=20&sort=event_time_asc", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
		}
	})

	t.Run("invalid status", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?status=done", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})
}

func TestParseRiskEventSort(t *testing.T) {
	t.Parallel()

	t.Run("empty uses default", func(t *testing.T) {
		t.Parallel()
		got, err := parseRiskEventSort("")
		if err != nil {
			t.Fatalf("parseRiskEventSort returned error: %v", err)
		}
		if got != defaultRiskEventSort {
			t.Fatalf("sort = %q, want %q", got, defaultRiskEventSort)
		}
	})

	t.Run("valid sort", func(t *testing.T) {
		t.Parallel()
		got, err := parseRiskEventSort(riskEventSortIDAsc)
		if err != nil {
			t.Fatalf("parseRiskEventSort returned error: %v", err)
		}
		if got != riskEventSortIDAsc {
			t.Fatalf("sort = %q, want %q", got, riskEventSortIDAsc)
		}
	})

	t.Run("invalid sort", func(t *testing.T) {
		t.Parallel()
		_, err := parseRiskEventSort("event_time_desc;drop")
		if err == nil {
			t.Fatalf("parseRiskEventSort should fail for invalid sort")
		}
		fiberErr, ok := err.(*fiber.Error)
		if !ok {
			t.Fatalf("error type = %T, want *fiber.Error", err)
		}
		if fiberErr.Code != fiber.StatusBadRequest {
			t.Fatalf("error code = %d, want %d", fiberErr.Code, fiber.StatusBadRequest)
		}
	})
}

func TestBuildRiskEventItems(t *testing.T) {
	t.Parallel()

	actor := "1001"
	target := "2002"
	ip := "127.0.0.1"
	action := "login"
	reason := "too many failures"
	resolvedBy := "admin-1"
	eventAt := time.Date(2026, time.March, 8, 10, 0, 0, 0, time.FixedZone("UTC+8", 8*3600))
	resolvedAt := time.Date(2026, time.March, 8, 12, 30, 0, 0, time.FixedZone("UTC+8", 8*3600))

	rows := []*postgresql.RiskEvent{
		{
			ID:           7,
			EventTime:    eventAt,
			Status:       riskevent.StatusResolved,
			Severity:     riskevent.SeverityHigh,
			Source:       "system",
			ActorUserID:  &actor,
			TargetUserID: &target,
			IP:           &ip,
			Action:       &action,
			Reason:       &reason,
			ResolvedAt:   &resolvedAt,
			ResolvedBy:   &resolvedBy,
			Metadata: map[string]any{
				"k": "v",
			},
		},
	}

	items := buildRiskEventItems(rows)
	if len(items) != 1 {
		t.Fatalf("items length = %d, want %d", len(items), 1)
	}
	item := items[0]
	if item.ID != 7 {
		t.Fatalf("id = %d, want %d", item.ID, 7)
	}
	if item.Status != string(riskevent.StatusResolved) {
		t.Fatalf("status = %q, want %q", item.Status, riskevent.StatusResolved)
	}
	if item.Severity != string(riskevent.SeverityHigh) {
		t.Fatalf("severity = %q, want %q", item.Severity, riskevent.SeverityHigh)
	}
	if item.EventTime.Location() != time.UTC {
		t.Fatalf("event time should be UTC, got %s", item.EventTime.Location())
	}
	if item.ResolvedAt == nil || item.ResolvedAt.Location() != time.UTC {
		t.Fatalf("resolved_at should be UTC and not nil")
	}
	if item.ActorUserID != actor || item.TargetUserID != target {
		t.Fatalf("actor/target mismatch, got %q/%q", item.ActorUserID, item.TargetUserID)
	}
	if item.Metadata["k"] != "v" {
		t.Fatalf("metadata mismatch, got %#v", item.Metadata)
	}
}
