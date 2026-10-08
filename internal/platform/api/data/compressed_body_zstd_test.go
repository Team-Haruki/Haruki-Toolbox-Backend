package data

import (
	"bytes"
	stdgzip "compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/compress"
	"github.com/klauspost/compress/zstd"
)

func decodeZstdForTest(t *testing.T, data []byte) string {
	t.Helper()
	// A fresh decoder with default options stands in for an independent
	// client (Haruki Cloud uses the same library with defaults).
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	plain, err := decoder.DecodeAll(data, nil)
	if err != nil {
		t.Fatalf("zstd decode: %v", err)
	}
	return string(plain)
}

func decodeGzipForTest(t *testing.T, data []byte) string {
	t.Helper()
	reader, err := stdgzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	plain, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("gzip decode: %v", err)
	}
	return string(plain)
}

func TestCompressGameDataBodyZstdConcurrentReuse(t *testing.T) {
	const workers = 16
	const iterations = 8
	type result struct {
		plain  string
		stored string
	}
	results := make([]result, workers*iterations)
	var work sync.WaitGroup
	for worker := range workers {
		work.Go(func() {
			for iteration := range iterations {
				index := worker*iterations + iteration
				plain := fmt.Sprintf(`{"upload_time":%d,"name":"%s"}`, index, strings.Repeat(fmt.Sprintf("user-%d-", index), 1024))
				stored, err := CompressGameDataBodyZstd([]byte(plain))
				if err != nil {
					t.Errorf("compression %d: %v", index, err)
					return
				}
				results[index] = result{plain: plain, stored: stored}
			}
		})
	}
	work.Wait()
	if t.Failed() {
		return
	}
	// Verify only after every call has finished, so a returned entry that
	// aliased a pooled buffer would have been overwritten by now.
	for index, result := range results {
		if !isZstdEntry([]byte(result.stored)) {
			t.Fatalf("entry %d not recognized as zstd", index)
		}
		if got := decodeZstdForTest(t, []byte(result.stored)); got != result.plain {
			t.Fatalf("compression %d changed after another call reused the pool", index)
		}
	}
}

func TestCompressGameDataBodyZstdLargeBodyWindow(t *testing.T) {
	// Larger than the 8 MiB window: must still round-trip, and a decoder that
	// enforces RFC 9659's 8 MiB window limit must accept it.
	plain := syntheticSuiteBody(12 << 20)
	stored, err := CompressGameDataBodyZstd(plain)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) >= len(plain)/4 {
		t.Fatalf("stored %d bytes for %d bytes of JSON; compression looks broken", len(stored), len(plain))
	}
	decoder, err := zstd.NewReader(nil, zstd.WithDecoderMaxWindow(8<<20), zstd.WithDecoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	if err := decoder.Reset(strings.NewReader(stored)); err != nil {
		t.Fatalf("streaming decoder with 8 MiB window rejected the frame: %v", err)
	}
	got, err := io.ReadAll(decoder)
	if err != nil {
		t.Fatalf("streaming decode: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatal("large body did not round-trip")
	}
}

func TestAcceptsZstd(t *testing.T) {
	cases := []struct {
		header string
		want   bool
	}{
		{header: "", want: false},
		{header: "zstd", want: true},
		{header: "ZSTD", want: true},
		{header: "gzip, deflate, br, zstd", want: true},
		{header: " zstd ;q=0.5", want: true},
		{header: "zstd;q=0", want: false},
		{header: "zstd;q=0.00, gzip", want: false},
		{header: "gzip", want: false},
		{header: "*", want: false},
		{header: "identity", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.header, func(t *testing.T) {
			if got := acceptsZstd(tc.header); got != tc.want {
				t.Fatalf("acceptsZstd(%q) = %v, want %v", tc.header, got, tc.want)
			}
		})
	}
}

func TestAcceptsGzipExplicitMemberBeatsWildcard(t *testing.T) {
	for header, want := range map[string]bool{
		"*, gzip;q=0": false,
		"gzip;q=0, *": false,
		"zstd, *":     true,
		"zstd":        false,
	} {
		if got := acceptsGzip(header); got != want {
			t.Fatalf("acceptsGzip(%q) = %v, want %v", header, got, want)
		}
	}
}

func TestNegotiateStoredBodyZstd(t *testing.T) {
	plain := `{"upload_time":1752600000,"userGamedata":{"name":"test"}}`
	storedZstd, err := CompressGameDataBodyZstd([]byte(plain))
	if err != nil {
		t.Fatal(err)
	}
	storedGzip, err := CompressGameDataBody([]byte(plain))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name           string
		stored         string
		acceptEncoding string
		wantEncoding   string
		wantBody       string
	}{
		{name: "zstd entry, zstd-only client passes through", stored: storedZstd, acceptEncoding: "zstd", wantEncoding: "zstd", wantBody: storedZstd},
		{name: "zstd entry, browser passes through", stored: storedZstd, acceptEncoding: "gzip, deflate, br, zstd", wantEncoding: "zstd", wantBody: storedZstd},
		{name: "zstd entry, gzip client decodes", stored: storedZstd, acceptEncoding: "gzip", wantEncoding: "", wantBody: plain},
		{name: "zstd entry, zstd refused decodes", stored: storedZstd, acceptEncoding: "zstd;q=0, gzip", wantEncoding: "", wantBody: plain},
		{name: "zstd entry, identity client decodes", stored: storedZstd, acceptEncoding: "", wantEncoding: "", wantBody: plain},
		{name: "legacy gzip entry, zstd-only client decodes", stored: storedGzip, acceptEncoding: "zstd", wantEncoding: "", wantBody: plain},
		{name: "legacy gzip entry, gzip client passes through", stored: storedGzip, acceptEncoding: "gzip", wantEncoding: "gzip", wantBody: storedGzip},
		{name: "legacy plain entry, zstd client", stored: plain, acceptEncoding: "zstd", wantEncoding: "", wantBody: plain},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, encoding, err := negotiateStoredBody([]byte(tc.stored), tc.acceptEncoding)
			if err != nil {
				t.Fatalf("negotiate error: %v", err)
			}
			if encoding != tc.wantEncoding || string(body) != tc.wantBody {
				t.Fatalf("encoding=%q body-match=%v, want encoding=%q", encoding, string(body) == tc.wantBody, tc.wantEncoding)
			}
		})
	}

	if _, _, err := negotiateStoredBody([]byte("\x28\xb5\x2f\xfdcorrupt"), ""); err == nil {
		t.Fatal("corrupt zstd entry should surface an error so the caller refetches")
	}
}

