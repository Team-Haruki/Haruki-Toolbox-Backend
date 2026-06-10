package userauthorizesocial

import (
	userCoreModule "haruki-suite/internal/modules/usercore"
	harukiAPIHelper "haruki-suite/utils/api"

	"github.com/gofiber/fiber/v3"
)

func RegisterUserAuthorizeSocialRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	base := apiHelper.Router.Group("/api/user/:toolbox_user_id/authorize-social-platform")
	verifiedSelf := func(handlers ...fiber.Handler) (any, []any) {
		routeHandler, routeRest := userCoreModule.RouteHandlerParts(userCoreModule.RequireAuthenticatedVerifiedSelf(apiHelper, "toolbox_user_id"), handlers...)
		return routeHandler, routeRest
	}
	authenticatedSelf := func(handlers ...fiber.Handler) (any, []any) {
		routeHandler, routeRest := userCoreModule.RouteHandlerParts(userCoreModule.RequireAuthenticatedSelf(apiHelper, "toolbox_user_id"), handlers...)
		return routeHandler, routeRest
	}

	createHandler, createRest := verifiedSelf(verifyUserHasVerifiedSocialPlatform(apiHelper), handleCreateAuthorizeSocialPlatform(apiHelper))
	base.Post("/", createHandler, createRest...)

	r := base.Group("/:id")
	createAtIDHandler, createAtIDRest := verifiedSelf(verifyUserHasVerifiedSocialPlatform(apiHelper), handleCreateAuthorizeSocialPlatformAtID(apiHelper))
	updateHandler, updateRest := verifiedSelf(verifyUserHasVerifiedSocialPlatform(apiHelper), handleUpdateAuthorizeSocialPlatform(apiHelper))
	deleteHandler, deleteRest := authenticatedSelf(verifyUserHasVerifiedSocialPlatform(apiHelper), handleDeleteAuthorizeSocialPlatform(apiHelper))
	r.RouteChain("/").
		Post(createAtIDHandler, createAtIDRest...).
		Put(updateHandler, updateRest...).
		Delete(deleteHandler, deleteRest...)
}
