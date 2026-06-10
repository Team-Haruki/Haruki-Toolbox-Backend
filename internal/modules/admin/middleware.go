package admin

import (
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"

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
