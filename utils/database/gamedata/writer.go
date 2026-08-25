package gamedata

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/gamemerge"
)

// WriteMode selects one of the four write semantics.
//
// They are four separate implementations on purpose. The MongoDB path expressed
// them as three different `$set` documents plus a full-document replace, and no
// single rule covers them: collapsing any two loses data.
type WriteMode int

const (
	// WriteSuite is an upload of suite data. `$set` on MongoDB is a TOP-LEVEL
	// MERGE, so a key the upload omits keeps its stored value; and the three
	// history keys accumulate rather than being replaced.
	WriteSuite WriteMode = iota
	// WriteMysekai is an upload of mysekai data: the columns present are
	// written, the ones absent are left alone. Same merge semantics, no history
	// accumulation.
	WriteMysekai
	// WriteBirthdayParty writes only the harvest map, upload time and server.
	// It is a partial write of a mysekai row, not a mysekai upload.
	WriteBirthdayParty
	// WriteMigrate replaces the whole row. Only the migration CLI uses it: the
	// source is a complete document, so a merge would be meaningless and would
	// preserve rows a re-run is meant to rebuild.
	WriteMigrate
)

// WriteStats reports what one write did.
type WriteStats struct {
	// DeniedDropped counts, per key, values discarded because the key is on the
	// denied list. A SECURITY counter: these keys are tiny, so no size metric
	// will ever reveal whether the drop is working.
	DeniedDropped map[string]int
	// ExtraKeys are the unknown top-level keys parked in `extra`.
	ExtraKeys []string
	// AliasConflicts counts documents keys that arrived in BOTH spellings —
	// `userX` and `compactUserX` — for the same column. Production data contains
	// these; the row form wins and the loser is preserved in `extra`.
	AliasConflicts map[string]int
	// Columns is how many data columns the statement wrote.
	Columns int
	// Bytes is the encoded size of everything written.
	Bytes int
}

// Write persists one upload.
func (s *Store) Write(ctx context.Context, userID int64, server string, data map[string]any, mode WriteMode, limits Limits) (WriteStats, error) {
	var stats WriteStats
	if s == nil || s.pool == nil {
		return stats, fmt.Errorf("gamedata: nil store")
	}
	code, ok := catalog.ServerCode(server)
	if !ok {
		if server != "" {
			return stats, fmt.Errorf("gamedata: unknown server %q", server)
		}
		// Production contains documents with no `server` field at all. They are
		// parked under a reserved code so the NOT NULL primary key is satisfied
		// and the row is preserved; no read can reach them, because every read
		// filters on a real region.
		code = catalog.ServerUnknown
	}
	if err := ValidateUploadFieldNames(data); err != nil {
		return stats, err
	}

	enc, err := s.encode(data, mode, &stats)
	if err != nil {
		return stats, err
	}
	if err := checkLimits(limits, enc.perKeyBytes, len(stats.ExtraKeys), enc.extraBytes, stats.Bytes); err != nil {
		return stats, err
	}

	switch mode {
	case WriteSuite:
		return stats, s.writeSuite(ctx, userID, code, enc, &stats)
	case WriteMigrate:
		return stats, s.writeReplace(ctx, userID, code, enc)
	case WriteBirthdayParty:
		return stats, s.writeBirthdayParty(ctx, userID, code, enc)
	default:
		return stats, s.writeMerge(ctx, userID, code, enc)
	}
}

// encoded is one upload rendered into column values.
type encoded struct {
	// columns maps column name -> encoded json bytes, for columns the upload
	// actually carried.
	columns map[string][]byte
	// order preserves catalog order so generated SQL is stable.
	order []string
	// mergedRaw holds the decoded values of the three history keys, kept as Go
	// values because merging happens against the stored side.
	mergedRaw map[string]any
	// extra is the encoded `extra` object, or nil.
	extra       []byte
	extraBytes  int
	perKeyBytes map[string]int
	uploadTime  *int64
	hasUpload   bool
}

