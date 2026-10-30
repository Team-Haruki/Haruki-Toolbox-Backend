package upload

import (
	"context"
	"errors"
	"net/http"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiSekai "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/game/sekai"

	"github.com/gofiber/fiber/v3"
)

// apiProblem is an error answer of the inherit job and vault endpoints:
// {"status": N, "message": "...", "updatedData": {"code": "..."}}.
type apiProblem struct {
	Status  int
	Code    string
	Message string
}

func (p *apiProblem) Error() string { return p.Code }

type problemBody struct {
	Code string `json:"code"`
	// JobID is set only on job_already_running: the caller's own running
	// job, so a client that lost its ID can reattach to it.
	JobID string `json:"job_id,omitempty"`
}

func respondProblem(c fiber.Ctx, p *apiProblem) error {
	return harukiAPIHelper.Responses.UpdatedDataResponse(c, p.Status, p.Message, &problemBody{Code: p.Code})
}

// respondJobAlreadyRunning answers job_already_running with the owner's
// running job ID.
func respondJobAlreadyRunning(c fiber.Ctx, jobID string) error {
	p := errJobAlreadyRunning
	return harukiAPIHelper.Responses.UpdatedDataResponse(c, p.Status, p.Message, &problemBody{Code: p.Code, JobID: jobID})
}

func problem(status int, code, message string) *apiProblem {
	return &apiProblem{Status: status, Code: code, Message: message}
}

// Request errors.
var (
	errInvalidRequest       = problem(http.StatusBadRequest, "invalid_request", "请求格式不正确")
	errInvalidServer        = problem(http.StatusBadRequest, "invalid_server", "服务器只能是 jp 或 en")
	errInvalidUploadType    = problem(http.StatusBadRequest, "invalid_upload_type", "上传类型只能是 suite 或 mysekai")
	errInheritIDInvalid     = problem(http.StatusBadRequest, "inherit_id_invalid", "引继码格式不正确（应为 16 位字母或数字）")
	errInheritPwInvalid     = problem(http.StatusBadRequest, "inherit_password_invalid", "请输入引继密码")
	errNewPwModeInvalid     = problem(http.StatusBadRequest, "new_password_mode_invalid", "新引继码密码方式只能是 reuse 或 random")
	errReusePasswordInvalid = problem(http.StatusBadRequest, "reuse_password_invalid", "沿用的引继密码需为 8–16 个字符，请改用随机密码")
	errMysekaiRequiresQQ    = problem(http.StatusForbidden, "mysekai_requires_qq", "上传 MySekai 数据需要先绑定并验证 QQ")
	errVaultUnavailable     = problem(http.StatusServiceUnavailable, "vault_unavailable", "引继功能暂不可用")
	errJobAlreadyRunning    = problem(http.StatusConflict, "job_already_running", "你已有一个进行中的引继任务")
	errJobRateLimited       = problem(http.StatusTooManyRequests, "rate_limited", "提交过于频繁，请稍后再试")
	errServerBusy           = problem(http.StatusTooManyRequests, "server_busy", "当前引继请求过多，请稍后再试")
	errGameDegraded         = problem(http.StatusServiceUnavailable, "game_degraded", "游戏服务器暂时不稳定，请稍后再试")
	errJobNotFound          = problem(http.StatusNotFound, "job_not_found", "任务不存在或已过期")
	errJobNotAwaiting       = problem(http.StatusConflict, "job_not_awaiting_confirm", "任务当前不在等待确认")
	errCannotCancel         = problem(http.StatusConflict, "cannot_cancel", "引继已开始执行，无法取消")
	errUnauthenticated      = problem(http.StatusUnauthorized, "unauthenticated", "请先登录")
	errStoreUnavailable     = problem(http.StatusServiceUnavailable, "store_unavailable", "服务暂时不可用，请稍后再试")
	errWSTicketInvalid      = problem(http.StatusUnauthorized, "ws_ticket_invalid", "连接凭证无效或已过期")
	errInternal             = problem(http.StatusInternalServerError, "internal_error", "服务器内部错误")
)

