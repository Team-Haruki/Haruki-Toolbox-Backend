package admincore

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func NormalizeRole(role string) string {
	return normalizeRole(role)
}

func IsValidRole(role string) bool {
	return isValidRole(role)
}

func CurrentAdminActor(c fiber.Ctx) (string, string, error) {
	userID, ok := c.Locals("userID").(string)
	if !ok || strings.TrimSpace(userID) == "" {
		return "", "", fiber.NewError(fiber.StatusUnauthorized, "missing user session")
	}

	role, ok := c.Locals("userRole").(string)
	normalizedRole := NormalizeRole(role)
	if !ok || !IsValidRole(normalizedRole) {
		return "", "", fiber.NewError(fiber.StatusUnauthorized, "missing user role")
	}

	return userID, normalizedRole, nil
}

func EnsureAdminCanManageTargetUser(actorUserID, actorRole, targetUserID, targetRole string) error {
	if actorUserID == targetUserID {
		return fiber.NewError(fiber.StatusBadRequest, "cannot manage current user")
	}

	if NormalizeRole(actorRole) != RoleSuperAdmin && NormalizeRole(targetRole) == RoleSuperAdmin {
		return fiber.NewError(fiber.StatusForbidden, "only super admin can manage super admin users")
	}
	return nil
}

func ParseOptionalBoolField(raw, fieldName string) (*bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}

	v, err := strconv.ParseBool(trimmed)
	if err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid "+fieldName+" filter")
	}
	return &v, nil
}

func WriteAdminAuditLog(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, action string, targetType string, targetID string, result string, metadata map[string]any) {
	writeAdminAuditLog(c, apiHelper, action, targetType, targetID, result, metadata)
}

func AdminFailureMetadata(reason string, extra map[string]any) map[string]any {
	return adminFailureMetadata(reason, extra)
}

func RespondFiberOrBadRequest(c fiber.Ctx, err error, fallbackMessage string) error {
	if fiberErr, ok := err.(*fiber.Error); ok {
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
	}
	return harukiAPIHelper.ErrorBadRequest(c, fallbackMessage)
}

func RespondFiberOrUnauthorized(c fiber.Ctx, err error, fallbackMessage string) error {
	if fiberErr, ok := err.(*fiber.Error); ok {
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
	}
	return harukiAPIHelper.ErrorUnauthorized(c, fallbackMessage)
}

func RespondFiberOrInternal(c fiber.Ctx, err error, fallbackMessage string) error {
	if fiberErr, ok := err.(*fiber.Error); ok {
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
	}
	return harukiAPIHelper.ErrorInternal(c, fallbackMessage)
}

func RespondFiberOrForbidden(c fiber.Ctx, err error, fallbackMessage string) error {
	if fiberErr, ok := err.(*fiber.Error); ok {
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
	}
	return harukiAPIHelper.ErrorForbidden(c, fallbackMessage)
}
