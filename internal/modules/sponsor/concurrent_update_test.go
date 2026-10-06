package sponsor

import (
	"context"
	"entgo.io/ent"
	pg "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"testing"
	"time"
)

func TestSyncMustNotDeactivateConcurrentRenewal(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", uniqueSponsorSQLiteDSN(t))
	defer client.Close()
	paidAt := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	now := paidAt.AddDate(0, 2, 0)
	seed, err := UpsertParsedSponsor(ctx, client, paidAfdianOrder(t, "race-user", "old-order", "monthly", 1, paidAt), paidAt, true)
	if err != nil {
		t.Fatal(err)
	}
	injected := false
	client.Sponsor.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			sm := m.(*pg.SponsorMutation)
			active, set := sm.IsActive()
			if sm.Op().Is(ent.OpUpdateOne) && set && !active && !injected {
				injected = true
				// A paid renewal commits after the sync reads the old expiry but before its UPDATE.
				if _, err := UpsertParsedSponsor(ctx, client, paidAfdianOrder(t, "race-user", "new-order", "monthly", 1, now), now, true); err != nil {
					return nil, err
				}
			}
			return next.Mutate(ctx, m)
		})
	})
	_, err = UpsertParsedSponsor(ctx, client, querySponsorItem(t, "race-user", map[string]any{"name": ""}, paidAt), now, false)
	if err != nil {
		t.Fatal(err)
	}
	row, err := client.Sponsor.Get(ctx, seed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !injected {
		t.Fatal("interleaving not exercised")
	}
	if row.PlanExpiresAt != nil && row.PlanExpiresAt.After(now) && !row.IsActive {
		t.Fatal("renewed sponsor has future expiry but sync overwrote is_active=false")
	}
}

func TestSyncMustNotOverwriteConcurrentAdminPin(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", uniqueSponsorSQLiteDSN(t))
	defer client.Close()
	paidAt := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	now := paidAt.AddDate(0, 2, 0)
	seed, err := UpsertParsedSponsor(ctx, client, paidAfdianOrder(t, "pin-user", "old-order", "monthly", 1, paidAt), paidAt, true)
	if err != nil {
		t.Fatal(err)
	}
	injected := false
	client.Sponsor.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if !injected && m.Op().Is(ent.OpUpdateOne) {
				injected = true
				if _, err := client.Sponsor.UpdateOneID(seed.ID).SetAfdianSyncDisabled(true).SetName("Pinned").Save(ctx); err != nil {
					return nil, err
				}
			}
			return next.Mutate(ctx, m)
		})
	})
	row, err := UpsertParsedSponsor(ctx, client, querySponsorItem(t, "pin-user", map[string]any{"name": ""}, paidAt), now, false)
	if err != nil {
		t.Fatal(err)
	}
	if !row.AfdianSyncDisabled || !row.IsActive || row.Name == nil || *row.Name != "Pinned" {
		t.Fatal("sync overwrote concurrent admin pin")
	}
}

func TestSponsorUpdateConflictRetryLimit(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", uniqueSponsorSQLiteDSN(t))
	defer client.Close()
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	item := paidAfdianOrder(t, "busy-user", "order", "monthly", 1, now)
	seed, err := UpsertParsedSponsor(ctx, client, item, now, true)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	nested := false
	client.Sponsor.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if !nested && m.Op().Is(ent.OpUpdateOne) {
				calls++
				nested = true
				_, err := client.Sponsor.UpdateOneID(seed.ID).SetUpdatedAt(seed.UpdatedAt.Add(time.Duration(calls) * time.Second)).Save(ctx)
				nested = false
				if err != nil {
					return nil, err
				}
			}
			return next.Mutate(ctx, m)
		})
	})
	_, err = UpsertParsedSponsor(ctx, client, item, now, false)
	if err != errSponsorUpdateConflict || calls != 3 {
		t.Fatalf("err=%v, attempts=%d; want conflict after 3 attempts", err, calls)
	}
}
