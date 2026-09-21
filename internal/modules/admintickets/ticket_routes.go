package admintickets

import (
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	ticketsModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/tickets"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"

	"github.com/gofiber/fiber/v3"
)

func RegisterAdminTicketRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, adminGroup fiber.Router, notificationConfig ticketsModule.NotificationConfig) {
	tickets := adminGroup.Group("/tickets", adminCoreModule.RequireAdmin(apiHelper))
	tickets.Get("", handleAdminListTickets(apiHelper))
	tickets.Get("/:ticket_id", handleAdminGetTicketDetail(apiHelper))
	tickets.Post("/:ticket_id/messages", handleAdminAppendTicketMessage(apiHelper, notificationConfig))
	tickets.Put("/:ticket_id/status", handleAdminUpdateTicketStatus(apiHelper))
	tickets.Put("/:ticket_id/assign", handleAdminAssignTicket(apiHelper))
}
