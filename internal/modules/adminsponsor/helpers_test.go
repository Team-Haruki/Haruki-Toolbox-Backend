package adminsponsor

import (
	"context"
	"fmt"
	"testing"
	"time"

	sharedSponsor "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/sponsor"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"

	_ "github.com/mattn/go-sqlite3"
)

func TestAdminSponsorDetailShowsBothSources(t *testing.T) {
	ctx := context.Background()
	db := enttest.Open(t, "sqlite3", fmt.Sprintf("file:admin-detail-%d?mode=memory&cache=shared&_fk=1", time.Now().UnixNano()))
	defer db.Close()
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)

	row, err := sharedSponsor.CreateManualSponsor(ctx, db, sharedSponsor.ManualSponsorInput{Name: "线下赞助者"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SponsorAfdianOrder.Create().SetID("o1").SetSponsorID(row.ID).SetAfdianUserID("u").
		SetPlanID("").SetMonth(2).SetStatus(2).SetProductType(0).SetTotalAmount("10.00").SetPaidAt(now).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.SponsorAfdianOrder.Create().SetID("o2").SetSponsorID(row.ID).SetAfdianUserID("u").
		SetPlanID("item").SetMonth(1).SetStatus(2).SetProductType(1).SetPaidAt(now).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	amount, unit, note := 7, "day", "微信转账"
	if _, err := sharedSponsor.AddManualDuration(ctx, db, row.ID, sharedSponsor.ManualDurationInput{Amount: &amount, Unit: &unit, Note: &note}, "admin-1", now); err != nil {
		t.Fatal(err)
	}
	row, _ = db.Sponsor.Get(ctx, row.ID)

	detail, err := buildAdminSponsorDetail(ctx, db, row, now)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Sponsor.Category != string(sharedSponsor.CategoryCurrent) || detail.Afdian.Months != 2 {
		t.Fatalf("category=%s months=%d", detail.Sponsor.Category, detail.Afdian.Months)
	}
	kinds := map[string]string{}
	for _, order := range detail.Afdian.Orders {
		kinds[order.OutTradeNo] = order.Kind
	}
	if kinds["o1"] != "duration" || kinds["o2"] != "one_time" {
		t.Fatalf("order kinds = %v", kinds)
	}
	if len(detail.ManualDurations) != 1 || detail.ManualDurations[0].CreatedBy != "admin-1" || detail.ManualDurations[0].Note != note {
		t.Fatalf("manual entries = %+v", detail.ManualDurations)
	}
	if detail.EffectiveExpiresAt == nil || !detail.EffectiveExpiresAt.Equal(detail.Afdian.ExpiresAt.Add(7*24*time.Hour)) {
		t.Fatalf("effective %v, afdian %v", detail.EffectiveExpiresAt, detail.Afdian.ExpiresAt)
	}
	if detail.Sponsor.DurationMigrationPending {
		t.Fatalf("manual sponsor reported as pending migration")
	}
}
