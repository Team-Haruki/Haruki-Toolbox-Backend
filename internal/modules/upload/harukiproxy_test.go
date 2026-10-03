package upload

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"github.com/gofiber/fiber/v3"
)

func TestUnpackKeyFromHelper(t *testing.T) {
	t.Parallel()

	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{HarukiProxyUnpackKey: "my-secret"}
	key, err := unpackKeyFromHelper(helper)
	if err != nil {
		t.Fatalf("unpackKeyFromHelper returned error: %v", err)
	}
	want := sha256.Sum256([]byte("my-secret"))
	if !bytes.Equal(key, want[:]) {
		t.Fatalf("unexpected unpack key hash")
	}

	helper.HarukiProxyUnpackKey = " "
	if _, err := unpackKeyFromHelper(helper); err == nil {
		t.Fatalf("unpackKeyFromHelper should fail when key is missing")
	}
}

func TestUnpackRoundTrip(t *testing.T) {
	t.Parallel()

	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{HarukiProxyUnpackKey: "my-secret"}
	aad := "jp|123|suite"
	plaintext := []byte(`{"ok":true}`)

	ciphertext, err := packForTest(plaintext, aad, helper.HarukiProxyUnpackKey)
	if err != nil {
		t.Fatalf("packForTest returned error: %v", err)
	}

	decoded, err := Unpack(ciphertext, aad, helper)
	if err != nil {
		t.Fatalf("Unpack returned error: %v", err)
	}
	if !bytes.Equal(decoded, plaintext) {
		t.Fatalf("Unpack plaintext mismatch")
	}

	if _, err := Unpack(ciphertext, "wrong-aad", helper); err == nil {
		t.Fatalf("Unpack should fail with wrong AAD")
	}
	if _, err := Unpack(ciphertext[:4], aad, helper); err == nil {
		t.Fatalf("Unpack should fail on truncated ciphertext")
	}
}

func TestValidateHarukiProxyClientHeader(t *testing.T) {
	t.Parallel()

	t.Run("configured middleware passes valid headers", func(t *testing.T) {
		t.Parallel()
		app := fiber.New()
		helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
			HarukiProxyUserAgent: "HarukiProxy",
			HarukiProxyVersion:   "v1.2.0",
			HarukiProxySecret:    "secret",
		}
		app.Post("/",
			validateHarukiProxyClientHeader(helper),
			func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) },
		)

		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("User-Agent", "HarukiProxy/v1.2.3")
		req.Header.Set("X-Haruki-Toolbox-Secret", "secret")

		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
		}
	})

	t.Run("configured middleware rejects invalid headers", func(t *testing.T) {
		t.Parallel()
		app := fiber.New()
		helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
			HarukiProxyUserAgent: "HarukiProxy",
			HarukiProxyVersion:   "v1.2.0",
			HarukiProxySecret:    "secret",
		}
		app.Post("/",
			validateHarukiProxyClientHeader(helper),
			func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) },
		)

		tests := []struct {
			name      string
			userAgent string
			secret    string
		}{
			{
				name:      "wrong secret",
				userAgent: "HarukiProxy/v1.2.3",
				secret:    "wrong",
			},
			{
				name:      "invalid user agent format",
				userAgent: "HarukiProxy 1.2.3",
				secret:    "secret",
			},
			{
				name:      "version too low",
				userAgent: "HarukiProxy/v1.1.9",
				secret:    "secret",
			},
		}

		for _, tc := range tests {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				req := httptest.NewRequest(http.MethodPost, "/", nil)
				req.Header.Set("User-Agent", tc.userAgent)
				req.Header.Set("X-Haruki-Toolbox-Secret", tc.secret)
				resp, err := app.Test(req)
				if err != nil {
					t.Fatalf("app.Test returned error: %v", err)
				}
				if resp.StatusCode != fiber.StatusBadRequest {
					t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
				}
			})
		}
	})

	t.Run("middleware fails closed when auth not configured", func(t *testing.T) {
		t.Parallel()
		app := fiber.New()
		helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{}
		app.Post("/",
			validateHarukiProxyClientHeader(helper),
			func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) },
		)

		req := httptest.NewRequest(http.MethodPost, "/", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusInternalServerError {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusInternalServerError)
		}
	})

	t.Run("runtime config update takes effect immediately", func(t *testing.T) {
		t.Parallel()
		app := fiber.New()
		helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{}
		helper.SetHarukiProxyUserAgent("HarukiProxy")
		helper.SetHarukiProxyVersion("v1.2.0")
		helper.SetHarukiProxySecret("secret-a")
		app.Post("/",
			validateHarukiProxyClientHeader(helper),
			func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) },
		)

		reqA := httptest.NewRequest(http.MethodPost, "/", nil)
		reqA.Header.Set("User-Agent", "HarukiProxy/v1.2.3")
		reqA.Header.Set("X-Haruki-Toolbox-Secret", "secret-a")
		respA, err := app.Test(reqA)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if respA.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status code = %d, want %d", respA.StatusCode, fiber.StatusNoContent)
		}

		helper.SetHarukiProxySecret("secret-b")

		reqOld := httptest.NewRequest(http.MethodPost, "/", nil)
		reqOld.Header.Set("User-Agent", "HarukiProxy/v1.2.3")
		reqOld.Header.Set("X-Haruki-Toolbox-Secret", "secret-a")
		respOld, err := app.Test(reqOld)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if respOld.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", respOld.StatusCode, fiber.StatusBadRequest)
		}

		reqNew := httptest.NewRequest(http.MethodPost, "/", nil)
		reqNew.Header.Set("User-Agent", "HarukiProxy/v1.2.3")
		reqNew.Header.Set("X-Haruki-Toolbox-Secret", "secret-b")
		respNew, err := app.Test(reqNew)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if respNew.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status code = %d, want %d", respNew.StatusCode, fiber.StatusNoContent)
		}
	})
}

