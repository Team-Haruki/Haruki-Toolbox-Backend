package ios

import (
	"fmt"
	harukiConfig "haruki-suite/config"
	harukiAPIHelper "haruki-suite/utils/api"
	iosGen "haruki-suite/utils/api/ios"
	"haruki-suite/utils/database/postgresql/iosscriptcode"
	"haruki-suite/utils/database/postgresql/user"
	"regexp"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
)

// Module path pattern: /{regions}-haruki-toolbox-{datatypes}.{ext}
// Example: jp-en-haruki-toolbox-suite-mysekai.sgmodule
var modulePathPattern = regexp.MustCompile(`^([a-z-]+)-haruki-toolbox-([a-z_-]+)\.(\w+)$`)

// getEndpoint returns the appropriate endpoint URL based on endpoint type
func getEndpoint(endpointType iosGen.EndpointType) string {
	if endpointType == iosGen.EndpointTypeCDN && harukiConfig.Cfg.Backend.BackendCDNURL != "" {
		return harukiConfig.Cfg.Backend.BackendCDNURL
	}
	return harukiConfig.Cfg.Backend.BackendURL
}

// handleModuleGeneration handles requests to generate iOS proxy modules
// Route: /ios/module/:upload_code/*filepath
// Query params: mode=proxy|script, endpoint=cdn|direct, chunk=1-10
func handleModuleGeneration(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		uploadCode := c.Params("upload_code")
		filepath := c.Params("*")

		if uploadCode == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "missing upload_code")
		}
		if filepath == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "missing module filename")
		}

		// Validate upload code
		record, err := apiHelper.DBManager.DB.IOSScriptCode.Query().
			Where(iosscriptcode.UploadCodeEQ(uploadCode)).
			Only(ctx)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "invalid upload code")
		}
		userID := record.UserID

		// Remove leading slash if present
		filepath = strings.TrimPrefix(filepath, "/")

		// Parse the filepath
		matches := modulePathPattern.FindStringSubmatch(filepath)
		if matches == nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid module path format. Expected: {regions}-haruki-toolbox-{datatypes}.{ext}")
		}

		regionsStr := matches[1]
		dataTypesStr := matches[2]
		ext := matches[3]

		// Parse proxy app from extension
		app, ok := iosGen.ParseProxyApp(ext)
		if !ok {
			return harukiAPIHelper.ErrorBadRequest(c, fmt.Sprintf("unsupported extension: %s. Supported: sgmodule, lnplugin, conf, stoverride", ext))
		}

		// Parse endpoint type (default: direct)
		endpointStr := c.Query("endpoint", "direct")
		endpointType, ok := iosGen.ParseEndpointType(endpointStr)
		if !ok {
			return harukiAPIHelper.ErrorBadRequest(c, fmt.Sprintf("unsupported endpoint: %s. Supported: direct, cdn", endpointStr))
		}

		// Parse chunk size from query param (default: 1 MB, max: 10 MB)
		chunkSizeMB := 1
		if chunkStr := c.Query("chunk"); chunkStr != "" {
			parsed, err := strconv.Atoi(chunkStr)
			if err != nil || parsed < 1 || parsed > 10 {
				return harukiAPIHelper.ErrorBadRequest(c, "chunk must be between 1 and 10 MB")
			}
			chunkSizeMB = parsed
		}

		// Parse regions
		regionStrs := strings.Split(regionsStr, "-")
		var regions []iosGen.Region
		hasCN := false
		for _, rs := range regionStrs {
			region, ok := iosGen.ParseRegion(rs)
			if !ok {
				return harukiAPIHelper.ErrorBadRequest(c, fmt.Sprintf("unsupported region: %s. Supported: jp, en, tw, kr, cn", rs))
			}
			regions = append(regions, region)
			if region == iosGen.RegionCN {
				hasCN = true
			}
		}

		// Parse data types
		dataTypeStrs := strings.Split(dataTypesStr, "-")
		var dataTypes []iosGen.DataType
		hasMysekai := false
		hasMysekaiForce := false
		hasCNMysekaiType := false

		for _, dts := range dataTypeStrs {
			dt, ok := iosGen.ParseDataType(dts)
			if !ok {
				return harukiAPIHelper.ErrorBadRequest(c, fmt.Sprintf("unsupported data type: %s. Supported: suite, mysekai, mysekai_force, mysekai-birthday", dts))
			}

			// Track mysekai types for CN check
			if dt == iosGen.DataTypeMysekai || dt == iosGen.DataTypeMysekaiForce || dt == iosGen.DataTypeMysekaiBirthday {
				hasCNMysekaiType = true
			}
			if dt == iosGen.DataTypeMysekai {
				hasMysekai = true
			}
			if dt == iosGen.DataTypeMysekaiForce {
				hasMysekaiForce = true
			}

			dataTypes = append(dataTypes, dt)
		}

		// Deduplication: if both mysekai and mysekai_force are selected, keep only mysekai_force
		if hasMysekai && hasMysekaiForce {
			var filtered []iosGen.DataType
			for _, dt := range dataTypes {
				if dt != iosGen.DataTypeMysekai {
					filtered = append(filtered, dt)
				}
			}
			dataTypes = filtered
		}

		// CN mysekai permission check
		if hasCN && hasCNMysekaiType {
			u, err := apiHelper.DBManager.DB.User.Query().Where(user.IDEQ(userID)).Only(ctx)
			if err != nil {
				return harukiAPIHelper.ErrorBadRequest(c, "user not found")
			}
			if !u.AllowCnMysekai {
				return harukiAPIHelper.ErrorForbidden(c, "CN mysekai data types require allow_cn_mysekai permission")
			}
		}

		// Determine upload mode from query parameter (default: proxy)
		modeStr := c.Query("mode", "proxy")
		var mode iosGen.UploadMode
		switch modeStr {
		case "proxy":
			mode = iosGen.UploadModeProxy
		case "script":
			mode = iosGen.UploadModeScript
			// Quantumult X does not support script mode
			if app == iosGen.ProxyAppQuantumultX {
				return harukiAPIHelper.ErrorBadRequest(c, "Quantumult X does not support script upload mode. Use proxy mode instead.")
			}
		default:
			return harukiAPIHelper.ErrorBadRequest(c, fmt.Sprintf("unsupported mode: %s. Supported: proxy, script", modeStr))
		}

		// Build request
		req := &iosGen.ModuleRequest{
			UploadCode:  uploadCode,
			Regions:     regions,
			DataTypes:   dataTypes,
			App:         app,
			Mode:        mode,
			ChunkSizeMB: chunkSizeMB,
		}

		// Generate module with selected endpoint
		endpoint := getEndpoint(endpointType)
		content, err := iosGen.GenerateModule(req, endpoint)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to generate module")
		}

		// Set headers
		c.Set("Content-Type", app.ContentType())
		c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", req.FileName()))

		return c.SendString(content)
	}
}

