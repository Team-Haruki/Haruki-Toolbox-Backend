package ios

import (
	"fmt"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	iosGen "haruki-suite/utils/api/ios"
	"haruki-suite/utils/database/postgresql/iosscriptcode"
	"haruki-suite/utils/database/postgresql/user"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
)

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
		record, err := apiHelper.DBManager.DB.IOSScriptCode.Query().
			Where(iosscriptcode.UploadCodeEQ(uploadCode)).
			Only(ctx)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "invalid upload code")
		}
		userID := record.UserID
		filepath = strings.TrimPrefix(filepath, "/")
		matches := modulePathPattern.FindStringSubmatch(filepath)
		if matches == nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid module path format. Expected: {regions}-haruki-toolbox-{datatypes}.{ext}")
		}
		regionsStr := matches[1]
		dataTypesStr := matches[2]
		ext := matches[3]
		app, ok := iosGen.ParseProxyApp(ext)
		if !ok {
			return harukiAPIHelper.ErrorBadRequest(c, fmt.Sprintf("unsupported extension: %s. Supported: sgmodule, lnplugin, conf, stoverride", ext))
		}
		endpointStr := c.Query("endpoint", "direct")
		endpointType, ok := iosGen.ParseEndpointType(endpointStr)
		if !ok {
			return harukiAPIHelper.ErrorBadRequest(c, fmt.Sprintf("unsupported endpoint: %s. Supported: direct, cdn", endpointStr))
		}
		chunkSizeMB := 1
		if chunkStr := c.Query("chunk"); chunkStr != "" {
			parsed, err := strconv.Atoi(chunkStr)
			if err != nil || parsed < 1 || parsed > 10 {
				return harukiAPIHelper.ErrorBadRequest(c, "chunk must be between 1 and 10 MB")
			}
			chunkSizeMB = parsed
		}
		regionStrs := strings.Split(regionsStr, "-")
		var regions []harukiUtils.SupportedDataUploadServer
		hasCN := false
		for _, rs := range regionStrs {
			region, err := harukiUtils.ParseSupportedDataUploadServer(rs)
			if err != nil {
				return harukiAPIHelper.ErrorBadRequest(c, fmt.Sprintf("unsupported region: %s. Supported: jp, en, tw, kr, cn", rs))
			}
			regions = append(regions, region)
			if region == harukiUtils.SupportedDataUploadServerCN {
				hasCN = true
			}
		}
		dataTypeStrs := strings.Split(dataTypesStr, "-")
		var dataTypes []iosGen.DataType
		hasMysekai := false
		hasMysekaiForce := false
		hasCNMysekaiType := false
		for _, dts := range dataTypeStrs {
			dt, ok := iosGen.ParseDataType(dts)
			if !ok {
				return harukiAPIHelper.ErrorBadRequest(c, fmt.Sprintf("unsupported data type: %s. Supported: suite, mysekai, mysekai_force, mysekai_birthday_party", dts))
			}
			if dt == iosGen.DataTypeMysekai || dt == iosGen.DataTypeMysekaiForce || dt == iosGen.DataTypeMysekaiBirthdayParty {
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
		if hasMysekai && hasMysekaiForce {
			var filtered []iosGen.DataType
			for _, dt := range dataTypes {
				if dt != iosGen.DataTypeMysekai {
					filtered = append(filtered, dt)
				}
			}
			dataTypes = filtered
		}
		if hasCN && hasCNMysekaiType {
			u, err := apiHelper.DBManager.DB.User.Query().Where(user.IDEQ(userID)).Only(ctx)
			if err != nil {
				return harukiAPIHelper.ErrorBadRequest(c, "user not found")
			}
			if !u.AllowCnMysekai {
				return harukiAPIHelper.ErrorForbidden(c, "You are not allowed to use CN mysekai function")
			}
		}
		modeStr := c.Query("mode", "proxy")
		var mode iosGen.UploadMode
		switch modeStr {
		case "proxy":
			mode = iosGen.UploadModeProxy
		case "script":
			mode = iosGen.UploadModeScript
			if app == iosGen.ProxyAppQuantumultX {
				return harukiAPIHelper.ErrorBadRequest(c, "Quantumult X does not support script upload mode. Use proxy mode instead.")
			}
		default:
			return harukiAPIHelper.ErrorBadRequest(c, fmt.Sprintf("unsupported mode: %s. Supported: proxy, script", modeStr))
		}
		req := &iosGen.ModuleRequest{
			UploadCode:  uploadCode,
			Regions:     regions,
			DataTypes:   dataTypes,
			App:         app,
			Mode:        mode,
			ChunkSizeMB: chunkSizeMB,
		}
		endpoint := getEndpoint(endpointType)
		content, err := iosGen.GenerateModule(req, endpoint, endpointStr)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to generate module")
		}
		c.Set("Content-Type", app.ContentType())
		c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", req.FileName()))
		return c.SendString(content)
	}
}
