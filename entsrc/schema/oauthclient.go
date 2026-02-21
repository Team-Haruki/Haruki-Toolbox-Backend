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

type OAuthClient struct {
	ent.Schema
}

func (OAuthClient) Fields() []ent.Field {
	return []ent.Field{
		field.String("client_id").Unique().NotEmpty(),
		field.String("client_secret").NotEmpty(),
		field.String("name").NotEmpty(),
		field.String("client_type").Default("public").Comment("confidential or public"),
		field.JSON("redirect_uris", []string{}),
		field.JSON("scopes", []string{}),
		field.Bool("active").Default(true),
		field.Time("created_at").Default(time.Now),
	}
}

func (OAuthClient) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("authorizations", OAuthAuthorization.Type),
		edge.To("tokens", OAuthToken.Type),
	}
}

func (OAuthClient) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("client_id").Unique(),
	}
}

func (OAuthClient) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "oauth_clients"},
	}
}
