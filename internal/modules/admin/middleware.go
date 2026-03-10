package admin

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	harukiAPIHelper "haruki-suite/utils/api"

	"github.com/gofiber/fiber/v3"
)

type userRoleLookup = adminCoreModule.UserRoleLookup

func requireAnyRole(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, lookup userRoleLookup, allowedRoles ...string) fiber.Handler {
	return adminCoreModule.RequireAnyRoleWithLookup(apiHelper, lookup, allowedRoles...)
}

func RequireAdmin(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return adminCoreModule.RequireAdmin(apiHelper)
}

func RequireSuperAdmin(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return adminCoreModule.RequireSuperAdmin(apiHelper)
}
