package user

import (
	"context"
	"fmt"
	"haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql/gameaccountbinding"
	"haruki-suite/utils/database/postgresql/user"
	"time"

	"github.com/bytedance/sonic"
	"github.com/gofiber/fiber/v2"
)

func registerGameAccountBindingRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	r := apiHelper.Router.Group("/api/user/:toolbox_user_id/game-account")

	r.Post("/generate-verification-code", apiHelper.SessionHandler.VerifySessionToken, func(c *fiber.Ctx) error {
		ctx := context.Background()
		userID := c.Locals("userID").(string)
		var req harukiAPIHelper.GameAccountBindingPayload
		if err := c.BodyParser(&req); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid request body", nil)
		}
		code := GenerateCode(true)
		storageKey := fmt.Sprintf("%s:game-account:verify:%s:%s", userID, string(req.Server), req.UserID)
		if err := apiHelper.DBManager.Redis.SetCache(ctx, storageKey, code, 5*time.Minute); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to save code", nil)
		}

		resp := harukiAPIHelper.GenerateGameAccountCodeResponse{
			Status:          fiber.StatusOK,
			Message:         "ok",
			OneTimePassword: code,
		}
		return harukiAPIHelper.ResponseWithStruct(c, fiber.StatusOK, resp)
	})

	r.Put("/:server/:game_user_id", apiHelper.SessionHandler.VerifySessionToken, func(c *fiber.Ctx) error {
		ctx := context.Background()
		userID := c.Locals("userID").(string)
		serverStr := c.Params("server")
		gameUserIDStr := c.Params("game_user_id")

		var req harukiAPIHelper.GameAccountBindingPayload
		if err := c.BodyParser(&req); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid request body", nil)
		}

		existing, err := apiHelper.DBManager.DB.GameAccountBinding.
			Query().
			Where(
				gameaccountbinding.ServerEQ(serverStr),
				gameaccountbinding.GameUserID(gameUserIDStr),
			).
			WithUser().
			Only(ctx)

		if err == nil && existing != nil {
			if existing.Verified {
				if existing.Edges.User.ID != userID {
					return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusForbidden, "this account is already bound by another user", nil)
				}
				_, err := existing.Update().
					SetSuite(req.Suite).
					SetMysekai(req.MySekai).
					Save(ctx)
				if err != nil {
					return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to update binding", nil)
				}
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusOK, "binding updated successfully", nil)
			}
		}

		storageKey := fmt.Sprintf("%s:game-account:verify:%s:%s", userID, serverStr, gameUserIDStr)
		var code string
		ok, err := apiHelper.DBManager.Redis.GetCache(ctx, storageKey, &code)
		if err != nil || !ok {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "verification code expired or not found", nil)
		}

		resultInfo, body, err := apiHelper.SekaiAPIClient.GetUserProfile(gameUserIDStr, serverStr)
		if resultInfo == nil && err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadGateway, fmt.Sprintf("request sekai account profile failed: %v", err), nil)
		}
		if !resultInfo.ServerAvailable {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadGateway, "server unavailable or under maintenance", nil)
		}
		if !resultInfo.AccountExists {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "game account not found", nil)
		}
		if !resultInfo.Body || len(body) == 0 {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "empty user profile response", nil)
		}
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to get user profile", nil)
		}

		var data map[string]interface{}
		if err := sonic.Unmarshal(body, &data); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to parse profile", nil)
		}

		userProfile, ok := data["userProfile"].(map[string]interface{})
		if !ok {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "userProfile missing", nil)
		}
		word, ok := userProfile["word"].(string)
		if !ok {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "verification code missing in user profile", nil)
		}
		if word != code {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "verification code mismatch", nil)
		}

		if existing != nil {
			_, err = existing.Update().
				SetVerified(true).
				SetSuite(req.Suite).
				SetMysekai(req.MySekai).
				Save(ctx)
		} else {
			_, err = apiHelper.DBManager.DB.GameAccountBinding.
				Create().
				SetServer(serverStr).
				SetGameUserID(gameUserIDStr).
				SetVerified(true).
				SetSuite(req.Suite).
				SetMysekai(req.MySekai).
				SetUserID(userID).
				Save(ctx)
		}
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to save binding", nil)
		}
		bindings, err := apiHelper.DBManager.DB.GameAccountBinding.
			Query().
			Where(gameaccountbinding.HasUserWith(user.IDEQ(userID))).
			All(ctx)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to query bindings", nil)
		}
		var resp []harukiAPIHelper.GameAccountBinding
		for _, b := range bindings {
			resp = append(resp, harukiAPIHelper.GameAccountBinding{
				Server:   utils.SupportedDataUploadServer(b.Server),
				UserID:   b.GameUserID,
				Verified: b.Verified,
				Suite:    b.Suite,
				Mysekai:  b.Mysekai,
			})
		}
		ud := harukiAPIHelper.HarukiToolboxUserData{
			GameAccountBindings: &resp,
		}
		return harukiAPIHelper.UpdatedDataResponse(c, fiber.StatusOK, "binding updated successfully", &ud)
	})

	r.Delete("/:server/:game_user_id", apiHelper.SessionHandler.VerifySessionToken, func(c *fiber.Ctx) error {
		ctx := context.Background()
		userID := c.Locals("userID").(string)
		serverStr := c.Params("server")
		gameUserIDStr := c.Params("game_user_id")

		existing, err := apiHelper.DBManager.DB.GameAccountBinding.
			Query().
			Where(
				gameaccountbinding.ServerEQ(serverStr),
				gameaccountbinding.GameUserID(gameUserIDStr),
			).
			WithUser().
			Only(ctx)

		if err != nil || existing == nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusNotFound, "binding not found", nil)
		}

		if existing.Edges.User.ID != userID {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusForbidden, "not authorized to delete this binding", nil)
		}

		err = apiHelper.DBManager.DB.GameAccountBinding.DeleteOne(existing).Exec(ctx)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to delete binding", nil)
		}

		bindings, err := apiHelper.DBManager.DB.GameAccountBinding.
			Query().
			Where(gameaccountbinding.HasUserWith(user.IDEQ(userID))).
			All(ctx)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to query bindings", nil)
		}
		var resp []harukiAPIHelper.GameAccountBinding
		for _, b := range bindings {
			resp = append(resp, harukiAPIHelper.GameAccountBinding{
				Server:   utils.SupportedDataUploadServer(b.Server),
				UserID:   b.GameUserID,
				Verified: b.Verified,
				Suite:    b.Suite,
				Mysekai:  b.Mysekai,
			})
		}
		ud := harukiAPIHelper.HarukiToolboxUserData{
			GameAccountBindings: &resp,
		}
		return harukiAPIHelper.UpdatedDataResponse(c, fiber.StatusOK, "binding deleted successfully", &ud)
	})
}
