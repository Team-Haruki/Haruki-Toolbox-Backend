package usergamebindings

import (
	"context"
	userCoreModule "haruki-suite/internal/modules/usercore"
	userEmailModule "haruki-suite/internal/modules/useremail"
	"haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/gameaccountbinding"
	userSchema "haruki-suite/utils/database/postgresql/user"
	harukiRedis "haruki-suite/utils/database/redis"
	harukiLogger "haruki-suite/utils/logger"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func handleGenerateGameAccountVerificationCode(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		serverStr := c.Params("server")
		gameUserIDStr := c.Params("game_user_id")

		if _, err := utils.ParseSupportedDataUploadServer(serverStr); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid server")
		}

		if strings.TrimSpace(gameUserIDStr) == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "game_user_id is required")
		}
		code, err := userEmailModule.GenerateCode(true)
		if err != nil {
			harukiLogger.Errorf("Failed to generate code: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to generate verification code")
		}
		storageKey := harukiRedis.BuildGameAccountVerifyKey(userID, serverStr, gameUserIDStr)
		attemptKey := harukiRedis.BuildGameAccountVerifyAttemptKey(userID, serverStr, gameUserIDStr)
		if err := apiHelper.DBManager.Redis.SetCachesAtomically(ctx, []harukiRedis.CacheItem{
			{Key: storageKey, Value: code},
			{Key: attemptKey, Value: 0},
		}, gameAccountVerificationTTL); err != nil {
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

func clearGameAccountPublicCaches(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, serverStr, gameUserIDStr string) {
	if apiHelper == nil || apiHelper.DBManager == nil || apiHelper.DBManager.Redis == nil {
		return
	}
	gameUserID, err := strconv.ParseInt(strings.TrimSpace(gameUserIDStr), 10, 64)
	if err != nil {
		harukiLogger.Warnf("Failed to parse game user id for cache clear: server=%s gameUserID=%s err=%v", serverStr, gameUserIDStr, err)
		return
	}
	if err := apiHelper.DBManager.Redis.ClearPublicGameDataCaches(ctx, serverStr, gameUserID); err != nil {
		harukiLogger.Warnf("Failed to clear public game data caches: server=%s gameUserID=%s err=%v", serverStr, gameUserIDStr, err)
	}
}

func getUserBindings(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID string) ([]harukiAPIHelper.GameAccountBinding, error) {
	bindings, err := apiHelper.DBManager.DB.GameAccountBinding.
		Query().
		Where(gameaccountbinding.HasUserWith(userSchema.IDEQ(userID))).
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
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		serverStr := c.Params("server")
		gameUserIDStr := c.Params("game_user_id")
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		defer func() {
			userCoreModule.WriteUserAuditLog(c, apiHelper, "user.game_account_binding.create", result, userID, map[string]any{
				"reason":     reason,
				"server":     serverStr,
				"gameUserID": gameUserIDStr,
			})
		}()
		harukiLogger.Infof("[GameAccountBinding] START: userID=%s, server=%s, gameUserID=%s", userID, serverStr, gameUserIDStr)

		if !isNumericGameUserID(gameUserIDStr) {
			reason = "invalid_game_user_id"
			return harukiAPIHelper.ErrorBadRequest(c, "game_user_id must be numeric")
		}
		var req harukiAPIHelper.CreateGameAccountBindingPayload
		if err := c.Bind().Body(&req); err != nil {
			reason = "invalid_payload"
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}
		existing, err := queryExistingBinding(ctx, apiHelper, serverStr, gameUserIDStr)
		if err != nil {
			harukiLogger.Errorf("Failed to query existing binding: %v", err)
			reason = "query_existing_binding_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to query existing binding")
		}
		harukiLogger.Infof("[GameAccountBinding] existing binding: %v", existing != nil)

		switch classifyExistingBinding(existing, userID) {
		case existingBindingStateOwnedByOther:
			reason = "binding_owned_by_other_user"
			return harukiAPIHelper.ErrorBadRequest(c, "this account is already bound by another user")
		case existingBindingStateVerifiedBySelf:
			bindings, err := getUserBindings(ctx, apiHelper, userID)
			if err != nil {
				harukiLogger.Errorf("Failed to get user bindings: %v", err)
				reason = "query_bindings_failed"
				return harukiAPIHelper.ErrorInternal(c, "failed to query bindings")
			}
			ud := harukiAPIHelper.HarukiToolboxUserData{
				GameAccountBindings: &bindings,
			}
			result = harukiAPIHelper.SystemLogResultSuccess
			reason = "already_verified"
			harukiLogger.Infof("[GameAccountBinding] existing verified binding found, short-circuiting")
			return harukiAPIHelper.SuccessResponse(c, "account already verified", &ud)
		}
		harukiLogger.Infof("[GameAccountBinding] existing binding check passed, proceeding to verification code check")

		code, err := getVerificationCode(ctx, apiHelper, userID, serverStr, gameUserIDStr)
		if err != nil {
			reason = "verification_code_missing"
			mapped := mapGameAccountVerificationCodeLookupError(err)
			if mapped.Code >= fiber.StatusInternalServerError {
				harukiLogger.Errorf("[GameAccountBinding] verification code lookup failed: %v", err)
			} else {
				harukiLogger.Infof("[GameAccountBinding] verification code lookup rejected: %v", err)
			}
			return harukiAPIHelper.UpdatedDataResponse[string](c, mapped.Code, mapped.Message, nil)
		}
		harukiLogger.Infof("[GameAccountBinding] verification code found, proceeding to Sekai API verification")

		if err := verifyGameAccountOwnership(apiHelper, gameUserIDStr, serverStr, code); err != nil {
			if shouldIncrementGameAccountVerificationAttempt(err) {
				if attemptErr := incrementGameAccountVerificationAttempt(ctx, apiHelper, userID, serverStr, gameUserIDStr); attemptErr != nil {
					harukiLogger.Errorf("Failed to increment game account verification attempt: %v", attemptErr)
					reason = "verification_attempt_update_failed"
					return harukiAPIHelper.ErrorInternal(c, "verification service unavailable")
				}
			}
			reason = "verify_ownership_failed"
			mapped := mapGameAccountOwnershipVerificationError(err)
			if mapped.Code >= fiber.StatusInternalServerError {
				harukiLogger.Errorf("[GameAccountBinding] verifyGameAccountOwnership failed: %v", err)
			} else {
				harukiLogger.Infof("[GameAccountBinding] verifyGameAccountOwnership rejected: %v", err)
			}
			return harukiAPIHelper.UpdatedDataResponse[string](c, mapped.Code, mapped.Message, nil)
		}
		harukiLogger.Infof("[GameAccountBinding] verifyGameAccountOwnership PASSED, saving binding")

		if err := consumeGameAccountVerificationCode(ctx, apiHelper, userID, serverStr, gameUserIDStr, code); err != nil {
			mapped := mapGameAccountVerificationCodeLookupError(err)
			if mapped.Code >= fiber.StatusInternalServerError {
				harukiLogger.Errorf("Failed to consume game account verification code: %v", err)
				reason = "verification_code_consume_failed"
			} else {
				reason = "verification_code_expired"
			}
			return harukiAPIHelper.UpdatedDataResponse[string](c, mapped.Code, mapped.Message, nil)
		}

		if err := saveGameAccountBinding(ctx, apiHelper, existing, serverStr, gameUserIDStr, userID, req); err != nil {
			harukiLogger.Errorf("Failed to save game account binding: %v", err)
			reason = "save_binding_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to save binding")
		}
		clearGameAccountPublicCaches(ctx, apiHelper, serverStr, gameUserIDStr)

		bindings, err := getUserBindings(ctx, apiHelper, userID)
		if err != nil {
			harukiLogger.Errorf("Failed to get user bindings: %v", err)
			reason = "query_bindings_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to query bindings")
		}
		ud := harukiAPIHelper.HarukiToolboxUserData{
			GameAccountBindings: &bindings,
		}
		result = harukiAPIHelper.SystemLogResultSuccess
		reason = "ok"
		return harukiAPIHelper.SuccessResponse(c, "verification succeeded", &ud)
	}
}

