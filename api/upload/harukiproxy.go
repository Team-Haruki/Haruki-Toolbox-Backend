package upload

import (
	"context"
	"fmt"
	harukiRootApi "haruki-suite/api"
	harukiUtils "haruki-suite/utils"
	harukiMongo "haruki-suite/utils/database/mongo"
	"regexp"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/hashicorp/go-version"
	"github.com/redis/go-redis/v9"
)

var harukiProxyClientUserAgent *string
var harukiProxyClientVersion *string
var harukiProxyClientSecret *string

func validateHarukiProxyClientHeader() fiber.Handler {
	if harukiProxyClientUserAgent != nil && harukiProxyClientVersion != nil && harukiProxyClientSecret != nil {
		return func(c *fiber.Ctx) error {
			requestUserAgent := c.Get("User-Agent")
			requestSecret := c.Get("X-Haruki-Toolbox-Secret")

			if requestSecret != *harukiProxyClientSecret {
				return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
					Message: "Invalid HarukiProxy Secret",
					Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
				})
			}

			re := regexp.MustCompile(`^([A-Za-z0-9\-]+)/([vV][0-9]+\.[0-9]+\.[0-9]+(?:-[a-zA-Z0-9]+)?)$`)
			matches := re.FindStringSubmatch(requestUserAgent)
			if len(matches) < 3 {
				return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
					Message: "Invalid User-Agent format",
					Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
				})
			}

			uaName := matches[1]
			if *harukiProxyClientUserAgent != uaName {
				return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
					Message: "Invalid User-Agent name",
					Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
				})
			}

			clientVerStr := matches[2]
			clientVerStr = strings.TrimPrefix(clientVerStr, "v")
			minVerStr := strings.TrimPrefix(*harukiProxyClientVersion, "v")
			clientVer, err1 := version.NewVersion(clientVerStr)
			minVer, err2 := version.NewVersion(minVerStr)
			if err1 != nil || err2 != nil {
				return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
					Message: "Invalid version string",
					Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
				})
			}
			if clientVer.LessThan(minVer) {
				return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
					Message: fmt.Sprintf("Client version %s is below minimum required %s", clientVerStr, *harukiProxyClientVersion),
					Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
				})
			}

			return c.Next()
		}
	}

	return func(c *fiber.Ctx) error {
		return c.Next()
	}
}

func registerHarukiProxyRoutes(app *fiber.App, mongoManager *harukiMongo.MongoDBManager, redisClient *redis.Client, proxyUserAgent, proxyVersion, proxySecret *string) {
	api := app.Group("/harukiproxy/:server/:upload_type/:policy", validateHarukiProxyClientHeader())

	if proxyUserAgent != nil && proxyVersion != nil && proxySecret != nil {
		harukiProxyClientUserAgent = proxyUserAgent
		harukiProxyClientSecret = proxySecret
	}

	api.Post("/upload", func(c *fiber.Ctx) error {
		serverStr := c.Params("server")
		policyStr := c.Params("policy")

		_, err := harukiUtils.ParseUploadPolicy(policyStr)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: err.Error(),
				Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
			})
		}

		result, err := HandleUpload(
			context.Background(),
			c.Request().Body(),
			serverStr,
			policyStr,
			mongoManager,
			redisClient,
			string(harukiUtils.UploadDataTypeSuite),
			nil,
		)

		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: err.Error(),
				Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
			})
		}

		if result.UserID != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: fmt.Sprintf("%s server user %d successfully uploaded suite data.", serverStr, *result.UserID),
			})
		} else {
			fmt.Println("Debug: UserID is nil")
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: fmt.Sprintf("%s server user with unknown ID successfully uploaded suite data.", serverStr),
			})
		}
	})

	api.Post("/:user_id/upload", func(c *fiber.Ctx) error {
		serverStr := c.Params("server")
		policyStr := c.Params("policy")
		userIdStr := c.Params("user_id")

		_, err := harukiUtils.ParseUploadPolicy(policyStr)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: err.Error(),
				Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
			})
		}

		userId, err := strconv.ParseInt(userIdStr, 10, 64)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: err.Error(),
			})
		}

		result, err := HandleUpload(
			context.Background(),
			c.Request().Body(),
			serverStr,
			policyStr,
			mongoManager,
			redisClient,
			string(harukiUtils.UploadDataTypeMysekai),
			&userId,
		)

		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: err.Error(),
				Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
			})
		}

		if result.UserID != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: fmt.Sprintf("%s server user %d successfully uploaded mysekai data.", serverStr, *result.UserID),
			})
		} else {
			fmt.Println("Debug: UserID is nil")
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: fmt.Sprintf("%s server user with unknown ID successfully uploaded mysekai data.", serverStr),
			})
		}
	})

}