func (s *Store) encode(data map[string]any, mode WriteMode, stats *WriteStats) (*encoded, error) {
	enc := &encoded{
		columns:     make(map[string][]byte, len(data)),
		mergedRaw:   map[string]any{},
		perKeyBytes: make(map[string]int, len(data)),
	}
	stats.DeniedDropped = map[string]int{}
	stats.AliasConflicts = map[string]int{}
	// writtenBy records which document key currently owns each column, so a key
	// arriving in both spellings resolves DETERMINISTICALLY instead of by Go map
	// iteration order.
	writtenBy := make(map[string]string, len(data))

	extraMembers := make(map[string]json.RawMessage)
	flattenExtra := make(map[string]json.RawMessage)

	for key, value := range data {
		// DENIED: dropped before the value is encoded, so it never reaches a
		// column, never reaches `extra`, and is never even rendered to bytes.
		if catalog.IsDenied(key) {
			stats.DeniedDropped[key]++
			continue
		}
		if key == catalog.ColUploadTime {
			if n, ok := gamemerge.ToInt64(value); ok {
				enc.uploadTime, enc.hasUpload = &n, true
			}
			continue
		}
		if key == "_id" || key == catalog.ColServer {
			// Identity is carried by the primary key, never by a json column.
			continue
		}

		// The flattened parent is split into per-child columns.
		if s.cat.FlattenKey != "" && key == s.cat.FlattenKey {
			sub, ok := value.(map[string]any)
			if !ok {
				b, err := encodeJSON(value)
				if err != nil {
					return nil, err
				}
				extraMembers[key] = b
				continue
			}
			for child, cv := range sub {
				if catalog.IsDenied(child) {
					stats.DeniedDropped[s.cat.FlattenKey+"."+child]++
					continue
				}
				b, err := encodeJSON(cv)
				if err != nil {
					return nil, err
				}
				e, place := s.cat.Resolve(s.cat.FlattenKey + "." + child)
				if place != catalog.PlaceColumn {
					flattenExtra[child] = b
					continue
				}
				enc.setColumn(e.Column, b)
				enc.perKeyBytes[e.Key] = len(b)
			}
			continue
		}

		e, place := s.cat.Resolve(key)
		if place != catalog.PlaceColumn {
			b, err := encodeJSON(value)
			if err != nil {
				return nil, err
			}
			extraMembers[key] = b
			stats.ExtraKeys = append(stats.ExtraKeys, key)
			continue
		}

		if mode == WriteSuite && gamemerge.IsMergedKey(e.Key) {
			// Held back: the merge needs the stored side, which is read inside
			// the transaction.
			enc.mergedRaw[e.Key] = value
			continue
		}

		b, err := encodeJSON(value)
		if err != nil {
			return nil, err
		}
		// Both spellings of a compact key map to one column. Production resolves
		// this in favour of the ROW form: GetValueFromResult scans for the exact
		// key first and only falls back to compact<Key>. Without an explicit
		// rule the winner would depend on Go map iteration order, so the same
		// document could migrate differently on two runs.
		if prev, taken := writtenBy[e.Column]; taken {
			stats.AliasConflicts[e.Key]++
			if e.IsAlias(key) && !e.IsAlias(prev) {
				// The incumbent is the row form; keep it and park this one.
				extraMembers[key] = b
				continue
			}
			// This one is the row form; the incumbent was the compact alias.
			extraMembers[prev] = enc.columns[e.Column]
		}
		writtenBy[e.Column] = key
		enc.setColumn(e.Column, b)
		enc.perKeyBytes[e.Key] = len(b)
	}

	if len(flattenExtra) > 0 {
		b, err := json.Marshal(flattenExtra)
		if err != nil {
			return nil, err
		}
		extraMembers[s.cat.FlattenKey] = b
	}
	if len(extraMembers) > 0 {
		b, err := json.Marshal(extraMembers)
		if err != nil {
			return nil, err
		}
		enc.extra = b
		enc.extraBytes = len(b)
	}

	for _, n := range enc.perKeyBytes {
		stats.Bytes += n
	}
	stats.Bytes += enc.extraBytes
	stats.Columns = len(enc.columns)
	return enc, nil
}

func (e *encoded) setColumn(col string, b []byte) {
	if _, seen := e.columns[col]; !seen {
		e.order = append(e.order, col)
	}
	e.columns[col] = b
}

// writeMerge upserts the columns the upload carried and leaves the rest alone.
//
// This is `$set`'s top-level merge, expressed in SQL. Writing every column and
// letting the absent ones default to NULL would clear stored data on every
// partial upload.
func (s *Store) writeMerge(ctx context.Context, userID int64, code int16, enc *encoded) error {
	sql, args := s.upsertStatement(userID, code, enc, false)
	_, err := s.pool.Exec(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("gamedata: upsert %s: %w", s.cat.Table, err)
	}
	return nil
}

