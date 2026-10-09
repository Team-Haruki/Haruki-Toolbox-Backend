package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// SponsorAfdianOrder is one paid Afdian order, keyed by out_trade_no. Orders
// are the history the Afdian part of a sponsor's duration is recomputed from.
// Only the fields that classification and display need are kept: no buyer
// address, phone or private id.
type SponsorAfdianOrder struct {
	ent.Schema
}

func (SponsorAfdianOrder) Fields() []ent.Field {
	return []ent.Field{
		// out_trade_no.
		field.String("id").MaxLen(128).NotEmpty().Unique().Immutable(),
		field.String("sponsor_id").MaxLen(128).NotEmpty(),
		field.String("afdian_user_id").MaxLen(128).NotEmpty(),
		// Empty for 自选方案 orders.
		field.String("plan_id").MaxLen(128).Default(""),
		field.String("plan_title").MaxLen(128).Default(""),
		// 0 = 常规方案, 1 = 售卖方案.
		field.Int("product_type").Default(0),
		field.Int("month").Default(0),
		field.Int("status").Default(0),
		field.String("total_amount").MaxLen(32).Default(""),
		field.String("show_amount").MaxLen(32).Default(""),
		field.String("remark").MaxLen(1000).Default(""),
		// create_time of the order; the receive time when Afdian omits it.
		field.Time("paid_at"),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (SponsorAfdianOrder) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("sponsor_id", "paid_at"),
		index.Fields("afdian_user_id"),
	}
}

func (SponsorAfdianOrder) Edges() []ent.Edge {
	return nil
}

func (SponsorAfdianOrder) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "sponsor_afdian_orders"},
	}
}
