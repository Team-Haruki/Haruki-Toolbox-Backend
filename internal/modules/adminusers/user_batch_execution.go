package adminusers

import (
	"context"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func executeBatchBan(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, actorUserID, actorRole, targetUserID string, reason *string) (int, error) {
	update := applyManagedTargetUserUpdateGuards(
		apiHelper.DBManager.DB.User.Update().SetBanned(true),
		actorUserID,
		actorRole,
		targetUserID,
	)
	if reason != nil {
		update.SetBanReason(*reason)
	} else {
		update.ClearBanReason()
	}
	return update.Save(ctx)
}

func executeBatchUnban(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, actorUserID, actorRole, targetUserID string) (int, error) {
	return applyManagedTargetUserUpdateGuards(
		apiHelper.DBManager.DB.User.Update().
			SetBanned(false).
			ClearBanReason(),
		actorUserID,
		actorRole,
		targetUserID,
	).Save(ctx)
}

func executeBatchForceLogout(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, targetUserID string, kratosIdentityID *string) error {
	kratosRevokeFailed := false
	if apiHelper != nil && apiHelper.SessionHandler != nil && apiHelper.SessionHandler.UsesKratosProvider() &&
		kratosIdentityID != nil && strings.TrimSpace(*kratosIdentityID) != "" {
		if err := apiHelper.SessionHandler.RevokeKratosSessionsByIdentityID(ctx, strings.TrimSpace(*kratosIdentityID)); err != nil {
			kratosRevokeFailed = true
		}
	}
	if err := harukiAPIHelper.ClearUserSessions(apiHelper.RedisClient(), targetUserID); err != nil {
		return err
	}
	if kratosRevokeFailed {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to revoke kratos sessions")
	}
	return nil
}

type batchUserInfo struct {
	ID               string
	Role             string
	KratosIdentityID *string
}

func batchFetchUsers(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userIDs []string) (map[string]*batchUserInfo, error) {
	users, err := apiHelper.DBManager.DB.User.Query().
		Where(user.IDIn(userIDs...)).
		Select(user.FieldID, user.FieldRole, user.FieldKratosIdentityID).
		All(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[string]*batchUserInfo, len(users))
	for _, u := range users {
		result[u.ID] = &batchUserInfo{
			ID:               u.ID,
			Role:             string(u.Role),
			KratosIdentityID: u.KratosIdentityID,
		}
	}
	return result, nil
}
