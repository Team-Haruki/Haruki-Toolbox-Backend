package schema

import (
	"slices"
	"testing"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
)

type inheritVaultSchemaDef interface {
	Fields() []ent.Field
	Edges() []ent.Edge
}

func fieldsByName(def inheritVaultSchemaDef) map[string]ent.Field {
	out := map[string]ent.Field{}
	for _, f := range def.Fields() {
		out[f.Descriptor().Name] = f
	}
	return out
}

// Sealed secrets must stay out of ent's String() output and debug logs, and
// both tables hang off the user so deleting an account removes them.
func TestInheritVaultSchemas(t *testing.T) {
	cases := []struct {
		name      string
		def       inheritVaultSchemaDef
		sensitive []string
		immutable []string
		ref       string
	}{
		{"inherit_vault_entries", InheritVaultEntry{}, []string{"credential_sealed", "code_sealed"}, []string{"id", "user_id", "server", "game_user_id", "created_at"}, "inherit_vault_entries"},
		{"inherit_vault_keys", InheritVaultKey{}, []string{"wrapped_private_key"}, []string{"user_id", "created_at"}, "inherit_vault_key"},
	}
	for _, tc := range cases {
		fields := fieldsByName(tc.def)
		for _, name := range tc.sensitive {
			if f, ok := fields[name]; !ok || !f.Descriptor().Sensitive {
				t.Errorf("%s.%s must exist and be Sensitive", tc.name, name)
			}
		}
		for _, name := range tc.immutable {
			if f, ok := fields[name]; !ok || !f.Descriptor().Immutable {
				t.Errorf("%s.%s must exist and be Immutable", tc.name, name)
			}
		}
		for name, f := range fields {
			if f.Descriptor().Sensitive && !slices.Contains(tc.sensitive, name) {
				t.Errorf("%s.%s is Sensitive but not listed here", tc.name, name)
			}
		}
		edges := tc.def.Edges()
		if len(edges) != 1 {
			t.Fatalf("%s edges = %d, want 1", tc.name, len(edges))
		}
		e := edges[0].Descriptor()
		if e.Name != "user" || !e.Inverse || !e.Unique || !e.Required || e.Field != "user_id" || e.RefName != tc.ref {
			t.Errorf("%s user edge = %+v", tc.name, e)
		}
	}
	if n := len(InheritVaultEntry{}.Indexes()); n != 3 {
		t.Errorf("inherit_vault_entries indexes = %d, want 3 (owner list, credential and code expiry)", n)
	}
}

func TestUserCascadesToInheritVault(t *testing.T) {
	want := map[string]bool{"inherit_vault_key": true, "inherit_vault_entries": false}
	for _, e := range (User{}).Edges() {
		d := e.Descriptor()
		unique, ok := want[d.Name]
		if !ok {
			continue
		}
		delete(want, d.Name)
		if d.Unique != unique {
			t.Errorf("user.%s unique = %v, want %v", d.Name, d.Unique, unique)
		}
		cascade := false
		for _, a := range d.Annotations {
			if sa, ok := a.(*entsql.Annotation); ok && sa.OnDelete == entsql.Cascade {
				cascade = true
			}
		}
		if !cascade {
			t.Errorf("user.%s must cascade on delete", d.Name)
		}
	}
	if len(want) != 0 {
		t.Errorf("user edges missing: %v", want)
	}
}
