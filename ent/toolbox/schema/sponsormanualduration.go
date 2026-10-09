package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// SponsorManualDuration is sponsorship time an admin recorded for support
// received outside Afdian. Entries stack after the Afdian time (see
// internal/modules/sponsor/duration.go). They are admin-only: the public
// sponsor wall never shows the note or who entered it.
type SponsorManualDuration struct {
	ent.Schema
}

func (SponsorManualDuration) Fields() []ent.Field {
	return []ent.Field{
		field.String("sponsor_id").MaxLen(128).NotEmpty(),
		field.Int("amount").Positive(),
		// A month is 31 days, the length Afdian gives one month.
		field.Enum("unit").Values("day", "month"),
		// Earliest start: the entry begins at the later of starts_at and the
		// end of everything before it.
		field.Time("starts_at"),
		field.String("note").MaxLen(500).NotEmpty(),
		// admin: entered in the admin UI; migration: split from the legacy
		// single expiry when the two sources were introduced.
		field.Enum("origin").Values("admin", "migration").Default("admin"),
		field.String("created_by").MaxLen(128).NotEmpty().Immutable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.String("updated_by").MaxLen(128).Optional().Nillable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (SponsorManualDuration) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("sponsor_id", "starts_at"),
	}
}

func (SponsorManualDuration) Edges() []ent.Edge {
	return nil
}

func (SponsorManualDuration) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "sponsor_manual_durations"},
	}
}