func handleUpdateGameAccountBinding(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		serverStr := c.Params("server")
		gameUserIDStr := c.Params("game_user_id")
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		defer func() {
			userCoreModule.WriteUserAuditLog(c, apiHelper, "user.game_account_binding.update", result, userID, map[string]any{
				"reason":     reason,
				"server":     serverStr,
				"gameUserID": gameUserIDStr,
			})
		}()

		var req harukiAPIHelper.CreateGameAccountBindingPayload
		if err := c.Bind().Body(&req); err != nil {
			reason = "invalid_payload"
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

		if err != nil {
			if postgresql.IsNotFound(err) {
				reason = "binding_not_found"
				return harukiAPIHelper.ErrorNotFound(c, "binding not found")
			}
			harukiLogger.Errorf("Failed to query game account binding: %v", err)
			reason = "query_binding_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to query binding")
		}
		if existing == nil {
			reason = "binding_not_found"
			return harukiAPIHelper.ErrorNotFound(c, "binding not found")
		}
		if bindingOwnerMissing(existing) {
			reason = "binding_owner_missing"
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "binding owner missing", nil)
		}
		if !isBindingOwnedByUser(existing, userID) {
			reason = "binding_owned_by_other_user"
			return harukiAPIHelper.ErrorForbidden(c, "this account is bound by another user")
		}
		if !existing.Verified {
			reason = "binding_not_verified"
			return harukiAPIHelper.ErrorBadRequest(c, "binding is not verified yet")
		}

		_, err = existing.Update().
			SetSuite(req.Suite).
			SetMysekai(req.MySekai).
			Save(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to update game account binding: %v", err)
			reason = "update_binding_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to update binding")
		}
		clearGameAccountPublicCaches(ctx, apiHelper, serverStr, gameUserIDStr)

		bindings, err := getUserBindings(ctx, apiHelper, userID)
		if err != nil {
			harukiLogger.Errorf("Failed to get user bindings: %v", err)
			reason = "query_bindings_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to query bindings")
		}
		ud := harukiAPIHelper.HarukiToolboxUserData{
			GameAccountBindings: &bindings,
		}
		result = harukiAPIHelper.SystemLogResultSuccess
		reason = "ok"
		return harukiAPIHelper.SuccessResponse(c, "binding updated successfully", &ud)
	}
}

