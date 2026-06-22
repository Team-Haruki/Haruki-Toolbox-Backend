package bootstrap

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	"os"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/compress"
	"github.com/gofiber/fiber/v3/middleware/logger"
)

func newFiberApp(cfg harukiConfig.Config) (*fiber.App, func() error, error) {
	app := fiber.New(fiber.Config{
		BodyLimit:   100 * 1024 * 1024,
		JSONEncoder: sonic.Marshal,
		JSONDecoder: sonic.Unmarshal,
		ProxyHeader: cfg.Backend.ProxyHeader,
		TrustProxy:  cfg.Backend.EnableTrustProxy,
		TrustProxyConfig: fiber.TrustProxyConfig{
			Proxies: cfg.Backend.TrustProxies,
		},
		// Validate the resolved client IP. Without this, a forwarded-header IP is
		// returned verbatim, letting a client spoof c.IP() (used for rate-limit
		// keys, audit logs, and upstream X-Forwarded-For). Deployments must also
		// keep trusted_proxies scoped to the actual edge proxy, not broad ranges.
		EnableIPValidation: true,
	})

	app.Use(compress.New(compress.Config{Level: compress.LevelBestSpeed}))
	app.Use(cspMiddleware(cfg))

	closeAccessLogFile, err := configureAccessLog(app, cfg)
	if err != nil {
		return nil, nil, err
	}
	return app, closeAccessLogFile, nil
}

func cspMiddleware(cfg harukiConfig.Config) fiber.Handler {
	return func(c fiber.Ctx) error {
		nonceBytes := make([]byte, 16)
		if _, err := rand.Read(nonceBytes); err != nil {
			return err
		}
		nonce := base64.StdEncoding.EncodeToString(nonceBytes)

		var cspConnectSrc strings.Builder
		cspConnectSrc.WriteString("'self'")
		for _, src := range cfg.Backend.CSPConnectSrc {
			cspConnectSrc.WriteString(" " + src)
		}

		c.Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' https://challenges.cloudflare.com 'nonce-"+nonce+"'; "+
				"frame-src https://challenges.cloudflare.com; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data: https:; "+
				"connect-src "+cspConnectSrc.String()+"; "+
				"object-src 'none'; "+
				"base-uri 'self'; "+
				"form-action 'self';",
		)
		c.Locals("cspNonce", nonce)
		return c.Next()
	}
}

func configureAccessLog(app *fiber.App, cfg harukiConfig.Config) (func() error, error) {
	if cfg.Backend.AccessLog == "" {
		return func() error { return nil }, nil
	}

	loggerConfig := logger.Config{
		Format:     cfg.Backend.AccessLog,
		TimeFormat: "2006-01-02 15:04:05",
		TimeZone:   "Local",
		CustomTags: map[string]logger.LogFunc{
			"bytesSent": func(output logger.Buffer, c fiber.Ctx, data *logger.Data, extra string) (int, error) {
				return output.WriteString(fmt.Sprintf("%d", len(c.Response().Body())))
			},
		},
	}

	closeAccessLogFile := func() error { return nil }
	if cfg.Backend.AccessLogPath != "" {
		accessLogFile, err := os.OpenFile(cfg.Backend.AccessLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return nil, fmt.Errorf("open access log file: %w", err)
		}
		closeAccessLogFile = accessLogFile.Close
		loggerConfig.Stream = accessLogFile
	}
	app.Use(logger.New(loggerConfig))
	return closeAccessLogFile, nil
}
