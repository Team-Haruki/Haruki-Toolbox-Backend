package upload

import (
	"context"
	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	oauth "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	"github.com/gofiber/fiber/v3"
)

func proxyOAuthAuthentication(helper *api.HarukiToolboxRouterHelpers, d Dependencies) fiber.Handler {
	return func(c fiber.Ctx) error {
		if d.HydraConfig == nil || d.OAuth2ClientActiveChecker == nil || helper == nil || helper.DBManager == nil {
			return proxyResponse(c, 503, "temporarily_unavailable", "OAuth2 upload unavailable", true, nil)
		}
		if err := oauth.AuthenticateUploadRequest(c, d.HydraConfig, helper.DBManager.DB, d.OAuth2ClientActiveChecker); err != nil {
			status := fiber.StatusServiceUnavailable
			if e, ok := err.(*fiber.Error); ok {
				status = e.Code
			}
			code := "temporarily_unavailable"
			if status == 401 {
				code = "invalid_token"
			}
			if status == 403 {
				code = "insufficient_scope"
			}
			return proxyResponse(c, status, code, "OAuth2 upload authorization failed", status == 503, nil)
		}
		return c.Next()
	}
}

func oauthUploadDependencies(c fiber.Ctx, helper *api.HarukiToolboxRouterHelpers, d Dependencies) Dependencies {
	actor, _ := c.Locals("userID").(string)
	client, _ := c.Locals("oauth2ClientID").(string)
	d.ValidateUploadIdentity = func(context.Context) error {
		if err := oauth.AuthenticateUploadRequest(c, d.HydraConfig, helper.DBManager.DB, d.OAuth2ClientActiveChecker); err != nil {
			if e, ok := err.(*fiber.Error); ok && (e.Code == 401 || e.Code == 403) {
				return errUploadOwnershipMismatch
			}
			return err
		}
		if c.Locals("userID") != actor || c.Locals("oauth2ClientID") != client {
			return errUploadOwnershipMismatch
		}
		return nil
	}
	return d
}
