package api

import (
	Utils "haruki-suite/utils"
	"net/http"

	"github.com/gofiber/fiber/v2"
)

func JSONResponse(c *fiber.Ctx, resp Utils.APIResponse) error {
	status := http.StatusOK
	if resp.Status != nil {
		status = *resp.Status
	}
	return c.Status(status).JSON(resp)
}

func IntPtr(v int) *int {
	return &v
}
