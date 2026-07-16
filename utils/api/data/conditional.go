package data

import (
	"context"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"strconv"
	"strings"
	"time"

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
	// a header sourced from a fresh Mongo read can (the Redis body cache may
	// briefly trail an upload), and a client adopting such a stamp would 304
	// against a stale body until the next upload.
	HeaderUploadTime = "X-Upload-Time"
)

// CheckNotModified implements the ?known_upload_time conditional read shared by
// the public, private, and OAuth2 game-data endpoints. It must run only after
// the caller has fully authorized the request. It reports true when the client
// already holds the current document, in which case the caller responds 304.
//
// publicKeyFiltered is true on the public and OAuth2 surfaces, whose responses
// are filtered through the public-API key allowlist; the private surface passes
// false. The conditional read is disabled whenever answering 304 could diverge
// from what the full path would say about the same request: a non-empty key
// filter must include upload_time (otherwise the client cannot maintain its
// stamp, and on allowlisted surfaces an existing document can project to an
// empty result, which the full path 404s), on filtered surfaces the operator
// must not have excluded upload_time from the allowlist (the 304/200
// distinction is an equality oracle on a stamp the body itself would not
// reveal) and the key filter must be one the full path accepts, and on the
// private surface Mongo-operator/dotted/empty key segments are skipped so the
// full path keeps owning their outcome.
//
// The check is a pure optimization and never introduces a new failure mode:
// malformed values are ignored the way HTTP ignores an unparsable
// If-Modified-Since, and a missing document or a lookup error falls through to
// the existing full path, which owns the 404/500 semantics of its surface.
func CheckNotModified(
	ctx context.Context,
	c fiber.Ctx,
	apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	userID int64,
	server harukiUtils.SupportedDataUploadServer,
	dataType harukiUtils.UploadDataType,
	requestKey string,
	publicKeyFiltered bool,
) bool {
	known := ParseKnownUploadTime(c.Query(QueryKnownUploadTime))
	if known <= 0 {
		return false
	}
	if apiHelper == nil || apiHelper.DBManager == nil || apiHelper.DBManager.Mongo == nil {
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
			publicAPIAllowedKeys := apiHelper.GetPublicAPIAllowedKeys()
			allowedKeySet := make(map[string]struct{}, len(publicAPIAllowedKeys))
			for _, k := range publicAPIAllowedKeys {
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
		// The private surface has no key allowlist, but a dotted, empty, or
		// $-prefixed key segment changes the full path's outcome (nested
		// projection or a Mongo projection error); skip the conditional answer
		// and let the full path behave exactly as it always has.
		return false
	}
	current, found, err := apiHelper.DBManager.Mongo.GetUploadTime(ctx, userID, string(server), dataType)
	if err != nil {
		harukiLogger.Warnf("Failed to read upload_time for conditional request (server=%s,data_type=%s,user_id=%d): %v", server, dataType, userID, err)
		return false
	}
	if !notModifiedDecision(known, current, time.Now().Unix(), found) {
		return false
	}
	c.Set(HeaderUploadTime, strconv.FormatInt(current, 10))
	return true
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
