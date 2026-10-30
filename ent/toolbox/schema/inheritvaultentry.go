package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// InheritVaultEntry is a game login credential (and the transfer code issued
// with it) that an inherit job obtained for its owner. Both secrets are
// sealed to the owner's InheritVaultKey public key; the row keeps only
// metadata in the clear. Rows are hard-deleted when the credential expires or
// after the owner destroys them.
type InheritVaultEntry struct {
	ent.Schema
}

func (InheritVaultEntry) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").NotEmpty().Unique().Immutable(),
		field.String("user_id").NotEmpty().Immutable(),
		field.Enum("server").Values("jp", "en").Immutable(),
		field.Int64("game_user_id").Immutable(),
		field.String("job_id").Optional(),
		// key_version and wrap_scheme identify the key and format the sealed
		// fields were written with, so either can change later.
		field.Int("key_version").Positive(),
		field.String("wrap_scheme").MaxLen(64),
		field.Bytes("credential_sealed").Sensitive(),
		field.Bytes("code_sealed").Optional().Nillable().Sensitive(),
		field.Time("code_issued_at").Optional().Nillable(),
		field.Time("code_expires_at").Optional().Nillable(),
		field.Time("credential_expires_at"),
		field.Time("created_at").Immutable(),
		field.Time("updated_at"),
		field.Time("destroyed_at").Optional().Nillable(),
	}
}

func (InheritVaultEntry) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).
			Ref("inherit_vault_entries").
			Unique().
			Required().
			Immutable().
			Field("user_id"),
	}
}

func (InheritVaultEntry) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id", "created_at"),
		index.Fields("credential_expires_at"),
		index.Fields("code_expires_at"),
	}
}
