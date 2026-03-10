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

type TicketMessage struct {
	ent.Schema
}

func (TicketMessage) Fields() []ent.Field {
	return []ent.Field{
		field.Int("ticket_id"),
		field.String("sender_user_id").MaxLen(64).Optional().Nillable(),
		field.Enum("sender_role").Values("user", "admin", "system").Default("user"),
		field.String("message").MaxLen(4000),
		field.Bool("internal").Default(false),
		field.Time("created_at").Default(time.Now),
	}
}

func (TicketMessage) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("ticket", Ticket.Type).
			Ref("messages").
			Field("ticket_id").
			Required().
			Unique(),
	}
}

func (TicketMessage) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("ticket_id", "created_at"),
	}
}

func (TicketMessage) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "ticket_messages"},
	}
}
