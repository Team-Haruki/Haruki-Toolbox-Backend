package admintickets

import (
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/ticket"
	"haruki-suite/utils/database/postgresql/ticketmessage"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"
)

func TestParseAdminTicketFilters(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		_, err := parseAdminTicketFilters(c, "admin-1")
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.SendStatus(fiberErr.Code)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}
		return c.SendStatus(fiber.StatusNoContent)
	})

	t.Run("valid filters", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?q=abc&status=open&priority=high&creator_user_id=1001&assignee_admin_id=2001&page=2&page_size=20", nil)
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

	t.Run("invalid quick filter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?quick_filter=unknown", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})
}

func TestParseAdminTicketQuickFilter(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  adminTicketQuickFilter
	}{
		{name: "empty", input: "", want: ""},
		{name: "all alias", input: "all", want: ""},
		{name: "pending admin", input: "pending_admin", want: adminTicketQuickFilterPendingAdmin},
		{name: "pending user", input: "pending_user", want: adminTicketQuickFilterPendingUser},
		{name: "unassigned", input: "unassigned", want: adminTicketQuickFilterUnassigned},
		{name: "mine", input: "mine", want: adminTicketQuickFilterMine},
		{name: "high or urgent", input: "high_or_urgent", want: adminTicketQuickFilterHighOrUrgent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAdminTicketQuickFilter(tt.input)
			if err != nil {
				t.Fatalf("parseAdminTicketQuickFilter returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("quick filter = %q, want %q", got, tt.want)
			}
		})
	}

	if _, err := parseAdminTicketQuickFilter("unknown"); err == nil {
		t.Fatalf("expected invalid quick_filter to fail")
	}
}

