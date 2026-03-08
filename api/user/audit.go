package user

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func writeUserAuditLog(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, action string, result string, targetUserID string, metadata map[string]any) {
	if strings.TrimSpace(targetUserID) == "" {
		if localUserID, ok := c.Locals("userID").(string); ok {
			targetUserID = strings.TrimSpace(localUserID)
		}
	}

	targetType := "user"
	var targetIDPtr *string
	if targetUserID != "" {
		targetID := targetUserID
		targetIDPtr = &targetID
	}

	entry := harukiAPIHelper.BuildSystemLogEntryFromFiber(c, action, result, &targetType, targetIDPtr, metadata)
	if targetUserID != "" && (entry.ActorUserID == nil || strings.TrimSpace(*entry.ActorUserID) == "") {
		entry.ActorUserID = &targetUserID
		role := "user"
		if localRole, ok := c.Locals("userRole").(string); ok {
			trimmedRole := strings.ToLower(strings.TrimSpace(localRole))
			if trimmedRole != "" {
				role = trimmedRole
			}
		}
		entry.ActorRole = &role
		if role == "admin" || role == "super_admin" {
			entry.ActorType = harukiAPIHelper.SystemLogActorTypeAdmin
		} else {
			entry.ActorType = harukiAPIHelper.SystemLogActorTypeUser
		}
	}

	_ = harukiAPIHelper.WriteSystemLog(c.Context(), apiHelper, entry)
}
