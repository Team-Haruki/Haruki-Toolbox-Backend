package data

import (
	"bytes"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/valyala/fasthttp"
)

func serveStored(t *testing.T, stored []byte, acceptEncoding string) *fasthttp.RequestCtx {
	t.Helper()
	handler := newGameDataBenchmarkApp(func(c fiber.Ctx) error { return ServeGameDataBody(c, stored) })
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod(fasthttp.MethodGet)
	ctx.Request.SetRequestURI("/")
	if acceptEncoding != "" {
		ctx.Request.Header.Set(fasthttp.HeaderAcceptEncoding, acceptEncoding)
	}
	handler(ctx)
	if ctx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("status = %d", ctx.Response.StatusCode())
	}
	return ctx
}

// A cache hit read as []byte goes to the response as is: no copy, and nothing
// on the way (fasthttp, the compress middleware) writes into it.
func TestServeGameDataBodyBytesPassThroughWithoutCopy(t *testing.T) {
	plain := []byte(`{"userGamedata":{"name":"synthetic"}}`)
	stored, err := CompressGameDataBodyZstd(plain)
	if err != nil {
		t.Fatal(err)
	}
	entry := []byte(stored)
	snapshot := bytes.Clone(entry)

	ctx := serveStored(t, entry, "zstd")
	body := ctx.Response.Body()
	if string(ctx.Response.Header.ContentEncoding()) != encodingZstd || !bytes.Equal(body, entry) {
		t.Fatalf("zstd client got encoding %q, %d bytes", ctx.Response.Header.ContentEncoding(), len(body))
	}
	if &body[0] != &entry[0] {
		t.Fatal("the stored entry was copied on the way to the response")
	}

	// A client without zstd gets the decoded body; the entry stays intact.
	ctx = serveStored(t, entry, "")
	if !bytes.Equal(ctx.Response.Body(), plain) {
		t.Fatalf("identity client body = %q", ctx.Response.Body())
	}
	if !bytes.Equal(entry, snapshot) {
		t.Fatal("serving modified the stored entry")
	}
}

func TestServeGameDataBodyAcceptsStringAndBytes(t *testing.T) {
	stored, err := CompressGameDataBody([]byte(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	fromString := newGameDataBenchmarkApp(func(c fiber.Ctx) error { return ServeGameDataBody(c, stored) })
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.SetRequestURI("/")
	ctx.Request.Header.Set(fasthttp.HeaderAcceptEncoding, "gzip")
	fromString(ctx)
	viaBytes := serveStored(t, []byte(stored), "gzip")
	if !bytes.Equal(ctx.Response.Body(), viaBytes.Response.Body()) {
		t.Fatal("string and []byte entries served differently")
	}
}
