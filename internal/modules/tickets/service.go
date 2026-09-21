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
)

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

	created, err := tx.TicketMessage.Create().
		SetTicketID(row.ID).
		SetSenderUserID(actorUserID).
		SetSenderRole(ticketmessage.SenderRoleUser).
		SetInternal(false).
		SetMessage(message).
		Save(ctx)
	if err != nil {
		rollback()
		return nil, err
	}

	update := tx.Ticket.UpdateOneID(row.ID).SetStatus(ticket.StatusPendingAdmin)
	if row.ClosedAt != nil {
		update.ClearClosedAt()
	}
	if _, err := update.Save(ctx); err != nil {
		rollback()
		return nil, fmt.Errorf("%w: %v", ErrPersistTicketStatus, err)
	}
	if err := tx.Commit(); err != nil {
		rollback()
		return nil, err
	}
	return created, nil
}

func (s Service) AppendAdminMessage(ctx context.Context, row *postgresql.Ticket, actorUserID, message string, internal bool) (*postgresql.TicketMessage, error) {
	if s.db == nil || row == nil {
		return nil, ErrServiceUnavailable
	}

	tx, err := s.db.Tx(ctx)
	if err != nil {
		return nil, err
	}
	rollback := func() { _ = tx.Rollback() }

	created, err := tx.TicketMessage.Create().
		SetTicketID(row.ID).
		SetSenderUserID(actorUserID).
		SetSenderRole(ticketmessage.SenderRoleAdmin).
		SetInternal(internal).
		SetMessage(message).
		Save(ctx)
	if err != nil {
		rollback()
		return nil, err
	}

	if !internal && row.Status != ticket.StatusClosed {
		update := tx.Ticket.UpdateOneID(row.ID).SetStatus(ticket.StatusPendingUser)
		if row.ClosedAt != nil {
			update.ClearClosedAt()
		}
		if _, err := update.Save(ctx); err != nil {
			rollback()
			return nil, fmt.Errorf("%w: %v", ErrPersistTicketStatus, err)
		}
	}
	if err := tx.Commit(); err != nil {
		rollback()
		return nil, err
	}
	return created, nil
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

func (s Service) UpdateStatus(ctx context.Context, row *postgresql.Ticket, next ticket.Status) (*postgresql.Ticket, error) {
	if s.db == nil || row == nil {
		return nil, ErrServiceUnavailable
	}
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return nil, err
	}
	rollback := func() { _ = tx.Rollback() }

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
		if err := appendSystemMessage(ctx, tx, row.ID, BuildStatusEventMessage(row.Status, next)); err != nil {
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

func AppendSystemMessage(ctx context.Context, tx *postgresql.Tx, ticketID int, message string) error {
	if tx == nil {
		return ErrServiceUnavailable
	}
	return appendSystemMessage(ctx, tx, ticketID, message)
}

func appendSystemMessage(ctx context.Context, tx *postgresql.Tx, ticketID int, message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	_, err := tx.TicketMessage.Create().
		SetTicketID(ticketID).
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
