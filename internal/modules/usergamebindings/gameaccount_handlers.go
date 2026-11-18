package usergamebindings

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"strconv"

	"strings"
	"time"

	userCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	platformMailNotify "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/mailnotify"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/gameaccountbinding"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

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

		server, err := utils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid server")
		}

		numericID, err := strconv.ParseInt(gameUserIDStr, 10, 64)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "game_user_id must be numeric")
		}
		serverStr, gameUserIDStr = string(server), strconv.FormatInt(numericID, 10)
		count, err := apiHelper.DBManager.Redis.IncrementWithTTL(ctx, harukiRedis.BuildGameAccountVerificationIssueKey(userID), gameAccountVerificationTTL)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to check issuance limit")
		}
		if count > 10 {
			return c.SendStatus(fiber.StatusTooManyRequests)
		}
		count, err = apiHelper.DBManager.Redis.IncrementWithTTL(ctx, harukiRedis.BuildGameAccountVerificationTargetIssueKey(serverStr, gameUserIDStr), gameAccountVerificationTTL)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to check issuance limit")
		}
		if count > gameAccountVerificationTargetLimit {
			return c.SendStatus(fiber.StatusTooManyRequests)
		}
		code := newGameAccountVerificationCode()
		storageKey := harukiRedis.BuildGameAccountVerifyKey(userID, serverStr, gameUserIDStr)
		if err := apiHelper.DBManager.Redis.SetCache(ctx, storageKey, code, gameAccountVerificationTTL); err != nil {
			harukiLogger.Errorf("Failed to set redis cache: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to save code")
		}
		resp := harukiAPIHelper.GenerateGameAccountCodeResponse{
			Status:          fiber.StatusOK,
			Message:         "ok",
			OneTimePassword: code,
		}
		return harukiAPIHelper.Responses.ResponseWithStruct(c, fiber.StatusOK, resp)
	}
}

