package schema

import (
	"slices"
	"testing"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
)

type sponsorSchemaDef interface {
	Fields() []ent.Field
	Indexes() []ent.Index
	Edges() []ent.Edge
	Annotations() []schema.Annotation
}

// The operations docs carry hand-written DDL for these tables (for
// deployments without auto_migrate); a renamed column must update it too.
func TestSponsorDurationSchemaColumns(t *testing.T) {
	cases := []struct {
		def     sponsorSchemaDef
		table   string
		columns []string
	}{
		{Sponsor{}, "sponsors", []string{"afdian_expires_at", "afdian_duration_months", "afdian_reported_expires_at", "afdian_reported_at", "has_duration", "duration_split_at", "plan_expires_at"}},
		{SponsorAfdianOrder{}, "sponsor_afdian_orders", []string{"id", "sponsor_id", "afdian_user_id", "plan_id", "plan_title", "product_type", "month", "status", "total_amount", "show_amount", "remark", "paid_at", "created_at", "updated_at"}},
		{SponsorManualDuration{}, "sponsor_manual_durations", []string{"sponsor_id", "amount", "unit", "starts_at", "note", "origin", "created_by", "created_at", "updated_by", "updated_at"}},
	}
	for _, tc := range cases {
		names := []string{}
		for _, f := range tc.def.Fields() {
			names = append(names, f.Descriptor().Name)
		}
		for _, column := range tc.columns {
			if !slices.Contains(names, column) {
				t.Errorf("%s: column %s missing (have %v)", tc.table, column, names)
			}
		}
		if len(tc.def.Indexes()) == 0 || tc.def.Edges() != nil {
			t.Errorf("%s: unexpected indexes/edges", tc.table)
		}
		annotation, ok := tc.def.Annotations()[0].(entsql.Annotation)
		if !ok || annotation.Table != tc.table {
			t.Errorf("table name = %+v, want %s", tc.def.Annotations(), tc.table)
		}
	}
}
