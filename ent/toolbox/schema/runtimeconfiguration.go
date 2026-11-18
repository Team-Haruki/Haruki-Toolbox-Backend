package schema

import (
	"encoding/json"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

type RuntimeConfiguration struct{ ent.Schema }

func (RuntimeConfiguration) Fields() []ent.Field {
	return []ent.Field{field.JSON("snapshot", json.RawMessage{})}
}

func (RuntimeConfiguration) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "runtime_configurations"}}
}
