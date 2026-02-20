package user

import (
	"context"
	"fmt"
	"haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/gameaccountbinding"
	"haruki-suite/utils/database/postgresql/user"
	harukiRedis "haruki-suite/utils/database/redis"
	harukiLogger "haruki-suite/utils/logger"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/gofiber/fiber/v3"
)

func handleGenerateGameAccountVerificationCode(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID := c.Locals("userID").(string)
		serverStr := c.Params("server")
		gameUserIDStr := c.Params("game_user_id")
		// Validate server
		if _, err := utils.ParseSupportedDataUploadServer(serverStr); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid server")
		}
		// Validate game user ID
		if strings.TrimSpace(gameUserIDStr) == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "game_user_id is required")
		}
		code, err := GenerateCode(true)
		if err != nil {
			harukiLogger.Errorf("Failed to generate code: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to generate verification code")
		}
		storageKey := harukiRedis.BuildGameAccountVerifyKey(userID, serverStr, gameUserIDStr)
		if err := apiHelper.DBManager.Redis.SetCache(ctx, storageKey, code, 5*time.Minute); err != nil {
			harukiLogger.Errorf("Failed to set redis cache: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to save code")
		}
		resp := harukiAPIHelper.GenerateGameAccountCodeResponse{
			Status:          fiber.StatusOK,
			Message:         "ok",
			OneTimePassword: code,
		}
		return harukiAPIHelper.ResponseWithStruct(c, fiber.StatusOK, resp)
	}
}

func getUserBindings(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID string) ([]harukiAPIHelper.GameAccountBinding, error) {
	bindings, err := apiHelper.DBManager.DB.GameAccountBinding.
		Query().
		Where(gameaccountbinding.HasUserWith(user.IDEQ(userID))).
		All(ctx)
	if err != nil {
		return nil, err
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
	return resp, nil
}

func handleCreateGameAccountBinding(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID := c.Locals("userID").(string)
		serverStr := c.Params("server")
		gameUserIDStr := c.Params("game_user_id")
		// Validate game user ID is numeric
		if _, err := strconv.Atoi(gameUserIDStr); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "game_user_id must be numeric")
		}
		var req harukiAPIHelper.CreateGameAccountBindingPayload
		if err := c.Bind().Body(&req); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}
		existing, err := queryExistingBinding(ctx, apiHelper, serverStr, gameUserIDStr)
		if err != nil {
			harukiLogger.Errorf("Failed to query existing binding: %v", err)
			return err
		}

		if resp := checkExistingBinding(c, ctx, apiHelper, existing, userID); resp != nil {
			return resp
		}
		// Redis key uses URL params (server + game_user_id) — consistent with generate-verification-code
		code, err := getVerificationCode(ctx, apiHelper, userID, serverStr, gameUserIDStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
		}

		if err := verifyGameAccountOwnership(c, apiHelper, gameUserIDStr, serverStr, code); err != nil {
			return err
		}

		if err := saveGameAccountBinding(ctx, apiHelper, existing, serverStr, gameUserIDStr, userID, req); err != nil {
			harukiLogger.Errorf("Failed to save game account binding: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to save binding")
		}

		// Delete verification code from Redis after successful binding to prevent reuse
		storageKey := harukiRedis.BuildGameAccountVerifyKey(userID, serverStr, gameUserIDStr)
		_ = apiHelper.DBManager.Redis.DeleteCache(ctx, storageKey)

		bindings, err := getUserBindings(ctx, apiHelper, userID)
		if err != nil {
			harukiLogger.Errorf("Failed to get user bindings: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to query bindings")
		}
		ud := harukiAPIHelper.HarukiToolboxUserData{
			GameAccountBindings: &bindings,
		}
		return harukiAPIHelper.SuccessResponse(c, "verification succeeded", &ud)
	}
}

func handleUpdateGameAccountBinding(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID := c.Locals("userID").(string)
		serverStr := c.Params("server")
		gameUserIDStr := c.Params("game_user_id")

		var req harukiAPIHelper.CreateGameAccountBindingPayload
		if err := c.Bind().Body(&req); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}

		existing, err := apiHelper.DBManager.DB.GameAccountBinding.
			Query().
			Where(
				gameaccountbinding.ServerEQ(serverStr),
				gameaccountbinding.GameUserID(gameUserIDStr),
			).
			WithUser().
			Only(ctx)

		if err != nil || existing == nil {
			return harukiAPIHelper.ErrorNotFound(c, "binding not found")
		}
		if existing.Edges.User.ID != userID {
			return harukiAPIHelper.ErrorForbidden(c, "this account is bound by another user")
		}
		if !existing.Verified {
			return harukiAPIHelper.ErrorBadRequest(c, "binding is not verified yet")
		}

		_, err = existing.Update().
			SetSuite(req.Suite).
			SetMysekai(req.MySekai).
			Save(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to update game account binding: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to update binding")
		}

		bindings, err := getUserBindings(ctx, apiHelper, userID)
		if err != nil {
			harukiLogger.Errorf("Failed to get user bindings: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to query bindings")
		}
		ud := harukiAPIHelper.HarukiToolboxUserData{
			GameAccountBindings: &bindings,
		}
		return harukiAPIHelper.SuccessResponse(c, "binding updated successfully", &ud)
	}
}

