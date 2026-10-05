package admincore

import (
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
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
	ActorUserID          *string    `json:"actorUserId,omitempty"`
	AuthMethod           *string    `json:"authMethod,omitempty"`
	GrantID              *int       `json:"grantId,omitempty"`
	AuthorizationSource  *string    `json:"authorizationSource,omitempty"`
	ClientName           *string    `json:"clientName,omitzero"`
	ClientVersion        *string    `json:"clientVersion,omitzero"`
	ClientChannel        *string    `json:"clientChannel,omitzero"`
	ClientMetadataFormat *string    `json:"clientMetadataFormat,omitzero"`
	ProtocolVersion      *string    `json:"protocolVersion,omitzero"`
	Platform             *string    `json:"platform,omitzero"`
	OsVersion            *string    `json:"osVersion,omitzero"`
	OsBuild              *string    `json:"osBuild,omitzero"`
	OsArch               *string    `json:"osArch,omitzero"`
	AppArch              *string    `json:"appArch,omitzero"`
	FailureStage         *string    `json:"failureStage,omitzero"`
	ErrorCode            *string    `json:"errorCode,omitzero"`
	RequestID            *string    `json:"requestId,omitzero"`
	OauthClientID        *string    `json:"oauthClientId,omitzero"`
	ProcessingDurationMs *int64     `json:"processingDurationMs,omitzero"`
	RequestBytes         *int64     `json:"requestBytes,omitzero"`
	IdentityVerified     *bool      `json:"identityVerified,omitzero"`
	ReceivedAt           *time.Time `json:"receivedAt,omitzero"`
	ClaimedGameUserID    *string    `json:"claimedGameUserId,omitzero"`

	ID            int       `json:"id"`
	Server        string    `json:"server"`
	GameUserID    string    `json:"gameUserId"`
	ToolboxUserID string    `json:"toolboxUserId,omitempty"`
	DataType      string    `json:"dataType"`
	UploadMethod  string    `json:"uploadMethod"`
	Success       bool      `json:"success"`
	ErrorMessage  *string   `json:"errorMessage,omitzero"`
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

func BuildUploadLogItems(rows []*postgresql.UploadLog, includeClaims ...bool) []UploadLogListItem {
	items := make([]UploadLogListItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, UploadLogListItem{
			ActorUserID: row.ActorUserID, AuthMethod: row.AuthMethod, GrantID: row.GrantID, AuthorizationSource: row.AuthorizationSource,
			ClientName:           row.ClientName,
			ClientVersion:        row.ClientVersion,
			ClientChannel:        row.ClientChannel,
			ClientMetadataFormat: row.ClientMetadataFormat,
			ProtocolVersion:      row.ProtocolVersion,
			Platform:             row.Platform,
			OsVersion:            row.OsVersion,
			OsBuild:              row.OsBuild,
			OsArch:               row.OsArch,
			AppArch:              row.AppArch,
			FailureStage:         row.FailureStage,
			ErrorCode:            row.ErrorCode,
			RequestID:            row.RequestID,
			OauthClientID:        row.OauthClientID,
			ProcessingDurationMs: row.ProcessingDurationMs,
			RequestBytes:         row.RequestBytes,
			IdentityVerified:     row.IdentityVerified,
			ReceivedAt:           row.ReceivedAt,
			ID:                   row.ID,
			Server:               row.Server,
			GameUserID:           row.GameUserID,
			ToolboxUserID:        row.ToolboxUserID,
			DataType:             row.DataType,
			UploadMethod:         row.UploadMethod,
			Success:              row.Success,
			ErrorMessage:         row.ErrorMessage,
			UploadTime:           row.UploadTime.UTC(),
		})
	}
	if len(includeClaims) > 0 && includeClaims[0] {
		for i, row := range rows {
			items[i].ClaimedGameUserID = row.ClaimedGameUserID
		}
	}
	return items
}
