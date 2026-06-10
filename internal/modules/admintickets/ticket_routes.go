package admintickets

import (
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
)

func RegisterAdminTicketRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	adminGroup := adminCoreModule.AdminRootGroup(apiHelper)
	tickets := adminGroup.Group("/tickets", adminCoreModule.RequireAdmin(apiHelper))
	tickets.Get("", handleAdminListTickets(apiHelper))
	tickets.Get("/:ticket_id", handleAdminGetTicketDetail(apiHelper))
	tickets.Post("/:ticket_id/messages", handleAdminAppendTicketMessage(apiHelper))
	tickets.Put("/:ticket_id/status", handleAdminUpdateTicketStatus(apiHelper))
	tickets.Put("/:ticket_id/assign", handleAdminAssignTicket(apiHelper))
}
