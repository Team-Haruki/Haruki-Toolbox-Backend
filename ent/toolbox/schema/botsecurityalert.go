package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// BotSecurityAlert is one threshold crossing reported by Haruki Cloud for a
// bot (or, without a bot id, for a source address). Rows are written by the
// internal ingest endpoint and triaged by admins.
type BotSecurityAlert struct {
	ent.Schema
}

func (BotSecurityAlert) Fields() []ent.Field {
	return []ent.Field{
		field.String("kind").MaxLen(64).NotEmpty().Immutable(),
		field.String("bot_id").MaxLen(64).Optional().Nillable().Immutable(),
		// subject is what Cloud counted: bot_id, else source_ip, else
		// "global". It is part of the dedupe key because bot_id is nullable.
		field.String("subject").MaxLen(128).NotEmpty().Immutable(),
		field.String("source_ip").MaxLen(64).Default("").Immutable(),
		field.String("build_id").MaxLen(128).Default("").Immutable(),
		field.String("client_version").MaxLen(128).Default("").Immutable(),
		field.String("reason").MaxLen(1024).Default("").Immutable(),
		field.Bool("enforced").Default(false).Immutable(),
		field.Int64("count").Default(0).Immutable(),
		field.Int64("threshold").Default(0).Immutable(),
		field.Int64("window_seconds").Default(0).Immutable(),
		field.String("node").MaxLen(64).Default("").Immutable(),
		field.Time("alert_time").Immutable(),
		field.Time("received_at").Default(time.Now).Immutable(),
		field.Enum("status").Values("open", "resolved", "ignored").Default("open"),
		field.String("note").MaxLen(4000).Default(""),
		field.String("handled_by_user_id").MaxLen(64).Optional().Nillable(),
		field.Time("handled_at").Optional().Nillable(),
	}
}

func (BotSecurityAlert) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status", "alert_time").
			Annotations(entsql.DescColumns("alert_time")),
		index.Fields("alert_time"),
		index.Fields("bot_id"),
		index.Fields("kind", "subject", "node", "alert_time").Unique(),
	}
}

func (BotSecurityAlert) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "bot_security_alerts"},
	}
}
