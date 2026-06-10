package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type OAuth2ClientWebhookEndpoint struct {
	ent.Schema
}

func (OAuth2ClientWebhookEndpoint) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").NotEmpty().Unique().Immutable(),
		field.String("client_id").NotEmpty(),
		field.String("callback_url").NotEmpty(),
		field.String("bearer").Optional().Nillable(),
		field.Bool("enabled").Default(true),
		field.Time("created_at").Default(time.Now),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (OAuth2ClientWebhookEndpoint) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("client_id"),
		index.Fields("client_id", "callback_url").Unique(),
	}
}

func (OAuth2ClientWebhookEndpoint) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "oauth2_client_webhook_endpoints"},
	}
}
