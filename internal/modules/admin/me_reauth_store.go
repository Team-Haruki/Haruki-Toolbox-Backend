package admin

import (
	"strings"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"

	"github.com/gofiber/fiber/v3"
)

func buildAdminReauthMarkerKey(userID, sessionMarker string) string {
	return adminReauthMarkerPrefix + strings.TrimSpace(userID) + ":" + strings.TrimSpace(sessionMarker)
}

func ensureCurrentAdminSessionReauthenticated(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID string) error {
	if apiHelper == nil || apiHelper.DBManager == nil || apiHelper.DBManager.Redis == nil || apiHelper.DBManager.Redis.Redis == nil {
		return fiber.NewError(fiber.StatusInternalServerError, "session store unavailable")
	}

	sessionMarker, err := resolveCurrentAdminSessionMarker(c, apiHelper)
	if err != nil {
		return err
	}
	reauthKey := buildAdminReauthMarkerKey(userID, sessionMarker)
	exists, redisErr := apiHelper.DBManager.Redis.Redis.Exists(c.Context(), reauthKey).Result()
	if redisErr != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "session store unavailable")
	}
	if exists == 0 {
		return fiber.NewError(fiber.StatusForbidden, "reauthentication required")
	}
	return nil
}

func markCurrentAdminSessionReauthenticated(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID string) error {
	if apiHelper == nil || apiHelper.DBManager == nil || apiHelper.DBManager.Redis == nil || apiHelper.DBManager.Redis.Redis == nil {
		return fiber.NewError(fiber.StatusInternalServerError, "session store unavailable")
	}

	sessionMarker, err := resolveCurrentAdminSessionMarker(c, apiHelper)
	if err != nil {
		return err
	}
	reauthKey := buildAdminReauthMarkerKey(userID, sessionMarker)
	if err := apiHelper.DBManager.Redis.Redis.Set(c.Context(), reauthKey, adminNowUTC().Format(time.RFC3339Nano), adminReauthTTL).Err(); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "session store unavailable")
	}
	return nil
}
