package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Ticket struct {
	ent.Schema
}

func (Ticket) Fields() []ent.Field {
	return []ent.Field{
		field.String("ticket_id").MaxLen(64).Unique(),
		field.String("creator_user_id").MaxLen(64),
		field.String("subject").MaxLen(200),
		field.String("category").MaxLen(64).Optional().Nillable(),
		field.Enum("priority").Values("low", "normal", "high", "urgent").Default("normal"),
		field.Enum("status").Values("open", "pending_user", "pending_admin", "resolved", "closed").Default("open"),
		field.String("assignee_admin_id").MaxLen(64).Optional().Nillable(),
		field.Time("created_at").Default(time.Now),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
		field.Time("closed_at").Optional().Nillable(),
		field.JSON("metadata", map[string]any{}).Optional(),
	}
}

func (Ticket) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("messages", TicketMessage.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

func (Ticket) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("ticket_id").Unique(),
		index.Fields("creator_user_id", "created_at"),
		index.Fields("status", "created_at"),
		index.Fields("assignee_admin_id", "created_at"),
	}
}

func (Ticket) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "tickets"},
	}
}
