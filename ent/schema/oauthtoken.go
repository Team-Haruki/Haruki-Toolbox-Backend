package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type OAuthToken struct {
	ent.Schema
}

func (OAuthToken) Fields() []ent.Field {
	return []ent.Field{
		field.String("access_token").Unique().NotEmpty(),
		field.String("refresh_token").Unique().Optional().Nillable(),
		field.JSON("scopes", []string{}),
		field.Time("expires_at").Optional().Nillable(),
		field.Bool("revoked").Default(false),
		field.Time("created_at").Default(time.Now),
	}
}

func (OAuthToken) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("oauth_tokens").Unique().Required(),
		edge.From("client", OAuthClient.Type).Ref("tokens").Unique().Required(),
	}
}

func (OAuthToken) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "oauth_tokens"},
	}
}
