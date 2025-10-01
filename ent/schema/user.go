package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type User struct {
	ent.Schema
}

func (User) Fields() []ent.Field {
	return []ent.Field{
		field.String("name"),
		field.String("id").NotEmpty().Unique().Immutable(),
		field.String("email"),
		field.String("password_hash"),
		field.String("avatar_path").Optional().Nillable(),
		field.Bool("allow_cn_mysekai").Default(false),
	}
}

func (User) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("email_info", EmailInfo.Type).Unique(),
		edge.To("social_platform_info", SocialPlatformInfo.Type).Unique(), // User -> SocialPlatformInfo
		edge.To("authorized_social_platforms", AuthorizeSocialPlatformInfo.Type),
		edge.To("game_account_bindings", GameAccountBinding.Type),
	}
}
