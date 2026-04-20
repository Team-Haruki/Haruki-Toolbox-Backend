package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// CommandManifest holds the schema definition for the CommandManifest entity.
// Each row describes one bot API endpoint and the command prefixes that map to it.
// The Bot client downloads the full manifest at startup to pre-match user commands
// to the correct endpoint without a round-trip to the server.
type CommandManifest struct {
	ent.Schema
}

func (CommandManifest) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "command_manifests"},
	}
}

func (CommandManifest) Fields() []ent.Field {
	return []ent.Field{
		// command_prefixes is the list of text prefixes that trigger this endpoint,
		// e.g. ["/查卡", "/card", "/cards"]. Stored as a JSON array.
		field.JSON("command_prefixes", []string{}),

		// command_priority controls matching order when multiple entries could match.
		// Higher value = matched first. Default 0.
		field.Int("command_priority").
			Default(0).
			Comment("Higher value is matched first"),

		// command_mode is the accepted HTTP method(s), e.g. "GET", "POST", "GET,POST".
		field.String("command_mode").
			MaxLen(16),

		// command_module is the top-level game module, e.g. "pjsk", "chunithm".
		field.String("command_module").
			MaxLen(64),

		// command_path is the path relative to the module base,
		// e.g. "card/detail" (no leading slash).
		field.String("command_path").
			MaxLen(256),

		// command_additional_params is an optional list of extra query parameter
		// names the endpoint accepts, e.g. ["server", "rank"]. Stored as JSON.
		field.JSON("command_additional_params", []string{}).
			Optional(),
	}
}

func (CommandManifest) Edges() []ent.Edge {
	return nil
}

func (CommandManifest) Indexes() []ent.Index {
	return []ent.Index{
		// Each (module, path) pair must be unique — one manifest entry per endpoint.
		index.Fields("command_module", "command_path").Unique(),
	}
}
