package oauth2

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	harukiAPIHelper "haruki-suite/utils/api"
	harukiLogger "haruki-suite/utils/logger"
	harukiOAuth2 "haruki-suite/utils/oauth2"

	"github.com/gofiber/fiber/v3"
)

func handleHydraAuthorizeRedirect() fiber.Handler {
	return func(c fiber.Ctx) error {
		targetURL, err := harukiOAuth2.HydraBrowserEndpoint("/oauth2/auth")
		if err != nil {
			harukiLogger.Errorf("Hydra authorize endpoint is not configured: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "oauth2 provider is not configured")
		}
		rawQuery := string(c.Request().URI().QueryString())
		if rawQuery != "" {
			targetURL += "?" + rawQuery
		}
		return c.Redirect().To(targetURL)
	}
}

func handleHydraPublicProxy(endpointPath string) fiber.Handler {
	return func(c fiber.Ctx) error {
		targetURL, err := harukiOAuth2.HydraPublicEndpoint(endpointPath)
		if err != nil {
			harukiLogger.Errorf("Hydra public endpoint is not configured: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "oauth2 provider is not configured")
		}

		rawQuery := string(c.Request().URI().QueryString())
		if rawQuery != "" {
			targetURL += "?" + rawQuery
		}

		req, err := http.NewRequestWithContext(c.Context(), c.Method(), targetURL, bytes.NewReader(c.Body()))
		if err != nil {
			harukiLogger.Errorf("Failed to create Hydra proxy request: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to build oauth2 provider request")
		}
		copyRequestHeaderIfPresent(c, req, "Authorization")
		copyRequestHeaderIfPresent(c, req, "Content-Type")
		copyRequestHeaderIfPresent(c, req, "Accept")

		resp, err := hydraHTTPClient().Do(req)
		if err != nil {
			harukiLogger.Errorf("Hydra proxy request failed: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "oauth2 provider unavailable")
		}
		defer func(body io.ReadCloser) {
			_ = body.Close()
		}(resp.Body)

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			harukiLogger.Errorf("Failed to read Hydra proxy response: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to read oauth2 provider response")
		}

		copyHydraResponseHeaders(c, resp.Header)
		c.Status(resp.StatusCode)
		if len(respBody) == 0 {
			return nil
		}
		return c.Send(respBody)
	}
}

func copyHydraResponseHeaders(c fiber.Ctx, header http.Header) {
	for _, name := range []string{"Content-Type", "Cache-Control", "Pragma", "WWW-Authenticate", "Location"} {
		if value := strings.TrimSpace(header.Get(name)); value != "" {
			c.Set(name, value)
		}
	}
}

func copyRequestHeaderIfPresent(c fiber.Ctx, req *http.Request, name string) {
	if value := strings.TrimSpace(c.Get(name)); value != "" {
		req.Header.Set(name, value)
	}
}
