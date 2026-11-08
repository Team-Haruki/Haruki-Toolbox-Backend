package upload

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"

	"github.com/gofiber/fiber/v3"
)

// HarukiProxy uploads to Haruki Toolbox only with an OAuth2 login
// (/harukiproxy/v3, a game-data:write bearer token). The shared-key channel
// (X-Haruki-Toolbox-Secret plus an AES-GCM wrapped body, on the v2 paths) is
// closed: its routes answer protocol_retired, the v3 route answers
// oauth2_required to a request that carries no bearer token, and no shared
// key is configured or accepted anywhere.
const (
	proxyOAuth2RequiredCode    = "oauth2_required"
	proxyOAuth2RequiredMessage = "Haruki Toolbox accepts HarukiProxy uploads only with an OAuth2 login; connect the app to Haruki Toolbox (shared-key uploads are closed)"
	proxyRetiredCode           = "protocol_retired"
	proxyRetiredMessage        = "Shared-key HarukiProxy upload is closed; update HarukiProxy and log in to Haruki Toolbox with OAuth2 (/harukiproxy/v3)"
)

// harukiProxySharedKeySunset is when the shared-key channel stopped taking
// uploads (an absolute UTC+8 instant); its routes keep sending it as Sunset.
var harukiProxySharedKeySunset = time.Date(2026, time.November, 1, 0, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))

// requireProxyOAuth2Bearer refuses a v3 upload without a bearer token before
// any token check, so a client still using the shared key (or a custom
// endpoint set to Haruki Toolbox, which sends the shared key) is told to log
// in instead of seeing an expired-token error.
func requireProxyOAuth2Bearer(c fiber.Ctx) error {
	scheme, _, _ := strings.Cut(strings.TrimSpace(c.Get(fiber.HeaderAuthorization)), " ")
	if !strings.EqualFold(scheme, "Bearer") {
		c.Set(fiber.HeaderWWWAuthenticate, `Bearer error="invalid_request", error_description="OAuth2 login required"`)
		return proxyResponse(c, fiber.StatusUnauthorized, proxyOAuth2RequiredCode, proxyOAuth2RequiredMessage, false, nil)
	}
	return c.Next()
}

// harukiProxySharedKeyRetired answers every request on the old shared-key
// upload paths. It reads no header or body: nothing on these paths is
// authenticated, decrypted or stored any more.
func harukiProxySharedKeyRetired(c fiber.Ctx) error {
	c.Set("Sunset", harukiProxySharedKeySunset.UTC().Format(http.TimeFormat))
	return proxyResponse(c, fiber.StatusGone, proxyRetiredCode, proxyRetiredMessage, false, nil)
}

func handleHarukiProxyUpload(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, dependencies Dependencies) fiber.Handler {
	return func(c fiber.Ctx) error {
		a := proxyAttempt(c)
		invalid := func(message string) error {
			return proxyResponse(c, 400, "invalid_upload_payload", message, false, nil)
		}
		server, err := harukiUtils.ParseSupportedDataUploadServer(c.Params("server"))
		if err != nil {
			return invalid("invalid server")
		}
		dataType, err := harukiUtils.ParseUploadDataType(c.Params("data_type"))
		if err != nil {
			return invalid("invalid data_type")
		}
		gameUserID, err := strconv.ParseInt(c.Params("user_id"), 10, 64)
		if err != nil || gameUserID <= 0 {
			return invalid("invalid user_id")
		}
		c.Locals(proxyIngressKey{}, "accepted")
		id, ok := c.Locals("userID").(string)
		if !ok || id == "" {
			return proxyResponse(c, 401, "invalid_token", "OAuth2 authentication required", false, nil)
		}
		_, err = runUpload(c.Context(), c.Request().Body(), server, dataType, &gameUserID, &id, apiHelper, oauthUploadDependencies(c, apiHelper, dependencies), harukiUtils.UploadMethodHarukiProxy, a)
		if reason, skipped := uploadSkipReason(err); skipped {
			return proxySkippedResponse(c, reason)
		}
		if err != nil {
			if a.ErrorCode == "" {
				classifyAttemptFailure(a, uploadStageBuildContext, err)
			}
			message := "failed to process upload"
			if a.HTTPStatus == 403 {
				message = "upload not allowed"
			}
			return proxyResponse(c, a.HTTPStatus, a.ErrorCode, message, a.Retryable, nil)
		}
		return proxyResponse(c, 200, "", "Upload successful", false, nil)
	}
}

func registerHarukiProxyRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, dependencies Dependencies) {
	for _, prefix := range []string{"/harukiproxy", "/api/harukiproxy"} {
		apiHelper.Router.Post(prefix+"/v3/:server/:user_id/:data_type/upload",
			proxyIngress(apiHelper, "3"), requireProxyOAuth2Bearer, proxyOAuthAuthentication(apiHelper, dependencies), validateProxyV3Client(apiHelper, dependencies), handleHarukiProxyUpload(apiHelper, dependencies))
		apiHelper.Router.Post(prefix+"/:server/:user_id/:data_type/upload",
			proxyIngress(apiHelper, "2"), harukiProxySharedKeyRetired)
	}
}
