package adminusers

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"time"
)

type adminUserQueryFilters struct {
	Query          string
	Role           string
	Banned         *bool
	AllowCNMysekai *bool
	CreatedFrom    *time.Time
	CreatedTo      *time.Time
	Page           int
	PageSize       int
	Sort           string
}

type adminUserListItem struct {
	UserID         string     `json:"userId"`
	Name           string     `json:"name"`
	Email          string     `json:"email"`
	Role           string     `json:"role"`
	Banned         bool       `json:"banned"`
	AllowCNMysekai bool       `json:"allowCNMysekai"`
	BanReason      *string    `json:"banReason,omitempty"`
	CreatedAt      *time.Time `json:"createdAt,omitempty"`
}

type adminUserAppliedFilters struct {
	Query          string     `json:"q,omitempty"`
	Role           string     `json:"role,omitempty"`
	Banned         *bool      `json:"banned,omitempty"`
	AllowCNMysekai *bool      `json:"allowCNMysekai,omitempty"`
	CreatedFrom    *time.Time `json:"createdFrom,omitempty"`
	CreatedTo      *time.Time `json:"createdTo,omitempty"`
}

type adminUserListResponse struct {
	GeneratedAt time.Time               `json:"generatedAt"`
	Page        int                     `json:"page"`
	PageSize    int                     `json:"pageSize"`
	Total       int                     `json:"total"`
	TotalPages  int                     `json:"totalPages"`
	HasMore     bool                    `json:"hasMore"`
	Sort        string                  `json:"sort"`
	Filters     adminUserAppliedFilters `json:"filters"`
	Items       []adminUserListItem     `json:"items"`
}

type updateUserBanPayload struct {
	Reason *string `json:"reason"`
}

type userBanStatusResponse struct {
	UserID             string  `json:"userId"`
	Role               string  `json:"role"`
	Banned             bool    `json:"banned"`
	BanReason          *string `json:"banReason,omitempty"`
	ClearedSessions    *bool   `json:"clearedSessions,omitempty"`
	RevokedOAuthTokens *bool   `json:"revokedOAuthTokens,omitempty"`
}

type batchUserOperationPayload struct {
	UserIDs []string `json:"userIds"`
	Reason  *string  `json:"reason,omitempty"`
}

type batchUserRoleUpdatePayload struct {
	UserIDs []string `json:"userIds"`
	Role    string   `json:"role"`
}

type batchUserAllowCNMysekaiUpdatePayload struct {
	UserIDs             []string `json:"userIds"`
	AllowCNMysekai      *bool    `json:"allowCNMysekai"`
	AllowCNMysekaiSnake *bool    `json:"allow_cn_mysekai"`
}

