package tickets

import (
	"context"
	"slices"

	"entgo.io/ent/dialect/sql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/ticketmessage"
)

const maxTicketMessagesPerPage = 100

// ReadMessagePage returns a chronological page of recent messages. The cursor
// is resolved within the already-scoped thread and handles equal timestamps.
func ReadMessagePage(ctx context.Context, query *postgresql.TicketMessageQuery, beforeID int) ([]*postgresql.TicketMessage, int, error) {
	if beforeID > 0 {
		anchor, err := query.Clone().Where(ticketmessage.IDEQ(beforeID)).
			Select(ticketmessage.FieldCreatedAt).Only(ctx)
		if err != nil {
			return nil, 0, err
		}
		query.Where(ticketmessage.Or(ticketmessage.CreatedAtLT(anchor.CreatedAt), ticketmessage.And(
			ticketmessage.CreatedAtEQ(anchor.CreatedAt), ticketmessage.IDLT(beforeID),
		)))
	}
	rows, err := query.Order(ticketmessage.ByCreatedAt(sql.OrderDesc()), ticketmessage.ByID(sql.OrderDesc())).
		Limit(maxTicketMessagesPerPage + 1).All(ctx)
	if err != nil {
		return nil, 0, err
	}
	var nextBeforeID int
	if len(rows) > maxTicketMessagesPerPage {
		rows = rows[:maxTicketMessagesPerPage]
		nextBeforeID = rows[len(rows)-1].ID
	}
	slices.Reverse(rows)
	return rows, nextBeforeID, nil
}
