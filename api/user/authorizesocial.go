package user

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql/authorizesocialplatforminfo"
	"strconv"

	"github.com/gofiber/fiber/v2"
)

func registerAuthorizeSocialPlatformRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	r := apiHelper.Router.Group("/api/user/:toolbox_user_id/authorize-social-platform/:id", apiHelper.SessionHandler.VerifySessionToken)

	r.Put("/", func(c *fiber.Ctx) error {
		toolboxUserID := c.Params("toolbox_user_id")
		idParam := c.Params("id")
		userAccountID, err := strconv.Atoi(idParam)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid id parameter", nil)
		}

		var payload harukiAPIHelper.AuthorizeSocialPlatformPayload
		if err := c.BodyParser(&payload); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid request body", nil)
		}

		ctx := c.Context()
		client := apiHelper.DBManager.DB.AuthorizeSocialPlatformInfo

		existing, err := client.Query().
			Where(
				authorizesocialplatforminfo.UserID(toolboxUserID),
				authorizesocialplatforminfo.IDEQ(userAccountID),
			).
			Only(ctx)
		if err == nil && existing != nil {
			_, err = client.Update().
				Where(
					authorizesocialplatforminfo.UserID(toolboxUserID),
					authorizesocialplatforminfo.IDEQ(userAccountID),
				).
				SetPlatform(payload.Platform).
				SetPlatformUserID(payload.UserID).
				SetComment(payload.Comment).
				Save(ctx)
			if err != nil {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to update social platform", nil)
			}
		} else {
			_, err = client.Create().
				SetUserID(toolboxUserID).
				SetID(userAccountID).
				SetPlatform(payload.Platform).
				SetPlatformUserID(payload.UserID).
				SetComment(payload.Comment).
				Save(ctx)
			if err != nil {
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
				ID:       i.ID,
				Platform: i.Platform,
				UserID:   i.PlatformUserID,
				Comment:  i.Comment,
			})
		}
		ud := harukiAPIHelper.HarukiToolboxUserData{
			AuthorizeSocialPlatformInfo: resp,
		}
		return harukiAPIHelper.UpdatedDataResponse(c, fiber.StatusOK, "authorized social platform updated", &ud)
	})

	r.Delete("/", func(c *fiber.Ctx) error {
		toolboxUserID := c.Params("toolbox_user_id")
		idParam := c.Params("id")
		authorizeSocialPlatformAccountID, err := strconv.Atoi(idParam)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid id parameter", nil)
		}

		ctx := c.Context()
		client := apiHelper.DBManager.DB.AuthorizeSocialPlatformInfo

		_, err = client.Delete().
			Where(
				authorizesocialplatforminfo.UserID(toolboxUserID),
				authorizesocialplatforminfo.IDEQ(authorizeSocialPlatformAccountID),
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
				ID:       i.ID,
				Platform: i.Platform,
				UserID:   i.PlatformUserID,
				Comment:  i.Comment,
			})
		}
		ud := harukiAPIHelper.HarukiToolboxUserData{
			AuthorizeSocialPlatformInfo: resp,
		}
		return harukiAPIHelper.UpdatedDataResponse(c, fiber.StatusOK, "authorized social platform updated", &ud)
	})
}
