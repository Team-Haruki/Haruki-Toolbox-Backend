package usertickets

import (
	userCoreModule "haruki-suite/internal/modules/usercore"
	harukiAPIHelper "haruki-suite/utils/api"
)

func RegisterUserTicketRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	r := apiHelper.Router.Group("/api/user/:toolbox_user_id/tickets")
	r.Get("/", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(apiHelper), handleListOwnTickets(apiHelper))
	r.Post("/", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(apiHelper), handleCreateOwnTicket(apiHelper))
	r.Get("/:ticket_id", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(apiHelper), handleGetOwnTicketDetail(apiHelper))
	r.Post("/:ticket_id/messages", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(apiHelper), handleAppendOwnTicketMessage(apiHelper))
	r.Post("/:ticket_id/close", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(apiHelper), handleCloseOwnTicket(apiHelper))
}