// Unlock (step-up) errors.
var (
	errPasswordRequired      = problem(http.StatusBadRequest, "password_required", "请输入 Toolbox 账号密码")
	errPINRequired           = problem(http.StatusBadRequest, "vault_pin_required", "请输入保险库 PIN")
	errPINFormat             = problem(http.StatusBadRequest, "vault_pin_invalid_format", "保险库 PIN 需为 8–128 个字符")
	errPasswordInvalid       = problem(http.StatusForbidden, "password_invalid", "密码错误")
	errPINInvalid            = problem(http.StatusForbidden, "vault_pin_invalid", "保险库 PIN 错误")
	errPreviousPwInvalid     = problem(http.StatusForbidden, "previous_password_invalid", "旧密码无法解开保险库")
	errVaultPasswordChanged  = problem(http.StatusConflict, "vault_password_changed", "你的 Toolbox 密码在保险库创建后改过。请再输入改密码之前的旧密码以迁移保险库；如果忘记了旧密码（例如通过找回密码重置过），只能重置保险库，已保存的记录会被销毁")
	errUnlockRateLimited     = problem(http.StatusTooManyRequests, "unlock_rate_limited", "尝试次数过多，请 15 分钟后再试")
	errIdentityUnavailable   = problem(http.StatusServiceUnavailable, "identity_unavailable", "暂时无法验证身份，请稍后再试")
	errVaultNotInitialized   = problem(http.StatusConflict, "vault_not_initialized", "保险库尚未创建")
	errEntryNotFound         = problem(http.StatusNotFound, "entry_not_found", "记录不存在或已过期")
	errEntryStale            = problem(http.StatusGone, "entry_stale", "该记录属于已重置的保险库，无法读取")
	errCodeNotAvailable      = problem(http.StatusNotFound, "code_not_available", "该记录没有可用的引继码，请重新签发")
	errNoPreviousPassword    = problem(http.StatusConflict, "no_previous_password", "没有可沿用的旧密码，请改用随机密码")
	errResetConfirmation     = problem(http.StatusBadRequest, "reset_confirmation_required", "请确认重置保险库")
	errReissueFailed         = problem(http.StatusBadGateway, "reissue_failed", "重新签发引继码失败，请稍后再试")
	errReissueLoginFailed    = problem(http.StatusBadGateway, "reissue_login_failed", "使用保存的凭据登录失败，该凭据可能已失效")
	errReissueInheritInvalid = problem(http.StatusBadGateway, "reissue_rejected", "游戏服务器拒绝了请求")
)

// Job failure and warning codes (job.error.code / job.warnings[].code).
const (
	codeInheritInvalid     = "inherit_invalid"
	codeInheritRestricted  = "inherit_restricted"
	codeTransferRestricted = "transfer_restricted"
	codeConfirmTimeout     = "confirm_timeout"
	codeCancelled          = "cancelled"
	codeGameMaintenance    = "game_maintenance"
	codeGameUnavailable    = "game_unavailable"
	codeGameError          = "game_error"
	codeExecuteUncertain   = "execute_uncertain"
	codeLoginFailed        = "login_failed"
	codeIssueCodeFailed    = "issue_code_failed"
	codeVaultFallback      = "vault_db_unavailable"
	codeVaultStoreFailed   = "vault_store_failed"
	codeFetchFailed        = "fetch_failed"
	codeMysekaiUnavailable = "mysekai_unavailable"
	codeUploadFailed       = "upload_failed"
	codeUploadForbidden    = "upload_forbidden"
	codeJobInterrupted     = "job_interrupted"
	codeServerBusy         = "server_busy"
	codeGameDegraded       = "game_degraded"
	codeInternal           = "internal_error"
)

var jobMessages = map[string]string{
	codeInheritInvalid:     "继承码无效、已使用或已过期",
	codeInheritRestricted:  "该账号目前不能引继",
	codeTransferRestricted: "活动期间该账号的引继次数已用完，未执行引继",
	codeConfirmTimeout:     "等待确认超时，未执行引继，引继码仍然有效",
	codeCancelled:          "已取消，未执行引继，引继码仍然有效",
	codeGameMaintenance:    "游戏服务器维护中",
	codeGameUnavailable:    "游戏服务器暂时无法访问",
	codeGameError:          "游戏服务器返回了错误",
	codeExecuteUncertain:   "执行引继时连接中断，引继码可能已被使用；请勿在其他设备重试，可在保险库查看是否已保存凭据",
	codeLoginFailed:        "账号凭据已保存到保险库，但登录失败；可稍后在保险库重新签发引继码",
	codeIssueCodeFailed:    "未能签发新引继码；账号凭据已保存到保险库，请尽快在保险库重新签发",
	codeVaultFallback:      "数据库暂时不可用，账号凭据已临时保存，稍后会自动转存",
	codeVaultStoreFailed:   "账号凭据保存失败；请务必立即记下本页显示的新引继码",
	codeFetchFailed:        "获取游戏数据失败",
	codeMysekaiUnavailable: "获取 MySekai 数据失败，可能尚未完成教程",
	codeUploadFailed:       "上传数据处理失败",
	codeUploadForbidden:    "该游戏账号已被其他 Toolbox 用户绑定，无法上传",
	codeJobInterrupted:     "服务重启，任务已中断",
	codeServerBusy:         "当前引继请求过多，未执行引继，请稍后再试",
	codeGameDegraded:       "游戏服务器暂时不稳定，未执行引继，请稍后再试",
	codeInternal:           "服务器内部错误",
}

func jobProblemFor(code string) *jobProblem {
	return &jobProblem{Code: code, Message: jobMessages[code]}
}

// gameFailureCode classifies a game API failure. A bare 403 on the inherit
// calls means the code is wrong, used, or older than 24 hours.
func gameFailureCode(err error) string {
	switch {
	case harukiSekai.IsMaintenanceError(err):
		return codeGameMaintenance
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return codeGameUnavailable
	}
	switch status := harukiSekai.APIStatus(err); {
	case status == http.StatusForbidden:
		return codeInheritInvalid
	case status == http.StatusMethodNotAllowed:
		return codeInheritRestricted
	case status == 0 || status == http.StatusTooManyRequests || status >= 500:
		return codeGameUnavailable
	}
	return codeGameError
}
