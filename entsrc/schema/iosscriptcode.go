package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type IOSScriptCode struct {
	ent.Schema
}

func (IOSScriptCode) Fields() []ent.Field {
	return []ent.Field{
		field.String("user_id").
			Unique(),
		field.String("upload_code").
			MaxLen(10).
			Unique(),
	}
}

func (IOSScriptCode) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).
			Ref("ios_script_code").
			Unique().
			Required().
			Field("user_id"),
	}
}