func newGameAccountVerificationCode() string {
	// A short, unpadded ASCII code fits the in-game profile comment field.
	var raw [12]byte
	rand.Read(raw[:])
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:])
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
		previousOwnerUserID := ""
		defer func() {
			userCoreModule.WriteUserAuditLog(c, apiHelper, "user.game_account_binding.create", result, userID, map[string]any{
				"reason":              reason,
				"server":              serverStr,
				"gameUserID":          gameUserIDStr,
				"previousOwnerUserID": previousOwnerUserID,
			})
		}()
		harukiLogger.Infof("[GameAccountBinding] START: userID=%s, server=%s, gameUserID=%s", userID, serverStr, gameUserIDStr)

		numericID, err := strconv.ParseInt(gameUserIDStr, 10, 64)
		if err != nil {
			reason = "invalid_game_user_id"
			return harukiAPIHelper.ErrorBadRequest(c, "game_user_id must be numeric")
		}
		server, err := utils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			reason = "invalid_server"
			return harukiAPIHelper.ErrorBadRequest(c, "invalid server")
		}
		serverStr, gameUserIDStr = string(server), strconv.FormatInt(numericID, 10)
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
			return harukiAPIHelper.Responses.SuccessResponse(c, "account already verified", &ud)
		case existingBindingStateOwnedByOther:
			if bindingOwnerBanned(existing) {
				reason = "binding_owner_banned"
				return harukiAPIHelper.ErrorForbidden(c, "this account is bound by a banned user")
			}
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
			return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, mapped.Code, mapped.Message, nil)
		}
		harukiLogger.Infof("[GameAccountBinding] verification code found, proceeding to Sekai API verification")

		if err := verifyGameAccountOwnership(ctx, apiHelper, gameUserIDStr, serverStr, code); err != nil {
			reason = "verify_ownership_failed"
			mapped := mapGameAccountOwnershipVerificationError(err)
			if mapped.Code >= fiber.StatusInternalServerError {
				harukiLogger.Errorf("[GameAccountBinding] verifyGameAccountOwnership failed: %v", err)
			} else {
				harukiLogger.Infof("[GameAccountBinding] verifyGameAccountOwnership rejected: %v", err)
			}
			return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, mapped.Code, mapped.Message, nil)
		}
		harukiLogger.Infof("[GameAccountBinding] verifyGameAccountOwnership PASSED, saving binding")
		release, err := apiHelper.DBManager.DB.LockGameAccountMutation(ctx, serverStr, gameUserIDStr)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to reserve binding")
		}
		defer release()
		existing, err = queryExistingBinding(ctx, apiHelper, serverStr, gameUserIDStr)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to query existing binding")
		}

		var notification *platformMailNotify.Reservation
		if classifyExistingBinding(existing, userID) == existingBindingStateOwnedByOther && apiHelper.SMTPClient != nil {
			notification = platformMailNotify.Reserve()
			if notification == nil {
				reason = "notification_queue_full"
				return c.SendStatus(fiber.StatusServiceUnavailable)
			}
			defer notification.Cancel()
		}

		if err := consumeGameAccountVerificationCode(ctx, apiHelper, userID, serverStr, gameUserIDStr, code); err != nil {
			mapped := mapGameAccountVerificationCodeLookupError(err)
			if mapped.Code >= fiber.StatusInternalServerError {
				harukiLogger.Errorf("Failed to consume game account verification code: %v", err)
				reason = "verification_code_consume_failed"
			} else {
				reason = "verification_code_expired"
			}
			return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, mapped.Code, mapped.Message, nil)
		}

		saveResult, err := saveGameAccountBinding(ctx, apiHelper, existing, serverStr, gameUserIDStr, userID, req)
		if err != nil {
			if errors.Is(err, errGameAccountBindingOwnerBanned) {
				reason = "binding_owner_banned"
				return harukiAPIHelper.ErrorForbidden(c, "this account is bound by a banned user")
			}
			harukiLogger.Errorf("Failed to save game account binding: %v", err)
			reason = "save_binding_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to save binding")
		}
		clearGameAccountPublicCaches(ctx, apiHelper, serverStr, gameUserIDStr)
		if saveResult != nil && saveResult.Transferred {
			previousOwnerUserID = saveResult.PreviousOwnerUserID
		}
		if notification != nil && previousOwnerUserID != "" {
			// Send the transfer mail off the request path: its result was always
			// discarded (log-only), so a slow SMTP server should not delay the
			// binding response. Clone the params — fiber recycles their backing
			// buffers after the handler returns.
			transferServer := strings.Clone(serverStr)
			transferGameUserID := strings.Clone(gameUserIDStr)
			transferredAt := time.Now()
			notification.Send(func(context.Context) {
				notifyPreviousGameAccountBindingOwner(apiHelper, saveResult, transferServer, transferGameUserID, transferredAt)
			})
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
		if saveResult != nil && saveResult.Transferred {
			reason = "transferred"
		} else {
			reason = "ok"
		}
		return harukiAPIHelper.Responses.SuccessResponse(c, "verification succeeded", &ud)
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
		server, serverErr := utils.ParseSupportedDataUploadServer(serverStr)
		gameID, idErr := strconv.ParseInt(gameUserIDStr, 10, 64)
		if serverErr != nil || idErr != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid game account")
		}
		serverStr, gameUserIDStr = string(server), strconv.FormatInt(gameID, 10)
		release, err := apiHelper.DBManager.DB.LockGameAccountMutation(ctx, serverStr, gameUserIDStr)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to reserve binding")
		}
		defer release()

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
			return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, fiber.StatusConflict, "binding owner missing", nil)
		}
		if !isBindingOwnedByUser(existing, userID) {
			reason = "binding_owned_by_other_user"
			return harukiAPIHelper.ErrorForbidden(c, "this account is bound by another user")
		}
		if !existing.Verified {
			reason = "binding_not_verified"
			return harukiAPIHelper.ErrorBadRequest(c, "binding is not verified yet")
		}

		if req.IsDefault != nil && *req.IsDefault {
			// Making this binding the default clears the flag from every other
			// binding of the user inside one transaction.
			tx, txErr := apiHelper.DBManager.DB.Tx(ctx)
			if txErr != nil {
				harukiLogger.Errorf("Failed to open transaction for binding update: %v", txErr)
				reason = "update_binding_failed"
				return harukiAPIHelper.ErrorInternal(c, "failed to update binding")
			}
			if _, txErr = tx.GameAccountBinding.Update().
				Where(
					gameaccountbinding.HasUserWith(userSchema.IDEQ(userID)),
					gameaccountbinding.IsDefaultEQ(true),
					gameaccountbinding.IDNEQ(existing.ID),
				).
				SetIsDefault(false).
				Save(ctx); txErr != nil {
				_ = tx.Rollback()
				harukiLogger.Errorf("Failed to clear previous default binding: %v", txErr)
				reason = "update_binding_failed"
				return harukiAPIHelper.ErrorInternal(c, "failed to update binding")
			}
			if _, txErr = tx.GameAccountBinding.UpdateOneID(existing.ID).
				Where(gameaccountbinding.HasUserWith(userSchema.IDEQ(userID)), gameaccountbinding.VerifiedEQ(true)).
				SetSuite(req.Suite).
				SetMysekai(req.MySekai).
				SetIsDefault(true).
				Save(ctx); txErr != nil {
				_ = tx.Rollback()
				if postgresql.IsNotFound(txErr) {
					return harukiAPIHelper.ErrorNotFound(c, "binding not found")
				}
				harukiLogger.Errorf("Failed to update game account binding: %v", txErr)
				reason = "update_binding_failed"
				return harukiAPIHelper.ErrorInternal(c, "failed to update binding")
			}
			if txErr = tx.Commit(); txErr != nil {
				_ = tx.Rollback()
				harukiLogger.Errorf("Failed to commit binding update: %v", txErr)
				reason = "update_binding_failed"
				return harukiAPIHelper.ErrorInternal(c, "failed to update binding")
			}
		} else {
			update := existing.Update().Where(gameaccountbinding.HasUserWith(userSchema.IDEQ(userID)), gameaccountbinding.VerifiedEQ(true)).
				SetSuite(req.Suite).
				SetMysekai(req.MySekai)
			if req.IsDefault != nil {
				update = update.SetIsDefault(false)
			}
			if _, err = update.Save(ctx); err != nil {
				if postgresql.IsNotFound(err) {
					return harukiAPIHelper.ErrorNotFound(c, "binding not found")
				}
				harukiLogger.Errorf("Failed to update game account binding: %v", err)
				reason = "update_binding_failed"
				return harukiAPIHelper.ErrorInternal(c, "failed to update binding")
			}
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
		return harukiAPIHelper.Responses.SuccessResponse(c, "binding updated successfully", &ud)
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

		server, serverErr := utils.ParseSupportedDataUploadServer(serverStr)
		gameID, idErr := strconv.ParseInt(gameUserIDStr, 10, 64)
		if serverErr != nil || idErr != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid game account")
		}
		serverStr, gameUserIDStr = string(server), strconv.FormatInt(gameID, 10)
		release, err := apiHelper.DBManager.DB.LockGameAccountMutation(ctx, serverStr, gameUserIDStr)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to reserve binding")
		}
		defer release()
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
			return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, fiber.StatusConflict, "binding owner missing", nil)
		}

		if !isBindingOwnedByUser(existing, userID) {
			reason = "binding_owned_by_other_user"
			return harukiAPIHelper.ErrorForbidden(c, "not authorized to delete this binding")
		}

		err = apiHelper.DBManager.DB.ChangeGameAccountBinding(ctx, serverStr, gameUserIDStr, true, func(db *postgresql.Client) error {
			return db.GameAccountBinding.DeleteOne(existing).Where(gameaccountbinding.HasUserWith(userSchema.IDEQ(userID))).Exec(ctx)
		})
		if err != nil {
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorNotFound(c, "binding not found")
			}
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
		return harukiAPIHelper.Responses.SuccessResponse(c, "binding deleted successfully", &ud)
	}
}
