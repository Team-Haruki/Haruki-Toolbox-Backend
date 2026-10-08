package bootstrap

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/jsoncodec"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/redact"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/compress"
	"github.com/gofiber/fiber/v3/middleware/logger"
)

// requestHeaderBufferSize bounds the request line plus all headers. Fiber's
// 4 KiB default answers 431 to Hydra consent/login redirects: the challenge in
// the query is ~2.5 KiB and the browser adds Kratos and Hydra CSRF cookies on
// top of the identity headers Oathkeeper injects. fasthttp allocates a reader
// of this size per connection (pooled) and answers 431 when the header block
// does not fit.
const requestHeaderBufferSize = 32 << 10

// Server timeout defaults (backend.*_timeout_seconds). fasthttp applies them as
// follows, which is why handler run time is not limited by any of them:
//   - read: from the first byte of a request until its body is read;
//   - write: from the moment the handler returns until the response is written;
//   - idle: between requests on a keep-alive connection.
//
// Without them a stalled client holds its connection, read buffer and request
// body indefinitely, and Shutdown cannot reap idle keep-alive connections.
const (
	defaultServerReadTimeout  = 120 * time.Second
	defaultServerWriteTimeout = 60 * time.Second
	defaultServerIdleTimeout  = 120 * time.Second
)

// iOS proxy responses are the game server's encrypted octet-stream bodies:
// compressing them burns CPU and makes them slightly larger.
var uncompressedPathPrefixes = []string{"/ios/proxy/", "/api/ios/proxy/"}

func newFiberApp(cfg harukiConfig.Config) (*fiber.App, func() error, error) {
	app := fiber.New(fiberConfig(cfg))

	app.Use(compress.New(compress.Config{Level: compress.LevelBestSpeed, Next: skipCompression}))
	app.Use(cspMiddleware(cfg))

	closeAccessLogFile, err := configureAccessLog(app, cfg)
	if err != nil {
		return nil, nil, err
	}
	return app, closeAccessLogFile, nil
}

func fiberConfig(cfg harukiConfig.Config) fiber.Config {
	return fiber.Config{
		BodyLimit:      100 * 1024 * 1024,
		ReadBufferSize: requestHeaderBufferSize,
		ReadTimeout:    secondsOrDefault(cfg.Backend.ReadTimeoutSeconds, defaultServerReadTimeout),
		WriteTimeout:   secondsOrDefault(cfg.Backend.WriteTimeoutSeconds, defaultServerWriteTimeout),
		IdleTimeout:    secondsOrDefault(cfg.Backend.IdleTimeoutSeconds, defaultServerIdleTimeout),
		JSONEncoder:    jsoncodec.Marshal,
		JSONDecoder:    jsoncodec.Unmarshal,
		ProxyHeader:    cfg.Backend.ProxyHeader,
		TrustProxy:     cfg.Backend.EnableTrustProxy,
		TrustProxyConfig: fiber.TrustProxyConfig{
			Proxies: cfg.Backend.TrustProxies,
		},
		// Validate the resolved client IP. Without this, a forwarded-header IP is
		// returned verbatim, letting a client spoof c.IP() (used for rate-limit
		// keys, audit logs, and upstream X-Forwarded-For). Deployments must also
		// keep trusted_proxies scoped to the actual edge proxy, not broad ranges.
		EnableIPValidation: true,
	}
}

func secondsOrDefault(seconds int, fallback time.Duration) time.Duration {
	if seconds <= 0 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

func skipCompression(c fiber.Ctx) bool {
	path := c.Path()
	for _, prefix := range uncompressedPathPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
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

	// Route paths can carry credentials (iOS upload codes, the Afdian callback
	// secret) and ${url}/${queryParams} tags would add query strings, so every
	// access log entry passes through the redacting writer.
	loggerConfig.Stream = redact.Writer(os.Stdout)
	closeAccessLogFile := func() error { return nil }
	if cfg.Backend.AccessLogPath != "" {
		accessLogFile, err := os.OpenFile(cfg.Backend.AccessLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return nil, fmt.Errorf("open access log file: %w", err)
		}
		closeAccessLogFile = accessLogFile.Close
		loggerConfig.Stream = redact.Writer(accessLogFile)
	}
	app.Use(logger.New(loggerConfig))
	return closeAccessLogFile, nil
}
