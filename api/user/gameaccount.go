package user

import (
	"context"
	"fmt"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql/gameaccountbinding"
	"strconv"
	"time"

	"github.com/bytedance/sonic"
	"github.com/gofiber/fiber/v2"
)

func registerGameAccountBindingRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	r := apiHelper.Router.Group("/api/user/:toolbox_user_id/game-account", apiHelper.SessionHandler.VerifySessionToken)

	r.Post("/generate-verification-code", func(c *fiber.Ctx) error {
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

	r.Put("/:server/:game_user_id", func(c *fiber.Ctx) error {
		ctx := context.Background()
		userID := c.Locals("userID").(string)
		serverStr := c.Params("server")
		gameUserIDStr := c.Params("game_user_id")

		gameUserID, err := strconv.Atoi(gameUserIDStr)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid game_user_id", nil)
		}

		var req harukiAPIHelper.GameAccountBindingPayload
		if err := c.BodyParser(&req); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid request body", nil)
		}

		existing, err := apiHelper.DBManager.DB.GameAccountBinding.
			Query().
			Where(
				gameaccountbinding.ServerEQ(serverStr),
				gameaccountbinding.GameUserID(strconv.Itoa(gameUserID)),
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

		storageKey := fmt.Sprintf("%s:game-account:verify:%s:%d", userID, serverStr, gameUserID)
		var code string
		ok, err := apiHelper.DBManager.Redis.GetCache(ctx, storageKey, &code)
		if err != nil || !ok {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "verification code expired or not found", nil)
		}

		ok, result, err := apiHelper.SekaiAPIClient.GetUserProfile(gameUserIDStr, serverStr)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to get user profile", nil)
		}
		if !ok {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "failed to get user profile", nil)
		}

		var data map[string]interface{}
		if err := sonic.Unmarshal(result, &data); err != nil {
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
				SetGameUserID(strconv.Itoa(gameUserID)).
				SetVerified(true).
				SetSuite(req.Suite).
				SetMysekai(req.MySekai).
				SetUserID(userID).
				Save(ctx)
		}
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to save binding", nil)
		}

		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusOK, "binding created successfully", nil)
	})

	r.Delete("/:server/:game_user_id", func(c *fiber.Ctx) error {
		ctx := context.Background()
		userID := c.Locals("userID").(string)
		serverStr := c.Params("server")
		gameUserIDStr := c.Params("game_user_id")

		gameUserID, err := strconv.Atoi(gameUserIDStr)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid game_user_id", nil)
		}

		existing, err := apiHelper.DBManager.DB.GameAccountBinding.
			Query().
			Where(
				gameaccountbinding.ServerEQ(serverStr),
				gameaccountbinding.GameUserID(strconv.Itoa(gameUserID)),
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

		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusOK, "binding deleted successfully", nil)
	})
}
