package usergamebindings

import (
	"time"

	dbManager "haruki-suite/utils/database/postgresql"
)

type gameAccountDataGrantPayload struct {
	ExpiresAt time.Time `json:"expiresAt"`
}

type gameAccountDataGrantItem struct {
	ID            int       `json:"id"`
	OwnerUserID   string    `json:"ownerUserId"`
	GranteeUserID string    `json:"granteeUserId"`
	Server        string    `json:"server"`
	GameUserID    string    `json:"gameUserId"`
	DataType      string    `json:"dataType"`
	ExpiresAt     time.Time `json:"expiresAt"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type gameAccountDataGrantListResponse struct {
	GeneratedAt time.Time                  `json:"generatedAt"`
	Total       int                        `json:"total"`
	Items       []gameAccountDataGrantItem `json:"items"`
}

type gameAccountDataGrantMutationResponse struct {
	GeneratedAt time.Time                `json:"generatedAt"`
	Grant       gameAccountDataGrantItem `json:"grant"`
}

func buildGameAccountDataGrantItem(record dbManager.GameAccountDataGrantRecord) gameAccountDataGrantItem {
	return gameAccountDataGrantItem{
		ID:            record.ID,
		OwnerUserID:   record.OwnerUserID,
		GranteeUserID: record.GranteeUserID,
		Server:        record.Server,
		GameUserID:    record.GameUserID,
		DataType:      record.DataType,
		ExpiresAt:     record.ExpiresAt.UTC(),
		CreatedAt:     record.CreatedAt.UTC(),
		UpdatedAt:     record.UpdatedAt.UTC(),
	}
}

func buildGameAccountDataGrantItemFromRow(row *dbManager.GameAccountDataGrant) gameAccountDataGrantItem {
	if row == nil {
		return gameAccountDataGrantItem{}
	}
	return gameAccountDataGrantItem{
		ID:            row.ID,
		OwnerUserID:   row.OwnerUserID,
		GranteeUserID: row.GranteeUserID,
		Server:        row.Server,
		GameUserID:    row.GameUserID,
		DataType:      row.DataType,
		ExpiresAt:     row.ExpiresAt.UTC(),
		CreatedAt:     row.CreatedAt.UTC(),
		UpdatedAt:     row.UpdatedAt.UTC(),
	}
}

func buildGameAccountDataGrantItems(records []dbManager.GameAccountDataGrantRecord) []gameAccountDataGrantItem {
	items := make([]gameAccountDataGrantItem, 0, len(records))
	for _, record := range records {
		items = append(items, buildGameAccountDataGrantItem(record))
	}
	return items
}

func gameAccountGrantNowUTC() time.Time {
	return time.Now().UTC()
}
