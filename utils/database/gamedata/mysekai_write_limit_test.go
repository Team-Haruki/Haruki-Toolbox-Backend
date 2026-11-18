package gamedata

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type measuredMysekaiTx struct {
	pgx.Tx
	steps                            []string
	total                            int64
	query                            string
	args                             []any
	upsertErr, measureErr, commitErr error
}

func (tx *measuredMysekaiTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	tx.steps = append(tx.steps, "upsert")
	return pgconn.NewCommandTag("INSERT 0 1"), tx.upsertErr
}

func (tx *measuredMysekaiTx) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	tx.steps = append(tx.steps, "measure")
	tx.query, tx.args = query, args
	return scannedGameDataRow(func(dest ...any) error {
		*dest[0].(*int64) = tx.total
		return tx.measureErr
	})
}

func (tx *measuredMysekaiTx) Commit(context.Context) error {
	tx.steps = append(tx.steps, "commit")
	return tx.commitErr
}

func TestMysekaiWriteRollsBackAfterStorageErrors(t *testing.T) {
	store := NewStore(nil, catalog.Mysekai())
	failure := errors.New("fixture database failure")
	for _, tc := range []struct {
		beginErr, upsertErr, measureErr, commitErr error
		steps                                      []string
	}{
		{beginErr: failure},
		{upsertErr: failure, steps: []string{"upsert", "rollback"}},
		{measureErr: failure, steps: []string{"upsert", "measure", "rollback"}},
		{commitErr: failure, steps: []string{"upsert", "measure", "commit", "rollback"}},
	} {
		tx := &measuredMysekaiTx{upsertErr: tc.upsertErr, measureErr: tc.measureErr, commitErr: tc.commitErr}
		begin := func(context.Context) (pgx.Tx, error) { return tx, tc.beginErr }
		err := store.writeBoundedMysekaiRow(t.Context(), begin, 42, 1, 128, "fixture-upsert", nil)
		if !errors.Is(err, failure) || !reflect.DeepEqual(tx.steps, tc.steps) {
			t.Fatalf("write failure produced %v, %v", tx.steps, err)
		}
	}
}

func (tx *measuredMysekaiTx) Rollback(context.Context) error {
	tx.steps = append(tx.steps, "rollback")
	return nil
}

func TestMysekaiWriteChecksEntireStoredRowBeforeCommit(t *testing.T) {
	store := NewStore(nil, catalog.Mysekai())
	const id, code, limit = int64(42), int16(1), 128
	for _, tc := range []struct {
		stored int64
		steps  []string
		want   error
	}{
		{limit, []string{"upsert", "measure", "commit", "rollback"}, nil},
		{limit + 1, []string{"upsert", "measure", "rollback"}, errStoredMysekaiRowTooLarge},
	} {
		tx := &measuredMysekaiTx{total: tc.stored}
		err := store.writeBoundedMysekaiRow(t.Context(), func(context.Context) (pgx.Tx, error) { return tx, nil }, id, code, limit, "fixture-upsert", nil)
		if !errors.Is(err, tc.want) || !reflect.DeepEqual(tx.steps, tc.steps) {
			t.Fatalf("stored size %d produced %v, %v", tc.stored, tx.steps, err)
		}
		cols, _ := store.selectWithout(nil)
		for _, column := range cols {
			if !strings.Contains(tx.query, "octet_length("+column+"::text)") {
				t.Fatalf("stored measurement omitted %s", column)
			}
		}
		if !reflect.DeepEqual(tx.args, []any{id, code}) {
			t.Fatalf("row measurement not scoped to account and server: %v", tx.args)
		}
	}
}
