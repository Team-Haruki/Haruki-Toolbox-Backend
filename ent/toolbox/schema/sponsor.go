package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Sponsor is one supporter on the sponsor wall: an Afdian user (id
// "afdian_<user_id>") or a supporter an admin entered by hand. Duration is
// split into two sources, merged by the sponsor module (see
// internal/modules/sponsor/duration.go):
//   - Afdian time, derived from the rows in sponsor_afdian_orders plus the
//     plan expiry Afdian last reported (afdian_reported_expires_at);
//   - manual time, the entries in sponsor_manual_durations.
//
// plan_expires_at, is_active, has_duration, afdian_expires_at and
// afdian_duration_months are caches the module recomputes from those sources;
// nothing else writes them once duration_split_at is set.
type Sponsor struct {
	ent.Schema
}

func (Sponsor) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").MaxLen(128).NotEmpty().Unique().Immutable(),
		field.String("afdian_user_id").MaxLen(128).Optional().Nillable(),
		field.String("out_trade_no").MaxLen(128).Optional().Nillable().Unique(),
		field.String("name").MaxLen(128).Optional().Nillable(),
		field.String("avatar").MaxLen(500).Optional().Nillable(),
		field.String("plan_id").MaxLen(128).Optional().Nillable(),
		field.String("plan_name").MaxLen(128).Optional().Nillable(),
		field.Int("plan_rank").Default(0),
		field.Int("plan_pay_months").Optional().Nillable(),
		field.String("message").MaxLen(1000).Optional().Nillable(),
		field.Enum("source").Values("afdian", "manual", "legacy", "imported").Default("afdian"),
		field.Bool("is_active").Default(true),
		field.Bool("afdian_sync_disabled").Default(false),
		field.Time("paid_at").Optional().Nillable(),
		// Effective expiry: Afdian time followed by the manual entries.
		field.Time("plan_expires_at").Optional().Nillable(),
		// End of the Afdian time alone.
		field.Time("afdian_expires_at").Optional().Nillable(),
		// Months granted by Afdian duration orders (one-time orders excluded).
		field.Int("afdian_duration_months").Default(0),
		// current_plan.expire_time from query-sponsor, kept after the plan
		// lapses (Afdian stops reporting it then), and when it was observed.
		field.Time("afdian_reported_expires_at").Optional().Nillable(),
		field.Time("afdian_reported_at").Optional().Nillable(),
		// True once the sponsor has had any duration (Afdian or manual).
		field.Bool("has_duration").Default(false),
		// When the pre-split expiry was divided into Afdian and manual time.
		// Nil means the row still carries the legacy single expiry.
		field.Time("duration_split_at").Optional().Nillable(),
		field.Int("support_count").Default(1),
		field.String("total_amount").MaxLen(32).Optional().Nillable(),
		field.JSON("raw", map[string]any{}).Optional(),
		field.Time("created_at").Default(time.Now),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (Sponsor) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("afdian_user_id"),
		index.Fields("out_trade_no").Unique(),
		index.Fields("source", "is_active"),
		index.Fields("plan_rank", "created_at"),
		index.Fields("plan_expires_at"),
	}
}

func (Sponsor) Edges() []ent.Edge {
	return nil
}

func (Sponsor) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "sponsors"},
	}
}
