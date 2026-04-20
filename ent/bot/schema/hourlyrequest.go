package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

type HourlyRequests struct {
	ent.Schema
}

func (HourlyRequests) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "hourly_requests"},
	}
}

func (HourlyRequests) Fields() []ent.Field {
	return []ent.Field{
		field.Time("hour_key").
			SchemaType(map[string]string{
				"mysql": "datetime",
			}).
			Unique().
			Immutable().
			Comment("Primary key datetime (hour key)"),
		field.Int("count").
			Default(0).
			Comment("Request count for this hour"),
	}
}
