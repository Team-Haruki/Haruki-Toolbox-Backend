package tickets

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/ticket"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/ticketmessage"
)

var (
	ErrServiceUnavailable  = errors.New("ticket service is unavailable")
	ErrPersistTicketStatus = errors.New("persist ticket status")
	ErrAppendSystemMessage = errors.New("append ticket system message")
	ErrTicketMessageLimit  = errors.New("ticket has reached the user message limit")
)

const maxUserTicketMessages = 500

type Service struct {
	db  *postgresql.Client
	now func() time.Time
}

func NewService(db *postgresql.Client, now func() time.Time) Service {
	if now == nil {
		now = time.Now
	}
	return Service{db: db, now: now}
}

func (s Service) AppendUserMessage(ctx context.Context, row *postgresql.Ticket, actorUserID, message string) (*postgresql.TicketMessage, error) {
	if s.db == nil || row == nil {
		return nil, ErrServiceUnavailable
	}
	if row.Status == ticket.StatusClosed {
		return nil, ErrTicketClosed
	}

	tx, err := s.db.Tx(ctx)
	if err != nil {
		return nil, err
	}
	rollback := func() { _ = tx.Rollback() }

	// Acquire the ticket row lock before reading the budget. The status
	// change and reply commit together, including when two requests race.
	update := tx.Ticket.UpdateOneID(row.ID).
		Where(ticket.StatusNEQ(ticket.StatusClosed), ticket.CreatorUserIDEQ(actorUserID)).
		SetStatus(ticket.StatusPendingAdmin).ClearClosedAt()
	if _, err := update.Save(ctx); err != nil {
		rollback()
		if postgresql.IsNotFound(err) {
			return nil, ErrTicketClosed
		}
		return nil, fmt.Errorf("%w: %v", ErrPersistTicketStatus, err)
	}
	last, err := tx.TicketMessage.Query().
		Where(ticketmessage.TicketIDEQ(row.ID), ticketmessage.SenderRoleEQ(ticketmessage.SenderRoleUser)).
		Offset(maxUserTicketMessages - 1).Limit(1).IDs(ctx)
	if err != nil {
		rollback()
		return nil, err
	}
	if len(last) != 0 {
		rollback()
		return nil, ErrTicketMessageLimit
	}
	created, err := tx.TicketMessage.Create().SetTicketID(row.ID).SetSenderUserID(actorUserID).
		SetSenderRole(ticketmessage.SenderRoleUser).SetInternal(false).SetMessage(message).Save(ctx)
	if err != nil {
		rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		rollback()
		return nil, err
	}
	return created, nil
}

func (s Service) AppendAdminMessage(ctx context.Context, row *postgresql.Ticket, actorUserID, actorRole, message string, internal bool) (*postgresql.TicketMessage, ticket.Status, error) {
	if s.db == nil || row == nil {
		return nil, "", ErrServiceUnavailable
	}

	tx, err := s.db.Tx(ctx)
	if err != nil {
		return nil, "", err
	}
	rollback := func() { _ = tx.Rollback() }
	current, err := LockAdminTicket(ctx, tx, row.ID, actorRole)
	if err != nil {
		rollback()
		return nil, "", err
	}

	created, err := tx.TicketMessage.Create().
		SetTicketID(row.ID).
		SetSenderUserID(actorUserID).
		SetSenderRole(ticketmessage.SenderRoleAdmin).
		SetInternal(internal).
		SetMessage(message).
		Save(ctx)
	if err != nil {
		rollback()
		return nil, "", err
	}

	if !internal && current.Status != ticket.StatusClosed {
		current, err = tx.Ticket.UpdateOneID(row.ID).SetStatus(ticket.StatusPendingUser).ClearClosedAt().Save(ctx)
		if err != nil {
			rollback()
			return nil, "", fmt.Errorf("%w: %v", ErrPersistTicketStatus, err)
		}
	}
	if err := tx.Commit(); err != nil {
		rollback()
		return nil, "", err
	}
	return created, current.Status, nil
}

func (s Service) Close(ctx context.Context, row *postgresql.Ticket) (*postgresql.Ticket, error) {
	if s.db == nil || row == nil {
		return nil, ErrServiceUnavailable
	}
	update := row.Update().SetStatus(ticket.StatusClosed)
	if row.ClosedAt == nil {
		update.SetClosedAt(s.now().UTC())
	}
	return update.Save(ctx)
}

func (s Service) UpdateStatus(ctx context.Context, row *postgresql.Ticket, actorUserID, actorRole string, next ticket.Status) (*postgresql.Ticket, error) {
	if s.db == nil || row == nil {
		return nil, ErrServiceUnavailable
	}
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return nil, err
	}
	rollback := func() { _ = tx.Rollback() }
	row, err = LockAdminTicket(ctx, tx, row.ID, actorRole)
	if err != nil {
		rollback()
		return nil, err
	}

	update := tx.Ticket.UpdateOneID(row.ID).SetStatus(next)
	if next == ticket.StatusClosed {
		if row.ClosedAt == nil {
			update.SetClosedAt(s.now().UTC())
		}
	} else {
		update.ClearClosedAt()
	}
	updated, err := update.Save(ctx)
	if err != nil {
		rollback()
		return nil, err
	}
	if row.Status != next {
		if err := AppendSystemMessage(ctx, tx, row.ID, actorUserID, BuildStatusEventMessage(row.Status, next)); err != nil {
			rollback()
			return nil, fmt.Errorf("%w: %v", ErrAppendSystemMessage, err)
		}
	}
	if err := tx.Commit(); err != nil {
		rollback()
		return nil, err
	}
	return updated, nil
}

// LockAdminTicket serializes ticket writes, then checks the ticket audience
// again in a fresh statement while the ticket row is reserved.
func LockAdminTicket(ctx context.Context, tx *postgresql.Tx, id int, actorRole string) (*postgresql.Ticket, error) {
	if _, err := tx.Ticket.Update().Where(ticket.IDEQ(id)).SetUpdatedAt(time.Now().UTC()).Save(ctx); err != nil {
		return nil, err
	}
	return tx.Ticket.Query().Where(ticket.IDEQ(id), LimitAdminTicketVisibility(actorRole)).Only(ctx)
}

func AppendSystemMessage(ctx context.Context, tx *postgresql.Tx, ticketID int, actorUserID, message string) error {
	if tx == nil {
		return ErrServiceUnavailable
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	_, err := tx.TicketMessage.Create().
		SetTicketID(ticketID).
		SetSenderUserID(actorUserID).
		SetSenderRole(ticketmessage.SenderRoleSystem).
		SetInternal(true).
		SetMessage(message).
		Save(ctx)
	return err
}

func BuildStatusEventMessage(previous, next ticket.Status) string {
	return fmt.Sprintf("Status changed: %s -> %s", FormatStatusLabel(previous), FormatStatusLabel(next))
}

func FormatStatusLabel(status ticket.Status) string {
	switch status {
	case ticket.StatusOpen:
		return "Open"
	case ticket.StatusPendingAdmin:
		return "Pending admin"
	case ticket.StatusPendingUser:
		return "Pending user"
	case ticket.StatusResolved:
		return "Resolved"
	case ticket.StatusClosed:
		return "Closed"
	default:
		value := strings.TrimSpace(string(status))
		if value == "" {
			return "Unknown"
		}
		return value
	}
}
