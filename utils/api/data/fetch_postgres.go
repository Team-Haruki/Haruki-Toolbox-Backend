package data

import (
	"context"
	"errors"

	"github.com/bytedance/sonic"

	"github.com/gofiber/fiber/v3"

	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiGameData "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
)

// The PostgreSQL read path.
//
// It returns a fully rendered response BODY rather than a Go value: the measured
// 14.5-47x read win comes entirely from not re-encoding, so a value handed back
// here and marshalled by the caller would give all of it back. Callers that get
// a non-nil body serve it directly.

// suiteBodyFromPostgres renders a suite response from PostgreSQL.
func suiteBodyFromPostgres(
	ctx context.Context,
	store *harukiGameData.Store,
	userID int64,
	server harukiUtils.SupportedDataUploadServer,
	keys []string,
	unwrapSingle bool,
) ([]byte, error) {
	row, err := store.Fetch(ctx, userID, string(server), keys)
	if err != nil {
		if errors.Is(err, harukiGameData.ErrNoRow) {
			return nil, fiber.NewError(fiber.StatusNotFound, "Player data not found.")
		}
		harukiLogger.Errorf("Failed to fetch game data: %v", err)
		return nil, fiber.NewError(fiber.StatusInternalServerError, "failed to get user data")
	}
	// MongoDB's inclusion projection returned an EMPTY document when none of the
	// requested keys existed, and the handler turned that into a 404. In
	// PostgreSQL the row exists regardless, so without this the same request
	// would answer 200 with a body full of [].
	if !row.HasAny(keys) {
		return nil, fiber.NewError(fiber.StatusNotFound, "Player data not found.")
	}
	body, err := row.SuiteBody(keys, unwrapSingle)
	if err != nil {
		harukiLogger.Errorf("Failed to render suite body: %v", err)
		return nil, fiber.NewError(fiber.StatusInternalServerError, "failed to get user data")
	}
	return body, nil
}

// mysekaiBodyFromPostgres renders a mysekai response from PostgreSQL.
func mysekaiBodyFromPostgres(
	ctx context.Context,
	store *harukiGameData.Store,
	userID int64,
	server harukiUtils.SupportedDataUploadServer,
	keys []string,
) ([]byte, error) {
	row, err := store.Fetch(ctx, userID, string(server), keys)
	if err != nil {
		if errors.Is(err, harukiGameData.ErrNoRow) {
			return nil, fiber.NewError(fiber.StatusNotFound, "Player data not found.")
		}
		harukiLogger.Errorf("Failed to fetch game data: %v", err)
		return nil, fiber.NewError(fiber.StatusInternalServerError, "failed to get user data")
	}
	if len(keys) > 0 && !row.HasAny(keys) {
		return nil, fiber.NewError(fiber.StatusNotFound, "Player data not found.")
	}
	body, err := row.MysekaiBody(keys)
	if err != nil {
		harukiLogger.Errorf("Failed to render mysekai body: %v", err)
		return nil, fiber.NewError(fiber.StatusInternalServerError, "failed to get user data")
	}
	return body, nil
}

// PrivateBodyFromPostgres renders the private-surface body.
//
// Its 404 rule differs from every other surface on purpose: here 404 means the
// ROW is absent, never "the requested keys were empty". Harmonising the two
// would reintroduce, by a new mechanism, the bug the private projection test was
// written to block.
// fetchKeys select which columns to read (trimmed, mirroring
// buildKeyProjection); renderKeys are the keys the body is built from
// (untrimmed, mirroring buildPrivateDataResponse). They differ only when a
// caller pads a key with spaces, which today selects the column and then fails
// to find it — answering null. That is preserved, not fixed.
func PrivateBodyFromPostgres(
	ctx context.Context,
	store *harukiGameData.Store,
	userID int64,
	server string,
	fetchKeys []string,
	renderKeys []string,
) ([]byte, error) {
	row, err := store.Fetch(ctx, userID, server, fetchKeys)
	if err != nil {
		if errors.Is(err, harukiGameData.ErrNoRow) {
			return nil, fiber.NewError(fiber.StatusNotFound, "Player data not found.")
		}
		harukiLogger.Errorf("Failed to fetch game data: %v", err)
		return nil, fiber.NewError(fiber.StatusInternalServerError, "failed to get user data")
	}
	body, err := row.PrivateBody(renderKeys)
	if err != nil {
		harukiLogger.Errorf("Failed to render private body: %v", err)
		return nil, fiber.NewError(fiber.StatusInternalServerError, "failed to get user data")
	}
	return body, nil
}

// EncodeGameDataBody turns a game-data handler result into response bytes.
//
// It exists because of one sharp edge: the PostgreSQL path returns an ALREADY
// RENDERED body as []byte, and handing that to a JSON marshaller does not
// re-emit it — encoding/json and sonic both encode a []byte as a base64 STRING.
// The response would be a valid JSON document containing garbage, with no error
// anywhere, so the failure would only ever be noticed by a client.
func EncodeGameDataBody(resp any) ([]byte, error) {
	if body, ok := resp.([]byte); ok {
		return body, nil
	}
	return sonic.Marshal(resp)
}

// SendGameDataResponse writes a game-data handler result to the response.
//
// Same hazard as EncodeGameDataBody: fiber's c.JSON would base64-encode a
// []byte, so the already-rendered PostgreSQL body has to be sent verbatim.
func SendGameDataResponse(c fiber.Ctx, resp any) error {
	if body, ok := resp.([]byte); ok {
		c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
		return c.Send(body)
	}
	return c.JSON(resp)
}
