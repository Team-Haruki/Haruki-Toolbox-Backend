package data

import (
	"bytes"
	"io"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/klauspost/compress/gzip"
)

// Cached game-data bodies are stored gzip-compressed (written once per
// document generation by the singleflight leader) and served as-is to clients
// whose Accept-Encoding admits gzip, so the per-request compress middleware —
// which fasthttp skips whenever Content-Encoding is already set — no longer
// re-compresses multi-megabyte bodies on every hit. Entries are sniffed by the
// gzip magic number: a JSON body can never begin with 0x1f 0x8b, so legacy
// plain entries from before this scheme (and the plain fallback below) stay
// servable without a key migration.

// CompressGameDataBody gzips a marshaled response body for cache storage.
// BestSpeed matches the level the compress middleware already used per
// request, so the miss path pays the same CPU as before while every hit pays
// none.
func CompressGameDataBody(encoded []byte) (string, error) {
	var buf bytes.Buffer
	buf.Grow(len(encoded)/4 + 64)
	w, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return "", err
	}
	if _, err := w.Write(encoded); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// ServeGameDataBody writes a stored cache entry as the JSON response,
// negotiating the transfer form against the client's Accept-Encoding. It
// returns an error only when a compressed entry cannot be decoded; callers
// treat that as a cache miss and rematerialize.
func ServeGameDataBody(c fiber.Ctx, stored string) error {
	body, encoding, err := negotiateStoredBody(stored, c.Get(fiber.HeaderAcceptEncoding))
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
	// The representation now varies on Accept-Encoding at the origin, so
	// intermediaries (EdgeOne in front of the public API) must key on it.
	appendVaryAcceptEncoding(c)
	if encoding != "" {
		c.Set(fiber.HeaderContentEncoding, encoding)
	}
	return c.Send(body)
}

// negotiateStoredBody resolves a stored entry to the bytes and
// Content-Encoding to send for the given Accept-Encoding header.
func negotiateStoredBody(stored, acceptEncoding string) (body []byte, encoding string, err error) {
	if !isGzipEntry(stored) {
		return []byte(stored), "", nil
	}
	if acceptsGzip(acceptEncoding) {
		return []byte(stored), "gzip", nil
	}
	r, err := gzip.NewReader(strings.NewReader(stored))
	if err != nil {
		return nil, "", err
	}
	plain, err := io.ReadAll(r)
	if err != nil {
		return nil, "", err
	}
	if err := r.Close(); err != nil {
		return nil, "", err
	}
	return plain, "", nil
}

func isGzipEntry(stored string) bool {
	return len(stored) >= 2 && stored[0] == 0x1f && stored[1] == 0x8b
}

// acceptsGzip reports whether an Accept-Encoding header admits gzip: a gzip or
// * member whose q-value is not zero. An absent header serves identity, which
// matches what the compress middleware does for such clients today.
func acceptsGzip(acceptEncoding string) bool {
	for _, member := range strings.Split(acceptEncoding, ",") {
		parts := strings.Split(member, ";")
		token := strings.TrimSpace(parts[0])
		if !strings.EqualFold(token, "gzip") && token != "*" {
			continue
		}
		for _, param := range parts[1:] {
			param = strings.TrimSpace(param)
			if q, ok := strings.CutPrefix(param, "q="); ok {
				if v := strings.TrimSpace(q); v == "0" || strings.HasPrefix(v, "0.") && strings.Trim(v[2:], "0") == "" {
					return false
				}
			}
		}
		return true
	}
	return false
}

func appendVaryAcceptEncoding(c fiber.Ctx) {
	vary := c.GetRespHeader(fiber.HeaderVary)
	if vary == "" {
		c.Set(fiber.HeaderVary, fiber.HeaderAcceptEncoding)
		return
	}
	for _, member := range strings.Split(vary, ",") {
		member = strings.TrimSpace(member)
		if member == "*" || strings.EqualFold(member, fiber.HeaderAcceptEncoding) {
			return
		}
	}
	c.Set(fiber.HeaderVary, vary+", "+fiber.HeaderAcceptEncoding)
}
