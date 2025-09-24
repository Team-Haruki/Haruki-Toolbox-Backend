package upload

import (
	"context"
	"errors"
	harukiRootApi "haruki-suite/api"
	harukiConfig "haruki-suite/config"
	harukiUtils "haruki-suite/utils"
	harukiHandler "haruki-suite/utils/handler"
	harukiLogger "haruki-suite/utils/logger"
	harukiMongo "haruki-suite/utils/mongo"
	"haruki-suite/utils/sekai"
	"sort"
	"strconv"
	"strings"
	"time"

	"net/http"

	"github.com/go-resty/resty/v2"
	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

const chunkExpire = time.Minute * 3

type dataUploadHeader struct {
	ScriptVersion string                   `header:"x-script-version"`
	OriginalUrl   string                   `header:"x-original-url"`
	UploadId      string                   `header:"x-upload-id"`
	ChunkIndex    int                      `header:"x-chunk-index"`
	TotalChunks   int                      `header:"x-total-chunks"`
	Policy        harukiUtils.UploadPolicy `header:"x-upload-policy"`
}

func registerIOSRoutes(app *fiber.App, mongoManager *harukiMongo.MongoDBManager, redisClient *redis.Client) {
	api := app.Group("/ios")
	logger := harukiLogger.NewLogger("HarukiSekaiProxy", "DEBUG", nil)
	dataChunks := make(map[string][]harukiUtils.DataChunk)

	api.Post("/script/upload", func(c *fiber.Ctx) error {
		header := new(dataUploadHeader)
		err := c.ReqHeaderParser(header)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: "Invalid request header",
				Status:  harukiRootApi.IntPtr(http.StatusBadRequest),
			})
		}
		if header.ScriptVersion == "" {
			header.ScriptVersion = "unknown"
		}

		uploadType, userId := harukiRootApi.ExtractUploadTypeAndUserID(header.OriginalUrl)
		if uploadType == "" {
			logger.Errorf(
				"Unable to identify package data type: %s, upload: %s, chunk: %d/%d, script_version: %s",
				header.OriginalUrl, header.UploadId, header.ChunkIndex+1, header.TotalChunks, header.ScriptVersion,
			)
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: "Unknown upload type",
				Status:  harukiRootApi.IntPtr(http.StatusBadRequest),
			})
		}

		var server harukiUtils.SupportedDataUploadServer
		for s, tuple := range sekai.GetAPIEndpoint() {
			if !strings.Contains(header.OriginalUrl, tuple[1]) {
				continue
			}
			server = s
			break
		}
		if server == "" {
			logger.Errorf(
				"Unable to identify package game server: %s, upload: %s, chunk: %d/%d, script_version: %s",
				header.OriginalUrl, header.UploadId, header.ChunkIndex+1, header.TotalChunks, header.ScriptVersion,
			)
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: "Unknown game server",
				Status:  harukiRootApi.IntPtr(http.StatusBadRequest),
			})
		}

		now := time.Now()
		body := c.Request().Body()
		dataChunks[header.UploadId] = append(dataChunks[header.UploadId], harukiUtils.DataChunk{
			RequestURL:  header.OriginalUrl,
			UploadID:    header.UploadId,
			ChunkIndex:  header.ChunkIndex,
			TotalChunks: header.TotalChunks,
			Time:        now,
			Data:        body,
		})

		logger.Infof(
			"Receive %d data upload from %s_%s (%d+1/%d of %s, url=%s, script_version=%s)",
			userId, server, uploadType, header.ChunkIndex+1, header.TotalChunks,
			header.UploadId, header.OriginalUrl, header.ScriptVersion,
		)

		if len(dataChunks[header.UploadId]) == header.TotalChunks {
			chunks := make([]harukiUtils.DataChunk, len(dataChunks[header.UploadId]))
			copy(chunks, dataChunks[header.UploadId])

			sort.Slice(chunks, func(x, y int) bool {
				return chunks[x].ChunkIndex < chunks[y].ChunkIndex
			})

			var payload []byte
			for _, c := range chunks {
				payload = append(payload, c.Data...)
			}

			dataHandler := harukiHandler.DataHandler{MongoManager: mongoManager}
			result, err := dataHandler.HandleAndUpdateData(
				context.Background(), payload, server, header.Policy, uploadType, &userId,
			)
			if err != nil {
				return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
					Status:  harukiRootApi.IntPtr(http.StatusInternalServerError),
					Message: err.Error(),
				})
			}
			if *result.Status != 200 {
				if *result.ErrorMessage == "" {
					*result.ErrorMessage = "Unknown Error"
				}
				return errors.New(*result.ErrorMessage)
			}

			delete(dataChunks, header.UploadId)
			logger.Infof(
				"Receive %d data upload from %s_%s (upload_id=%s, script_version=%s)",
				userId, server, uploadType, header.UploadId, header.ScriptVersion,
			)

			// python:
			// for path in get_clear_cache_paths(server, upload_type, user_id):
			// await clear_cache_by_path(**path)
		}

		for upid, chunks := range dataChunks {
			var filtered []harukiUtils.DataChunk
			for _, c := range chunks {
				if now.Sub(c.Time) < chunkExpire {
					filtered = append(filtered, c)
				}
			}

			if len(filtered) > 0 {
				dataChunks[upid] = filtered
			} else {
				delete(dataChunks, upid)
			}
		}

		return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{Message: "Successfully uploaded data."})
	})

	api.Get("/proxy/:server/:policy/suite/user/:user_id", func(c *fiber.Ctx) error {
		userIDStr := c.Params("user_id")
		serverStr := c.Params("server")
		policyStr := c.Params("policy")

		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Status:  harukiRootApi.IntPtr(http.StatusBadRequest),
				Message: err.Error(),
			})
		}
		policy, err := harukiUtils.ParseUploadPolicy(policyStr)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Status:  harukiRootApi.IntPtr(http.StatusBadRequest),
				Message: err.Error(),
			})
		}
		userID, err := strconv.ParseInt(userIDStr, 0, 64)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Status:  harukiRootApi.IntPtr(http.StatusBadRequest),
				Message: err.Error(),
			})
		}

		logger.Infof("Received %s server suite request from user %d", server, userID)

		dataHandler := harukiHandler.DataHandler{MongoManager: mongoManager, RestyClient: resty.New()}
		proxyHandler := sekai.HandleProxyUpload(
			mongoManager,
			harukiConfig.Cfg.Proxy,
			policy,
			dataHandler.PreHandleData,
			dataHandler.CallWebhook,
			redisClient,
			harukiUtils.UploadDataTypeSuite,
		)
		// python
		// for path in get_clear_cache_paths(server, UploadDataType.suite, user_id):
		// await clear_cache_by_path(**path)
		return proxyHandler(c)
	})

	api.Post("/proxy/:server/:policy/user/:user_id/mysekai", func(c *fiber.Ctx) error {
		userIDStr := c.Params("user_id")
		serverStr := c.Params("server")
		policyStr := c.Params("policy")

		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Status:  harukiRootApi.IntPtr(http.StatusBadRequest),
				Message: err.Error(),
			})
		}
		policy, err := harukiUtils.ParseUploadPolicy(policyStr)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Status:  harukiRootApi.IntPtr(http.StatusBadRequest),
				Message: err.Error(),
			})
		}
		userID, err := strconv.ParseInt(userIDStr, 0, 64)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Status:  harukiRootApi.IntPtr(http.StatusBadRequest),
				Message: err.Error(),
			})
		}

		logger.Infof("Received %s server mysekai request from user %d", server, userID)

		dataHandler := harukiHandler.DataHandler{MongoManager: mongoManager, RestyClient: resty.New(), Logger: logger}
		proxyHandler := sekai.HandleProxyUpload(
			mongoManager,
			harukiConfig.Cfg.Proxy,
			policy,
			dataHandler.PreHandleData,
			dataHandler.CallWebhook,
			redisClient,
			harukiUtils.UploadDataTypeMysekai,
		)
		// python
		// for path in get_clear_cache_paths(server, UploadDataType.suite, user_id):
		// await clear_cache_by_path(**path)
		return proxyHandler(c)
	})
}
