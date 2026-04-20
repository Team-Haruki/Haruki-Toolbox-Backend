package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

type DailyRequests struct {
	ent.Schema
}

func (DailyRequests) Annotations() []schema.Annotation {
	return []schema.Annotation{
		// 对应 MySQL 表名
		entsql.Annotation{Table: "daily_requests"},
	}
}

func (DailyRequests) Fields() []ent.Field {
	return []ent.Field{
		field.Time("date_key").
			SchemaType(map[string]string{
				"mysql": "date",
			}).
			Unique().
			Immutable().
			Comment("Primary key date"),
		field.Int("count").
			Default(0).
			Comment("Request count for this date"),
	}
}
