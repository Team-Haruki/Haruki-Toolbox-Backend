package upload

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	harukiAPIHelper "haruki-suite/utils/api"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

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
