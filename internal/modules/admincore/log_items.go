package admincore

import (
	"haruki-suite/utils/database/postgresql"
	"time"
)

type SystemLogListItem struct {
	ID          int            `json:"id"`
	EventTime   time.Time      `json:"eventTime"`
	ActorUserID string         `json:"actorUserId,omitempty"`
	ActorRole   string         `json:"actorRole,omitempty"`
	ActorType   string         `json:"actorType"`
	Action      string         `json:"action"`
	TargetType  string         `json:"targetType,omitempty"`
	TargetID    string         `json:"targetId,omitempty"`
	Result      string         `json:"result"`
	IP          string         `json:"ip,omitempty"`
	UserAgent   string         `json:"userAgent,omitempty"`
	Method      string         `json:"method,omitempty"`
	Path        string         `json:"path,omitempty"`
	RequestID   string         `json:"requestId,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type UploadLogListItem struct {
	ID            int       `json:"id"`
	Server        string    `json:"server"`
	GameUserID    string    `json:"gameUserId"`
	ToolboxUserID string    `json:"toolboxUserId,omitempty"`
	DataType      string    `json:"dataType"`
	UploadMethod  string    `json:"uploadMethod"`
	Success       bool      `json:"success"`
	ErrorMessage  *string   `json:"errorMessage,omitempty"`
	UploadTime    time.Time `json:"uploadTime"`
}

func BuildSystemLogItems(rows []*postgresql.SystemLog) []SystemLogListItem {
	items := make([]SystemLogListItem, 0, len(rows))
	for _, row := range rows {
		item := SystemLogListItem{
			ID:        row.ID,
			EventTime: row.EventTime.UTC(),
			ActorType: string(row.ActorType),
			Action:    row.Action,
			Result:    string(row.Result),
			Metadata:  row.Metadata,
		}
		if row.ActorUserID != nil {
			item.ActorUserID = *row.ActorUserID
		}
		if row.ActorRole != nil {
			item.ActorRole = *row.ActorRole
		}
		if row.TargetType != nil {
			item.TargetType = *row.TargetType
		}
		if row.TargetID != nil {
			item.TargetID = *row.TargetID
		}
		if row.IP != nil {
			item.IP = *row.IP
		}
		if row.UserAgent != nil {
			item.UserAgent = *row.UserAgent
		}
		if row.Method != nil {
			item.Method = *row.Method
		}
		if row.Path != nil {
			item.Path = *row.Path
		}
		if row.RequestID != nil {
			item.RequestID = *row.RequestID
		}
		items = append(items, item)
	}
	return items
}

func BuildUploadLogItems(rows []*postgresql.UploadLog) []UploadLogListItem {
	items := make([]UploadLogListItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, UploadLogListItem{
			ID:            row.ID,
			Server:        row.Server,
			GameUserID:    row.GameUserID,
			ToolboxUserID: row.ToolboxUserID,
			DataType:      row.DataType,
			UploadMethod:  row.UploadMethod,
			Success:       row.Success,
			ErrorMessage:  row.ErrorMessage,
			UploadTime:    row.UploadTime.UTC(),
		})
	}
	return items
}
