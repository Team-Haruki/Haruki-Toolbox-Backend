package usergamebindings

import (
	"context"
	"errors"
	"fmt"
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
	"time"

	"github.com/bytedance/sonic"
	"github.com/gofiber/fiber/v3"
)

const (
	gameAccountVerificationTTL         = 5 * time.Minute
	gameAccountVerificationMaxAttempts = 5
)

var (
	errGameAccountVerificationCodeMissing     = errors.New("verification code missing in user profile")
	errGameAccountVerificationCodeMismatch    = errors.New("verification code mismatch")
	errGameAccountVerificationCodeExpired     = errors.New("verification code expired or not found")
	errGameAccountVerificationTooManyAttempts = errors.New("too many verification attempts, please generate a new code")
	errGameAccountVerificationServiceUnstable = errors.New("verification service unavailable")
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

		if resp := checkExistingBinding(c, ctx, apiHelper, existing, userID); resp != nil {
			reason = "already_verified_or_bound"
			result = harukiAPIHelper.SystemLogResultSuccess
			harukiLogger.Infof("[GameAccountBinding] checkExistingBinding returned non-nil, short-circuiting")
			return resp
		}
		harukiLogger.Infof("[GameAccountBinding] checkExistingBinding passed, proceeding to verification code check")

		code, err := getVerificationCode(ctx, apiHelper, userID, serverStr, gameUserIDStr)
		if err != nil {
			reason = "verification_code_missing"
			if errors.Is(err, errGameAccountVerificationServiceUnstable) {
				harukiLogger.Errorf("[GameAccountBinding] verification storage unavailable: %v", err)
				return harukiAPIHelper.ErrorInternal(c, "verification service unavailable")
			}
			harukiLogger.Infof("[GameAccountBinding] verification code not found: %v", err)
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
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
			harukiLogger.Infof("[GameAccountBinding] verifyGameAccountOwnership FAILED: %v", err)
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
		}
		harukiLogger.Infof("[GameAccountBinding] verifyGameAccountOwnership PASSED, saving binding")

		if err := consumeGameAccountVerificationCode(ctx, apiHelper, userID, serverStr, gameUserIDStr, code); err != nil {
			if errors.Is(err, errGameAccountVerificationServiceUnstable) {
				harukiLogger.Errorf("Failed to consume game account verification code: %v", err)
				reason = "verification_code_consume_failed"
				return harukiAPIHelper.ErrorInternal(c, "verification service unavailable")
			}
			reason = "verification_code_expired"
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
		}

		if err := saveGameAccountBinding(ctx, apiHelper, existing, serverStr, gameUserIDStr, userID, req); err != nil {
			harukiLogger.Errorf("Failed to save game account binding: %v", err)
			reason = "save_binding_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to save binding")
		}

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

func checkExistingBinding(c fiber.Ctx, ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, existing *postgresql.GameAccountBinding, userID string) error {
	if existing == nil {
		return nil
	}

	if ownerID := bindingOwnerID(existing); ownerID != "" && ownerID != userID {
		return harukiAPIHelper.ErrorBadRequest(c, "this account is already bound by another user")
	}

	if isBindingOwnedByUser(existing, userID) && existing.Verified {
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

	return nil
}

func getVerificationCode(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID, serverStr, gameUserIDStr string) (string, error) {
	attemptKey := harukiRedis.BuildGameAccountVerifyAttemptKey(userID, serverStr, gameUserIDStr)
	var attemptCount int
	found, err := apiHelper.DBManager.Redis.GetCache(ctx, attemptKey, &attemptCount)
	if err != nil {
		return "", errGameAccountVerificationServiceUnstable
	}
	if found && attemptCount >= gameAccountVerificationMaxAttempts {
		return "", errGameAccountVerificationTooManyAttempts
	}

	storageKey := harukiRedis.BuildGameAccountVerifyKey(userID, serverStr, gameUserIDStr)
	var code string
	ok, err := apiHelper.DBManager.Redis.GetCache(ctx, storageKey, &code)
	if err != nil {
		return "", errGameAccountVerificationServiceUnstable
	}
	if !ok {
		return "", errGameAccountVerificationCodeExpired
	}
	return code, nil
}

func incrementGameAccountVerificationAttempt(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID, serverStr, gameUserIDStr string) error {
	attemptKey := harukiRedis.BuildGameAccountVerifyAttemptKey(userID, serverStr, gameUserIDStr)
	_, err := apiHelper.DBManager.Redis.IncrementWithTTL(ctx, attemptKey, gameAccountVerificationTTL)
	return err
}

func consumeGameAccountVerificationCode(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID, serverStr, gameUserIDStr, expectedCode string) error {
	storageKey := harukiRedis.BuildGameAccountVerifyKey(userID, serverStr, gameUserIDStr)
	consumed, err := apiHelper.DBManager.Redis.DeleteCacheIfValueMatches(ctx, storageKey, expectedCode)
	if err != nil {
		return errGameAccountVerificationServiceUnstable
	}
	if !consumed {
		return errGameAccountVerificationCodeExpired
	}

	attemptKey := harukiRedis.BuildGameAccountVerifyAttemptKey(userID, serverStr, gameUserIDStr)
	if err := apiHelper.DBManager.Redis.DeleteCache(ctx, attemptKey); err != nil {
		harukiLogger.Warnf("Failed to clear game account verification attempt key: %v", err)
	}
	return nil
}

func shouldIncrementGameAccountVerificationAttempt(err error) bool {
	return errors.Is(err, errGameAccountVerificationCodeMissing) || errors.Is(err, errGameAccountVerificationCodeMismatch)
}

func verifyGameAccountOwnership(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, gameUserIDStr, serverStr, expectedCode string) error {
	resultInfo, body, err := apiHelper.SekaiAPIClient.GetUserProfile(gameUserIDStr, serverStr)
	if err != nil || resultInfo == nil {
		return fmt.Errorf("request sekai account profile failed: %v", err)
	}
	if !resultInfo.ServerAvailable {
		return fmt.Errorf("server unavailable or under maintenance")
	}
	if !resultInfo.AccountExists {
		return fmt.Errorf("game account not found")
	}
	if !resultInfo.Body || len(body) == 0 {
		return fmt.Errorf("empty user profile response")
	}

	var data map[string]any
	if err := sonic.Unmarshal(body, &data); err != nil {
		return fmt.Errorf("failed to parse profile: %v", err)
	}

	if _, hasError := data["errorCode"]; hasError {
		errMsg, _ := data["errorMessage"].(string)
		return fmt.Errorf("game account not found: %s", errMsg)
	}
	userProfile, ok := data["userProfile"].(map[string]any)
	if !ok {
		return fmt.Errorf("userProfile missing in response")
	}
	word, ok := userProfile["word"].(string)
	if !ok {
		return errGameAccountVerificationCodeMissing
	}
	word = strings.TrimSpace(word)
	if !strings.Contains(word, expectedCode) {
		return errGameAccountVerificationCodeMismatch
	}
	return nil
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

	r.RouteChain("/:server/:game_user_id").
		Post(apiHelper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(apiHelper), handleGenerateGameAccountVerificationCode(apiHelper)).
		Put(apiHelper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(apiHelper), handleCreateGameAccountBinding(apiHelper)).
		Patch(apiHelper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(apiHelper), handleUpdateGameAccountBinding(apiHelper)).
		Delete(apiHelper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(apiHelper), handleDeleteGameAccountBinding(apiHelper))
}

func isNumericGameUserID(gameUserID string) bool {
	_, err := strconv.Atoi(gameUserID)
	return err == nil
}
