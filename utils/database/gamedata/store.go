package gamedata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
)

// ErrNoRow is returned when (user_id, server) has no row at all.
//
// It is NOT the same condition as "the response would be empty". The public
// faces 404 when every REQUESTED key is absent, which a present row can satisfy;
// the private face 404s only when the row itself is missing. Keeping the two
// apart is the whole reason this is a distinct error rather than a nil result.
var ErrNoRow = errors.New("gamedata: no row")

// Store reads and writes one game-data table.
type Store struct {
	pool *Pool
	cat  *catalog.Catalog
}

// NewStore binds a pool to a pinned catalog.
func NewStore(p *Pool, c *catalog.Catalog) *Store { return &Store{pool: p, cat: c} }

// Catalog exposes the pinned catalog this store serves.
func (s *Store) Catalog() *catalog.Catalog { return s.cat }

// Row is one game-data row held as RAW COLUMN BYTES.
//
// Values are never decoded on the way out. Decoding here and re-encoding in the
// handler would rebuild the BSON -> Go -> JSON round trip this store exists to
// remove, which is where the measured 14.5-47x read cost lived.
type Row struct {
	UserID     int64
	Server     string
	UploadTime int64
	HasUpload  bool

	cat *catalog.Catalog
	// byColumn holds the raw json bytes of every column that was selected and
	// was not NULL, keyed by column name.
	byColumn map[string][]byte
	// extra is the raw `extra` column, or nil.
	extra []byte
	// extraMembers is parsed lazily from extra on first use.
	extraMembers map[string]json.RawMessage
	extraParsed  bool
}

// Fetch reads one row. keys selects which data columns to read; nil or empty
// reads every column plus `extra`.
//
// Column identifiers in the generated SQL come only from the compiled catalog.
// A requested key that names no column contributes nothing to the statement.
func (s *Store) Fetch(ctx context.Context, userID int64, server string, keys []string) (*Row, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("gamedata: nil store")
	}
	code, ok := catalog.ServerCode(server)
	if !ok {
		if server != "" {
			// An unknown region can never have a row; treat it as absent rather
			// than as an error so the faces render their usual 404.
			return nil, ErrNoRow
		}
		// The empty region addresses the parked rows written for documents that
		// carry no `server` field. No serving route can produce it — they all
		// validate the region first — so this is reachable only from tooling.
		code = catalog.ServerUnknown
	}

	cols, entries := s.selectFor(keys)
	sql := fmt.Sprintf(
		`SELECT %s, %s FROM %s WHERE %s = $1 AND %s = $2`,
		catalog.QuoteIdent(catalog.ColUploadTime),
		strings.Join(cols, ", "),
		catalog.QuoteIdent(s.cat.Table),
		catalog.QuoteIdent(catalog.ColUserID),
		catalog.QuoteIdent(catalog.ColServer),
	)

	dest := make([]any, len(cols)+1)
	var uploadTime *int64
	dest[0] = &uploadTime
	raw := make([][]byte, len(cols))
	for i := range raw {
		dest[i+1] = &raw[i]
	}

	if err := s.pool.QueryRow(ctx, sql, userID, code).Scan(dest...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNoRow
		}
		return nil, fmt.Errorf("gamedata: read %s: %w", s.cat.Table, err)
	}

	row := &Row{
		UserID:   userID,
		Server:   server,
		cat:      s.cat,
		byColumn: make(map[string][]byte, len(cols)),
	}
	if uploadTime != nil {
		row.UploadTime, row.HasUpload = *uploadTime, true
	}
	for i, col := range cols {
		if raw[i] == nil {
			continue
		}
		if col == catalog.QuoteIdent(catalog.ExtraColumn) {
			row.extra = raw[i]
			continue
		}
		row.byColumn[entries[i]] = raw[i]
	}
	return row, nil
}

