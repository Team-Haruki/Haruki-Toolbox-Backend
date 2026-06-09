package usertickets

import (
	"crypto/rand"
	"encoding/hex"
	"haruki-suite/utils/database/postgresql"
	"time"
)

func generateTicketPublicID() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "TK-" + time.Now().UTC().Format("20060102150405") + "-" + hex.EncodeToString(b), nil
}

func buildUserTicketListItem(row *postgresql.Ticket) userTicketListItem {
	item := userTicketListItem{
		TicketID:  row.TicketID,
		Subject:   row.Subject,
		Priority:  string(row.Priority),
		Status:    string(row.Status),
		CreatedAt: row.CreatedAt.UTC(),
		UpdatedAt: row.UpdatedAt.UTC(),
	}
	if row.Category != nil {
		item.Category = *row.Category
	}
	if row.AssigneeAdminID != nil {
		item.AssigneeAdminID = *row.AssigneeAdminID
	}
	if row.ClosedAt != nil {
		closed := row.ClosedAt.UTC()
		item.ClosedAt = &closed
	}
	return item
}

func buildUserTicketMessageItems(rows []*postgresql.TicketMessage) []userTicketMessageItem {
	items := make([]userTicketMessageItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, userTicketMessageItem{
			ID:         row.ID,
			SenderRole: string(row.SenderRole),
			Message:    row.Message,
			CreatedAt:  row.CreatedAt.UTC(),
		})
	}
	return items
}
