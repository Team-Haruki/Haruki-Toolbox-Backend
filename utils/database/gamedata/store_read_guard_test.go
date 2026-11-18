package gamedata

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/game/nuverserestore"
	"github.com/jackc/pgx/v5"
)

func TestHarvestRestorationUsesRemainingRowBudget(t *testing.T) {
	const raw = `[[5,[[111,-3,7,20,"spawned",null]],[ ["mysekai_item",24,-3,7,10,13,"before_drop",2,null] ]]]`
	const key = "userMysekaiHarvestMaps"
	restorer, err := nuverserestore.NewMysekai(map[string]string{"cn": "../../../data/suite_user_cn_6.4.0.avsc"})
	if err != nil {
		t.Fatal(err)
	}
	expanded, err := restorer.JSON("cn", []byte(raw))
	if err != nil || len(expanded) <= len(raw) {
		t.Fatalf("fixture must expand: %v", err)
	}
	for _, shortfall := range []int{0, 1} {
		r := newTestRow(t, catalog.Suite(), map[string]string{key: raw}, "")
		r.Server, r.restorer = "cn", restorer
		used := maxPrivateResponseBytes - len(expanded) + shortfall
		r.expandedBytes = used
		for range 2 {
			value, present, err := r.RawValue(key)
			if shortfall == 0 {
				if err != nil || !present || len(value) != len(expanded) || !bytes.Contains(value, []byte(`"resourceType":"mysekai_item"`)) || r.expandedBytes != maxPrivateResponseBytes {
					t.Fatalf("exact-budget restoration was not counted once: %v", err)
				}
			} else if !errors.Is(err, ErrPublishedResponseTooLarge) || present || value != nil || r.expandedBytes != used {
				t.Fatalf("oversized restoration was retained: %v", err)
			}
		}
	}
}

type scannedGameDataRow func(...any) error

func (scan scannedGameDataRow) Scan(dest ...any) error { return scan(dest...) }

func TestSelectedGameDataReadBoundsDatabaseResult(t *testing.T) {
	store := NewStore(nil, catalog.Suite())
	cols, names := store.selectFor([]string{"userCards", "userDecks", "extraFixture"})
	code, _ := catalog.ServerCode("jp")
	cards, _ := store.cat.Resolve("userCards")
	backendError := errors.New("fixture database failure")
	for _, tc := range []struct {
		name      string
		oversized bool
		scanError error
		want      error
	}{
		{name: "selected columns and extra"},
		{name: "oversized", oversized: true, want: ErrPublishedResponseTooLarge},
		{name: "missing", scanError: pgx.ErrNoRows, want: ErrNoRow},
		{name: "backend failure", scanError: backendError, want: backendError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := func(_ context.Context, sql string, args ...any) pgx.Row {
				if !slices.Equal(args, []any{int64(42), code, int64(DefaultLimits().MaxRowBytes)}) {
					t.Fatalf("unexpected query arguments: %v", args)
				}
				for _, col := range cols {
					if !strings.Contains(sql, "octet_length("+col+"::text)") ||
						!strings.Contains(sql, "CASE WHEN NOT oversized THEN "+col+" END") {
						t.Fatalf("missing transfer guard for %s", col)
					}
				}
				return scannedGameDataRow(func(dest ...any) error {
					if tc.scanError != nil {
						return tc.scanError
					}
					stamp := int64(123)
					*dest[0].(**int64) = &stamp
					*dest[1].(*bool) = tc.oversized
					if !tc.oversized {
						for i, name := range names {
							switch name {
							case cards.Column:
								*dest[i+2].(*[]byte) = []byte(`[{"cardId":1}]`)
							case catalog.ExtraColumn:
								*dest[i+2].(*[]byte) = []byte(`{"extraFixture":true}`)
							}
						}
					}
					return nil
				})
			}
			row, err := store.fetchSelected(t.Context(), query, 42, "jp", code, cols, names)
			if !errors.Is(err, tc.want) {
				t.Fatalf("read error = %v, want %v", err, tc.want)
			}
			if tc.want != nil {
				if row != nil {
					t.Fatal("failed read returned a partial row")
				}
				return
			}
			if row == nil || !row.HasUpload || row.UploadTime != 123 || row.UserID != 42 ||
				len(row.byColumn) != 1 || string(row.byColumn[cards.Column]) != `[{"cardId":1}]` ||
				string(row.extra) != `{"extraFixture":true}` {
				t.Fatalf("read lost selected fields: %#v", row)
			}
		})
	}
}
