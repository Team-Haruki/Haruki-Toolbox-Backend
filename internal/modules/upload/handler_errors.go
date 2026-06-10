package upload

import (
	"errors"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"strconv"

	"github.com/gofiber/fiber/v3"
)

func mapUploadProcessingError(err error) *fiber.Error {
	switch {
	case errors.Is(err, errUploadOwnershipMismatch):
		return fiber.NewError(fiber.StatusForbidden, "upload is not allowed for this bound account")
	case errors.Is(err, errUploadOwnerBanned):
		return fiber.NewError(fiber.StatusForbidden, "account owner is banned")
	case errors.Is(err, errUploadCNMysekaiDenied):
		return fiber.NewError(fiber.StatusForbidden, "cn mysekai upload is not allowed")
	default:
		return nil
	}
}

func validateUploadResult(result *harukiUtils.HandleDataResult) error {
	if result.Status != nil && *result.Status != 200 {
		return errors.New("upload failed with status: " + strconv.Itoa(*result.Status))
	}
	return nil
}
