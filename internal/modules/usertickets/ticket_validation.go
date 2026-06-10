package usertickets

import (
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/ticket"
	"strings"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"
)

func parseUserTicketPriority(raw string) (ticket.Priority, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return ticket.PriorityNormal, nil
	}
	switch ticket.Priority(trimmed) {
	case ticket.PriorityLow, ticket.PriorityNormal, ticket.PriorityHigh, ticket.PriorityUrgent:
		return ticket.Priority(trimmed), nil
	default:
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid priority")
	}
}

func parseUserTicketStatus(raw string) (ticket.Status, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return "", nil
	}
	switch ticket.Status(trimmed) {
	case ticket.StatusOpen, ticket.StatusPendingAdmin, ticket.StatusPendingUser, ticket.StatusResolved, ticket.StatusClosed:
		return ticket.Status(trimmed), nil
	default:
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid status")
	}
}

func normalizeUserTicketCategory(raw string) (string, error) {
	category := strings.TrimSpace(raw)
	if utf8.RuneCountInString(category) > maxUserTicketCategoryLen {
		return "", fiber.NewError(fiber.StatusBadRequest, "category must be 0-64 characters")
	}
	return category, nil
}

func normalizeUserTicketSubject(raw string) (string, error) {
	subject := strings.TrimSpace(raw)
	subjectLength := utf8.RuneCountInString(subject)
	if subjectLength == 0 || subjectLength > maxUserTicketSubjectLen {
		return "", fiber.NewError(fiber.StatusBadRequest, "subject must be 1-200 characters")
	}
	return subject, nil
}

func normalizeUserTicketMessage(raw string) (string, error) {
	message := strings.TrimSpace(raw)
	messageLength := utf8.RuneCountInString(message)
	if messageLength == 0 || messageLength > maxUserTicketMessageLen {
		return "", fiber.NewError(fiber.StatusBadRequest, "message must be 1-4000 characters")
	}
	return message, nil
}

func respondUserTicketBadRequest(c fiber.Ctx, err error, fallback string) error {
	if fiberErr, ok := err.(*fiber.Error); ok {
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
	}
	return harukiAPIHelper.ErrorBadRequest(c, fallback)
}
