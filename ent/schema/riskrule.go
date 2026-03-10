package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type RiskRule struct {
	ent.Schema
}

func (RiskRule) Fields() []ent.Field {
	return []ent.Field{
		field.String("rule_key").MaxLen(64).Unique(),
		field.String("description").MaxLen(300).Optional().Nillable(),
		field.JSON("config", map[string]any{}),
		field.Time("created_at").Default(time.Now),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
		field.String("updated_by").MaxLen(64).Optional().Nillable(),
	}
}

func (RiskRule) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("rule_key").Unique(),
		index.Fields("updated_at"),
	}
}

func (RiskRule) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "risk_rules"},
	}
}
