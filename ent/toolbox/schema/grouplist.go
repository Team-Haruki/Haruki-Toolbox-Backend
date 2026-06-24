package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type GroupList struct {
	ent.Schema
}

func (GroupList) Fields() []ent.Field {
	return []ent.Field{
		field.Int("id").
			Unique().
			Immutable(),
		field.String("name").
			MaxLen(64),
		field.String("avatar").
			MaxLen(500).
			Optional().
			Nillable(),
		field.String("bg").
			MaxLen(500).
			Optional().
			Nillable(),
		field.String("group_info").
			MaxLen(100),
		field.String("detail").
			MaxLen(300),
		field.Int("sort_order").
			Default(0).
			Comment("Manual ordering weight; ascending, lower values appear first."),
	}
}

func (GroupList) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("group", Group.Type).
			Ref("group_list").
			Unique().
			Required(),
	}
}