func handleDeleteGameAccountBinding(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
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
			return harukiAPIHelper.ErrorNotFound(c, "binding not found")
		}

		if existing.Edges.User.ID != userID {
			return harukiAPIHelper.ErrorForbidden(c, "not authorized to delete this binding")
		}

		err = apiHelper.DBManager.DB.GameAccountBinding.DeleteOne(existing).Exec(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to delete game account binding: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to delete binding")
		}

		bindings, err := getUserBindings(ctx, apiHelper, userID)
		if err != nil {
			harukiLogger.Errorf("Failed to get user bindings: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to query bindings")
		}
		ud := harukiAPIHelper.HarukiToolboxUserData{
			GameAccountBindings: &bindings,
		}
		return harukiAPIHelper.SuccessResponse(c, "binding deleted successfully", &ud)
	}
}

func queryExistingBinding(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, serverStr, gameUserIDStr string) (*postgresql.GameAccountBinding, error) {
	existing, err := apiHelper.DBManager.DB.GameAccountBinding.
		Query().
		Where(
			gameaccountbinding.ServerEQ(serverStr),
			gameaccountbinding.GameUserID(gameUserIDStr),
		).
		WithUser().
		Only(ctx)
	if err != nil {
		if postgresql.IsNotFound(err) {
			return nil, nil
		}
		harukiLogger.Errorf("Failed to query existing binding: %v", err)
		return nil, err
	}
	return existing, nil
}

func checkExistingBinding(c fiber.Ctx, ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, existing *postgresql.GameAccountBinding, userID string) error {
	if existing == nil {
		return nil
	}
	// If this binding belongs to a different user, always block regardless of verification status.
	// Only the original owner can delete the binding to free it for others.
	if existing.Edges.User.ID != userID {
		return harukiAPIHelper.ErrorBadRequest(c, "this account is already bound by another user")
	}
	// If it belongs to the current user and is already verified, return early success.
	if existing.Verified {
		bindings, err := getUserBindings(ctx, apiHelper, userID)
		if err != nil {
			harukiLogger.Errorf("Failed to get user bindings: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to query bindings")
		}
		ud := harukiAPIHelper.HarukiToolboxUserData{
			GameAccountBindings: &bindings,
		}
		return harukiAPIHelper.SuccessResponse(c, "account already verified", &ud)
	}
	// existing belongs to current user but not yet verified → allow re-verification
	return nil
}

func getVerificationCode(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID, serverStr, gameUserIDStr string) (string, error) {
	storageKey := harukiRedis.BuildGameAccountVerifyKey(userID, serverStr, gameUserIDStr)
	var code string
	ok, err := apiHelper.DBManager.Redis.GetCache(ctx, storageKey, &code)
	if err != nil || !ok {
		return "", fmt.Errorf("verification code expired or not found")
	}
	return code, nil
}

func verifyGameAccountOwnership(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, gameUserIDStr, serverStr, expectedCode string) error {
	resultInfo, body, err := apiHelper.SekaiAPIClient.GetUserProfile(gameUserIDStr, serverStr)
	if err != nil || resultInfo == nil {
		harukiLogger.Errorf("Failed to get user profile from Sekai API: %v", err)
		return harukiAPIHelper.ErrorBadRequest(c, fmt.Sprintf("request sekai account profile failed: %v", err))
	}
	if !resultInfo.ServerAvailable {
		return harukiAPIHelper.ErrorBadRequest(c, "server unavailable or under maintenance")
	}
	if !resultInfo.AccountExists {
		return harukiAPIHelper.ErrorBadRequest(c, "game account not found")
	}
	if !resultInfo.Body || len(body) == 0 {
		return harukiAPIHelper.ErrorInternal(c, "empty user profile response")
	}

	var data map[string]any
	if err := sonic.Unmarshal(body, &data); err != nil {
		harukiLogger.Errorf("Failed to unmarshal user profile: %v", err)
		return harukiAPIHelper.ErrorInternal(c, "failed to parse profile")
	}
	userProfile, ok := data["userProfile"].(map[string]any)
	if !ok {
		return harukiAPIHelper.ErrorInternal(c, "userProfile missing")
	}
	word, ok := userProfile["word"].(string)
	if !ok {
		return harukiAPIHelper.ErrorBadRequest(c, "verification code missing in user profile")
	}
	word = strings.TrimSpace(word)
	if !strings.Contains(word, expectedCode) {
		return harukiAPIHelper.ErrorBadRequest(c, "verification code mismatch")
	}
	return nil
}

func saveGameAccountBinding(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, existing *postgresql.GameAccountBinding, serverStr, gameUserIDStr, userID string, req harukiAPIHelper.CreateGameAccountBindingPayload) error {
	var err error
	if existing != nil && existing.Edges.User.ID == userID {
		// Update existing binding that belongs to the current user
		_, err = existing.Update().
			SetVerified(true).
			SetSuite(req.Suite).
			SetMysekai(req.MySekai).
			Save(ctx)
	} else {
		// Create new binding for the current user
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
	return err
}

func registerGameAccountBindingRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	r := apiHelper.Router.Group("/api/user/:toolbox_user_id/game-account")

	r.RouteChain("/:server/:game_user_id").
		Post(apiHelper.SessionHandler.VerifySessionToken, checkUserNotBanned(apiHelper), handleGenerateGameAccountVerificationCode(apiHelper)).
		Put(apiHelper.SessionHandler.VerifySessionToken, checkUserNotBanned(apiHelper), handleCreateGameAccountBinding(apiHelper)).
		Patch(apiHelper.SessionHandler.VerifySessionToken, checkUserNotBanned(apiHelper), handleUpdateGameAccountBinding(apiHelper)).
		Delete(apiHelper.SessionHandler.VerifySessionToken, checkUserNotBanned(apiHelper), handleDeleteGameAccountBinding(apiHelper))
}
