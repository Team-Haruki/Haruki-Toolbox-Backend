package usertickets

import "testing"

func TestParseUserTicketPriority(t *testing.T) {
	v, err := parseUserTicketPriority("")
	if err != nil {
		t.Fatalf("parseUserTicketPriority returned error: %v", err)
	}
	if v != "normal" {
		t.Fatalf("priority = %q, want normal", v)
	}

	v, err = parseUserTicketPriority("high")
	if err != nil {
		t.Fatalf("parseUserTicketPriority returned error: %v", err)
	}
	if v != "high" {
		t.Fatalf("priority = %q, want high", v)
	}

	if _, err := parseUserTicketPriority("invalid"); err == nil {
		t.Fatalf("expected invalid priority to fail")
	}
}

func TestParseUserTicketStatus(t *testing.T) {
	v, err := parseUserTicketStatus("resolved")
	if err != nil {
		t.Fatalf("parseUserTicketStatus returned error: %v", err)
	}
	if v != "resolved" {
		t.Fatalf("status = %q, want resolved", v)
	}

	if _, err := parseUserTicketStatus("done"); err == nil {
		t.Fatalf("expected invalid status to fail")
	}
}

func TestGenerateTicketPublicID(t *testing.T) {
	id, err := generateTicketPublicID()
	if err != nil {
		t.Fatalf("generateTicketPublicID returned error: %v", err)
	}
	if len(id) < 10 || id[:3] != "TK-" {
		t.Fatalf("unexpected ticket id format: %q", id)
	}
}

func TestNormalizeUserTicketCategory(t *testing.T) {
	t.Run("trimmed category", func(t *testing.T) {
		got, err := normalizeUserTicketCategory("  upload  ")
		if err != nil {
			t.Fatalf("normalizeUserTicketCategory returned error: %v", err)
		}
		if got != "upload" {
			t.Fatalf("category = %q, want %q", got, "upload")
		}
	})

	t.Run("empty category allowed", func(t *testing.T) {
		got, err := normalizeUserTicketCategory("   ")
		if err != nil {
			t.Fatalf("normalizeUserTicketCategory returned error: %v", err)
		}
		if got != "" {
			t.Fatalf("category = %q, want empty string", got)
		}
	})

	t.Run("category too long", func(t *testing.T) {
		tooLong := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890too-long"
		if _, err := normalizeUserTicketCategory(tooLong); err == nil {
			t.Fatalf("expected error when category is too long")
		}
	})
}
