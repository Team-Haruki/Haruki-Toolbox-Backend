package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// StoryTranslation is a machine translation of one scenario's dialogue,
// produced for JP stories that have no official text in the target language.
// Lines are aligned with the scenario's TalkData by index.
type StoryTranslation struct {
	ent.Schema
}

func (StoryTranslation) Fields() []ent.Field {
	return []ent.Field{
		field.String("scenario_id").MaxLen(128).NotEmpty().Immutable(),
		// lang is a BCP 47 tag of the output (zh-Hans).
		field.String("lang").MaxLen(16).NotEmpty().Immutable(),
		field.String("source_region").MaxLen(8).Default("jp"),
		// source_hash is the SHA-256 of the source dialogue; a changed scenario
		// is translated again.
		field.String("source_hash").MaxLen(64).Default(""),
		field.Enum("status").Values("done", "failed").Default("done"),
		field.String("model").MaxLen(128).Default(""),
		// names maps source speaker labels to translated ones.
		field.JSON("names", map[string]string{}).Optional(),
		field.JSON("lines", []string{}).Optional(),
		field.Text("summary").Default(""),
		field.String("error").MaxLen(2000).Default(""),
		field.Int("attempts").Default(0),
		field.Int("prompt_tokens").Default(0),
		field.Int("completion_tokens").Default(0),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (StoryTranslation) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("scenario_id", "lang").Unique(),
		index.Fields("status", "updated_at"),
	}
}

func (StoryTranslation) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "story_translations"},
	}
}
