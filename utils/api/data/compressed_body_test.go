package data

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestAcceptsGzip(t *testing.T) {
	cases := []struct {
		header string
		want   bool
	}{
		{header: "", want: false},
		{header: "gzip", want: true},
		{header: "GZIP", want: true},
		{header: "gzip, deflate, br", want: true},
		{header: " gzip ;q=0.8, br", want: true},
		{header: "gzip;q=0", want: false},
		{header: "gzip;q=0.000", want: false},
		{header: "br", want: false},
		{header: "*", want: true},
		{header: "*;q=0", want: false},
		{header: "br, *;q=0", want: false},
		{header: "identity", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.header, func(t *testing.T) {
			if got := acceptsGzip(tc.header); got != tc.want {
				t.Fatalf("acceptsGzip(%q) = %v, want %v", tc.header, got, tc.want)
			}
		})
	}
}

func TestNegotiateStoredBodyRoundTrip(t *testing.T) {
	plain := `{"upload_time":1752600000,"userGamedata":{"name":"test"}}`
	stored, err := CompressGameDataBody([]byte(plain))
	if err != nil {
		t.Fatalf("CompressGameDataBody error: %v", err)
	}
	if !isGzipEntry(stored) {
		t.Fatalf("compressed entry not recognized by sniffer")
	}

	body, encoding, err := negotiateStoredBody(stored, "gzip, br")
	if err != nil {
		t.Fatalf("negotiate (gzip client) error: %v", err)
	}
	if encoding != "gzip" || string(body) != stored {
		t.Fatalf("gzip client should receive stored bytes verbatim with gzip encoding, got encoding=%q", encoding)
	}

	body, encoding, err = negotiateStoredBody(stored, "")
	if err != nil {
		t.Fatalf("negotiate (identity client) error: %v", err)
	}
	if encoding != "" || string(body) != plain {
		t.Fatalf("identity client should receive decompressed plain body, got encoding=%q body=%q", encoding, body)
	}

	body, encoding, err = negotiateStoredBody(plain, "gzip")
	if err != nil {
		t.Fatalf("negotiate (legacy plain entry) error: %v", err)
	}
	if encoding != "" || string(body) != plain {
		t.Fatalf("legacy plain entry should pass through unchanged, got encoding=%q", encoding)
	}

	if _, _, err := negotiateStoredBody("\x1f\x8bcorrupt", ""); err == nil {
		t.Fatalf("corrupt compressed entry should surface an error")
	}
}

func TestServeGameDataBodyHeaders(t *testing.T) {
	plain := `{"upload_time":1752600000}`
	stored, err := CompressGameDataBody([]byte(plain))
	if err != nil {
		t.Fatalf("CompressGameDataBody error: %v", err)
	}

	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		return ServeGameDataBody(c, stored)
	})

	t.Run("gzip client gets pre-compressed bytes", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(fiber.HeaderAcceptEncoding, "gzip")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test error: %v", err)
		}
		if got := resp.Header.Get(fiber.HeaderContentEncoding); got != "gzip" {
			t.Fatalf("Content-Encoding = %q, want gzip", got)
		}
		if got := resp.Header.Get(fiber.HeaderVary); !strings.Contains(got, fiber.HeaderAcceptEncoding) {
			t.Fatalf("Vary = %q, want to contain Accept-Encoding", got)
		}
		if got := resp.Header.Get(fiber.HeaderContentType); got != fiber.MIMEApplicationJSONCharsetUTF8 {
			t.Fatalf("Content-Type = %q", got)
		}
	})

	t.Run("identity client gets plain body", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test error: %v", err)
		}
		if got := resp.Header.Get(fiber.HeaderContentEncoding); got != "" {
			t.Fatalf("Content-Encoding = %q, want empty", got)
		}
		buf := make([]byte, len(plain)+16)
		n, _ := resp.Body.Read(buf)
		if string(buf[:n]) != plain {
			t.Fatalf("body = %q, want %q", buf[:n], plain)
		}
	})
}
