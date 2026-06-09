package usertickets

import (
	userCoreModule "haruki-suite/internal/modules/usercore"
	harukiAPIHelper "haruki-suite/utils/api"
)

func RegisterUserTicketRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	r := apiHelper.Router.Group("/api/user/:toolbox_user_id/tickets", userCoreModule.RouteHandlers(userCoreModule.RequireAuthenticatedSelf(apiHelper, "toolbox_user_id"))...)
	r.Get("/", handleListOwnTickets(apiHelper))
	r.Post("/", handleCreateOwnTicket(apiHelper))
	r.Get("/:ticket_id", handleGetOwnTicketDetail(apiHelper))
	r.Post("/:ticket_id/messages", handleAppendOwnTicketMessage(apiHelper))
	r.Post("/:ticket_id/close", handleCloseOwnTicket(apiHelper))
}
