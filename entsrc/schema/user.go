package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type User struct {
	ent.Schema
}

func (User) Fields() []ent.Field {
	return []ent.Field{
		field.String("name"),
		field.String("id").NotEmpty().Unique().Immutable(),
		field.String("email").Unique(),
		field.String("password_hash"),
		field.String("avatar_path").Optional().Nillable(),
		field.Bool("allow_cn_mysekai").Default(false),
		field.Enum("role").Values("user", "admin", "super_admin").Default("user"),
		field.Bool("banned").Default(false),
		field.String("ban_reason").Optional().Nillable(),
		field.Time("created_at").Optional().Nillable(),
	}
}

func (User) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("social_platform_info", SocialPlatformInfo.Type).Unique(),
		edge.To("authorized_social_platforms", AuthorizeSocialPlatformInfo.Type),
		edge.To("game_account_bindings", GameAccountBinding.Type),
		edge.To("ios_script_code", IOSScriptCode.Type).Unique(),
		edge.To("oauth_authorizations", OAuthAuthorization.Type),
		edge.To("oauth_tokens", OAuthToken.Type),
	}
}

func (User) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("created_at"),
		index.Fields("role", "banned"),
	}
}
