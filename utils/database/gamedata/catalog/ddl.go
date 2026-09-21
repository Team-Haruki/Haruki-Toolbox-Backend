package catalog

import (
	"fmt"
	"sort"
	"strings"
)

// ServerCodes pins each region to a smallint. The code is part of the primary
// key, so these values are permanent: changing one silently re-points existing
// rows at a different account.
var ServerCodes = map[string]int16{
	"jp": 1,
	"en": 2,
	"tw": 3,
	"kr": 4,
	"cn": 5,
}

// ServerUnknown is NOT a game server. Production contains documents with no
// `server` field at all (the mysekai upsert only started setting it later), and
// such a row still has to satisfy a NOT NULL primary-key column. It is parked
// here and counted rather than silently dropped.
const ServerUnknown int16 = 0

// ServerCode maps a region string to its pinned smallint.
func ServerCode(s string) (int16, bool) {
	c, ok := ServerCodes[s]
	return c, ok
}

// ServerName maps a pinned smallint back to its region string. ok=false for
// ServerUnknown, which has no region name by construction.
func ServerName(code int16) (string, bool) {
	for name, c := range ServerCodes {
		if c == code {
			return name, true
		}
	}
	return "", false
}

// QuoteIdent quotes a PostgreSQL identifier. Every identifier passed here comes
// from the compile-time catalog, never from request input; quoting is belt to
// the catalog's braces.
func QuoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// DDLOptions tune the emitted storage parameters.
type DDLOptions struct {
	// Fillfactor leaves free space on each heap page. Nearly all payload lives
	// out-of-line in TOAST, so the heap tuple is a few hundred bytes of varlena
	// pointers — small enough that a full-row update can stay a HOT update and
	// avoid touching the primary-key index, but only if the new tuple version
	// fits on the same page.
	Fillfactor int
	// IfNotExists emits CREATE TABLE IF NOT EXISTS.
	IfNotExists bool
}

// DefaultDDLOptions is the decided shape.
func DefaultDDLOptions() DDLOptions { return DDLOptions{Fillfactor: 70, IfNotExists: true} }

// DDL emits the CREATE TABLE for this catalog.
//
// Column types are `json`, never `jsonb`. Measured: identical on-disk size, but
// json is 1.4-1.9x faster for this access pattern (a whole key is fetched and
// spliced into the response, so jsonb's binary form has to be re-rendered to
// text on every read while json returns the stored bytes). jsonb additionally
// reorders object keys, drops duplicate keys, rewrites number literals, and
// hard-rejects NUL — and upload payloads are attacker-influenced, so a string
// carrying NUL would turn a write into a hard error.
func (c *Catalog) DDL(opts DDLOptions) string {
	var b strings.Builder
	fmt.Fprintf(&b, "-- generated from the pinned %s catalog (checksum %s); do not hand-edit\n",
		c.Collection, c.Checksum())
	b.WriteString("CREATE TABLE ")
	if opts.IfNotExists {
		b.WriteString("IF NOT EXISTS ")
	}
	b.WriteString(QuoteIdent(c.Table))
	b.WriteString(" (\n")
	fmt.Fprintf(&b, "    %-*s bigint   NOT NULL,\n", MaxIdentLen, QuoteIdent(ColUserID))
	fmt.Fprintf(&b, "    %-*s smallint NOT NULL,\n", MaxIdentLen, QuoteIdent(ColServer))
	fmt.Fprintf(&b, "    %-*s bigint,\n", MaxIdentLen, QuoteIdent(ColUploadTime))
	for i := range c.Entries {
		e := &c.Entries[i]
		fmt.Fprintf(&b, "    %-*s %s,\n", MaxIdentLen, QuoteIdent(e.Column), e.Storage.SQLType())
	}
	fmt.Fprintf(&b, "    %-*s json,\n", MaxIdentLen, QuoteIdent(ExtraColumn))
	fmt.Fprintf(&b, "    PRIMARY KEY (%s, %s)\n", QuoteIdent(ColUserID), QuoteIdent(ColServer))
	b.WriteString(")")
	if opts.Fillfactor > 0 {
		fmt.Fprintf(&b, " WITH (fillfactor = %d)", opts.Fillfactor)
	}
	b.WriteString(";\n")
	return b.String()
}

// SelectColumns is the pinned SELECT list: metadata, then every data column in
// catalog order, then extra.
func (c *Catalog) SelectColumns() []string {
	out := make([]string, 0, c.TotalColumns())
	out = append(out, QuoteIdent(ColUploadTime))
	for i := range c.Entries {
		out = append(out, QuoteIdent(c.Entries[i].Column))
	}
	out = append(out, QuoteIdent(ExtraColumn))
	return out
}

// AllColumns is every column name in insert order, unquoted.
func (c *Catalog) AllColumns() []string {
	out := make([]string, 0, c.TotalColumns())
	out = append(out, ColUserID, ColServer, ColUploadTime)
	for i := range c.Entries {
		out = append(out, c.Entries[i].Column)
	}
	out = append(out, ExtraColumn)
	return out
}

// ColumnsSorted returns every column name sorted, for diffing against a live
// database.
func (c *Catalog) ColumnsSorted() []string {
	out := c.AllColumns()
	sort.Strings(out)
	return out
}
