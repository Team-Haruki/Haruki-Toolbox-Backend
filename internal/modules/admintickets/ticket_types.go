package admintickets

import (
	"haruki-suite/utils/database/postgresql/ticket"
	"time"
)

type adminTicketFilters struct {
	Query           string
	Status          ticket.Status
	Priority        ticket.Priority
	CreatorUserID   string
	AssigneeAdminID string
	Page            int
	PageSize        int
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
	TicketID        string     `json:"ticketId"`
	CreatorUserID   string     `json:"creatorUserId"`
	CreatorUserName string     `json:"creatorUserName,omitempty"`
	Subject         string     `json:"subject"`
	Category        string     `json:"category,omitempty"`
	Priority        string     `json:"priority"`
	Status          string     `json:"status"`
	AssigneeAdminID string     `json:"assigneeAdminId,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	ClosedAt        *time.Time `json:"closedAt,omitempty"`
}

type adminTicketMessageItem struct {
	ID           int       `json:"id"`
	SenderUserID string    `json:"senderUserId,omitempty"`
	SenderRole   string    `json:"senderRole"`
	Message      string    `json:"message"`
	Internal     bool      `json:"internal"`
	CreatedAt    time.Time `json:"createdAt"`
}

type adminTicketDetailResponse struct {
	Ticket   adminTicketListItem      `json:"ticket"`
	Messages []adminTicketMessageItem `json:"messages"`
}
