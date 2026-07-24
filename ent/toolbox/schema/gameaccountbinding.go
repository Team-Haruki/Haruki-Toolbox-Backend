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
		// At most one binding per user carries is_default=true (the user's
		// global default account); enforced in application transactions, not by
		// a DB constraint, so legacy rows may simply all be false.
		field.Bool("is_default").Default(false),
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
		// Index the implicit user FK column: every per-owner binding lookup
		// (browser /me and /settings eager loads, bot bindings resolution,
		// OAuth2 userinfo, iOS per-chunk ownership checks) filters on it, and
		// without an index each one sequential-scans a monotonically growing
		// table.
		index.Edges("user"),
	}
}

func (GameAccountBinding) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "game_account_bindings"},
	}
}
