package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type OAuthAuthorization struct {
	ent.Schema
}

func (OAuthAuthorization) Fields() []ent.Field {
	return []ent.Field{
		field.JSON("scopes", []string{}),
		field.Time("created_at").Default(time.Now),
		field.Bool("revoked").Default(false),
	}
}

func (OAuthAuthorization) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("oauth_authorizations").Unique().Required(),
		edge.From("client", OAuthClient.Type).Ref("authorizations").Unique().Required(),
	}
}

func (OAuthAuthorization) Indexes() []ent.Index {
	return []ent.Index{
		index.Edges("user", "client").Unique(),
	}
}

func (OAuthAuthorization) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "oauth_authorizations"},
	}
}