type batchUserOperationItemResult struct {
	UserID  string `json:"userId"`
	Success bool   `json:"success"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type batchUserOperationResponse struct {
	Action  string                         `json:"action"`
	Total   int                            `json:"total"`
	Success int                            `json:"success"`
	Failed  int                            `json:"failed"`
	Results []batchUserOperationItemResult `json:"results"`
}

type adminUserActivityFilters struct {
	From           time.Time
	To             time.Time
	SystemLogLimit int
	UploadLogLimit int
}

type adminUserActivitySummary struct {
	SystemLogTotal int `json:"systemLogTotal"`
	UploadLogTotal int `json:"uploadLogTotal"`
	UploadSuccess  int `json:"uploadSuccess"`
	UploadFailure  int `json:"uploadFailure"`
}

type adminUserActivityResponse struct {
	GeneratedAt    time.Time                `json:"generatedAt"`
	UserID         string                   `json:"userId"`
	From           time.Time                `json:"from"`
	To             time.Time                `json:"to"`
	SystemLogLimit int                      `json:"systemLogLimit"`
	UploadLogLimit int                      `json:"uploadLogLimit"`
	Summary        adminUserActivitySummary `json:"summary"`
	SystemLogs     []systemLogListItem      `json:"systemLogs"`
	UploadLogs     []uploadLogListItem      `json:"uploadLogs"`
}

type adminUserDetailActivitySummary struct {
	WindowHours    int        `json:"windowHours"`
	From           time.Time  `json:"from"`
	To             time.Time  `json:"to"`
	SystemLogTotal int        `json:"systemLogTotal"`
	UploadLogTotal int        `json:"uploadLogTotal"`
	UploadSuccess  int        `json:"uploadSuccess"`
	UploadFailure  int        `json:"uploadFailure"`
	LastSystemLog  *time.Time `json:"lastSystemLog,omitempty"`
	LastUploadLog  *time.Time `json:"lastUploadLog,omitempty"`
}

type adminUserDetailResponse struct {
	UserData        harukiAPIHelper.HarukiToolboxUserData `json:"userData"`
	Banned          bool                                  `json:"banned"`
	AllowCNMysekai  bool                                  `json:"allowCNMysekai"`
	BanReason       *string                               `json:"banReason,omitempty"`
	CreatedAt       *time.Time                            `json:"createdAt,omitempty"`
	ActivitySummary *adminUserDetailActivitySummary       `json:"activitySummary,omitempty"`
}

type adminForceLogoutResponse struct {
	UserID          string `json:"userId"`
	ClearedSessions bool   `json:"clearedSessions"`
}

type adminUserGameBindingsResponse struct {
	GeneratedAt time.Time                            `json:"generatedAt"`
	UserID      string                               `json:"userId"`
	Total       int                                  `json:"total"`
	Items       []harukiAPIHelper.GameAccountBinding `json:"items"`
}

type adminUserGameBindingUpsertResponse struct {
	UserID     string                             `json:"userId"`
	Server     string                             `json:"server"`
	GameUserID string                             `json:"gameUserId"`
	Created    bool                               `json:"created"`
	Binding    harukiAPIHelper.GameAccountBinding `json:"binding"`
}

type adminManagedSocialPlatformPayload struct {
	Platform      string `json:"platform"`
	PlatformSnake string `json:"platform_name"`
	UserID        string `json:"userId"`
	UserIDSnake   string `json:"user_id"`
	Verified      *bool  `json:"verified"`
}

type adminManagedAuthorizedSocialPayload struct {
	Platform      string `json:"platform"`
	PlatformSnake string `json:"platform_name"`
	UserID        string `json:"userId"`
	UserIDSnake   string `json:"user_id"`
	Comment       string `json:"comment"`
}

type adminUserSocialPlatformResponse struct {
	GeneratedAt    time.Time                           `json:"generatedAt"`
	UserID         string                              `json:"userId"`
	Exists         bool                                `json:"exists"`
	SocialPlatform *harukiAPIHelper.SocialPlatformInfo `json:"socialPlatform,omitempty"`
}

type adminUserAuthorizedSocialListResponse struct {
	GeneratedAt time.Time                                     `json:"generatedAt"`
	UserID      string                                        `json:"userId"`
	Total       int                                           `json:"total"`
	Items       []harukiAPIHelper.AuthorizeSocialPlatformInfo `json:"items"`
}

type adminUserAuthorizedSocialUpsertResponse struct {
	UserID     string                                      `json:"userId"`
	PlatformID int                                         `json:"platformId"`
	Created    bool                                        `json:"created"`
	Record     harukiAPIHelper.AuthorizeSocialPlatformInfo `json:"record"`
}

type adminUserIOSUploadCodeResponse struct {
	UserID     string `json:"userId"`
	UploadCode string `json:"uploadCode"`
}

type adminUserClearIOSUploadCodeResponse struct {
	UserID  string `json:"userId"`
	Cleared bool   `json:"cleared"`
}

type adminManagedEmailPayload struct {
	Email string `json:"email"`
}

type adminUserEmailResponse struct {
	UserID   string `json:"userId"`
	Email    string `json:"email"`
	Verified bool   `json:"verified"`
}

type adminUpdateAllowCNMysekaiPayload struct {
	AllowCNMysekai      *bool `json:"allowCNMysekai"`
	AllowCNMysekaiSnake *bool `json:"allow_cn_mysekai"`
}

type adminUserAllowCNMysekaiResponse struct {
	UserID         string `json:"userId"`
	AllowCNMysekai bool   `json:"allowCNMysekai"`
}

type adminSoftDeletePayload struct {
	Reason *string `json:"reason,omitempty"`
}

type adminResetPasswordPayload struct {
	NewPassword *string `json:"newPassword,omitempty"`
	ForceLogout *bool   `json:"forceLogout,omitempty"`
}

type adminLifecycleResponse struct {
	UserID          string  `json:"userId"`
	Banned          bool    `json:"banned"`
	BanReason       *string `json:"banReason,omitempty"`
	ClearedSessions *bool   `json:"clearedSessions,omitempty"`
}

type adminResetPasswordResponse struct {
	UserID            string `json:"userId"`
	TemporaryPassword string `json:"temporaryPassword,omitempty"`
	ForceLogout       bool   `json:"forceLogout"`
	ClearedSessions   *bool  `json:"clearedSessions,omitempty"`
}

type updateUserRolePayload struct {
	Role string `json:"role"`
}

type userRoleResponse struct {
	UserID string `json:"userId"`
	Role   string `json:"role"`
	Banned bool   `json:"banned"`
}
