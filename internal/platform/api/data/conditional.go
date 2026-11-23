package data

import (
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"
	"time"

	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"

	"github.com/gofiber/fiber/v3"
)

const (
	// QueryKnownUploadTime is the query parameter carrying the upload_time the
	// client already holds for this document. When it matches the stored stamp
	// the endpoint answers 304 with no body, merging the previous probe+fetch
	// two-round-trip pattern into one request.
	QueryKnownUploadTime = "known_upload_time"
	// HeaderUploadTime echoes the matched stamp on 304 responses. It is set
	// ONLY there: a full response's stamp must be read from the body's own
	// upload_time field, which cannot disagree with the body it arrived in —
	// a header sourced from a fresh game-data read can (the Redis body cache may
	// briefly trail an upload), and a client adopting such a stamp would 304
	// against a stale body until the next upload.
	HeaderUploadTime = "X-Upload-Time"
)

// CheckNotModified implements the ?known_upload_time conditional read shared by
// the public, private, and OAuth2 game-data endpoints. It must run only after
// the caller has fully authorized the request. stamp is the document's current
// upload_time as resolved by ResolveGameDataStamp (0 or negative when the
// document is missing, unstamped, or the stamp could not be resolved). It
// reports true when the client already holds the current document, in which
// case the caller responds 304.
//
// publicKeyFiltered is true when the response uses the shared field allowlist.
// Those callers pass the allowlist they fetched for this request. A conditional
// response requires a projection the full read accepts and that exposes
// upload_time. This lets the client maintain its stamp and keeps the conditional
// response consistent with the full read's data and error semantics.
//
// The check is a pure optimization and never introduces a new failure mode:
// malformed values are ignored the way HTTP ignores an unparsable
// If-Modified-Since, and a missing document or an unresolved stamp falls
// through to the existing full path, which owns the 404/500 semantics of its
// surface.
func CheckNotModified(
	c fiber.Ctx,
	dataType harukiUtils.UploadDataType,
	requestKey string,
	publicKeyFiltered bool,
	publicAllowedKeys []string,
	stamp int64,
) bool {
	if publicKeyFiltered && dataType == harukiUtils.UploadDataTypeMysekai {
		if _, err := mysekaiAllowedProjection("upload_time", publicAllowedKeys); err != nil {
			return false
		}
		if _, err := mysekaiAllowedProjection(requestKey, publicAllowedKeys); err != nil || stamp <= 0 {
			return false
		}
		if !matchesMysekaiETag(c, mysekaiPolicyETag(c.Path(), requestKey, publicAllowedKeys, stamp, "")) {
			return false
		}
	}

	known := ParseKnownUploadTime(c.Query(QueryKnownUploadTime))
	if known <= 0 {
		return false
	}
	if requestKey != "" && !requestKeyIncludesUploadTime(requestKey) {
		// A key filter without upload_time cannot maintain the client's stamp
		// (full responses carry it only in the body), and on allowlisted
		// surfaces it can project an existing document to an empty result,
		// which the full path answers with 404 — never 304 those requests.
		return false
	}
	if publicKeyFiltered {
		if dataType == harukiUtils.UploadDataTypeSuite {
			allowedKeySet := make(map[string]struct{}, len(publicAllowedKeys))
			for _, k := range publicAllowedKeys {
				allowedKeySet[k] = struct{}{}
			}
			if _, ok := allowedKeySet["upload_time"]; !ok {
				return false
			}
			if _, invalid := InvalidSuiteRequestKey(requestKey, allowedKeySet); invalid {
				return false
			}
		} else if _, invalid := InvalidMysekaiRequestKey(requestKey); invalid {
			return false
		}
	} else if _, invalid := InvalidMysekaiRequestKey(requestKey); invalid {
		// Let the full read handle unsupported key syntax.
		return false
	}
	if !notModifiedDecision(known, stamp, time.Now().Unix(), stamp > 0) {
		return false
	}
	// A 304 carries the headers its 200 counterpart would: the full response
	// varies on Accept-Encoding (see ServeGameDataBody), so shared caches must
	// see the same Vary here.
	appendVaryAcceptEncoding(c)
	c.Set(HeaderUploadTime, strconv.FormatInt(stamp, 10))
	return true
}

// CheckUnfilteredMysekaiNotModified validates the full field policy and the
// restoration fingerprint used to render the Mysekai response.
func CheckUnfilteredMysekaiNotModified(c fiber.Ctx, requestKey string, stamp int64, harvestFingerprint string) bool {
	if stamp <= 0 {
		return false
	}
	if _, invalid := InvalidMysekaiRequestKey(requestKey); invalid {
		return false
	}
	etag := mysekaiPolicyETag(c.Path(), requestKey, nil, stamp, harvestFingerprint)
	return matchesMysekaiETag(c, etag) &&
		CheckNotModified(c, harukiUtils.UploadDataTypeMysekai, requestKey, false, nil, stamp)
}

func matchesMysekaiETag(c fiber.Ctx, etag string) bool {
	c.Set(fiber.HeaderETag, etag)
	return matchesPolicyETag(c.Request().Header.PeekAll(fiber.HeaderIfNoneMatch), etag)
}

// Match a quoted validator by weak comparison. A wildcard does not
// establish which field policy the client cached.
func matchesPolicyETag(values [][]byte, current string) bool {
	for _, value := range values {
		remaining := string(value)
		for {
			remaining = strings.TrimLeft(remaining, " \t,")
			remaining = strings.TrimPrefix(remaining, "W/")
			if len(remaining) < 2 || remaining[0] != '"' {
				break
			}
			end := strings.IndexByte(remaining[1:], '"')
			if end < 0 {
				break
			}
			tag := remaining[:end+2]
			remaining = strings.TrimLeft(remaining[end+2:], " \t")
			if remaining != "" && remaining[0] != ',' {
				break
			}
			if tag == current {
				return true
			}
		}
	}
	return false
}

func mysekaiPolicyETag(path, requestKey string, allowedKeys []string, stamp int64, harvestFingerprint string) string {
	representation := fmt.Appendf(nil, "%d:%q:%q:%q", stamp, path, requestKey, allowedKeys)
	if harvestFingerprint != "" {
		representation = fmt.Appendf(representation, ":%q", harvestFingerprint)
	}
	return fmt.Sprintf(`"%x"`, sha256.Sum256(representation))
}

// notModifiedDecision holds the pure 304 decision: the document must exist with
// a positive stamp equal to the client's, and the stamp's second must have
// fully elapsed — upload_time has 1-second resolution, so another upload landing
// in the stamp's own second would overwrite the document without changing the
// stamp, and a 304 issued inside that second could hide it.
func notModifiedDecision(known, current, now int64, found bool) bool {
	return found && current > 0 && current == known && current < now
}

// requestKeyIncludesUploadTime reports whether a non-empty key filter requests
// the upload_time field itself. Keys are matched exactly, mirroring how the
// full path splits them.
func requestKeyIncludesUploadTime(requestKey string) bool {
	for _, key := range strings.Split(requestKey, ",") {
		if key == "upload_time" {
			return true
		}
	}
	return false
}

// ParseKnownUploadTime parses a raw known_upload_time query value, returning 0
// when it is absent or not a positive integer.
func ParseKnownUploadTime(raw string) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	known, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || known <= 0 {
		return 0
	}
	return known
}