// writeReplace overwrites every data column, clearing the ones the source did
// not carry. Migration only.
func (s *Store) writeReplace(ctx context.Context, userID int64, code int16, enc *encoded) error {
	sql, args := s.upsertStatement(userID, code, enc, true)
	_, err := s.pool.Exec(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("gamedata: replace %s: %w", s.cat.Table, err)
	}
	return nil
}

// writeBirthdayParty writes the three columns a birthday-party payload owns.
func (s *Store) writeBirthdayParty(ctx context.Context, userID int64, code int16, enc *encoded) error {
	e, place := s.cat.Resolve(s.cat.FlattenKey + ".userMysekaiHarvestMaps")
	if place != catalog.PlaceColumn {
		return fmt.Errorf("gamedata: no column for the birthday-party harvest map")
	}
	harvest := enc.columns[e.Column]
	sql := fmt.Sprintf(
		`INSERT INTO %s (%s, %s, %s, %s) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (%s, %s) DO UPDATE SET %s = EXCLUDED.%s, %s = EXCLUDED.%s`,
		catalog.QuoteIdent(s.cat.Table),
		catalog.QuoteIdent(catalog.ColUserID), catalog.QuoteIdent(catalog.ColServer),
		catalog.QuoteIdent(catalog.ColUploadTime), catalog.QuoteIdent(e.Column),
		catalog.QuoteIdent(catalog.ColUserID), catalog.QuoteIdent(catalog.ColServer),
		catalog.QuoteIdent(catalog.ColUploadTime), catalog.QuoteIdent(catalog.ColUploadTime),
		catalog.QuoteIdent(e.Column), catalog.QuoteIdent(e.Column),
	)
	var ut any
	if enc.hasUpload {
		ut = *enc.uploadTime
	}
	if _, err := s.pool.Exec(ctx, sql, userID, code, ut, harvest); err != nil {
		return fmt.Errorf("gamedata: birthday party write: %w", err)
	}
	return nil
}

// writeSuite performs the merge upload inside ONE transaction, because the three
// history keys are a read-modify-write against the stored row.
func (s *Store) writeSuite(ctx context.Context, userID int64, code int16, enc *encoded, stats *WriteStats) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("gamedata: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if len(enc.mergedRaw) > 0 {
		stored, err := s.readMergedColumns(ctx, tx, userID, code)
		if err != nil {
			return err
		}
		for key, uploaded := range enc.mergedRaw {
			merged := mergeHistory(key, stored[key], uploaded)
			if merged == nil {
				// nil means "leave the stored value alone" — writing [] here
				// would delete a player's history whenever an upload carried
				// none of that key.
				continue
			}
			b, err := encodeJSON(merged)
			if err != nil {
				return err
			}
			e, _ := s.cat.Resolve(key)
			enc.setColumn(e.Column, b)
			enc.perKeyBytes[key] = len(b)
			stats.Bytes += len(b)
		}
		stats.Columns = len(enc.columns)
	}

	sql, args := s.upsertStatement(userID, code, enc, false)
	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		return fmt.Errorf("gamedata: upsert %s: %w", s.cat.Table, err)
	}
	return tx.Commit(ctx)
}

func mergeHistory(key string, stored, uploaded any) []any {
	n := gamemerge.JSONNormalizer{}
	switch key {
	case gamemerge.KeyUserEvents:
		return gamemerge.Events(n, stored, uploaded)
	case gamemerge.KeyUserWorldBlooms:
		return gamemerge.WorldBlooms(n, stored, uploaded)
	case gamemerge.KeyUserGachas:
		return gamemerge.Gachas(n, stored, uploaded)
	}
	return nil
}

