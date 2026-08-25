package gamedata

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
)

// catalogTable records which pinned catalog each game-data table was built from.
//
// It exists because the failure it catches is silent: a deployment whose
// compiled catalog differs from the one the table was created with reads and
// writes columns that may not be the ones on disk, and PostgreSQL will happily
// serve NULL for a column the old catalog never filled. Comparing a checksum at
// boot turns that into a startup error.
const catalogTable = "game_data_catalog"

const catalogTableDDL = `
CREATE TABLE IF NOT EXISTS ` + catalogTable + ` (
    table_name text PRIMARY KEY,
    checksum   text NOT NULL,
    columns    int  NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now()
);`

// SchemaState is what EnsureSchema found or did for one table.
type SchemaState struct {
	Table          string
	Created        bool
	Checksum       string
	StoredChecksum string
	MissingColumns []string
	UnknownColumns []string
}

// EnsureSchema brings the game-data tables in line with the compiled catalogs.
//
// autoMigrate mirrors backend.auto_migrate: when true the tables and any columns
// a newer catalog added are created; when false nothing is written and a
// mismatch is an error, so a production process cannot quietly run against a
// schema it does not match.
//
// The tables are created through the GAME-DATA pool, never through the Ent
// lib/pq pool: they are separate DSNs and are not guaranteed to point at the
// same database, so creating in one and reading from the other would produce a
// process that starts cleanly and then 404s everything.
func EnsureSchema(ctx context.Context, p *Pool, autoMigrate bool, catalogs ...*catalog.Catalog) ([]SchemaState, error) {
	if p == nil || p.Pool == nil {
		return nil, fmt.Errorf("gamedata: nil pool")
	}
	if autoMigrate {
		if _, err := p.Exec(ctx, catalogTableDDL); err != nil {
			return nil, fmt.Errorf("gamedata: create %s: %w", catalogTable, err)
		}
	}

	states := make([]SchemaState, 0, len(catalogs))
	for _, c := range catalogs {
		st, err := ensureOne(ctx, p, autoMigrate, c)
		if err != nil {
			return states, err
		}
		states = append(states, st)
	}
	return states, nil
}

func ensureOne(ctx context.Context, p *Pool, autoMigrate bool, c *catalog.Catalog) (SchemaState, error) {
	st := SchemaState{Table: c.Table, Checksum: c.Checksum()}

	have, err := tableColumns(ctx, p, c.Table)
	if err != nil {
		return st, err
	}

	if len(have) == 0 {
		if !autoMigrate {
			return st, fmt.Errorf(
				"gamedata: table %q does not exist while backend.auto_migrate=false", c.Table)
		}
		if _, err := p.Exec(ctx, c.DDL(catalog.DefaultDDLOptions())); err != nil {
			return st, fmt.Errorf("gamedata: create %s: %w", c.Table, err)
		}
		st.Created = true
		have, err = tableColumns(ctx, p, c.Table)
		if err != nil {
			return st, err
		}
	}

	st.MissingColumns, st.UnknownColumns = DiffColumns(c.AllColumns(), have)

	if len(st.MissingColumns) > 0 {
		if !autoMigrate {
			return st, fmt.Errorf(
				"gamedata: table %q is missing %d column(s) the compiled catalog expects (first: %s) while backend.auto_migrate=false",
				c.Table, len(st.MissingColumns), st.MissingColumns[0])
		}
		if err := addMissingColumns(ctx, p, c, st.MissingColumns); err != nil {
			return st, err
		}
		st.MissingColumns = nil
	}

	// An UNKNOWN column is not an error. A rollback to an older build leaves
	// columns the compiled catalog no longer names, and those rows are still
	// perfectly readable — the data simply is not served until a build that
	// knows the key comes back. Dropping them would be the destructive choice.

	if autoMigrate {
		if _, err := p.Exec(ctx,
			`INSERT INTO `+catalogTable+` (table_name, checksum, columns)
			 VALUES ($1, $2, $3)
			 ON CONFLICT (table_name) DO UPDATE
			   SET checksum = EXCLUDED.checksum, columns = EXCLUDED.columns, applied_at = now()`,
			c.Table, st.Checksum, c.TotalColumns()); err != nil {
			return st, fmt.Errorf("gamedata: record catalog checksum for %s: %w", c.Table, err)
		}
		st.StoredChecksum = st.Checksum
		return st, nil
	}

	st.StoredChecksum, err = storedChecksum(ctx, p, c.Table)
	if err != nil {
		return st, err
	}
	if st.StoredChecksum != "" && st.StoredChecksum != st.Checksum {
		return st, fmt.Errorf(
			"gamedata: table %q was built from catalog %s but this build compiles catalog %s; "+
				"the column layout on disk is not the one this process expects",
			c.Table, st.StoredChecksum, st.Checksum)
	}
	return st, nil
}

func addMissingColumns(ctx context.Context, p *Pool, c *catalog.Catalog, missing []string) error {
	byName := make(map[string]string, c.Len())
	for i := range c.Entries {
		e := &c.Entries[i]
		byName[e.Column] = e.Storage.SQLType()
	}
	for _, col := range missing {
		typ, ok := byName[col]
		if !ok {
			// A metadata or extra column is missing: the table predates this
			// layout entirely and adding columns piecemeal would leave it
			// half-formed. Refuse rather than guess.
			return fmt.Errorf(
				"gamedata: table %q is missing structural column %q; recreate the table rather than patching it",
				c.Table, col)
		}
		// Identifier comes from the compiled catalog, never from input.
		if _, err := p.Exec(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s %s",
			catalog.QuoteIdent(c.Table), catalog.QuoteIdent(col), typ)); err != nil {
			return fmt.Errorf("gamedata: add column %s.%s: %w", c.Table, col, err)
		}
	}
	return nil
}

func tableColumns(ctx context.Context, p *Pool, table string) ([]string, error) {
	rows, err := p.Query(ctx,
		`SELECT column_name FROM information_schema.columns
		 WHERE table_schema = current_schema() AND table_name = $1`, table)
	if err != nil {
		return nil, fmt.Errorf("gamedata: inspect %s: %w", table, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

func storedChecksum(ctx context.Context, p *Pool, table string) (string, error) {
	var sum string
	err := p.QueryRow(ctx,
		`SELECT checksum FROM `+catalogTable+` WHERE table_name = $1`, table).Scan(&sum)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// No record: the table predates checksum tracking. Not an error — the
		// column diff above already proved the layout is serviceable.
		return "", nil
	case err != nil && strings.Contains(err.Error(), catalogTable):
		// The bookkeeping table itself is absent, same reasoning.
		return "", nil
	case err != nil:
		return "", fmt.Errorf("gamedata: read catalog checksum for %s: %w", table, err)
	}
	return sum, nil
}

// DiffColumns reports which of want are absent from have, and which of have are
// not named by want. Both results are sorted.
func DiffColumns(want, have []string) (missing, unknown []string) {
	haveSet := make(map[string]bool, len(have))
	for _, h := range have {
		haveSet[h] = true
	}
	wantSet := make(map[string]bool, len(want))
	for _, w := range want {
		wantSet[w] = true
		if !haveSet[w] {
			missing = append(missing, w)
		}
	}
	for _, h := range have {
		if !wantSet[h] {
			unknown = append(unknown, h)
		}
	}
	sort.Strings(missing)
	sort.Strings(unknown)
	return missing, unknown
}