func TestApplyAdminTicketQuickFilter(t *testing.T) {
	t.Run("pending admin applies default status", func(t *testing.T) {
		filters := &adminTicketFilters{QuickFilter: adminTicketQuickFilterPendingAdmin}
		if err := applyAdminTicketQuickFilter(filters, "admin-1"); err != nil {
			t.Fatalf("applyAdminTicketQuickFilter returned error: %v", err)
		}
		if filters.Status != ticket.StatusPendingAdmin {
			t.Fatalf("status = %q, want %q", filters.Status, ticket.StatusPendingAdmin)
		}
	})

	t.Run("explicit status overrides quick filter status", func(t *testing.T) {
		filters := &adminTicketFilters{
			QuickFilter: adminTicketQuickFilterPendingAdmin,
			Status:      ticket.StatusResolved,
		}
		if err := applyAdminTicketQuickFilter(filters, "admin-1"); err != nil {
			t.Fatalf("applyAdminTicketQuickFilter returned error: %v", err)
		}
		if filters.Status != ticket.StatusResolved {
			t.Fatalf("status = %q, want %q", filters.Status, ticket.StatusResolved)
		}
	})

	t.Run("mine uses current actor as default assignee", func(t *testing.T) {
		filters := &adminTicketFilters{QuickFilter: adminTicketQuickFilterMine}
		if err := applyAdminTicketQuickFilter(filters, "admin-1"); err != nil {
			t.Fatalf("applyAdminTicketQuickFilter returned error: %v", err)
		}
		if filters.AssigneeAdminID != "admin-1" {
			t.Fatalf("AssigneeAdminID = %q, want %q", filters.AssigneeAdminID, "admin-1")
		}
	})

	t.Run("explicit assignee overrides mine quick filter", func(t *testing.T) {
		filters := &adminTicketFilters{
			QuickFilter:     adminTicketQuickFilterMine,
			AssigneeAdminID: "admin-2",
		}
		if err := applyAdminTicketQuickFilter(filters, "admin-1"); err != nil {
			t.Fatalf("applyAdminTicketQuickFilter returned error: %v", err)
		}
		if filters.AssigneeAdminID != "admin-2" {
			t.Fatalf("AssigneeAdminID = %q, want %q", filters.AssigneeAdminID, "admin-2")
		}
	})

	t.Run("unassigned marks assignee nil filter", func(t *testing.T) {
		filters := &adminTicketFilters{QuickFilter: adminTicketQuickFilterUnassigned}
		if err := applyAdminTicketQuickFilter(filters, "admin-1"); err != nil {
			t.Fatalf("applyAdminTicketQuickFilter returned error: %v", err)
		}
		if !filters.RequireUnassigned {
			t.Fatalf("RequireUnassigned = false, want true")
		}
	})

	t.Run("explicit assignee overrides unassigned quick filter", func(t *testing.T) {
		filters := &adminTicketFilters{
			QuickFilter:     adminTicketQuickFilterUnassigned,
			AssigneeAdminID: "admin-2",
		}
		if err := applyAdminTicketQuickFilter(filters, "admin-1"); err != nil {
			t.Fatalf("applyAdminTicketQuickFilter returned error: %v", err)
		}
		if filters.RequireUnassigned {
			t.Fatalf("RequireUnassigned = true, want false")
		}
	})

	t.Run("high or urgent expands priority set", func(t *testing.T) {
		filters := &adminTicketFilters{QuickFilter: adminTicketQuickFilterHighOrUrgent}
		if err := applyAdminTicketQuickFilter(filters, "admin-1"); err != nil {
			t.Fatalf("applyAdminTicketQuickFilter returned error: %v", err)
		}
		if len(filters.PriorityValues) != 2 {
			t.Fatalf("PriorityValues length = %d, want 2", len(filters.PriorityValues))
		}
		if filters.PriorityValues[0] != ticket.PriorityHigh || filters.PriorityValues[1] != ticket.PriorityUrgent {
			t.Fatalf("PriorityValues = %#v, want [high urgent]", filters.PriorityValues)
		}
	})

	t.Run("explicit priority overrides high or urgent quick filter", func(t *testing.T) {
		filters := &adminTicketFilters{
			QuickFilter: adminTicketQuickFilterHighOrUrgent,
			Priority:    ticket.PriorityNormal,
		}
		if err := applyAdminTicketQuickFilter(filters, "admin-1"); err != nil {
			t.Fatalf("applyAdminTicketQuickFilter returned error: %v", err)
		}
		if len(filters.PriorityValues) != 0 {
			t.Fatalf("PriorityValues length = %d, want 0", len(filters.PriorityValues))
		}
	})

	t.Run("mine requires actor user id", func(t *testing.T) {
		filters := &adminTicketFilters{QuickFilter: adminTicketQuickFilterMine}
		if err := applyAdminTicketQuickFilter(filters, ""); err == nil {
			t.Fatalf("expected missing actor user id to fail")
		}
	})
}

func TestBuildAdminTicketListItemIncludesCreatorInfo(t *testing.T) {
	now := time.Now().UTC()
	assigneeAdminID := "admin-1"
	row := &postgresql.Ticket{
		TicketID:        "TK-20260308180000-abcdef123456",
		CreatorUserID:   "1241241241",
		Subject:         "upload failed",
		Priority:        "high",
		Status:          "open",
		AssigneeAdminID: &assigneeAdminID,
		CreatedAt:       now,
		UpdatedAt:       now,
		Edges: postgresql.TicketEdges{
			Messages: []*postgresql.TicketMessage{
				{
					ID:         2,
					SenderRole: ticketmessage.SenderRoleAdmin,
					Message:    " first line \n\n second line ",
					Internal:   true,
					CreatedAt:  now.Add(time.Minute),
				},
			},
		},
	}

	item := buildAdminTicketListItem(row, map[string]string{
		"1241241241": "test-user",
		"admin-1":    "ticket-admin",
	})
	if item.CreatorUserID != "1241241241" {
		t.Fatalf("CreatorUserID = %q, want %q", item.CreatorUserID, "1241241241")
	}
	if item.CreatorUserName != "test-user" {
		t.Fatalf("CreatorUserName = %q, want %q", item.CreatorUserName, "test-user")
	}
	if item.AssigneeAdminName != "ticket-admin" {
		t.Fatalf("AssigneeAdminName = %q, want %q", item.AssigneeAdminName, "ticket-admin")
	}
	if item.LastMessageSenderRole != string(ticketmessage.SenderRoleAdmin) {
		t.Fatalf("LastMessageSenderRole = %q, want %q", item.LastMessageSenderRole, ticketmessage.SenderRoleAdmin)
	}
	if item.LastMessagePreview != "first line second line" {
		t.Fatalf("LastMessagePreview = %q, want %q", item.LastMessagePreview, "first line second line")
	}
	if item.LastMessageInternal == nil || !*item.LastMessageInternal {
		t.Fatalf("LastMessageInternal = %#v, want true", item.LastMessageInternal)
	}
	if item.LastMessageAt == nil || !item.LastMessageAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("LastMessageAt = %#v, want %v", item.LastMessageAt, now.Add(time.Minute))
	}

	item = buildAdminTicketListItem(row, nil)
	if item.CreatorUserName != "" {
		t.Fatalf("CreatorUserName = %q, want empty string", item.CreatorUserName)
	}
}