// readMergedColumns reads the three history columns, decoding with UseNumber so
// a game user id above 2^53 is not corrupted on the way in.
func (s *Store) readMergedColumns(ctx context.Context, tx pgx.Tx, userID int64, code int16) (map[string]any, error) {
	keys := gamemerge.Keys()
	cols := make([]string, 0, len(keys))
	present := make([]string, 0, len(keys))
	for _, k := range keys {
		e, place := s.cat.Resolve(k)
		if place != catalog.PlaceColumn {
			continue
		}
		cols = append(cols, catalog.QuoteIdent(e.Column))
		present = append(present, k)
	}
	if len(cols) == 0 {
		return map[string]any{}, nil
	}
	sql := fmt.Sprintf(`SELECT %s FROM %s WHERE %s = $1 AND %s = $2`,
		strings.Join(cols, ", "), catalog.QuoteIdent(s.cat.Table),
		catalog.QuoteIdent(catalog.ColUserID), catalog.QuoteIdent(catalog.ColServer))

	raw := make([][]byte, len(cols))
	dest := make([]any, len(cols))
	for i := range raw {
		dest[i] = &raw[i]
	}
	out := make(map[string]any, len(cols))
	if err := tx.QueryRow(ctx, sql, userID, code).Scan(dest...); err != nil {
		if err == pgx.ErrNoRows {
			return out, nil
		}
		return nil, fmt.Errorf("gamedata: read history columns: %w", err)
	}
	for i, k := range present {
		if raw[i] == nil {
			continue
		}
		v, err := decodeJSONNumbers(raw[i])
		if err != nil {
			return nil, fmt.Errorf("gamedata: decode stored %s: %w", k, err)
		}
		out[k] = v
	}
	return out, nil
}

// upsertStatement builds the INSERT ... ON CONFLICT for the columns present.
// replaceAll=true additionally clears every catalog column the upload did not
// carry, which is the migration semantic.
func (s *Store) upsertStatement(userID int64, code int16, enc *encoded, replaceAll bool) (string, []any) {
	cols := []string{catalog.ColUserID, catalog.ColServer, catalog.ColUploadTime, catalog.ExtraColumn}
	args := []any{userID, code, nullableInt(enc), nullableBytes(enc.extra)}

	writeOrder := enc.order
	if replaceAll {
		writeOrder = make([]string, 0, s.cat.Len())
		for i := range s.cat.Entries {
			writeOrder = append(writeOrder, s.cat.Entries[i].Column)
		}
	}
	for _, col := range writeOrder {
		cols = append(cols, col)
		args = append(args, nullableBytes(enc.columns[col]))
	}

	quoted := make([]string, len(cols))
	placeholders := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = catalog.QuoteIdent(c)
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}

	sets := make([]string, 0, len(cols))
	for _, c := range cols[2:] { // skip the primary key columns
		q := catalog.QuoteIdent(c)
		if replaceAll {
			sets = append(sets, fmt.Sprintf("%s = EXCLUDED.%s", q, q))
			continue
		}
		// COALESCE is the `$set` merge: a column the upload did not carry keeps
		// whatever is stored, instead of being cleared to NULL.
		sets = append(sets, fmt.Sprintf("%s = COALESCE(EXCLUDED.%s, %s.%s)",
			q, q, catalog.QuoteIdent(s.cat.Table), q))
	}

	sql := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (%s, %s) DO UPDATE SET %s",
		catalog.QuoteIdent(s.cat.Table),
		strings.Join(quoted, ", "),
		strings.Join(placeholders, ", "),
		catalog.QuoteIdent(catalog.ColUserID), catalog.QuoteIdent(catalog.ColServer),
		strings.Join(sets, ", "),
	)
	return sql, args
}

func nullableInt(enc *encoded) any {
	if !enc.hasUpload {
		return nil
	}
	return *enc.uploadTime
}

func nullableBytes(b []byte) any {
	if b == nil {
		return nil
	}
	return b
}

// encodeJSON renders one upload value. encoding/json emits int64 and uint64 as
// exact integer literals, which is what keeps a game user id above 2^53 intact.
func encodeJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("gamedata: encode value: %w", err)
	}
	return b, nil
}

// decodeJSONNumbers decodes with UseNumber. Without it every number becomes a
// float64 and an identity above 2^53 is silently corrupted before the merge even
// compares it.
func decodeJSONNumbers(b []byte) (any, error) {
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// EncodeOnly runs the encode-and-limit half of a write without touching the
// database.
//
// The migration CLI uses it for its dry run: an upload that cannot be
// represented — a value that will not encode, a key over the size cap — then
// surfaces BEFORE the maintenance window instead of inside it.
func (s *Store) EncodeOnly(data map[string]any, mode WriteMode, limits Limits) (WriteStats, error) {
	var stats WriteStats
	if err := ValidateUploadFieldNames(data); err != nil {
		return stats, err
	}
	enc, err := s.encode(data, mode, &stats)
	if err != nil {
		return stats, err
	}
	return stats, checkLimits(limits, enc.perKeyBytes, len(stats.ExtraKeys), enc.extraBytes, stats.Bytes)
}
