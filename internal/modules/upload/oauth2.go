package upload

import (
	"fmt"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"

	userCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/oauth2"
)

// Delegated upload: a third-party client holding a game-data:write token uploads
// on behalf of the account owner, without going through the owner's own session
// or proxy.
//
// The payload is the RAW game payload, exactly what the manual and proxy routes
// take. That is not a convenience choice — it is what keeps the anti-forgery
// property. HandleUpload decrypts it and then requires the game user id the GAME
// itself wrote inside the payload to match the account being written to
// (ExtractGameUserIDForExpected). If clients could post already-decoded JSON,
// that id would be something the client wrote, and holding a token for one
// account would let a client write any content it liked into it.
//
// Authorization is deliberately STRICTER than the read side. Reads are permitted
// through a grant — another owner sharing their data with you — but a grant is
// permission to LOOK at someone's data, not to overwrite it. A delegated write
// therefore requires the binding to be owned outright.
func handleOAuth2Upload(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, dependencies Dependencies) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()

		authUserID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}

		serverStr := c.Params("server")
		dataTypeStr := c.Params("data_type")
		gameUserIDStr := c.Params("user_id")

		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid server")
		}
		dataType, err := harukiUtils.ParseUploadDataType(dataTypeStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid data_type")
		}
		gameUserID, err := strconv.ParseInt(gameUserIDStr, 10, 64)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "Invalid game user_id, it must be integer")
		}

		access, err := apiHelper.DBManager.DB.CanAccessGameAccountData(
			ctx, authUserID, string(server), gameUserIDStr, string(dataType), time.Now().UTC())
		if err != nil {
			harukiLogger.Errorf("Failed to verify oauth2 upload access: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to query game account binding")
		}
		if access == nil || !access.Allowed {
			// Same message for "no such binding" and "not yours": telling them
			// apart turns this endpoint into an oracle for which game accounts
			// are bound to the service.
			return harukiAPIHelper.ErrorNotFound(c, "game account binding not found or not owned by you")
		}
		if access.ViaGrant {
			// A grant conveys read access. Writing through one would let a
			// recipient overwrite the granter's data, which is not what anyone
			// consented to when sharing it.
			return harukiAPIHelper.ErrorForbidden(c, "delegated upload requires an owned binding; granted access is read-only")
		}

		body := c.Request().Body()
		if len(body) == 0 {
			return harukiAPIHelper.ErrorBadRequest(c, "empty upload body")
		}

		// From here the delegated path is the SAME path as every other upload:
		// ban checks, binding ownership, decryption, payload identity, the
		// per-account policy and the audit log all come from HandleUpload. The
		// upload method is recorded so an audit trail can tell a delegated
		// write from one the owner made.
		if _, err := HandleUpload(
			ctx, body, server, dataType, &gameUserID, &authUserID,
			apiHelper, dependencies, harukiUtils.UploadMethodOAuth2,
		); err != nil {
			if mapped := mapUploadProcessingError(err); mapped != nil {
				return harukiAPIHelper.UpdatedDataResponse[string](c, mapped.Code, mapped.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "failed to process upload")
		}

		return harukiAPIHelper.SuccessResponse[string](c,
			fmt.Sprintf("%s server user %d successfully uploaded %s data.", serverStr, gameUserID, dataType), nil)
	}
}

func registerOAuth2UploadRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, dependencies Dependencies) {
	if dependencies.HydraConfig == nil {
		return
	}
	if dependencies.OAuth2ClientActiveChecker == nil {
		// Refuse to expose a delegated WRITE without the disabled-client check.
		// Registering it anyway would mean a revoked integration keeps its
		// ability to overwrite player data for as long as its token lives.
		harukiLogger.Errorf("delegated OAuth2 upload route NOT registered: no client-active checker was supplied")
		return
	}
	o := apiHelper.Router.Group("/api/oauth2/game-data")
	o.Post("/:server/:data_type/:user_id",
		harukiOAuth2.VerifyOAuth2Token(
			dependencies.HydraConfig,
			apiHelper.DBManager.DB,
			harukiOAuth2.ScopeGameDataWrite,
			dependencies.OAuth2ClientActiveChecker,
		),
		handleOAuth2Upload(apiHelper, dependencies),
	)
}
