package usertickets

import (
	ticketsModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/tickets"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/ticket"

	"github.com/gofiber/fiber/v3"
)

func parseUserTicketPriority(raw string) (ticket.Priority, error) {
	value, err := ticketsModule.ParsePriority(raw, ticket.PriorityNormal)
	if err != nil {
		return "", fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	return value, nil
}

func parseUserTicketStatus(raw string) (ticket.Status, error) {
	value, err := ticketsModule.ParseStatus(raw)
	if err != nil {
		return "", fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	return value, nil
}

func normalizeUserTicketCategory(raw string) (string, error) {
	value, err := ticketsModule.NormalizeCategory(raw)
	if err != nil {
		return "", fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	return value, nil
}

func normalizeUserTicketSubject(raw string) (string, error) {
	value, err := ticketsModule.NormalizeSubject(raw)
	if err != nil {
		return "", fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	return value, nil
}

func normalizeUserTicketMessage(raw string) (string, error) {
	value, err := ticketsModule.NormalizeMessage(raw)
	if err != nil {
		return "", fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	return value, nil
}

func respondUserTicketBadRequest(c fiber.Ctx, err error, fallback string) error {
	if fiberErr, ok := err.(*fiber.Error); ok {
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
	}
	return harukiAPIHelper.ErrorBadRequest(c, fallback)
}