func handleDeleteGameAccountBinding(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		serverStr := c.Params("server")
		gameUserIDStr := c.Params("game_user_id")
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		defer func() {
			userCoreModule.WriteUserAuditLog(c, apiHelper, "user.game_account_binding.delete", result, userID, map[string]any{
				"reason":     reason,
				"server":     serverStr,
				"gameUserID": gameUserIDStr,
			})
		}()

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
				reason = "binding_not_found"
				return harukiAPIHelper.ErrorNotFound(c, "binding not found")
			}
			harukiLogger.Errorf("Failed to query game account binding: %v", err)
			reason = "query_binding_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to query binding")
		}
		if existing == nil {
			reason = "binding_not_found"
			return harukiAPIHelper.ErrorNotFound(c, "binding not found")
		}

		if bindingOwnerMissing(existing) {
			reason = "binding_owner_missing"
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "binding owner missing", nil)
		}

		if !isBindingOwnedByUser(existing, userID) {
			reason = "binding_owned_by_other_user"
			return harukiAPIHelper.ErrorForbidden(c, "not authorized to delete this binding")
		}

		err = apiHelper.DBManager.DB.GameAccountBinding.DeleteOne(existing).Exec(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to delete game account binding: %v", err)
			reason = "delete_binding_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to delete binding")
		}
		clearGameAccountPublicCaches(ctx, apiHelper, serverStr, gameUserIDStr)

		bindings, err := getUserBindings(ctx, apiHelper, userID)
		if err != nil {
			harukiLogger.Errorf("Failed to get user bindings: %v", err)
			reason = "query_bindings_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to query bindings")
		}
		ud := harukiAPIHelper.HarukiToolboxUserData{
			GameAccountBindings: &bindings,
		}
		result = harukiAPIHelper.SystemLogResultSuccess
		reason = "ok"
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

