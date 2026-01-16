package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type Group struct {
	ent.Schema
}

func (Group) Fields() []ent.Field {
	return []ent.Field{
		field.Int("id").
			Unique().
			Immutable(),
		field.String("group").
			MaxLen(64).
			Unique(),
	}
}

func (Group) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("group_list", GroupList.Type),
	}
}
