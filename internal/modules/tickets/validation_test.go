package tickets

import (
	"errors"
	"strings"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/ticket"
)

func TestSharedTicketValidation(t *testing.T) {
	priority, err := ParsePriority("", ticket.PriorityNormal)
	if err != nil || priority != ticket.PriorityNormal {
		t.Fatalf("default priority = %q, %v", priority, err)
	}
	if _, err := ParsePriority("invalid", ""); !errors.Is(err, ErrInvalidPriority) {
		t.Fatalf("invalid priority error = %v", err)
	}

	status, err := ParseStatus(" Pending_User ")
	if err != nil || status != ticket.StatusPendingUser {
		t.Fatalf("status = %q, %v", status, err)
	}
	if _, err := ParseStatus("done"); !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("invalid status error = %v", err)
	}

	message, err := NormalizeMessage("  hello  ")
	if err != nil || message != "hello" {
		t.Fatalf("message = %q, %v", message, err)
	}
	if _, err := NormalizeMessage(strings.Repeat("你", MaxMessageRunes+1)); !errors.Is(err, ErrMessageLength) {
		t.Fatalf("long message error = %v", err)
	}
}