type existingBindingState uint8

const (
	existingBindingStateNone existingBindingState = iota
	existingBindingStateOwnedByOther
	existingBindingStateVerifiedBySelf
)

func classifyExistingBinding(existing *postgresql.GameAccountBinding, userID string) existingBindingState {
	if existing == nil {
		return existingBindingStateNone
	}
	if ownerID := bindingOwnerID(existing); ownerID != "" && ownerID != userID {
		return existingBindingStateOwnedByOther
	}
	if isBindingOwnedByUser(existing, userID) && existing.Verified {
		return existingBindingStateVerifiedBySelf
	}
	return existingBindingStateNone
}

func saveGameAccountBinding(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, existing *postgresql.GameAccountBinding, serverStr, gameUserIDStr, userID string, req harukiAPIHelper.CreateGameAccountBindingPayload) error {
	var err error
	if existing != nil {
		update := existing.Update().
			SetVerified(true).
			SetSuite(req.Suite).
			SetMysekai(req.MySekai)
		if bindingOwnerMissing(existing) {
			update.SetUserID(userID)
		}
		_, err = update.Save(ctx)
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
	return err
}

func bindingOwnerID(binding *postgresql.GameAccountBinding) string {
	if binding == nil || binding.Edges.User == nil {
		return ""
	}
	return strings.TrimSpace(binding.Edges.User.ID)
}

func bindingOwnerMissing(binding *postgresql.GameAccountBinding) bool {
	return bindingOwnerID(binding) == ""
}

func isBindingOwnedByUser(binding *postgresql.GameAccountBinding, userID string) bool {
	ownerID := bindingOwnerID(binding)
	if ownerID == "" {
		return false
	}
	return ownerID == strings.TrimSpace(userID)
}

func RegisterUserGameAccountBindingRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	r := apiHelper.Router.Group("/api/user/:toolbox_user_id/game-account")

	verifySession := apiHelper.SessionHandler.VerifySessionToken
	requireSelf := userCoreModule.RequireSelfUserParam("toolbox_user_id")
	checkNotBanned := userCoreModule.CheckUserNotBanned(apiHelper)

	r.Get(
		"/:server/:game_user_id/recommend-data",
		verifySession,
		requireSelf,
		checkNotBanned,
		handleGetDeckRecommendData(apiHelper),
	)
	r.Get(
		"/:server/:game_user_id/:data_type",
		verifySession,
		requireSelf,
		checkNotBanned,
		handleGetOwnedGameAccountData(apiHelper),
	)

	r.RouteChain("/:server/:game_user_id").
		Post(
			verifySession,
			requireSelf,
			checkNotBanned,
			handleGenerateGameAccountVerificationCode(apiHelper),
		).
		Put(
			verifySession,
			requireSelf,
			checkNotBanned,
			handleCreateGameAccountBinding(apiHelper),
		).
		Patch(
			verifySession,
			requireSelf,
			checkNotBanned,
			handleUpdateGameAccountBinding(apiHelper),
		).
		Delete(
			verifySession,
			requireSelf,
			checkNotBanned,
			handleDeleteGameAccountBinding(apiHelper),
		)
}

func isNumericGameUserID(gameUserID string) bool {
	_, err := strconv.ParseInt(gameUserID, 10, 64)
	return err == nil
}
