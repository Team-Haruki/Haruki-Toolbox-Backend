package bootstrap

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"

	"github.com/gofiber/fiber/v3"
)

func TestFiberConfigTimeouts(t *testing.T) {
	defaults := fiberConfig(harukiConfig.Config{})
	if defaults.ReadTimeout != defaultServerReadTimeout || defaults.WriteTimeout != defaultServerWriteTimeout || defaults.IdleTimeout != defaultServerIdleTimeout {
		t.Fatalf("default timeouts = read %s, write %s, idle %s", defaults.ReadTimeout, defaults.WriteTimeout, defaults.IdleTimeout)
	}
	if defaults.ReadBufferSize != requestHeaderBufferSize {
		t.Fatalf("ReadBufferSize = %d, want %d", defaults.ReadBufferSize, requestHeaderBufferSize)
	}

	var cfg harukiConfig.Config
	cfg.Backend.ReadTimeoutSeconds = 300
	cfg.Backend.WriteTimeoutSeconds = 90
	cfg.Backend.IdleTimeoutSeconds = -1
	tuned := fiberConfig(cfg)
	if tuned.ReadTimeout != 300*time.Second || tuned.WriteTimeout != 90*time.Second || tuned.IdleTimeout != defaultServerIdleTimeout {
		t.Fatalf("configured timeouts = read %s, write %s, idle %s", tuned.ReadTimeout, tuned.WriteTimeout, tuned.IdleTimeout)
	}
}

// serveWithTimeouts runs the production Fiber config on a real listener with
// short timeouts, because fasthttp applies them to the connection.
func serveWithTimeouts(t *testing.T, read, write, idle time.Duration, register func(*fiber.App)) string {
	t.Helper()
	cfg := fiberConfig(harukiConfig.Config{})
	cfg.ReadTimeout, cfg.WriteTimeout, cfg.IdleTimeout = read, write, idle
	app := fiber.New(cfg)
	register(app)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true}) }()
	t.Cleanup(func() { _ = app.Shutdown() })
	return ln.Addr().String()
}

// A handler that runs far longer than every timeout (an inherit takes up to
// ~30 s) still answers: fasthttp's read deadline ends once the request is read
// and the write deadline starts only after the handler returns.
func TestServerTimeoutsDoNotLimitHandlerRunTime(t *testing.T) {
	addr := serveWithTimeouts(t, 150*time.Millisecond, 150*time.Millisecond, 150*time.Millisecond, func(app *fiber.App) {
		app.Post("/slow", func(c fiber.Ctx) error {
			time.Sleep(600 * time.Millisecond)
			return c.SendString(fmt.Sprintf("read %d bytes", len(c.Body())))
		})
	})
	body := bytes.Repeat([]byte("x"), 4<<20)
	resp, err := http.Post("http://"+addr+"/slow", "application/octet-stream", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("slow handler was cut off: %v", err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(got) != fmt.Sprintf("read %d bytes", len(body)) {
		t.Fatalf("slow handler = %d %q", resp.StatusCode, got)
	}
}

// A client that stalls while sending its body is disconnected after the read
// timeout instead of holding the connection and its buffers forever.
func TestServerReadTimeoutDropsStalledBody(t *testing.T) {
	addr := serveWithTimeouts(t, 200*time.Millisecond, time.Second, time.Second, func(app *fiber.App) {
		app.Post("/upload", func(c fiber.Ctx) error { return c.SendStatus(http.StatusNoContent) })
	})
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "POST /upload HTTP/1.1\r\nHost: test\r\nContent-Length: 1000\r\n\r\npartial"); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	reply, err := io.ReadAll(bufio.NewReader(conn))
	if err != nil {
		t.Fatalf("connection was not closed by the server: %v", err)
	}
	if strings.Contains(string(reply), "204") {
		t.Fatalf("stalled request reached the handler: %q", reply)
	}
}

func TestCompressionSkipsIOSProxyBodies(t *testing.T) {
	app, closeLog, err := newFiberApp(harukiConfig.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeLog()
	payload := bytes.Repeat([]byte("compressible "), 1024)
	handler := func(c fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, fiber.MIMEOctetStream)
		return c.Send(payload)
	}
	app.Get("/ios/proxy/jp/suite/user/1", handler)
	app.Get("/api/ios/proxy/jp/suite/user/1", handler)
	app.Get("/api/other", handler)

	for path, wantEncoded := range map[string]bool{
		"/ios/proxy/jp/suite/user/1":     false,
		"/api/ios/proxy/jp/suite/user/1": false,
		"/api/other":                     true,
	} {
		req := httptest.NewRequest(fiber.MethodGet, path, nil)
		req.Header.Set(fiber.HeaderAcceptEncoding, "gzip")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		encoded := resp.Header.Get(fiber.HeaderContentEncoding) != ""
		if encoded != wantEncoded {
			t.Fatalf("%s: Content-Encoding %q, want encoded=%v", path, resp.Header.Get(fiber.HeaderContentEncoding), wantEncoded)
		}
		if !wantEncoded && !bytes.Equal(got, payload) {
			t.Fatalf("%s: body changed", path)
		}
	}
}

func TestSekaiAPIOptionsFromConfig(t *testing.T) {
	options := sekaiAPIOptions(harukiConfig.SekaiAPIConfig{
		ProfileViewTimeoutSeconds:         4,
		ProfileViewTimeoutSecondsByServer: map[string]int{"tw": 6},
		VerifyTimeoutSeconds:              9,
		ProfileCacheTTLSeconds:            -1,
	})
	if options.ProfileViewTimeout != 4*time.Second || options.ProfileViewTimeoutByServer["tw"] != 6*time.Second ||
		options.VerifyTimeout != 9*time.Second || options.ProfileCacheTTL != -time.Second {
		t.Fatalf("options = %#v", options)
	}
	if empty := sekaiAPIOptions(harukiConfig.SekaiAPIConfig{}); empty.ProfileViewTimeout != 0 || len(empty.ProfileViewTimeoutByServer) != 0 {
		t.Fatalf("empty config options = %#v", empty)
	}
}

// An idle keep-alive connection is closed after the idle timeout.
func TestServerIdleTimeoutClosesKeepAlive(t *testing.T) {
	addr := serveWithTimeouts(t, time.Second, time.Second, 150*time.Millisecond, func(app *fiber.App) {
		app.Get("/", func(c fiber.Ctx) error { return c.SendString("ok") })
	})
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: test\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	start := time.Now()
	if _, err := reader.ReadByte(); err != io.EOF {
		t.Fatalf("idle connection read = %v, want EOF from the server closing it", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("idle connection closed after %s", elapsed)
	}
}