// selectFor returns the quoted column list and, positionally, the plain column
// name each element refers to.
func (s *Store) selectFor(keys []string) (quoted []string, plain []string) {
	if len(keys) == 0 {
		cols := s.cat.SelectColumns() // upload_time first, then data, then extra
		// Drop the leading upload_time: Fetch selects it explicitly.
		cols = cols[1:]
		plain = make([]string, 0, len(cols))
		for i := range s.cat.Entries {
			plain = append(plain, s.cat.Entries[i].Column)
		}
		plain = append(plain, catalog.ExtraColumn)
		return cols, plain
	}

	seen := make(map[string]bool, len(keys))
	for _, k := range keys {
		e, place := s.cat.Resolve(k)
		switch place {
		case catalog.PlaceColumn:
			if seen[e.Column] {
				continue
			}
			seen[e.Column] = true
			quoted = append(quoted, catalog.QuoteIdent(e.Column))
			plain = append(plain, e.Column)
		case catalog.PlaceUnknown:
			// Served out of `extra`; select it once.
			if !seen[catalog.ExtraColumn] {
				seen[catalog.ExtraColumn] = true
				quoted = append(quoted, catalog.QuoteIdent(catalog.ExtraColumn))
				plain = append(plain, catalog.ExtraColumn)
			}
		case catalog.PlaceMetadata:
			// upload_time is always selected; _id and server are the lookup key.
		case catalog.PlaceFlattenParent:
			// The parent owns no column: reading it means reading every child,
			// plus `extra` for the children this build does not name.
			for _, child := range s.cat.FlattenChildren() {
				if seen[child.Column] {
					continue
				}
				seen[child.Column] = true
				quoted = append(quoted, catalog.QuoteIdent(child.Column))
				plain = append(plain, child.Column)
			}
			if !seen[catalog.ExtraColumn] {
				seen[catalog.ExtraColumn] = true
				quoted = append(quoted, catalog.QuoteIdent(catalog.ExtraColumn))
				plain = append(plain, catalog.ExtraColumn)
			}
		}
	}
	if len(quoted) == 0 {
		// Every requested key was metadata. Select `extra` so the statement has
		// a column list at all; it costs one NULL read.
		quoted = append(quoted, catalog.QuoteIdent(catalog.ExtraColumn))
		plain = append(plain, catalog.ExtraColumn)
	}
	return quoted, plain
}

// RawValue returns the raw JSON bytes for a requested key, expanding a compact
// value on the way out. ok=false means the key has no value in this row.
func (r *Row) RawValue(key string) (raw []byte, ok bool, err error) {
	if r == nil {
		return nil, false, nil
	}
	e, place := r.cat.Resolve(key)
	switch place {
	case catalog.PlaceMetadata:
		return r.metadataValue(key)
	case catalog.PlaceColumn:
		v, present := r.byColumn[e.Column]
		if !present {
			return nil, false, nil
		}
		if e.Storage == catalog.StorageCompactJSON {
			expanded, expErr := ExpandCompactJSON(v)
			if expErr != nil {
				return nil, false, expErr
			}
			return expanded, true, nil
		}
		return v, true, nil
	case catalog.PlaceFlattenParent:
		// The flattened parent owns no column of its own; it is rebuilt from its
		// children. Falling through to `extra` here would answer 404 for the
		// single largest key in a mysekai document — updatedResources is 97.9%
		// of one, and `?key=updatedResources` is the main mysekai query.
		return r.flattenParent()
	default:
		v, present := r.extraMember(key)
		return v, present, nil
	}
}

// flattenParent rebuilds the flattened sub-document from its per-child columns,
// re-nesting any unknown children parked under the same name inside `extra`.
func (r *Row) flattenParent() ([]byte, bool, error) {
	out := make([]byte, 0, 4096)
	out = append(out, '{')
	first := true
	any := false

	for _, child := range r.cat.FlattenChildren() {
		raw, present := r.byColumn[child.Column]
		if !present {
			continue
		}
		v := raw
		if child.Storage == catalog.StorageCompactJSON {
			expanded, err := ExpandCompactJSON(raw)
			if err != nil {
				return nil, false, err
			}
			v = expanded
		}
		out = appendMember(out, &first, child.Child, v)
		any = true
	}

	if parked, ok := r.extraMember(r.cat.FlattenKey); ok {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(parked, &m); err == nil {
			for k, v := range m {
				out = appendMember(out, &first, k, v)
				any = true
			}
		}
	}

	if !any {
		return nil, false, nil
	}
	return append(out, '}'), true, nil
}

