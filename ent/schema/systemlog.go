package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type SystemLog struct {
	ent.Schema
}

func (SystemLog) Fields() []ent.Field {
	return []ent.Field{
		field.Time("event_time").Default(time.Now),
		field.String("actor_user_id").MaxLen(64).Optional().Nillable(),
		field.String("actor_role").MaxLen(32).Optional().Nillable(),
		field.Enum("actor_type").Values("anonymous", "user", "admin", "system").Default("anonymous"),
		field.String("action").MaxLen(128).NotEmpty(),
		field.String("target_type").MaxLen(64).Optional().Nillable(),
		field.String("target_id").MaxLen(128).Optional().Nillable(),
		field.Enum("result").Values("success", "failure").Default("success"),
		field.String("ip").MaxLen(128).Optional().Nillable(),
		field.String("user_agent").MaxLen(1024).Optional().Nillable(),
		field.String("method").MaxLen(16).Optional().Nillable(),
		field.String("path").MaxLen(512).Optional().Nillable(),
		field.String("request_id").MaxLen(128).Optional().Nillable(),
		field.JSON("metadata", map[string]any{}).Optional(),
	}
}

func (SystemLog) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("event_time"),
		index.Fields("actor_user_id", "event_time"),
		index.Fields("action", "event_time"),
		index.Fields("target_type", "target_id", "event_time"),
	}
}

func (SystemLog) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "system_logs"},
	}
}
