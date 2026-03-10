package usertickets

import (
	"strings"
	"testing"
)

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

	t.Run("unicode category rune limit", func(t *testing.T) {
		valid := strings.Repeat("你", 64)
		got, err := normalizeUserTicketCategory(valid)
		if err != nil {
			t.Fatalf("normalizeUserTicketCategory returned error: %v", err)
		}
		if got != valid {
			t.Fatalf("category = %q, want %q", got, valid)
		}

		invalid := strings.Repeat("你", 65)
		if _, err := normalizeUserTicketCategory(invalid); err == nil {
			t.Fatalf("expected error when unicode category exceeds rune limit")
		}
	})
}

func TestNormalizeUserTicketSubjectAndMessage(t *testing.T) {
	t.Run("subject unicode limits", func(t *testing.T) {
		valid := strings.Repeat("你", 200)
		got, err := normalizeUserTicketSubject(valid)
		if err != nil {
			t.Fatalf("normalizeUserTicketSubject returned error: %v", err)
		}
		if got != valid {
			t.Fatalf("subject = %q, want %q", got, valid)
		}

		invalid := strings.Repeat("你", 201)
		if _, err := normalizeUserTicketSubject(invalid); err == nil {
			t.Fatalf("expected subject exceeding rune limit to fail")
		}
	})

	t.Run("message unicode limits", func(t *testing.T) {
		valid := strings.Repeat("你", 4000)
		got, err := normalizeUserTicketMessage(valid)
		if err != nil {
			t.Fatalf("normalizeUserTicketMessage returned error: %v", err)
		}
		if got != valid {
			t.Fatalf("message = %q, want %q", got, valid)
		}

		invalid := strings.Repeat("你", 4001)
		if _, err := normalizeUserTicketMessage(invalid); err == nil {
			t.Fatalf("expected message exceeding rune limit to fail")
		}
	})
}
