package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// InheritVaultTombstone fences delayed fallback writes after destruction. It
// contains only the owner, entry identifier and replay-retention deadline.
type InheritVaultTombstone struct{ ent.Schema }

func (InheritVaultTombstone) Fields() []ent.Field {
	return []ent.Field{field.String("user_id").NotEmpty(), field.String("entry_id").NotEmpty(), field.Time("expires_at")}
}

func (InheritVaultTombstone) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "entry_id").Unique(), index.Fields("expires_at")}
}

func (InheritVaultTombstone) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "inherit_vault_tombstones"}}
}
