package adminstats

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	"time"
)

type uploadLogQueryFilters struct {
	From          time.Time
	To            time.Time
	GameUserIDs   []string
	UploadMethods []string
	DataTypes     []string
	Servers       []string
	Success       *bool
	Page          int
	PageSize      int
	Sort          string
}

type uploadLogListItem = adminCoreModule.UploadLogListItem

type uploadLogAppliedFilters struct {
	GameUserIDs   []string `json:"gameUserIds,omitempty"`
	UploadMethods []string `json:"uploadMethods,omitempty"`
	DataTypes     []string `json:"dataTypes,omitempty"`
	Servers       []string `json:"servers,omitempty"`
	Success       *bool    `json:"success,omitempty"`
}

type uploadLogQuerySummary struct {
	Success    int             `json:"success"`
	Failed     int             `json:"failed"`
	ByMethod   []categoryCount `json:"byMethod"`
	ByDataType []categoryCount `json:"byDataType"`
}

type uploadLogQueryResponse struct {
	GeneratedAt time.Time               `json:"generatedAt"`
	From        time.Time               `json:"from"`
	To          time.Time               `json:"to"`
	Page        int                     `json:"page"`
	PageSize    int                     `json:"pageSize"`
	Total       int                     `json:"total"`
	TotalPages  int                     `json:"totalPages"`
	HasMore     bool                    `json:"hasMore"`
	Sort        string                  `json:"sort"`
	Filters     uploadLogAppliedFilters `json:"filters"`
	Summary     uploadLogQuerySummary   `json:"summary"`
	Items       []uploadLogListItem     `json:"items"`
}
