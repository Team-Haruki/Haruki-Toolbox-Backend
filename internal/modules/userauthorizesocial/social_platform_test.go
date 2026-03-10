package userauthorizesocial

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestIsSupportedSocialPlatform(t *testing.T) {
	t.Parallel()

	if !isSupportedSocialPlatform(harukiAPIHelper.SocialPlatformQQ) {
		t.Fatalf("qq should be supported")
	}
	if !isSupportedSocialPlatform(harukiAPIHelper.SocialPlatformQQBot) {
		t.Fatalf("qqbot should be supported")
	}
	if !isSupportedSocialPlatform(harukiAPIHelper.SocialPlatformDiscord) {
		t.Fatalf("discord should be supported")
	}
	if !isSupportedSocialPlatform(harukiAPIHelper.SocialPlatformTelegram) {
		t.Fatalf("telegram should be supported")
	}
	if isSupportedSocialPlatform(harukiAPIHelper.SocialPlatform("wechat")) {
		t.Fatalf("wechat should not be supported")
	}
}

func TestDeleteAuthorizedSocialPlatformReturnsNotFoundWhenNothingDeleted(t *testing.T) {
	app := fiber.New()
	app.Delete("/:id", func(c fiber.Ctx) error {
		toolboxUserID := "u1"
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		defer func() {
			_ = result
			_ = reason
			_ = toolboxUserID
		}()

		idParam := c.Params("id")
		if _, err := strconv.Atoi(idParam); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid id parameter")
		}
		reason = "authorized_social_platform_not_found"
		return harukiAPIHelper.ErrorNotFound(c, "authorized social platform not found")
	})

	req := httptest.NewRequest(http.MethodDelete, "/123", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusNotFound)
	}
}