// newPrivateLikeApp reproduces the order of the private game-data handler's
// response path behind the production compress middleware: the conditional
// 304 check, then serving the stored cache entry.
func newPrivateLikeApp(stored string, stamp int64) *fiber.App {
	app := fiber.New()
	app.Use(compress.New(compress.Config{Level: compress.LevelBestSpeed}))
	app.Get("/", func(c fiber.Ctx) error {
		requestKey := c.Query("key")
		if CheckNotModified(c, harukiUtils.UploadDataTypeSuite, requestKey, false, nil, stamp) {
			return c.SendStatus(fiber.StatusNotModified)
		}
		return ServeGameDataBody(c, stored)
	})
	return app
}

type testResponse struct {
	status   int
	header   http.Header
	body     []byte
	decoded  string
	encoding string
}

func doGameDataRequest(t *testing.T, app *fiber.App, target, acceptEncoding string) testResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if acceptEncoding != "" {
		req.Header.Set(fiber.HeaderAcceptEncoding, acceptEncoding)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	out := testResponse{status: resp.StatusCode, header: resp.Header, body: body, encoding: resp.Header.Get(fiber.HeaderContentEncoding)}
	switch out.encoding {
	case "zstd":
		out.decoded = decodeZstdForTest(t, body)
	case "gzip":
		out.decoded = decodeGzipForTest(t, body)
	case "":
		out.decoded = string(body)
	default:
		// br and the like: callers below never negotiate them.
		t.Fatalf("unexpected Content-Encoding %q", out.encoding)
	}
	return out
}

func assertVaryOnce(t *testing.T, header http.Header) {
	t.Helper()
	vary := header.Get(fiber.HeaderVary)
	if strings.Count(strings.ToLower(vary), "accept-encoding") != 1 {
		t.Fatalf("Vary = %q, want Accept-Encoding exactly once", vary)
	}
}

