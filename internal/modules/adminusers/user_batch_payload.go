package adminusers

import (
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func parseBatchUserRoleUpdatePayload(c fiber.Ctx) (*batchUserRoleUpdatePayload, error) {
	var payload batchUserRoleUpdatePayload
	if err := c.Bind().Body(&payload); err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}

	userIDs, err := sanitizeBatchUserIDs(payload.UserIDs)
	if err != nil {
		return nil, err
	}

	roleRaw := strings.TrimSpace(payload.Role)
	if roleRaw == "" {
		return nil, fiber.NewError(fiber.StatusBadRequest, "role is required")
	}
	normalizedRole := adminCoreModule.NormalizeRole(roleRaw)
	if !adminCoreModule.IsValidRole(normalizedRole) {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid role")
	}

	return &batchUserRoleUpdatePayload{
		UserIDs: userIDs,
		Role:    normalizedRole,
	}, nil
}

func parseBatchUserAllowCNMysekaiUpdatePayload(c fiber.Ctx) (*batchUserAllowCNMysekaiUpdatePayload, error) {
	var payload batchUserAllowCNMysekaiUpdatePayload
	if err := c.Bind().Body(&payload); err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}

	userIDs, err := sanitizeBatchUserIDs(payload.UserIDs)
	if err != nil {
		return nil, err
	}

	allow := payload.AllowCNMysekai
	if allow == nil {
		allow = payload.AllowCNMysekaiSnake
	}
	if allow == nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "allowCNMysekai is required")
	}

	return &batchUserAllowCNMysekaiUpdatePayload{
		UserIDs:        userIDs,
		AllowCNMysekai: allow,
	}, nil
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
