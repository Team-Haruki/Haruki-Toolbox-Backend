package admintickets

import (
	"time"
)

const (
	adminAuditActionTicketMessageAppend = "admin.ticket.message.append"
	adminAuditActionTicketStatusUpdate  = "admin.ticket.status.update"
	adminAuditActionTicketAssign        = "admin.ticket.assign"
	adminAuditTargetTypeTicket          = "ticket"
)

var adminNow = time.Now

func adminNowUTC() time.Time {
	return adminNow().UTC()
}