func TestServeGameDataBodyThroughCompressMiddleware(t *testing.T) {
	plain := string(syntheticSuiteBody(64 << 10))
	stamp := time.Now().Add(-time.Hour).Unix()
	storedZstd, err := CompressGameDataBodyZstd([]byte(plain))
	if err != nil {
		t.Fatal(err)
	}
	storedGzip, err := CompressGameDataBody([]byte(plain))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("zstd entry to zstd-only client is written through untouched", func(t *testing.T) {
		resp := doGameDataRequest(t, newPrivateLikeApp(storedZstd, stamp), "/", "zstd")
		if resp.status != fiber.StatusOK || resp.encoding != "zstd" {
			t.Fatalf("status=%d encoding=%q", resp.status, resp.encoding)
		}
		// Byte-identical to the cache entry: the middleware did not re-encode.
		if string(resp.body) != storedZstd {
			t.Fatal("response body is not the stored entry; middleware re-encoded it")
		}
		if resp.decoded != plain {
			t.Fatal("decoded body mismatch")
		}
		if got := resp.header.Get(fiber.HeaderContentType); got != fiber.MIMEApplicationJSONCharsetUTF8 {
			t.Fatalf("Content-Type = %q", got)
		}
		assertVaryOnce(t, resp.header)
	})

	t.Run("zstd entry to gzip client is re-encoded by the middleware", func(t *testing.T) {
		resp := doGameDataRequest(t, newPrivateLikeApp(storedZstd, stamp), "/", "gzip")
		if resp.status != fiber.StatusOK || resp.encoding != "gzip" || resp.decoded != plain {
			t.Fatalf("status=%d encoding=%q decoded-match=%v", resp.status, resp.encoding, resp.decoded == plain)
		}
		assertVaryOnce(t, resp.header)
	})

	t.Run("zstd entry to identity client is plain JSON", func(t *testing.T) {
		resp := doGameDataRequest(t, newPrivateLikeApp(storedZstd, stamp), "/", "")
		if resp.status != fiber.StatusOK || resp.encoding != "" || resp.decoded != plain {
			t.Fatalf("status=%d encoding=%q decoded-match=%v", resp.status, resp.encoding, resp.decoded == plain)
		}
	})

	t.Run("legacy gzip entry to zstd-only client still works", func(t *testing.T) {
		resp := doGameDataRequest(t, newPrivateLikeApp(storedGzip, stamp), "/", "zstd")
		if resp.status != fiber.StatusOK || resp.encoding != "zstd" || resp.decoded != plain {
			t.Fatalf("status=%d encoding=%q decoded-match=%v", resp.status, resp.encoding, resp.decoded == plain)
		}
	})

	t.Run("legacy gzip entry to gzip client passes through", func(t *testing.T) {
		resp := doGameDataRequest(t, newPrivateLikeApp(storedGzip, stamp), "/", "gzip")
		if resp.status != fiber.StatusOK || resp.encoding != "gzip" || string(resp.body) != storedGzip {
			t.Fatalf("status=%d encoding=%q passthrough=%v", resp.status, resp.encoding, string(resp.body) == storedGzip)
		}
	})
}

func TestServeGameDataBodyConditionalAndKeyFiltered(t *testing.T) {
	stamp := time.Now().Add(-time.Hour).Unix()
	stampStr := strconv.FormatInt(stamp, 10)
	full := string(syntheticSuiteBody(32 << 10))
	keyed := fmt.Sprintf(`{"upload_time":%d,"userCards":[{"cardId":1,"level":60}]}`, stamp)
	storedFull, err := CompressGameDataBodyZstd([]byte(full))
	if err != nil {
		t.Fatal(err)
	}
	storedKeyed, err := CompressGameDataBodyZstd([]byte(keyed))
	if err != nil {
		t.Fatal(err)
	}

	for _, acceptEncoding := range []string{"zstd", "gzip", ""} {
		t.Run("304/"+acceptEncoding, func(t *testing.T) {
			resp := doGameDataRequest(t, newPrivateLikeApp(storedFull, stamp), "/?"+QueryKnownUploadTime+"="+stampStr, acceptEncoding)
			if resp.status != fiber.StatusNotModified {
				t.Fatalf("status = %d, want 304", resp.status)
			}
			if len(resp.body) != 0 || resp.encoding != "" {
				t.Fatalf("304 carried body=%d bytes encoding=%q", len(resp.body), resp.encoding)
			}
			if got := resp.header.Get(HeaderUploadTime); got != stampStr {
				t.Fatalf("%s = %q, want %q", HeaderUploadTime, got, stampStr)
			}
			assertVaryOnce(t, resp.header)
		})
	}

	t.Run("stale known_upload_time gets the full zstd body", func(t *testing.T) {
		resp := doGameDataRequest(t, newPrivateLikeApp(storedFull, stamp), "/?"+QueryKnownUploadTime+"="+strconv.FormatInt(stamp-10, 10), "zstd")
		if resp.status != fiber.StatusOK || resp.encoding != "zstd" || string(resp.body) != storedFull {
			t.Fatalf("status=%d encoding=%q passthrough=%v", resp.status, resp.encoding, string(resp.body) == storedFull)
		}
		// Full responses carry the stamp only in the body.
		if got := resp.header.Get(HeaderUploadTime); got != "" {
			t.Fatalf("full response must not set %s, got %q", HeaderUploadTime, got)
		}
	})

	t.Run("key filter with upload_time can 304", func(t *testing.T) {
		resp := doGameDataRequest(t, newPrivateLikeApp(storedKeyed, stamp), "/?key=upload_time,userCards&"+QueryKnownUploadTime+"="+stampStr, "zstd")
		if resp.status != fiber.StatusNotModified {
			t.Fatalf("status = %d, want 304", resp.status)
		}
	})

	t.Run("key filter without upload_time is always served", func(t *testing.T) {
		for _, acceptEncoding := range []string{"zstd", "gzip", ""} {
			resp := doGameDataRequest(t, newPrivateLikeApp(storedKeyed, stamp), "/?key=userCards&"+QueryKnownUploadTime+"="+stampStr, acceptEncoding)
			if resp.status != fiber.StatusOK || resp.decoded != keyed {
				t.Fatalf("accept=%q status=%d encoding=%q decoded-match=%v", acceptEncoding, resp.status, resp.encoding, resp.decoded == keyed)
			}
		}
	})
}
