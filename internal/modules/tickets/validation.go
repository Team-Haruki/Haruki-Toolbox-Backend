// Package tickets contains the transport-independent ticket workflows shared
// by the user and administrator HTTP modules.
package tickets

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/ticket"
)

const (
	MaxCategoryRunes = 64
	MaxSubjectRunes  = 200
	MaxMessageRunes  = 4000
)

var (
	ErrInvalidPriority = errors.New("invalid priority")
	ErrInvalidStatus   = errors.New("invalid status")
	ErrTicketClosed    = errors.New("ticket is closed")
	ErrCategoryLength  = errors.New("category must be 0-64 characters")
	ErrSubjectLength   = errors.New("subject must be 1-200 characters")
	ErrMessageLength   = errors.New("message must be 1-4000 characters")
)

func ParsePriority(raw string, emptyValue ticket.Priority) (ticket.Priority, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return emptyValue, nil
	}
	switch ticket.Priority(trimmed) {
	case ticket.PriorityLow, ticket.PriorityNormal, ticket.PriorityHigh, ticket.PriorityUrgent:
		return ticket.Priority(trimmed), nil
	default:
		return "", ErrInvalidPriority
	}
}

func ParseStatus(raw string) (ticket.Status, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return "", nil
	}
	switch ticket.Status(trimmed) {
	case ticket.StatusOpen, ticket.StatusPendingAdmin, ticket.StatusPendingUser, ticket.StatusResolved, ticket.StatusClosed:
		return ticket.Status(trimmed), nil
	default:
		return "", ErrInvalidStatus
	}
}

func NormalizeCategory(raw string) (string, error) {
	category := strings.TrimSpace(raw)
	if utf8.RuneCountInString(category) > MaxCategoryRunes {
		return "", ErrCategoryLength
	}
	return category, nil
}

func NormalizeSubject(raw string) (string, error) {
	subject := strings.TrimSpace(raw)
	length := utf8.RuneCountInString(subject)
	if length == 0 || length > MaxSubjectRunes {
		return "", ErrSubjectLength
	}
	return subject, nil
}

func NormalizeMessage(raw string) (string, error) {
	message := strings.TrimSpace(raw)
	length := utf8.RuneCountInString(message)
	if length == 0 || length > MaxMessageRunes {
		return "", ErrMessageLength
	}
	return message, nil
}
