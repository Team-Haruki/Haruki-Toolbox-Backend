package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

type RequestsRanking struct {
	ent.Schema
}

func (RequestsRanking) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "requests_ranking"},
	}
}

func (RequestsRanking) Fields() []ent.Field {
	return []ent.Field{
		field.Int("bot_id").
			Unique().
			Comment("Bot ID, primary key"),
		field.Int64("counts").
			Optional().
			Comment("Total request counts (bigint)"),
	}
}
