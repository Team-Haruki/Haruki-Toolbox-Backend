package adminusers

import (
	"context"
	"fmt"
	"strings"
	"time"

	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	oauth2Module "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/oauth2"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/inheritvault"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/gameaccountbinding"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

const adminAuditActionUserDelete = "admin.user.delete"

func handleDeleteUser(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, hydraConfig *harukiOAuth2.HydraConfig) fiber.Handler {
	return func(c fiber.Ctx) error {
		target, err := resolveManageableTargetUser(c, apiHelper, adminAuditActionUserDelete)
		if err != nil {
			return adminCoreModule.RespondFiberOrInternal(c, err, "failed to resolve target user")
		}
		result, reason := harukiAPIHelper.SystemLogResultFailure, "invalid_confirmation"
		defer func() {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserDelete, adminAuditTargetTypeUser, target.ID, result, map[string]any{"reason": reason})
		}()
		var payload struct {
			ConfirmUserID string `json:"confirmUserId"`
		}
		// Old clients sent an empty DELETE for soft deletion. Never silently turn
		// that request into a permanent deletion.
		if err := c.Bind().Body(&payload); err != nil || strings.TrimSpace(payload.ConfirmUserID) != target.ID {
			return harukiAPIHelper.ErrorBadRequest(c, "confirmUserId must match the user being permanently deleted")
		}
		actorID, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}
		ctx, cancel := context.WithTimeout(c.Context(), 30*time.Second)
		defer cancel()
		bindings, err := permanentlyDeleteUser(ctx, apiHelper, hydraConfig, actorID, actorRole, target.ID)
		if err != nil {
			reason = "delete_failed"
			return adminCoreModule.RespondFiberOrInternal(c, err, "failed to delete user; retry the operation")
		}
		for _, binding := range bindings {
			clearManagedBindingPublicCaches(ctx, apiHelper, binding.Server, binding.GameUserID)
		}
		result, reason = harukiAPIHelper.SystemLogResultSuccess, "ok"
		return harukiAPIHelper.Responses.SuccessResponse(c, "user permanently deleted", &struct {
			UserID  string `json:"userId"`
			Deleted bool   `json:"deleted"`
		}{target.ID, true})
	}
}

func permanentlyDeleteUser(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, hydraConfig *harukiOAuth2.HydraConfig, actorID, actorRole, targetID string) ([]*postgresql.GameAccountBinding, error) {
	rdb := apiHelper.RedisClient()
	if rdb == nil {
		return nil, fiber.NewError(fiber.StatusServiceUnavailable, "account deletion unavailable; retry later")
	}
	lockKey := harukiRedis.InheritActiveKeyPrefix + targetID
	lockValue := harukiRedis.AccountDeletionLockPrefix + uuid.NewString()
	locked, err := rdb.SetNX(ctx, lockKey, lockValue, time.Minute).Result()
	if err != nil {
		return nil, fiber.NewError(fiber.StatusServiceUnavailable, "account deletion unavailable; retry later")
	}
	if !locked {
		return nil, fiber.NewError(fiber.StatusConflict, "account has an active transfer job or deletion; retry after it finishes")
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = rdb.Eval(cleanupCtx, `if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end return 0`, []string{lockKey}, lockValue).Err()
	}()
	tx, err := apiHelper.DBManager.DB.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	// The guarded update locks the row until commit. Role and identity changes
	// cannot race authorization or deletion of the corresponding Ory identity.
	count, err := applyManagedTargetUserUpdateGuards(tx.User.Update().SetBanned(true), actorID, actorRole, targetID).Save(ctx)
	if err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, fiber.NewError(fiber.StatusConflict, "target user changed; refresh before deleting")
	}
	target, err := tx.User.Get(ctx, targetID)
	if err != nil {
		return nil, err
	}
	bindings, err := tx.GameAccountBinding.Query().Where(gameaccountbinding.HasUserWith(userSchema.IDEQ(targetID))).All(ctx)
	if err != nil {
		return nil, err
	}
	// Foreign keys remove owned/authorized social accounts, game bindings,
	// incoming/outgoing data grants, upload codes and encrypted vault records.
	// Audit and upload history remain available to administrators.
	if err := tx.User.DeleteOneID(targetID).Exec(ctx); err != nil {
		return nil, err
	}
	if err := harukiAPIHelper.ClearUserSessionsWithContext(ctx, apiHelper.RedisClient(), targetID); err != nil {
		return nil, fiber.NewError(fiber.StatusServiceUnavailable, "session cleanup unavailable; retry deletion")
	}
	if oauth2Module.HydraOAuthManagementEnabled(hydraConfig) {
		if revokeManagedUserOAuthTokens(ctx, apiHelper, hydraConfig, targetID, target.KratosIdentityID) {
			return nil, fiber.NewError(fiber.StatusServiceUnavailable, "oauth cleanup unavailable; retry deletion")
		}
	}
	if target.KratosIdentityID != nil && strings.TrimSpace(*target.KratosIdentityID) != "" {
		if err := apiHelper.SessionHandler.DeleteKratosIdentity(ctx, *target.KratosIdentityID); err != nil {
			return nil, fiber.NewError(fiber.StatusServiceUnavailable, "identity cleanup unavailable; retry deletion")
		}
	}
	if err := inheritvault.DeleteUserFallback(ctx, rdb, targetID); err != nil {
		return nil, fiber.NewError(fiber.StatusServiceUnavailable, "vault cleanup unavailable; retry deletion")
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit user deletion: %w", err)
	}
	return bindings, nil
}
