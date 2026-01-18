package ios

import (
	"fmt"
	harukiAPIHelper "haruki-suite/utils/api"
	iosGen "haruki-suite/utils/api/ios"
	"haruki-suite/utils/database/postgresql/iosscriptcode"
	"strconv"

	"github.com/gofiber/fiber/v3"
)

func handleScriptGeneration(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		uploadCode := c.Params("upload_code")
		if uploadCode == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "missing upload_code")
		}
		_, err := apiHelper.DBManager.DB.IOSScriptCode.Query().
			Where(iosscriptcode.UploadCodeEQ(uploadCode)).
			Only(ctx)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "invalid upload code")
		}
		chunkSizeMB := 1
		if chunkStr := c.Query("chunk"); chunkStr != "" {
			parsed, err := strconv.Atoi(chunkStr)
			if err != nil || parsed < 1 || parsed > 10 {
				return harukiAPIHelper.ErrorBadRequest(c, "chunk must be between 1 and 10 MB")
			}
			chunkSizeMB = parsed
		}
		endpointStr := c.Query("endpoint", "direct")
		endpointType, ok := iosGen.ParseEndpointType(endpointStr)
		if !ok {
			return harukiAPIHelper.ErrorBadRequest(c, fmt.Sprintf("unsupported endpoint: %s. Supported: direct, cdn", endpointStr))
		}
		endpoint := getEndpoint(endpointType)
		script := iosGen.GenerateScript(uploadCode, chunkSizeMB, endpoint)
		c.Set("Content-Type", "application/javascript; charset=utf-8")
		return c.SendString(script)
	}
}
