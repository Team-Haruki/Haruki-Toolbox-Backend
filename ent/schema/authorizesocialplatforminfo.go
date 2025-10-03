package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type AuthorizeSocialPlatformInfo struct {
	ent.Schema
}

func (AuthorizeSocialPlatformInfo) Fields() []ent.Field {
	return []ent.Field{
		field.String("user_id"),
		field.String("platform"),
		field.String("platform_user_id"),
		field.Int("platform_id"),
		field.String("comment").Optional(),
	}
}

func (AuthorizeSocialPlatformInfo) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("authorized_social_platforms").Field("user_id").Required().Unique(),
	}
}

func (AuthorizeSocialPlatformInfo) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id", "platform_id").Unique(),
	}
}
