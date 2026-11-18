package tickets

import (
	"entgo.io/ent/dialect/sql"
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/ticket"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/ticketmessage"
)

// LimitAdminTicketVisibility applies the same ticket audience to reads and writes.
func LimitAdminTicketVisibility(role string) func(*sql.Selector) {
	return func(s *sql.Selector) {
		if role != adminCoreModule.RoleSuperAdmin {
			s.Where(ordinaryAdminTicketVisibility(s))
		}
	}
}

func ordinaryAdminTicketVisibility(s *sql.Selector) *sql.Predicate {
	return sql.Not(adminCoreModule.HasSuperAdminReference(s, ticket.FieldCreatorUserID, ticket.FieldAssigneeAdminID))
}

const AssignmentEventPrefix = "Assignee changed: "

// LimitAdminMessageVisibility keeps protected authors out of ordinary support
// views. Legacy assignment events lack authorship and embed participant names
// and IDs in the message text.
func LimitAdminMessageVisibility(role string) func(*sql.Selector) {
	return func(s *sql.Selector) {
		if role != adminCoreModule.RoleSuperAdmin {
			s.Where(sql.Not(adminCoreModule.HasSuperAdminReference(s, ticketmessage.FieldSenderUserID)))
			ticketmessage.Not(ticketmessage.And(
				ticketmessage.SenderRoleEQ(ticketmessage.SenderRoleSystem),
				ticketmessage.SenderUserIDIsNil(),
				ticketmessage.MessageHasPrefix(AssignmentEventPrefix),
			))(s)
		}
	}
}
