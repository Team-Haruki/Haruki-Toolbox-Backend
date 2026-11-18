package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

type RuntimeConfiguration struct{ ent.Schema }

func (RuntimeConfiguration) Fields() []ent.Field {
	return []ent.Field{field.JSON("snapshot", map[string]any{}).Sensitive()}
}

func (RuntimeConfiguration) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{
		Table: "runtime_configurations",
		// PostgreSQL validates the JSONB syntax. Require object delimiters
		// with the same expression in SQLite's Ent test schema.
		Checks: map[string]string{"runtime_config_snapshot_object": `TRIM(CAST(snapshot AS TEXT)) LIKE '{%}'`},
	}}
}
