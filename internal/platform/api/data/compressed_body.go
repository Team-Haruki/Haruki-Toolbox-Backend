package data

import (
	"bytes"
	"io"
	"strings"
	"sync"

	"github.com/gofiber/fiber/v3"
	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
)

// Cached game-data bodies are stored compressed (written once per document
// generation by the singleflight leader) and served as-is to clients whose
// Accept-Encoding admits the stored coding, so the per-request compress
// middleware — which skips any response that already has Content-Encoding set —
// does not re-compress multi-megabyte bodies on every hit.
//
// The stored coding is sniffed from the entry's magic number, so no separate
// marker is kept and entries written by earlier releases keep working until
// they expire:
//   - gzip  (1f 8b):       public and OAuth2 surfaces, and legacy private entries
//   - zstd  (28 b5 2f fd): private surface, whose main consumer (Haruki Cloud)
//     sends only Accept-Encoding: zstd
//   - plain (anything else): entries from before compression and the fallback
//     when compression fails
//
// A JSON body can never begin with 0x1f or 0x28 ('('), so the sniff cannot
// misclassify a plain entry.

const (
	encodingGzip = "gzip"
	encodingZstd = "zstd"
)

const maxPooledGameDataBodyBuffer = 1 << 20

// gameDataZstdWindow keeps the frame window within the 8 MiB limit RFC 9659
// sets for the zstd content coding, so any conforming HTTP client can decode
// a passed-through entry without a larger history buffer.
const gameDataZstdWindow = 8 << 20

// maxDecodedGameDataBody bounds the decoded size of a stored zstd entry when a
// client that does not accept zstd forces a decode. The largest JP suites are
// ~20 MiB, so this only rejects corrupt or hostile cache contents.
const maxDecodedGameDataBody = 256 << 20

type gameDataBodyCompressor struct {
	buffer bytes.Buffer
	writer *gzip.Writer
}

var gameDataBodyCompressorPool = sync.Pool{
	New: func() any {
		// BestSpeed is a valid constant, so this constructor cannot fail.
		writer, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
		return &gameDataBodyCompressor{writer: writer}
	},
}

func (compressor *gameDataBodyCompressor) resetForPool() {
	compressor.writer.Reset(io.Discard)
	if compressor.buffer.Cap() > maxPooledGameDataBodyBuffer {
		// An unusually large or incompressible snapshot must not keep its
		// output allocation alive for subsequent small cache misses.
		compressor.buffer = bytes.Buffer{}
	} else {
		compressor.buffer.Reset()
	}
}

// CompressGameDataBody gzips a marshaled response body for cache storage.
// BestSpeed matches the level the compress middleware already used per
// request, so the miss path pays the same CPU as before while every hit pays
// none.
func CompressGameDataBody(encoded []byte) (string, error) {
	compressor := gameDataBodyCompressorPool.Get().(*gameDataBodyCompressor)
	defer func() {
		compressor.resetForPool()
		gameDataBodyCompressorPool.Put(compressor)
	}()
	compressor.buffer.Grow(len(encoded)/4 + 64)
	compressor.writer.Reset(&compressor.buffer)
	if _, err := compressor.writer.Write(encoded); err != nil {
		return "", err
	}
	if err := compressor.writer.Close(); err != nil {
		return "", err
	}
	// Buffer.String copies the bytes: cache entries remain valid after the
	// compressor and its backing buffer are reused by another goroutine.
	return compressor.buffer.String(), nil
}

// zstd encoders and decoders are pooled with one internal state each (~17 MiB
// for the encoder at an 8 MiB window) rather than shared with GOMAXPROCS
// states: a shared instance keeps every state it has ever used alive, while
// pooled ones are released by the GC once misses quiet down. That matches how
// the gzip writers above and the compress middleware's own encoders are held.
// Neither starts goroutines at concurrency 1, so dropping one needs no Close.
var gameDataZstdEncoderPool = sync.Pool{
	New: func() any {
		encoder, err := zstd.NewWriter(nil,
			zstd.WithEncoderLevel(zstd.SpeedFastest),
			zstd.WithEncoderConcurrency(1),
			zstd.WithWindowSize(gameDataZstdWindow),
			// Frames are self-contained cache entries; the checksum lets both
			// this process and the client detect a corrupt entry.
			zstd.WithEncoderCRC(true),
		)
		if err != nil {
			return err
		}
		return encoder
	},
}

var gameDataZstdDecoderPool = sync.Pool{
	New: func() any {
		decoder, err := zstd.NewReader(nil,
			zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderMaxWindow(gameDataZstdWindow),
			zstd.WithDecoderMaxMemory(maxDecodedGameDataBody),
		)
		if err != nil {
			return err
		}
		return decoder
	},
}

var gameDataZstdBufferPool = sync.Pool{
	New: func() any { return new([]byte) },
}

