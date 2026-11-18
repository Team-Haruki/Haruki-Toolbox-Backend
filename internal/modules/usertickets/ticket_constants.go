package usertickets

import "time"

const (
	defaultUserTicketPage      = 1
	defaultUserTicketPageSize  = 20
	maxUserTicketPageSize      = 100
	ticketNotificationWindow   = 10 * time.Minute
	ticketNotificationsPerUser = 20
)
