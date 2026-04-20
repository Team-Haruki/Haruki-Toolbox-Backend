package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type GameAccountBinding struct {
	ent.Schema
}

func (GameAccountBinding) Fields() []ent.Field {
	return []ent.Field{
		field.String("server").Comment("jp | en | tw | kr | cn"),
		field.String("game_user_id"),
		field.Bool("verified").Default(false),
		field.JSON("suite", &SuiteDataPrivacySettings{}).Optional(),
		field.JSON("mysekai", &MysekaiDataPrivacySettings{}).Optional(),
	}
}

func (GameAccountBinding) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("game_account_bindings").Unique(),
	}
}

func (GameAccountBinding) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("server", "game_user_id").Unique(),
	}
}

func (GameAccountBinding) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "game_account_bindings"},
	}
}
