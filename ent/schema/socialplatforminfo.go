package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

type SocialPlatformInfo struct {
	ent.Schema
}

func (SocialPlatformInfo) Fields() []ent.Field {
	return []ent.Field{
		field.String("platform"),
		field.String("user_id"),
		field.Bool("verified").Default(false),
	}
}
