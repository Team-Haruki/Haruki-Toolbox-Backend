package usertickets

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/ticket"

	"github.com/gofiber/fiber/v3"
)

func queryOwnTicketByPublicID(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID, publicTicketID string) (*postgresql.Ticket, error) {
	return apiHelper.DBManager.DB.Ticket.Query().
		Where(
			ticket.TicketIDEQ(publicTicketID),
			ticket.CreatorUserIDEQ(userID),
		).
		Only(c.Context())
}
