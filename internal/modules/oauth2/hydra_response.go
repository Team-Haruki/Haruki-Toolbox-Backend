package oauth2

import (
	"errors"
	"strings"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
)

func respondHydraError(c fiber.Ctx, err error, fallback string) error {
	var reqErr *hydraRequestError
	if errors.As(err, &reqErr) {
		status := reqErr.Status
		if status < 400 || status >= 600 {
			status = fiber.StatusBadGateway
		}
		message := strings.TrimSpace(reqErr.Message)
		if message == "" {
			message = fallback
		}
		return harukiAPIHelper.UpdatedDataResponse[string](c, status, message, nil)
	}

	var fiberErr *fiber.Error
	if errors.As(err, &fiberErr) {
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
	}

	harukiLogger.Errorf("Hydra request failed: %v", err)
	return harukiAPIHelper.ErrorInternal(c, fallback)
}
