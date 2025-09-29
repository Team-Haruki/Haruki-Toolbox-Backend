package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type AuthorizeSocialPlatformInfo struct {
	ent.Schema
}

func (AuthorizeSocialPlatformInfo) Fields() []ent.Field {
	return []ent.Field{
		field.String("platform"),
		field.String("user_id"),
		field.String("comment").Optional(),
	}
}

func (AuthorizeSocialPlatformInfo) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("authorized_social_platforms").Unique(),
	}
}
