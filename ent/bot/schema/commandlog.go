package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// CommandLog stores bot command invocation records for later lookup.
type CommandLog struct {
	ent.Schema
}

func (CommandLog) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "command_logs"},
	}
}

func (CommandLog) Fields() []ent.Field {
	return []ent.Field{
		field.String("platform").
			MaxLen(32).
			Default("").
			Comment("IM platform, e.g. qq"),
		field.String("pid").
			MaxLen(64).
			Default("").
			Comment("Bot platform instance identifier"),
		field.String("gid").
			MaxLen(128).
			Default("").
			Comment("Platform group id"),
		field.String("uid").
			MaxLen(128).
			Default("").
			Comment("Platform user id"),
		field.String("command").
			MaxLen(128).
			Default("").
			Comment("Matched command"),
		field.Time("created_at").
			Default(time.Now).
			Immutable().
			Comment("Record creation time"),
	}
}

func (CommandLog) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("platform"),
		index.Fields("pid"),
		index.Fields("gid"),
		index.Fields("uid"),
		index.Fields("command"),
		index.Fields("created_at"),
	}
}
