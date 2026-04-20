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

type WebhookEndpoint struct {
	ent.Schema
}

func (WebhookEndpoint) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").NotEmpty().Unique().Immutable(),
		field.String("credential").NotEmpty(),
		field.String("callback_url").NotEmpty(),
		field.String("bearer").Optional().Nillable(),
		field.Bool("enabled").Default(true),
		field.Time("created_at").Default(time.Now),
	}
}

func (WebhookEndpoint) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("subscriptions", WebhookSubscription.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

func (WebhookEndpoint) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("credential"),
	}
}

func (WebhookEndpoint) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "webhook_endpoints"},
	}
}
