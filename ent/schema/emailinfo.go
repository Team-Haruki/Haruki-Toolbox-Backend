package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type EmailInfo struct {
	ent.Schema
}

func (EmailInfo) Fields() []ent.Field {
	return []ent.Field{
		field.String("email").Unique(),
		field.Bool("verified").Default(false),
	}
}

func (EmailInfo) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).
			Ref("email_info").
			Unique(),
	}
}
