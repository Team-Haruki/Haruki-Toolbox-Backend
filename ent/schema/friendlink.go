package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

type FriendLink struct {
	ent.Schema
}

func (FriendLink) Fields() []ent.Field {
	return []ent.Field{
		field.Int("id").
			Unique().
			Immutable(),
		field.String("name").
			MaxLen(100),
		field.String("description").
			MaxLen(300),
		field.String("avatar").
			MaxLen(500),
		field.String("url").
			MaxLen(500),
		field.JSON("tags", []string{}).
			Optional(),
	}
}

func (FriendLink) Edges() []ent.Edge {
	return nil
}