// handleScriptGeneration serves the JavaScript upload script
// Route: /ios/script/:upload_code/haruki-toolbox.js
// Query params: chunk=1-10, endpoint=cdn|direct
func handleScriptGeneration(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		uploadCode := c.Params("upload_code")

		if uploadCode == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "missing upload_code")
		}

		// Validate upload code
		_, err := apiHelper.DBManager.DB.IOSScriptCode.Query().
			Where(iosscriptcode.UploadCodeEQ(uploadCode)).
			Only(ctx)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "invalid upload code")
		}

		// Parse chunk size (default: 1 MB)
		chunkSizeMB := 1
		if chunkStr := c.Query("chunk"); chunkStr != "" {
			parsed, err := strconv.Atoi(chunkStr)
			if err != nil || parsed < 1 || parsed > 10 {
				return harukiAPIHelper.ErrorBadRequest(c, "chunk must be between 1 and 10 MB")
			}
			chunkSizeMB = parsed
		}

		// Parse endpoint type (default: direct)
		endpointStr := c.Query("endpoint", "direct")
		endpointType, ok := iosGen.ParseEndpointType(endpointStr)
		if !ok {
			return harukiAPIHelper.ErrorBadRequest(c, fmt.Sprintf("unsupported endpoint: %s. Supported: direct, cdn", endpointStr))
		}

		// Generate script
		endpoint := getEndpoint(endpointType)
		script := iosGen.GenerateScript(uploadCode, chunkSizeMB, endpoint)

		c.Set("Content-Type", "application/javascript; charset=utf-8")
		return c.SendString(script)
	}
}

func RegisterIOSRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	api := apiHelper.Router.Group("/ios")

	// Module generation (requires valid upload_code)
	api.Get("/module/:upload_code/*", handleModuleGeneration(apiHelper))

	// Script generation (requires valid upload_code)
	api.Get("/script/:upload_code/haruki-toolbox.js", handleScriptGeneration(apiHelper))
}