// CompressGameDataBodyZstd zstd-compresses a marshaled response body for cache
// storage. SpeedFastest costs about the same CPU as gzip level 1, stores ~15%
// smaller on game-data JSON, and is the level the compress middleware used
// when it re-encoded these bodies per request, so zstd clients receive the
// same wire size as before (see BenchmarkGameDataBodyServe).
func CompressGameDataBodyZstd(encoded []byte) (string, error) {
	pooled := gameDataZstdEncoderPool.Get()
	encoder, ok := pooled.(*zstd.Encoder)
	if !ok {
		return "", pooled.(error)
	}
	defer gameDataZstdEncoderPool.Put(encoder)
	bufferRef := gameDataZstdBufferPool.Get().(*[]byte)
	defer func() {
		if cap(*bufferRef) > maxPooledGameDataBodyBuffer {
			*bufferRef = nil
		}
		gameDataZstdBufferPool.Put(bufferRef)
	}()
	out := encoder.EncodeAll(encoded, (*bufferRef)[:0])
	*bufferRef = out
	// The string conversion copies: the pooled buffer can be reused while the
	// cache entry lives on.
	return string(out), nil
}

// StoredGameDataBody is a cached game-data entry: the string the miss path
// produced, or the bytes a cache hit read from Redis (GetRawCacheBytes).
type StoredGameDataBody interface {
	string | []byte
}

// ServeGameDataBody writes a stored cache entry as the JSON response,
// negotiating the transfer form against the client's Accept-Encoding. It
// returns an error only when a compressed entry cannot be decoded; callers
// treat that as a cache miss and rematerialize. A []byte entry is sent as is,
// without a copy, so it must not be modified afterwards.
func ServeGameDataBody[B StoredGameDataBody](c fiber.Ctx, stored B) error {
	body, encoding, err := negotiateStoredBody([]byte(stored), c.Get(fiber.HeaderAcceptEncoding))
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
	// The representation now varies on Accept-Encoding at the origin, so
	// intermediaries (the CDN in front of the public API) must key on it.
	appendVaryAcceptEncoding(c)
	if encoding != "" {
		c.Set(fiber.HeaderContentEncoding, encoding)
	}
	return c.Send(body)
}

// negotiateStoredBody resolves a stored entry to the bytes and
// Content-Encoding to send for the given Accept-Encoding header. A compressed
// entry the client does not accept is decoded to plain JSON, which the
// compress middleware then encodes however that client asked. The entry
// itself is returned, not copied, when it can be sent as stored.
func negotiateStoredBody(stored []byte, acceptEncoding string) (body []byte, encoding string, err error) {
	switch storedEncoding(stored) {
	case encodingGzip:
		if acceptsGzip(acceptEncoding) {
			return stored, encodingGzip, nil
		}
		plain, err := gunzipStoredBody(stored)
		return plain, "", err
	case encodingZstd:
		if acceptsZstd(acceptEncoding) {
			return stored, encodingZstd, nil
		}
		plain, err := unzstdStoredBody(stored)
		return plain, "", err
	default:
		return stored, "", nil
	}
}

func storedEncoding(stored []byte) string {
	switch {
	case isGzipEntry(stored):
		return encodingGzip
	case isZstdEntry(stored):
		return encodingZstd
	default:
		return ""
	}
}

func isGzipEntry(stored []byte) bool {
	return len(stored) >= 2 && stored[0] == 0x1f && stored[1] == 0x8b
}

func isZstdEntry(stored []byte) bool {
	return len(stored) >= 4 && stored[0] == 0x28 && stored[1] == 0xb5 && stored[2] == 0x2f && stored[3] == 0xfd
}

func gunzipStoredBody(stored []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(stored))
	if err != nil {
		return nil, err
	}
	plain, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if err := r.Close(); err != nil {
		return nil, err
	}
	return plain, nil
}

func unzstdStoredBody(stored []byte) ([]byte, error) {
	pooled := gameDataZstdDecoderPool.Get()
	decoder, ok := pooled.(*zstd.Decoder)
	if !ok {
		return nil, pooled.(error)
	}
	defer gameDataZstdDecoderPool.Put(decoder)
	return decoder.DecodeAll(stored, nil)
}

// acceptsGzip reports whether an Accept-Encoding header admits gzip: a gzip
// member with a non-zero q-value, or failing that a * member with a non-zero
// q-value. An absent header serves identity, which matches what the compress
// middleware does for such clients.
func acceptsGzip(acceptEncoding string) bool {
	return acceptsCoding(acceptEncoding, encodingGzip, true)
}

// acceptsZstd reports whether an Accept-Encoding header explicitly admits
// zstd. A bare * is not enough: zstd support is recent and a client that
// merely tolerates "anything" is more safely served gzip or identity.
func acceptsZstd(acceptEncoding string) bool {
	return acceptsCoding(acceptEncoding, encodingZstd, false)
}

// acceptsCoding evaluates Accept-Encoding for one coding. An explicit member
// for the coding decides by its q-value; otherwise, when allowWildcard is set,
// a * member does.
func acceptsCoding(acceptEncoding, coding string, allowWildcard bool) bool {
	wildcard := false
	for member := range strings.SplitSeq(acceptEncoding, ",") {
		token, params, _ := strings.Cut(member, ";")
		token = strings.TrimSpace(token)
		switch {
		case strings.EqualFold(token, coding):
			return !hasZeroQuality(params)
		case allowWildcard && token == "*":
			wildcard = !hasZeroQuality(params)
		}
	}
	return wildcard
}

func hasZeroQuality(params string) bool {
	for param := range strings.SplitSeq(params, ";") {
		param = strings.TrimSpace(param)
		if q, ok := strings.CutPrefix(param, "q="); ok {
			v := strings.TrimSpace(q)
			if v == "0" || strings.HasPrefix(v, "0.") && strings.Trim(v[2:], "0") == "" {
				return true
			}
		}
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
