package usertickets

import (
	ticketsModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/tickets"
	userCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
)

func RegisterUserTicketRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, notificationConfig ticketsModule.NotificationConfig) {
	r := apiHelper.Router.Group("/api/user/:toolbox_user_id/tickets", userCoreModule.RouteHandlers(userCoreModule.RequireAuthenticatedSelf(apiHelper, "toolbox_user_id"))...)
	r.Get("/", handleListOwnTickets(apiHelper))
	r.Post("/", handleCreateOwnTicket(apiHelper, notificationConfig))
	r.Get("/:ticket_id", handleGetOwnTicketDetail(apiHelper))
	r.Post("/:ticket_id/messages", handleAppendOwnTicketMessage(apiHelper, notificationConfig))
	r.Post("/:ticket_id/close", handleCloseOwnTicket(apiHelper))
}
