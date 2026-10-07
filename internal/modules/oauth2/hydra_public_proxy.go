package oauth2

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
)

func handleHydraAuthorizeRedirect(hydraConfig *harukiOAuth2.HydraConfig) fiber.Handler {
	return func(c fiber.Ctx) error {
		targetURL, err := hydraConfig.BrowserEndpoint("/oauth2/auth")
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

func handleHydraPublicProxy(hydraConfig *harukiOAuth2.HydraConfig, endpointPath string) fiber.Handler {
	return func(c fiber.Ctx) error {
		header := http.Header{}
		for _, name := range []string{"Authorization", "Content-Type", "Accept"} {
			if value := strings.TrimSpace(c.Get(name)); value != "" {
				header.Set(name, value)
			}
		}
		resp, failure, err := forwardHydraPublicRequest(c.Context(), hydraConfig, c.Method(), endpointPath, string(c.Request().URI().QueryString()), c.Body(), header)
		if err != nil {
			harukiLogger.Errorf("Hydra public proxy to %s failed (%s): %v", endpointPath, failure, err)
			return harukiAPIHelper.ErrorInternal(c, failure)
		}
		return writeHydraPublicResponse(c, resp)
	}
}

// hydraPublicResponse is a buffered Hydra public-endpoint response.
type hydraPublicResponse struct {
	Status int
	Header http.Header
	Body   []byte
}

// forwardHydraPublicRequest sends body to a Hydra public endpoint, carrying
// rawQuery and the given request headers, and buffers the answer. On error the
// returned failure is the fixed message the generic proxy answers with.
func forwardHydraPublicRequest(ctx context.Context, hydraConfig *harukiOAuth2.HydraConfig, method, endpointPath, rawQuery string, body []byte, header http.Header) (*hydraPublicResponse, string, error) {
	targetURL, err := hydraConfig.PublicEndpoint(endpointPath)
	if err != nil {
		return nil, "oauth2 provider is not configured", err
	}
	if rawQuery != "" {
		targetURL += "?" + rawQuery
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, "failed to build oauth2 provider request", err
	}
	for name, values := range header {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	resp, err := hydraConfig.Do(req)
	if err != nil {
		return nil, "oauth2 provider unavailable", err
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "failed to read oauth2 provider response", err
	}
	return &hydraPublicResponse{Status: resp.StatusCode, Header: resp.Header, Body: respBody}, "", nil
}

func writeHydraPublicResponse(c fiber.Ctx, resp *hydraPublicResponse) error {
	copyHydraResponseHeaders(c, resp.Header)
	c.Status(resp.Status)
	if len(resp.Body) == 0 {
		return nil
	}
	return c.Send(resp.Body)
}

func copyHydraResponseHeaders(c fiber.Ctx, header http.Header) {
	for _, name := range []string{"Content-Type", "Cache-Control", "Pragma", "WWW-Authenticate", "Location"} {
		if value := strings.TrimSpace(header.Get(name)); value != "" {
			c.Set(name, value)
		}
	}
}
