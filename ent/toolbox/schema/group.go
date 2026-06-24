package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
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
		field.Int("sort_order").
			Default(0).
			Comment("Manual ordering weight; ascending, lower values appear first."),
	}
}

func (Group) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("group_list", GroupList.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}
