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

type GameAccountDataGrant struct {
	ent.Schema
}

func (GameAccountDataGrant) Fields() []ent.Field {
	return []ent.Field{
		field.String("owner_user_id").NotEmpty(),
		field.String("grantee_user_id").NotEmpty(),
		field.String("server").NotEmpty(),
		field.String("game_user_id").NotEmpty(),
		field.String("data_type").NotEmpty(),
		field.Time("expires_at"),
		field.Time("created_at").Default(time.Now),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (GameAccountDataGrant) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("owner", User.Type).
			Ref("game_account_data_grants_owned").
			Field("owner_user_id").
			Required().
			Unique(),
		edge.From("grantee", User.Type).
			Ref("game_account_data_grants_received").
			Field("grantee_user_id").
			Required().
			Unique(),
	}
}

func (GameAccountDataGrant) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("owner_user_id"),
		index.Fields("grantee_user_id"),
		index.Fields("server", "game_user_id", "data_type"),
		index.Fields("expires_at"),
		index.Fields("owner_user_id", "grantee_user_id", "server", "game_user_id", "data_type").Unique(),
	}
}

func (GameAccountDataGrant) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "game_account_data_grants"},
	}
}
