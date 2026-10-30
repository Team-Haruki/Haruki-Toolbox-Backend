package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// InheritVaultKey is a Toolbox user's inherit-vault key pair. The private key
// is wrapped with a key derived from the user's password (or vault PIN) and
// then with the server master key; see internal/platform/inheritvault.
type InheritVaultKey struct {
	ent.Schema
}

func (InheritVaultKey) Fields() []ent.Field {
	return []ent.Field{
		field.String("user_id").NotEmpty().Unique().Immutable(),
		// key_version increases every time the key pair is replaced; vault
		// entries sealed to an older version can no longer be opened.
		field.Int("key_version").Positive(),
		field.Bytes("public_key"),
		field.Bytes("wrapped_private_key").Sensitive(),
		field.String("wrap_scheme").MaxLen(64),
		// secret_kind says which secret unwraps the key: the Toolbox password,
		// or a separate vault PIN for identities without one.
		field.Enum("secret_kind").Values("password", "pin"),
		// kdf_params is the Argon2id parameters and salt, as JSON.
		field.String("kdf_params").MaxLen(512),
		field.Int("master_key_version").Positive(),
		field.Time("created_at").Immutable(),
		field.Time("updated_at"),
	}
}

func (InheritVaultKey) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).
			Ref("inherit_vault_key").
			Unique().
			Required().
			Immutable().
			Field("user_id"),
	}
}
