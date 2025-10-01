package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type SocialPlatformInfo struct {
	ent.Schema
}

func (SocialPlatformInfo) Fields() []ent.Field {
	return []ent.Field{
		field.String("platform"),
		field.String("platform_user_id"),
		field.Bool("verified").Default(false),
		field.String("user_social_platform_info"),
	}
}

func (SocialPlatformInfo) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).
			Ref("social_platform_info").
			Field("user_social_platform_info").
			Unique().
			Required(),
	}
}
