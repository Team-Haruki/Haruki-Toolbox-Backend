package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type RiskEvent struct {
	ent.Schema
}

func (RiskEvent) Fields() []ent.Field {
	return []ent.Field{
		field.Time("event_time").Default(time.Now),
		field.Enum("status").Values("open", "resolved").Default("open"),
		field.Enum("severity").Values("low", "medium", "high", "critical").Default("medium"),
		field.String("source").MaxLen(64).Default("manual"),
		field.String("actor_user_id").MaxLen(64).Optional().Nillable(),
		field.String("target_user_id").MaxLen(64).Optional().Nillable(),
		field.String("ip").MaxLen(128).Optional().Nillable(),
		field.String("action").MaxLen(128).Optional().Nillable(),
		field.String("reason").MaxLen(300).Optional().Nillable(),
		field.Time("resolved_at").Optional().Nillable(),
		field.String("resolved_by").MaxLen(64).Optional().Nillable(),
		field.JSON("metadata", map[string]any{}).Optional(),
	}
}

func (RiskEvent) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("event_time"),
		index.Fields("status", "event_time"),
		index.Fields("target_user_id", "event_time"),
		index.Fields("actor_user_id", "event_time"),
		index.Fields("ip", "event_time"),
	}
}

func (RiskEvent) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "risk_events"},
	}
}
