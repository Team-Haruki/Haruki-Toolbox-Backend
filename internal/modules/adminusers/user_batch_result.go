package adminusers

import "github.com/gofiber/fiber/v3"

func batchUserOperationFailureMessage(action string) string {
	switch action {
	case adminBatchActionBan:
		return "failed to ban user"
	case adminBatchActionUnban:
		return "failed to unban user"
	case adminBatchActionForceLogout:
		return "failed to force logout user"
	default:
		return "operation failed"
	}
}

func mapBatchManagedUpdateMiss(err error) (string, string) {
	fiberErr, ok := err.(*fiber.Error)
	if !ok {
		return adminBatchResultCodeOperationFailed, "operation failed"
	}

	switch fiberErr.Code {
	case fiber.StatusNotFound:
		return adminBatchResultCodeUserNotFound, "user not found"
	case fiber.StatusForbidden, fiber.StatusBadRequest:
		return adminFailureReasonPermissionDenied, "insufficient permissions"
	default:
		return adminBatchResultCodeOperationFailed, "operation failed"
	}
}