func TestNormalizeAdminTicketMessagePreview(t *testing.T) {
	t.Run("collapse whitespace", func(t *testing.T) {
		got := normalizeAdminTicketMessagePreview("  first \n second\tthird  ")
		if got != "first second third" {
			t.Fatalf("preview = %q, want %q", got, "first second third")
		}
	})

	t.Run("truncate long unicode preview", func(t *testing.T) {
		raw := strings.Repeat("你", maxAdminTicketPreviewLength+5)
		got := normalizeAdminTicketMessagePreview(raw)
		if utf8.RuneCountInString(got) != maxAdminTicketPreviewLength {
			t.Fatalf("preview rune length = %d, want %d", utf8.RuneCountInString(got), maxAdminTicketPreviewLength)
		}
		if !strings.HasSuffix(got, "…") {
			t.Fatalf("preview = %q, want ellipsis suffix", got)
		}
	})
}

func TestBuildAdminTicketMessageItemsIncludeSenderNames(t *testing.T) {
	now := time.Now().UTC()
	senderUserID := "admin-1"
	items := buildAdminTicketMessageItems([]*postgresql.TicketMessage{
		{
			ID:           7,
			SenderUserID: &senderUserID,
			SenderRole:   ticketmessage.SenderRoleAdmin,
			Message:      "hello",
			Internal:     false,
			CreatedAt:    now,
		},
		{
			ID:         8,
			SenderRole: ticketmessage.SenderRoleSystem,
			Message:    "system event",
			Internal:   false,
			CreatedAt:  now.Add(time.Minute),
		},
	}, map[string]string{
		"admin-1": "ticket-admin",
	})

	if len(items) != 2 {
		t.Fatalf("items length = %d, want 2", len(items))
	}
	if items[0].SenderUserName != "ticket-admin" {
		t.Fatalf("SenderUserName = %q, want %q", items[0].SenderUserName, "ticket-admin")
	}
	if items[1].SenderUserName != "" {
		t.Fatalf("SenderUserName = %q, want empty string", items[1].SenderUserName)
	}
}

func TestBuildAdminTicketSystemEventMessages(t *testing.T) {
	if got := buildAdminTicketStatusEventMessage(ticket.StatusOpen, ticket.StatusResolved); got != "Status changed: Open -> Resolved" {
		t.Fatalf("status event message = %q", got)
	}
	nameByUserID := map[string]string{"admin-1": "ticket-admin"}
	if got := buildAdminTicketAssigneeEventMessage("", "admin-1", nameByUserID); got != "Assignee changed: Unassigned -> ticket-admin (admin-1)" {
		t.Fatalf("assignee event message = %q", got)
	}
	if got := buildAdminTicketAssigneeEventMessage("admin-1", "", nameByUserID); got != "Assignee changed: ticket-admin (admin-1) -> Unassigned" {
		t.Fatalf("assignee clear event message = %q", got)
	}
	if got := buildAdminTicketAssigneeEventMessage("admin-2", "admin-3", nil); got != "Assignee changed: admin-2 -> admin-3" {
		t.Fatalf("assignee fallback event message = %q", got)
	}
}
