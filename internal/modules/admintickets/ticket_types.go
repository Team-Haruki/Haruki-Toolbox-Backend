package admintickets

import (
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/ticket"
	"time"
)

type adminTicketFilters struct {
	Query             string
	QuickFilter       adminTicketQuickFilter
	Status            ticket.Status
	Priority          ticket.Priority
	PriorityValues    []ticket.Priority
	CreatorUserID     string
	AssigneeAdminID   string
	RequireUnassigned bool
	Page              int
	PageSize          int
}

type adminTicketListResponse struct {
	GeneratedAt time.Time             `json:"generatedAt"`
	Page        int                   `json:"page"`
	PageSize    int                   `json:"pageSize"`
	Total       int                   `json:"total"`
	TotalPages  int                   `json:"totalPages"`
	HasMore     bool                  `json:"hasMore"`
	Items       []adminTicketListItem `json:"items"`
}

type adminAppendTicketMessagePayload struct {
	Message  string `json:"message"`
	Internal bool   `json:"internal"`
}

type adminUpdateTicketStatusPayload struct {
	Status string `json:"status"`
}

type adminAssignTicketPayload struct {
	AssigneeAdminID *string `json:"assigneeAdminId"`
}

type adminTicketListItem struct {
	TicketID              string     `json:"ticketId"`
	CreatorUserID         string     `json:"creatorUserId"`
	CreatorUserName       string     `json:"creatorUserName,omitempty"`
	Subject               string     `json:"subject"`
	Category              string     `json:"category,omitempty"`
	Priority              string     `json:"priority"`
	Status                string     `json:"status"`
	AssigneeAdminID       string     `json:"assigneeAdminId,omitempty"`
	AssigneeAdminName     string     `json:"assigneeAdminName,omitempty"`
	LastMessageSenderRole string     `json:"lastMessageSenderRole,omitempty"`
	LastMessagePreview    string     `json:"lastMessagePreview,omitempty"`
	LastMessageInternal   *bool      `json:"lastMessageInternal,omitempty"`
	CreatedAt             time.Time  `json:"createdAt"`
	UpdatedAt             time.Time  `json:"updatedAt"`
	LastMessageAt         *time.Time `json:"lastMessageAt,omitempty"`
	ClosedAt              *time.Time `json:"closedAt,omitempty"`
}

type adminTicketMessageItem struct {
	ID             int       `json:"id"`
	SenderUserID   string    `json:"senderUserId,omitempty"`
	SenderUserName string    `json:"senderUserName,omitempty"`
	SenderRole     string    `json:"senderRole"`
	Message        string    `json:"message"`
	Internal       bool      `json:"internal"`
	CreatedAt      time.Time `json:"createdAt"`
}

type adminTicketDetailResponse struct {
	Ticket   adminTicketListItem      `json:"ticket"`
	Messages []adminTicketMessageItem `json:"messages"`
}