func (r *Row) metadataValue(key string) ([]byte, bool, error) {
	switch key {
	case catalog.ColUploadTime:
		if !r.HasUpload {
			return nil, false, nil
		}
		return []byte(fmt.Sprintf("%d", r.UploadTime)), true, nil
	case "_id":
		return []byte(fmt.Sprintf("%d", r.UserID)), true, nil
	case catalog.ColServer:
		b, err := json.Marshal(r.Server)
		return b, err == nil, err
	}
	return nil, false, nil
}

func (r *Row) extraMember(key string) ([]byte, bool) {
	if !r.extraParsed {
		r.extraParsed = true
		if len(r.extra) > 0 {
			var m map[string]json.RawMessage
			if err := json.Unmarshal(r.extra, &m); err == nil {
				r.extraMembers = m
			}
		}
	}
	v, ok := r.extraMembers[key]
	if !ok {
		return nil, false
	}
	return v, true
}

// HasAny reports whether at least one of keys has a value in this row.
//
// This is the 404 test for the public faces: MongoDB's inclusion projection
// returned an EMPTY document when none of the requested keys existed, and the
// handler turned that into 404. A naive port answers 200 with a body full of
// `[]` instead, because in PostgreSQL the row exists regardless.
func (r *Row) HasAny(keys []string) bool {
	for _, k := range keys {
		if _, ok, err := r.RawValue(k); ok && err == nil {
			return true
		}
	}
	return false
}

// Keys returns every catalog key that has a value in this row, in catalog order,
// followed by the members of `extra` in stored order.
func (r *Row) Keys() []string {
	out := make([]string, 0, len(r.byColumn)+4)
	for i := range r.cat.Entries {
		e := &r.cat.Entries[i]
		if _, ok := r.byColumn[e.Column]; ok {
			out = append(out, e.Key)
		}
	}
	return out
}

// ExtraKeys returns the member names of the `extra` column.
func (r *Row) ExtraKeys() []string {
	r.extraMember("") // force parse
	if len(r.extraMembers) == 0 {
		return nil
	}
	out := make([]string, 0, len(r.extraMembers))
	for k := range r.extraMembers {
		out = append(out, k)
	}
	return out
}

// UploadTime reads just the generation stamp for one row.
//
// It is a separate, narrow query rather than a Fetch: the stamp is consulted on
// every conditional request and on the cache write fence, and pulling a whole
// wide row to read one bigint would make the 304 path more expensive than the
// response it avoids.
//
// found=false means the row exists but carries no upload_time, or there is no
// row at all — both of which the caller treats as "no usable generation".
func (s *Store) UploadTime(ctx context.Context, userID int64, server string) (stamp int64, found bool, err error) {
	if s == nil || s.pool == nil {
		return 0, false, fmt.Errorf("gamedata: nil store")
	}
	code, ok := catalog.ServerCode(server)
	if !ok {
		return 0, false, nil
	}
	sql := fmt.Sprintf(`SELECT %s FROM %s WHERE %s = $1 AND %s = $2`,
		catalog.QuoteIdent(catalog.ColUploadTime),
		catalog.QuoteIdent(s.cat.Table),
		catalog.QuoteIdent(catalog.ColUserID),
		catalog.QuoteIdent(catalog.ColServer))

	var v *int64
	if err := s.pool.QueryRow(ctx, sql, userID, code).Scan(&v); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("gamedata: read upload_time: %w", err)
	}
	if v == nil {
		return 0, false, nil
	}
	return *v, true, nil
}

// ExtraValue returns a member of the `extra` column by name, without consulting
// the catalog.
//
// It exists for the one case where a key legitimately lives in `extra` even
// though it DOES resolve to a column: when a document carries both spellings of
// a compact key, the row form takes the column and the compact form is parked
// here. RawValue would answer with the column for that key, which is the right
// answer for serving and the wrong one for auditing.
func (r *Row) ExtraValue(key string) ([]byte, bool) {
	if r == nil {
		return nil, false
	}
	return r.extraMember(key)
}
