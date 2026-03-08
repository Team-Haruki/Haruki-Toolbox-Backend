package admin

import (
	"context"
	harukiAPIHelper "haruki-suite/utils/api"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"strings"

	"github.com/gofiber/fiber/v3"
)

const maxBatchUserOperationCount = 200

type batchUserOperationPayload struct {
	UserIDs []string `json:"userIds"`
	Reason  *string  `json:"reason,omitempty"`
}

type batchUserOperationItemResult struct {
	UserID  string `json:"userId"`
	Success bool   `json:"success"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type batchUserOperationResponse struct {
	Action  string                         `json:"action"`
	Total   int                            `json:"total"`
	Success int                            `json:"success"`
	Failed  int                            `json:"failed"`
	Results []batchUserOperationItemResult `json:"results"`
}

func sanitizeBatchUserIDs(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, fiber.NewError(fiber.StatusBadRequest, "userIds is required")
	}

	seen := make(map[string]struct{}, len(raw))
	result := make([]string, 0, len(raw))
	for _, id := range raw {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}

	if len(result) == 0 {
		return nil, fiber.NewError(fiber.StatusBadRequest, "userIds is required")
	}
	if len(result) > maxBatchUserOperationCount {
		return nil, fiber.NewError(fiber.StatusBadRequest, "too many userIds in one batch")
	}
	return result, nil
}

func executeBatchBan(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, targetUserID string, reason *string) error {
	update := apiHelper.DBManager.DB.User.UpdateOneID(targetUserID).SetBanned(true)
	if reason != nil {
		update.SetBanReason(*reason)
	} else {
		update.ClearBanReason()
	}
	_, err := update.Save(ctx)
	return err
}

func executeBatchUnban(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, targetUserID string) error {
	_, err := apiHelper.DBManager.DB.User.UpdateOneID(targetUserID).
		SetBanned(false).
		ClearBanReason().
		Save(ctx)
	return err
}

func executeBatchForceLogout(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, targetUserID string) error {
	return harukiAPIHelper.ClearUserSessions(apiHelper.DBManager.Redis.Redis, targetUserID)
}

func handleBatchUserOperation(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, action string) fiber.Handler {
	return func(c fiber.Ctx) error {
		actorUserID, actorRole, err := currentAdminActor(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorUnauthorized(c, "missing user session")
		}

		var payload batchUserOperationPayload
		if err := c.Bind().Body(&payload); err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.batch."+action, "user", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_request_payload", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		userIDs, err := sanitizeBatchUserIDs(payload.UserIDs)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.batch."+action, "user", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_user_ids", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid userIds")
		}

		var reason *string
		if action == "ban" {
			reason, err = sanitizeBanReason(payload.Reason)
			if err != nil {
				writeAdminAuditLog(c, apiHelper, "admin.user.batch."+action, "user", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_reason", nil))
				if fiberErr, ok := err.(*fiber.Error); ok {
					return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
				}
				return harukiAPIHelper.ErrorBadRequest(c, "invalid reason")
			}
		}

		results := make([]batchUserOperationItemResult, 0, len(userIDs))
		successCount := 0
		for _, targetUserID := range userIDs {
			item := batchUserOperationItemResult{UserID: targetUserID}

			targetUser, err := apiHelper.DBManager.DB.User.Query().
				Where(userSchema.IDEQ(targetUserID)).
				Select(userSchema.FieldID, userSchema.FieldRole).
				Only(c.Context())
			if err != nil {
				item.Code = "user_not_found"
				item.Message = "user not found"
				results = append(results, item)
				continue
			}

			if err := ensureAdminCanManageTargetUser(actorUserID, actorRole, targetUser.ID, string(targetUser.Role)); err != nil {
				item.Code = "permission_denied"
				item.Message = "insufficient permissions"
				results = append(results, item)
				continue
			}

			switch action {
			case "ban":
				err = executeBatchBan(c.Context(), apiHelper, targetUser.ID, reason)
			case "unban":
				err = executeBatchUnban(c.Context(), apiHelper, targetUser.ID)
			case "force_logout":
				err = executeBatchForceLogout(apiHelper, targetUser.ID)
			default:
				err = fiber.NewError(fiber.StatusBadRequest, "unsupported batch action")
			}
			if err != nil {
				item.Code = "operation_failed"
				item.Message = err.Error()
				results = append(results, item)
				continue
			}

			item.Success = true
			successCount++
			results = append(results, item)
		}

		resp := batchUserOperationResponse{
			Action:  action,
			Total:   len(userIDs),
			Success: successCount,
			Failed:  len(userIDs) - successCount,
			Results: results,
		}
		resultState := harukiAPIHelper.SystemLogResultSuccess
		if resp.Failed > 0 {
			resultState = harukiAPIHelper.SystemLogResultFailure
		}
		writeAdminAuditLog(c, apiHelper, "admin.user.batch."+action, "user", "", resultState, map[string]any{
			"total":   resp.Total,
			"success": resp.Success,
			"failed":  resp.Failed,
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleBatchBanUsers(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return handleBatchUserOperation(apiHelper, "ban")
}

func handleBatchUnbanUsers(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return handleBatchUserOperation(apiHelper, "unban")
}

func handleBatchForceLogoutUsers(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return handleBatchUserOperation(apiHelper, "force_logout")
}
