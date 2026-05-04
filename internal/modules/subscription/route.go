package subscription

import (
	"strings"

	userPrivateAPI "haruki-suite/internal/modules/userprivateapi"
	apiHelper "haruki-suite/utils/api"
	dataHandler "haruki-suite/utils/handler"

	"github.com/gofiber/fiber/v3"
)

type eventLookupRequest struct {
	SubscriptionID      string `json:"subscription_id"`
	SubscriptionVersion string `json:"subscription_version"`
}

func RegisterSubscriptionRoutes(apiHelper *apiHelper.HarukiToolboxRouterHelpers) {
	if apiHelper == nil {
		return
	}
	internal := apiHelper.Router.Group("/internal", userPrivateAPI.ValidateUserPermission(apiHelper))
	internal.Put("/mysekai-birthday-monitors/:subscription_id", handleUpsertBirthdayMonitor(apiHelper))
	internal.Delete("/mysekai-birthday-monitors/:subscription_id", handleDeleteBirthdayMonitor(apiHelper))
	internal.Get("/mysekai-birthday-events/:event_id", handleGetBirthdayEvent(apiHelper))
	internal.Post("/mysekai-birthday-events/:event_id/ack", handleAckBirthdayEvent(apiHelper))
}

func handleUpsertBirthdayMonitor(apiHelper *apiHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		var req dataHandler.BirthdayMonitorMirror
		if err := c.Bind().Body(&req); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
		}
		req.SubscriptionID = strings.TrimSpace(c.Params("subscription_id"))
		if err := dataHandler.UpsertBirthdayMonitorMirror(c.Context(), apiHelper.DBManager.Redis, req); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		return c.Status(fiber.StatusOK).JSON(fiber.Map{"status": "ok"})
	}
}

func handleDeleteBirthdayMonitor(apiHelper *apiHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		version := strings.TrimSpace(c.Query("subscription_version"))
		if err := dataHandler.DeleteBirthdayMonitorMirror(c.Context(), apiHelper.DBManager.Redis, c.Params("subscription_id"), version); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		return c.Status(fiber.StatusOK).JSON(fiber.Map{"status": "ok"})
	}
}

func handleGetBirthdayEvent(apiHelper *apiHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		event, err := dataHandler.FetchBirthdayMonitorEvent(
			c.Context(),
			apiHelper.DBManager.Redis,
			c.Params("event_id"),
			c.Query("subscription_id"),
			c.Query("subscription_version"),
		)
		if err != nil {
			return fiber.NewError(fiber.StatusNotFound, err.Error())
		}
		return c.Status(fiber.StatusOK).JSON(event)
	}
}

func handleAckBirthdayEvent(apiHelper *apiHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		req := eventLookupRequest{
			SubscriptionID:      c.Query("subscription_id"),
			SubscriptionVersion: c.Query("subscription_version"),
		}
		if strings.TrimSpace(req.SubscriptionID) == "" || strings.TrimSpace(req.SubscriptionVersion) == "" {
			_ = c.Bind().Body(&req)
		}
		if err := dataHandler.AckBirthdayMonitorEvent(c.Context(), apiHelper.DBManager.Redis, c.Params("event_id"), req.SubscriptionID, req.SubscriptionVersion); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		return c.Status(fiber.StatusOK).JSON(fiber.Map{"status": "ok"})
	}
}
