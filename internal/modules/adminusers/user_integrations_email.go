package adminusers

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"time"

	"github.com/gofiber/fiber/v3"
)

const (
	adminLocalMirrorRetryAttempts = 3
	adminLocalMirrorRetryInterval = 150 * time.Millisecond
)

func resolveAdminUserEmailUpdateFinalizeOutcome(localMirrorFailed, sessionClearFailed bool) (status int, message string, auditResult string) {
	if localMirrorFailed && sessionClearFailed {
		return fiber.StatusInternalServerError, "user email updated in identity provider, but local mirror sync failed and some sessions were not cleared", harukiAPIHelper.SystemLogResultFailure
	}
	if localMirrorFailed {
		return fiber.StatusInternalServerError, "user email updated in identity provider, but local mirror sync failed", harukiAPIHelper.SystemLogResultFailure
	}
	if sessionClearFailed {
		return fiber.StatusOK, "user email updated, but failed to clear user sessions", harukiAPIHelper.SystemLogResultSuccess
	}
	return fiber.StatusOK, "user email updated", harukiAPIHelper.SystemLogResultSuccess
}
