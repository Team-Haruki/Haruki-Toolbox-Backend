package usertickets

import "time"

type userTicketListItem struct {
	TicketID        string     `json:"ticketId"`
	Subject         string     `json:"subject"`
	Category        string     `json:"category,omitempty"`
	Priority        string     `json:"priority"`
	Status          string     `json:"status"`
	AssigneeAdminID string     `json:"assigneeAdminId,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	ClosedAt        *time.Time `json:"closedAt,omitempty"`
}

type userTicketListResponse struct {
	GeneratedAt time.Time            `json:"generatedAt"`
	Page        int                  `json:"page"`
	PageSize    int                  `json:"pageSize"`
	Total       int                  `json:"total"`
	TotalPages  int                  `json:"totalPages"`
	HasMore     bool                 `json:"hasMore"`
	Items       []userTicketListItem `json:"items"`
}

type userTicketMessageItem struct {
	ID         int       `json:"id"`
	SenderRole string    `json:"senderRole"`
	Message    string    `json:"message"`
	CreatedAt  time.Time `json:"createdAt"`
}

type userTicketDetailResponse struct {
	Ticket   userTicketListItem      `json:"ticket"`
	Messages []userTicketMessageItem `json:"messages"`
}

type createUserTicketPayload struct {
	Subject  string         `json:"subject"`
	Category string         `json:"category,omitempty"`
	Priority string         `json:"priority,omitempty"`
	Message  string         `json:"message"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type createUserTicketResponse struct {
	TicketID string `json:"ticketId"`
}

type appendUserTicketMessagePayload struct {
	Message string `json:"message"`
}
