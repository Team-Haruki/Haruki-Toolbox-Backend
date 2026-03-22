package admingamebindings

import (
	"haruki-suite/ent/schema"
	"time"
)

type adminGlobalGameBindingQueryFilters struct {
	Query      string
	Server     string
	GameUserID string
	UserID     string
	Verified   *bool
	Page       int
	PageSize   int
	Sort       string
}

type adminGlobalGameBindingOwner struct {
	UserID string `json:"userId"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Role   string `json:"role"`
}

type adminGlobalGameBindingItem struct {
	ID         int                                `json:"id"`
	Server     string                             `json:"server"`
	GameUserID string                             `json:"gameUserId"`
	Verified   bool                               `json:"verified"`
	Suite      *schema.SuiteDataPrivacySettings   `json:"suite,omitempty"`
	Mysekai    *schema.MysekaiDataPrivacySettings `json:"mysekai,omitempty"`
	Owner      adminGlobalGameBindingOwner        `json:"owner"`
}

type adminGlobalGameBindingAppliedFilters struct {
	Query      string `json:"q,omitempty"`
	Server     string `json:"server,omitempty"`
	GameUserID string `json:"gameUserId,omitempty"`
	UserID     string `json:"userId,omitempty"`
	Verified   *bool  `json:"verified,omitempty"`
}

type adminGlobalGameBindingListResponse struct {
	GeneratedAt time.Time                            `json:"generatedAt"`
	Page        int                                  `json:"page"`
	PageSize    int                                  `json:"pageSize"`
	Total       int                                  `json:"total"`
	TotalPages  int                                  `json:"totalPages"`
	HasMore     bool                                 `json:"hasMore"`
	Sort        string                               `json:"sort"`
	Filters     adminGlobalGameBindingAppliedFilters `json:"filters"`
	Items       []adminGlobalGameBindingItem         `json:"items"`
}

type adminGlobalGameBindingReassignPayload struct {
	TargetUserID      string `json:"targetUserId"`
	TargetUserIDSnake string `json:"target_user_id"`
}

type adminGlobalGameBindingReassignResponse struct {
	Server       string `json:"server"`
	GameUserID   string `json:"gameUserId"`
	FromUserID   string `json:"fromUserId"`
	TargetUserID string `json:"targetUserId"`
	Changed      bool   `json:"changed"`
}

type adminGlobalGameBindingRef struct {
	Server          string `json:"server"`
	GameUserID      string `json:"gameUserId"`
	GameUserIDSnake string `json:"game_user_id"`
}

type adminGlobalGameBindingBatchDeletePayload struct {
	Items []adminGlobalGameBindingRef `json:"items"`
}

type adminGlobalGameBindingBatchDeleteItemResult struct {
	Server     string `json:"server"`
	GameUserID string `json:"gameUserId"`
	Success    bool   `json:"success"`
	Code       string `json:"code,omitempty"`
	Message    string `json:"message,omitempty"`
}

type adminGlobalGameBindingBatchDeleteResponse struct {
	Total   int                                           `json:"total"`
	Success int                                           `json:"success"`
	Failed  int                                           `json:"failed"`
	Results []adminGlobalGameBindingBatchDeleteItemResult `json:"results"`
}

type adminGlobalGameBindingBatchReassignItem struct {
	Server            string `json:"server"`
	GameUserID        string `json:"gameUserId"`
	GameUserIDSnake   string `json:"game_user_id"`
	TargetUserID      string `json:"targetUserId"`
	TargetUserIDSnake string `json:"target_user_id"`
}

type adminGlobalGameBindingBatchReassignPayload struct {
	Items []adminGlobalGameBindingBatchReassignItem `json:"items"`
}

type adminGlobalGameBindingBatchReassignItemResult struct {
	Server       string `json:"server"`
	GameUserID   string `json:"gameUserId"`
	FromUserID   string `json:"fromUserId,omitempty"`
	TargetUserID string `json:"targetUserId,omitempty"`
	Changed      bool   `json:"changed,omitempty"`
	Success      bool   `json:"success"`
	Code         string `json:"code,omitempty"`
	Message      string `json:"message,omitempty"`
}

type adminGlobalGameBindingBatchReassignResponse struct {
	Total   int                                             `json:"total"`
	Success int                                             `json:"success"`
	Failed  int                                             `json:"failed"`
	Results []adminGlobalGameBindingBatchReassignItemResult `json:"results"`
}
