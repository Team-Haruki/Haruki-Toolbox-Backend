package schema

import (
	"entgo.io/ent"
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