func packForTest(plaintext []byte, aad, keyMaterial string) ([]byte, error) {
	key := sha256.Sum256([]byte(keyMaterial))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	sealed := gcm.Seal(nil, nonce, plaintext, []byte(aad))
	out := make([]byte, 0, len(nonce)+len(sealed))
	out = append(out, nonce...)
	out = append(out, sealed...)
	return out, nil
}

func TestHarukiProxyLegacySunset(t *testing.T) {
	cutoff := time.Date(2026, 10, 31, 16, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		now    time.Time
		status int
	}{
		{"before", cutoff.Add(-time.Nanosecond), http.StatusNoContent},
		{"exact", cutoff, http.StatusGone},
		{"after", cutoff.Add(time.Second), http.StatusGone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := fiber.New()
			app.Post("/", harukiProxyLegacyGate(func() time.Time { return tc.now }), func(c fiber.Ctx) error { return c.SendStatus(http.StatusNoContent) })
			resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/", nil))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.status)
			}
			if resp.Header.Get("Sunset") != "Sat, 31 Oct 2026 16:00:00 GMT" {
				t.Fatalf("Sunset = %s", resp.Header.Get("Sunset"))
			}
		})
	}
}

func TestHarukiProxyV3RoutesAndKeys(t *testing.T) {
	dependencies := testUploadDependencies()
	dependencies.HarukiProxyV3Secret = "new-secret"
	dependencies.HarukiProxyV3UnpackKey = "new-key"
	client := enttest.Open(t, "sqlite3", uniqueUploadAuditSQLiteDSN(t, "proxy-v3"))
	t.Cleanup(func() { _ = client.Close() })
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
		HarukiProxyUserAgent: "HarukiProxy", HarukiProxyVersion: "v1.2.0",
		HarukiProxySecret: "old-secret", HarukiProxyUnpackKey: "old-key",
		DBManager: &database.HarukiToolboxDBManager{DB: client},
	}
	for _, prefix := range []string{"/harukiproxy", "/api/harukiproxy"} {
		for _, tc := range []struct{ name, secret, key, want string }{
			{"old auth rejected", "old-secret", "new-key", "Invalid HarukiProxy Secret"},
			{"old encryption rejected", "new-secret", "old-key", "failed to decrypt request body"},
			{"new encryption accepted", "new-secret", "new-key", "failed to process upload"},
		} {
			t.Run(prefix+tc.name, func(t *testing.T) {
				app := fiber.New()
				helper.Router = app
				registerHarukiProxyRoutes(helper, dependencies)
				// The empty account store and invalid payload prevent persistence.
				body, err := packForTest([]byte("not-game-data"), "jp|123|suite", tc.key)
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest(http.MethodPost, prefix+"/v3/jp/123/suite/upload", bytes.NewReader(body))
				req.Header.Set("User-Agent", "HarukiProxy/v1.2.3")
				req.Header.Set("X-Haruki-Toolbox-Secret", tc.secret)
				resp, err := app.Test(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				payload, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatal(err)
				}
				if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(payload), tc.want) {
					t.Fatalf("status = %d, body = %s", resp.StatusCode, payload)
				}
				if resp.Header.Get("Sunset") != "" {
					t.Fatal("v3 inherited legacy sunset")
				}
			})
		}
	}
	// Legacy encryption cannot decrypt v3 ciphertext either.
	body, err := packForTest([]byte("payload"), "jp|123|suite", dependencies.HarukiProxyV3UnpackKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Unpack(body, "jp|123|suite", helper); err == nil {
		t.Fatal("legacy accepted v3 key")
	}
}

func TestHarukiProxyV3DoesNotFallBackToLegacyAuth(t *testing.T) {
	for _, tc := range []struct{ name, secret, key string }{
		{"missing both", "", ""},
		{"missing auth", "", "new-key"},
		{"missing encryption", "new-secret", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := fiber.New()
			dependencies := Dependencies{HarukiProxyV3Secret: tc.secret, HarukiProxyV3UnpackKey: tc.key}
			helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{Router: app, HarukiProxyUserAgent: "HarukiProxy", HarukiProxyVersion: "v1.2.0", HarukiProxySecret: "legacy", HarukiProxyUnpackKey: "legacy-key"}
			registerHarukiProxyRoutes(helper, dependencies)
			req := httptest.NewRequest(http.MethodPost, "/harukiproxy/v3/jp/123/suite/upload", nil)
			req.Header.Set("User-Agent", "HarukiProxy/v1.2.3")
			req.Header.Set("X-Haruki-Toolbox-Secret", "legacy")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusInternalServerError {
				t.Fatalf("status = %d", resp.StatusCode)
			}
		})
	}
}
