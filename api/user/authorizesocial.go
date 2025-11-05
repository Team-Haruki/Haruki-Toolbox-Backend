package user

import (
	"fmt"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql/authorizesocialplatforminfo"
	"haruki-suite/utils/database/postgresql/socialplatforminfo"
	"strconv"

	"github.com/gofiber/fiber/v3"
)

func verifyUserHasVerifiedSocialPlatform(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		toolboxUserID := c.Params("toolbox_user_id")
		ctx := c.RequestCtx()
		client := apiHelper.DBManager.DB.SocialPlatformInfo

		info, err := client.Query().
			Where(
				socialplatforminfo.UserSocialPlatformInfoEQ(toolboxUserID),
				socialplatforminfo.Verified(true),
			).First(ctx)
		if err != nil || info == nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusForbidden, "user has no verified social platform info", nil)
		}
		return c.Next()
	}
}

func handleAuthorizeSocialPlatform(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		toolboxUserID := c.Params("toolbox_user_id")
		idParam := c.Params("id")
		userAccountID, err := strconv.Atoi(idParam)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid id parameter", nil)
		}

		var payload harukiAPIHelper.AuthorizeSocialPlatformPayload
		if err := c.Bind().Body(&payload); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid request body", nil)
		}

		ctx := c.RequestCtx()
		client := apiHelper.DBManager.DB.AuthorizeSocialPlatformInfo

		existing, err := client.Query().
			Where(
				authorizesocialplatforminfo.UserID(toolboxUserID),
				authorizesocialplatforminfo.Platform(payload.Platform),
				authorizesocialplatforminfo.PlatformUserID(payload.UserID),
			).
			Only(ctx)
		if err == nil && existing != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "this social platform account is authorized", nil)
		} else {
			_, err = client.Create().
				SetUserID(toolboxUserID).
				SetPlatformID(userAccountID).
				SetPlatform(payload.Platform).
				SetPlatformUserID(payload.UserID).
				SetComment(payload.Comment).
				Save(ctx)
			if err != nil {
				fmt.Println(err)
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to add social platform", nil)
			}
		}

		infos, err := client.Query().
			Where(authorizesocialplatforminfo.UserID(toolboxUserID)).
			All(ctx)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to fetch authorized social platforms", nil)
		}

		var resp []harukiAPIHelper.AuthorizeSocialPlatformInfo
		for _, i := range infos {
			resp = append(resp, harukiAPIHelper.AuthorizeSocialPlatformInfo{
				ID:       i.PlatformID,
				Platform: i.Platform,
				UserID:   i.PlatformUserID,
				Comment:  i.Comment,
			})
		}
		ud := harukiAPIHelper.HarukiToolboxUserData{
			AuthorizeSocialPlatformInfo: &resp,
		}
		return harukiAPIHelper.UpdatedDataResponse(c, fiber.StatusOK, "authorized social platform updated", &ud)
	}
}

func handleDeleteAuthorizeSocialPlatform(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		toolboxUserID := c.Params("toolbox_user_id")
		idParam := c.Params("id")
		authorizeSocialPlatformAccountID, err := strconv.Atoi(idParam)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid id parameter", nil)
		}

		ctx := c.RequestCtx()
		client := apiHelper.DBManager.DB.AuthorizeSocialPlatformInfo

		_, err = client.Delete().
			Where(
				authorizesocialplatforminfo.UserID(toolboxUserID),
				authorizesocialplatforminfo.PlatformIDEQ(authorizeSocialPlatformAccountID),
			).
			Exec(ctx)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to delete authorized social platform", nil)
		}

		infos, err := client.Query().
			Where(authorizesocialplatforminfo.UserID(toolboxUserID)).
			All(ctx)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to fetch authorized social platforms", nil)
		}

		var resp []harukiAPIHelper.AuthorizeSocialPlatformInfo
		for _, i := range infos {
			resp = append(resp, harukiAPIHelper.AuthorizeSocialPlatformInfo{
				ID:       i.PlatformID,
				Platform: i.Platform,
				UserID:   i.PlatformUserID,
				Comment:  i.Comment,
			})
		}
		ud := harukiAPIHelper.HarukiToolboxUserData{
			AuthorizeSocialPlatformInfo: &resp,
		}
		return harukiAPIHelper.UpdatedDataResponse(c, fiber.StatusOK, "authorized social platform updated", &ud)
	}
}

func registerAuthorizeSocialPlatformRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	r := apiHelper.Router.Group("/api/user/:toolbox_user_id/authorize-social-platform/:id")

	r.RouteChain("/").
		Put(apiHelper.SessionHandler.VerifySessionToken, verifyUserHasVerifiedSocialPlatform(apiHelper), handleAuthorizeSocialPlatform(apiHelper)).
		Delete(apiHelper.SessionHandler.VerifySessionToken, verifyUserHasVerifiedSocialPlatform(apiHelper), handleDeleteAuthorizeSocialPlatform(apiHelper))
}
