package upload

import (
	"context"
	"errors"
	"time"

	usercore "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiSekai "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/game/sekai"

	"github.com/gofiber/fiber/v3"
)

// inheritSubmitGoneMessage answers the retired synchronous route. It ran the
// transfer without handing the account back, so no client may use it any
// more; the message keeps the safety advice of the JP suspension.
const inheritSubmitGoneMessage = "引继码上传已改为新流程（需要登录，并会为你生成新的引继码），请刷新页面或更新客户端后重试。如果你此前提交过日服引继码，请立即进入游戏重新生成新引继码并妥善保存。 Transfer-code uploads moved to a new flow that requires signing in and hands you a new transfer code; reload the page or update your client. If you submitted a JP transfer code before, generate a new one in the game now and keep it safe."

func handleInheritSubmitGone(c fiber.Ctx) error {
	return harukiAPIHelper.Responses.UpdatedDataResponse(c, fiber.StatusGone, inheritSubmitGoneMessage, &problemBody{Code: "client_update_required"})
}

func recordInheritRetrievalFailure(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, dependencies Dependencies, server harukiUtils.SupportedDataUploadServer, uploadType harukiUtils.UploadDataType, gameUserID int64, err error) {
	logger := dependencies.DataHandlerLogger
	if gameUserID <= 0 {
		return
	}
	dataType := inheritRetrievalFailureDataType(uploadType, err)
	uploadCtx := &uploadContext{
		Server:             server,
		DataType:           dataType,
		ExpectedGameUserID: gameUserID,
		UploadMethod:       harukiUtils.UploadMethodInherit,
		FailureStage:       "retrieve_" + string(dataType),
	}
	uploadCtx.Attempt = newAttempt(harukiUtils.UploadMethodInherit, 0)
	// The game user ID comes from the authenticated game response, not a URL claim.
	uploadCtx.Attempt.IdentityVerified = true
	uploadCtx.Attempt.FailureStage = uploadCtx.FailureStage
	uploadCtx.Attempt.ErrorCode = "temporarily_unavailable"
	ownerCtx, cancelOwner := context.WithTimeout(context.Background(), 5*time.Second)
	if ownerErr := resolveAttemptOwner(ownerCtx, apiHelper, uploadCtx); ownerErr != nil && logger != nil {
		logger.Warnf("Failed to resolve inherit audit owner: %v", ownerErr)
	}
	cancelOwner()
	finishAttempt(uploadCtx, false)
	dispatchUploadAuditLog(apiHelper, logger, dependencies.BackgroundTasks, uploadCtx, false, buildUploadAuditErrorMessage(err, nil))
}

func inheritRetrievalFailureDataType(uploadType harukiUtils.UploadDataType, err error) harukiUtils.UploadDataType {
	var retrievalErr *harukiSekai.DataRetrievalError
	if errors.As(err, &retrievalErr) {
		switch retrievalErr.DataType {
		case string(harukiUtils.UploadDataTypeMysekai):
			return harukiUtils.UploadDataTypeMysekai
		case string(harukiUtils.UploadDataTypeSuite):
			return harukiUtils.UploadDataTypeSuite
		}
	}
	if uploadType == harukiUtils.UploadDataTypeMysekai {
		return harukiUtils.UploadDataTypeMysekai
	}
	return harukiUtils.UploadDataTypeSuite
}

// registerInheritRoutes mounts the inherit job and vault API. All of it needs
// a Toolbox session; the retired synchronous route only says so.
func registerInheritRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, dependencies Dependencies) {
	apiHelper.Router.Post("/api/inherit/:server/:upload_type/submit", openUploadEntryGuard(apiHelper), handleInheritSubmitGone)
	if apiHelper.SessionHandler == nil {
		return
	}
	s := newInheritService(apiHelper, dependencies)
	guards := usercore.RequireAuthenticatedUser(apiHelper)
	route := func(method, path string, handler fiber.Handler) {
		first, rest := usercore.RouteHandlerParts(guards, handler)
		apiHelper.Router.Add([]string{method}, path, first, rest...)
	}
	route(fiber.MethodPost, "/api/inherit/jobs", s.handleCreateJob)
	route(fiber.MethodGet, "/api/inherit/jobs/:job_id", s.handleGetJob)
	route(fiber.MethodPost, "/api/inherit/jobs/:job_id/confirm", s.handleConfirm)
	route(fiber.MethodPost, "/api/inherit/jobs/:job_id/cancel", s.handleCancel)
	route(fiber.MethodPost, "/api/inherit/jobs/:job_id/ws-ticket", s.handleWSTicket)
	route(fiber.MethodGet, "/api/inherit/jobs/:job_id/ws", s.handleWS)
	route(fiber.MethodGet, "/api/inherit/vault/key", s.handleVaultKey)
	route(fiber.MethodPost, "/api/inherit/vault/key/reset", s.handleVaultReset)
	route(fiber.MethodGet, "/api/inherit/vault/entries", s.handleVaultList)
	route(fiber.MethodPost, "/api/inherit/vault/entries/:entry_id/reveal", s.handleVaultReveal)
	route(fiber.MethodPost, "/api/inherit/vault/entries/:entry_id/reissue", s.handleVaultReissue)
	route(fiber.MethodDelete, "/api/inherit/vault/entries/:entry_id", s.handleVaultDestroy)
}
