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

type WebhookSubscription struct {
	ent.Schema
}

func (WebhookSubscription) Fields() []ent.Field {
	return []ent.Field{
		field.String("user_id").NotEmpty(),
		field.String("server").NotEmpty(),
		field.String("data_type").NotEmpty(),
		field.String("webhook_id").NotEmpty(),
		field.Time("created_at").Default(time.Now),
	}
}

func (WebhookSubscription) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("endpoint", WebhookEndpoint.Type).
			Ref("subscriptions").
			Field("webhook_id").
			Required().
			Unique(),
	}
}

func (WebhookSubscription) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id", "server", "data_type"),
		index.Fields("webhook_id"),
		index.Fields("user_id", "server", "data_type", "webhook_id").Unique(),
	}
}

func (WebhookSubscription) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "webhook_subscriptions"},
	}
}
