// Package catalog is the compile-time map from a Project Sekai top-level data
// key to the PostgreSQL column that stores it.
//
// It is the single source of column identifiers for the game-data tables.
// Column names are NEVER derived from request input or from stored documents at
// runtime: an unknown key becomes a JSON *value* inside the `extra` column, never
// an identifier. That is what keeps an attacker-controlled key name (upload
// payloads are decrypted with the PUBLIC Project Sekai client key) out of SQL.
//
// The pinned registries live beside this file as JSON and are compiled in via
// `go generate ./utils/database/gamedata/catalog`.
package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Storage is how one key's value is kept in its column.
type Storage string

const (
	// StorageJSON keeps the exact JSON text in a `json` column.
	StorageJSON Storage = "json"

	// StorageCompactJSON also uses a `json` column, but the stored value is
	// SELF-DESCRIBING and may be in either of two shapes:
	//
	//	{...} carrying __ENUM__  -> the columnar "compact" form, must be expanded on read
	//	[...]                    -> untouched row form (compaction was refused as unsafe)
	//
	// One column serves both because the shape decides, not the column. Measured
	// on the full production corpus, user_costume3d_statuses_j holds 6,570
	// compact objects (cn/tw/kr) and 4,148 row arrays (jp/en) simultaneously.
	StorageCompactJSON Storage = "compact-json"
)

// Suffix is the pinned column-name suffix for the storage class. Both classes
// are `json`; the suffix exists so a future storage class cannot silently reuse
// an existing column name.
func (s Storage) Suffix() string { return "_j" }

// SQLType is the PostgreSQL column type.
func (s Storage) SQLType() string { return "json" }

// Valid reports whether s is a storage class this build knows.
func (s Storage) Valid() bool { return s == StorageJSON || s == StorageCompactJSON }

// MetadataKeys are top-level keys promoted to dedicated typed columns instead of
// json columns. `_id` is the game user id.
var MetadataKeys = map[string]bool{
	"_id":         true,
	"server":      true,
	"upload_time": true,
}

// DeniedKeys are top-level keys that must NEVER reach PostgreSQL: no column, no
// `extra`, nothing on disk.
//
// This is a SECURITY control and nothing else can verify it. Measured on the
// full production corpus the five together are only ~1.7 MB, so no size or row
// metric will ever show whether the drop is working — enforcement is by the
// assertions in Validate plus the drop in the writer.
//
// What they carry, measured on 10,928 production suite documents:
//
//   - userRegistration is non-empty in 10,716 rows and carries age / dayOfBirth /
//     yearOfBirth, deviceModel / operatingSystem / platform, and an HS256 JWT in
//     `signature`.
//   - userPlatforms is non-empty in 3,047 rows.
//   - userInherit is non-empty in 238 rows.
//   - userPlatformInheritIos / userPlatformInheritAndroid are account-transfer
//     material.
//
// None of the five is referenced by non-test Go code, and none is on the public
// key allowlist. See docs/database-consolidation-plan.zh-CN.md §4.7.3.
var DeniedKeys = map[string]bool{
	"userInherit":                true,
	"userPlatformInheritIos":     true,
	"userPlatformInheritAndroid": true,
	"userPlatforms":              true,
	"userRegistration":           true,
}

// CompactPairs maps a row-form key to the compact* key cn/tw/kr clients send for
// it. These are the six the game itself compacts; we do not invent new ones.
// The derivation is production's own: "compact" + upper(key[0]) + key[1:].
var CompactPairs = map[string]string{
	"userCharacterMissionV2Statuses": "compactUserCharacterMissionV2Statuses",
	"userCostume3dShopItems":         "compactUserCostume3dShopItems",
	"userCostume3dStatuses":          "compactUserCostume3dStatuses",
	"userMissionStatuses":            "compactUserMissionStatuses",
	"userMusicAchievements":          "compactUserMusicAchievements",
	"userMusicResults":               "compactUserMusicResults",
}

// CompactFieldName mirrors utils/api/data compactFieldName.
func CompactFieldName(key string) string {
	if key == "" {
		return ""
	}
	return "compact" + strings.ToUpper(key[:1]) + key[1:]
}

// Entry is one key's placement.
type Entry struct {
	// Key is the Mongo top-level key, or "<FlattenKey>.<child>" for a key lifted
	// out of the flattened sub-document.
	Key string `json:"key"`
	// Path is "" for a top-level key, or the parent key for a flattened child.
	Path string `json:"path,omitempty"`
	// Child is the bare child name when Path is set.
	Child string `json:"child,omitempty"`
	// Column is the pinned PostgreSQL identifier.
	Column string `json:"column"`
	// Storage is the storage class.
	Storage Storage `json:"storage"`
	// Aliases are other document keys that land in the same column, notably the
	// compact* sibling of a compact-json key.
	Aliases []string `json:"aliases,omitempty"`
}

// IsAlias reports whether name is one of e's aliases (not its primary key).
func (e *Entry) IsAlias(name string) bool {
	for _, a := range e.Aliases {
		if a == name {
			return true
		}
	}
	return false
}

// Catalog is one table's compiled key map.
type Catalog struct {
	Collection string `json:"collection"`
	Table      string `json:"table"`
	// FlattenKey, when set, is the top-level key whose first-level children each
	// get their own column (mysekai's updatedResources, which alone is 97.9% of
	// a mysekai document).
	FlattenKey string  `json:"flattenKey,omitempty"`
	Entries    []Entry `json:"entries"`

	byKey map[string]*Entry
	index map[string]int
}

// ExtraColumn holds every top-level key with no column of its own, as a JSON
// object. Unknown key names appear only as JSON member names inside it.
const ExtraColumn = "extra"

// Metadata column names.
const (
	ColUserID     = "user_id"
	ColServer     = "server"
	ColUploadTime = "upload_time"
)

// MaxDataColumns is a self-imposed ceiling well under PostgreSQL's hard 1600, so
// a runaway key union fails loudly at generate time instead of at CREATE TABLE.
const MaxDataColumns = 900

// Lookup returns the entry a document key maps to, following aliases.
func (c *Catalog) Lookup(key string) (*Entry, bool) {
	e, ok := c.byKey[key]
	return e, ok
}

// ColumnIndex returns the position of an entry's column in the pinned order.
func (c *Catalog) ColumnIndex(key string) (int, bool) {
	i, ok := c.index[key]
	return i, ok
}

// Len is the number of data columns (excluding metadata and extra).
func (c *Catalog) Len() int { return len(c.Entries) }

// TotalColumns includes the three metadata columns and `extra`.
func (c *Catalog) TotalColumns() int { return len(c.Entries) + 4 }

// Checksum is a stable digest of the pinned placement. A deployment whose
// catalog checksum differs from the one the table was created with is reading a
// different schema than it thinks.
func (c *Catalog) Checksum() string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00", c.Collection, c.Table, c.FlattenKey)
	for _, e := range c.Entries {
		fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00", e.Key, e.Column, e.Storage, strings.Join(e.Aliases, ","))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// build fills the lookup indexes and validates the catalog.
func (c *Catalog) build() error {
	c.byKey = make(map[string]*Entry, len(c.Entries)*2)
	c.index = make(map[string]int, len(c.Entries)*2)
	seenCol := make(map[string]bool, len(c.Entries))

	if c.Table == "" {
		return fmt.Errorf("catalog %q: empty table name", c.Collection)
	}
	for i := range c.Entries {
		e := &c.Entries[i]
		if e.Key == "" {
			return fmt.Errorf("%s: empty key at index %d", c.Table, i)
		}
		if !e.Storage.Valid() {
			return fmt.Errorf("%s: key %q has unknown storage class %q", c.Table, e.Key, e.Storage)
		}
		// A denied key must never reach a column. Validate rather than trust the
		// generator: this is the only place the security decision is mechanically
		// enforced, and a hand-edited registry could reintroduce it.
		if DeniedKeys[e.Key] {
			return fmt.Errorf("%s: key %q is denied but has column %q", c.Table, e.Key, e.Column)
		}
		if e.Path != "" && DeniedKeys[e.Child] {
			return fmt.Errorf("%s: flattened key %q is denied but has column %q", c.Table, e.Key, e.Column)
		}
		if MetadataKeys[e.Key] {
			return fmt.Errorf("%s: key %q is a metadata column, it must not also have a json column", c.Table, e.Key)
		}
		if e.Column == ExtraColumn || e.Column == ColUserID || e.Column == ColServer || e.Column == ColUploadTime {
			return fmt.Errorf("%s: key %q collides with reserved column %q", c.Table, e.Key, e.Column)
		}
		if len(e.Column) > MaxIdentLen {
			return fmt.Errorf("%s: column %q for key %q exceeds %d bytes", c.Table, e.Column, e.Key, MaxIdentLen)
		}
		if !strings.HasSuffix(e.Column, e.Storage.Suffix()) {
			return fmt.Errorf("%s: column %q for key %q lacks storage suffix %q", c.Table, e.Column, e.Key, e.Storage.Suffix())
		}
		if seenCol[e.Column] {
			return fmt.Errorf("%s: duplicate column %q (key %q)", c.Table, e.Column, e.Key)
		}
		seenCol[e.Column] = true

		if _, dup := c.byKey[e.Key]; dup {
			return fmt.Errorf("%s: duplicate key %q", c.Table, e.Key)
		}
		c.byKey[e.Key] = e
		c.index[e.Key] = i
		for _, a := range e.Aliases {
			if DeniedKeys[a] {
				return fmt.Errorf("%s: alias %q of key %q is denied", c.Table, a, e.Key)
			}
			if _, dup := c.byKey[a]; dup {
				return fmt.Errorf("%s: alias %q of key %q collides with another key", c.Table, a, e.Key)
			}
			c.byKey[a] = e
			c.index[a] = i
		}
	}
	if c.TotalColumns() > MaxDataColumns {
		return fmt.Errorf("%s: %d columns, above the %d ceiling", c.Table, c.TotalColumns(), MaxDataColumns)
	}
	return nil
}

// Keys returns every primary key in pinned column order.
func (c *Catalog) Keys() []string {
	out := make([]string, 0, len(c.Entries))
	for i := range c.Entries {
		out = append(out, c.Entries[i].Key)
	}
	return out
}

// SortedKeys returns every primary key, sorted, for stable reporting.
func (c *Catalog) SortedKeys() []string {
	out := c.Keys()
	sort.Strings(out)
	return out
}

// Placement is where a requested key's value comes from.
type Placement int

const (
	// PlaceUnknown means the key has no column and no metadata slot: its value,
	// if any, lives inside the `extra` json column.
	PlaceUnknown Placement = iota
	// PlaceColumn means the key owns a data column.
	PlaceColumn
	// PlaceMetadata means the key is one of the typed metadata columns
	// (user_id / server / upload_time) rather than a json column.
	PlaceMetadata
	// PlaceFlattenParent means the key is the sub-document that was split into
	// per-child columns (mysekai's updatedResources). It owns no column of its
	// own and must be reassembled from its children plus any unknown children
	// parked in `extra`.
	PlaceFlattenParent
)

// Resolve classifies a requested `?key=` segment.
//
// This exists as one function because the three-way split is easy to get wrong
// in a way nothing catches. In particular `upload_time` IS on the public key
// allowlist but is NOT a catalog entry — MetadataKeys refuses it a column and
// build() rejects a registry that gives it one. A resolver written as
// "Lookup, else extra" therefore answers `[]` for `?key=upload_time`, which
// silently kills the whole conditional-request path: the 304 machinery needs
// that exact segment to carry a real timestamp.
//
// Verified against the pinned catalog: `upload_time` is the ONLY allowlist entry
// with no suite column. Everything else that looks mysekai-ish
// (userMysekaiGates, userMysekaiCanvases, userMysekaiMaterials,
// userMysekaiCharacterTalks, userMysekaiFixtureGameCharacterPerformanceBonuses)
// is a genuine suite top-level key and does own a column.
func (c *Catalog) Resolve(key string) (*Entry, Placement) {
	if MetadataKeys[key] {
		return nil, PlaceMetadata
	}
	if e, ok := c.byKey[key]; ok {
		return e, PlaceColumn
	}
	// The flattened parent has no column of its own — its children do — so a
	// plain "not in the catalog, look in extra" answer would serve an empty
	// object for the single largest key in a mysekai document.
	if c.FlattenKey != "" && key == c.FlattenKey {
		return nil, PlaceFlattenParent
	}
	return nil, PlaceUnknown
}

// FlattenChildren returns the entries that make up the flattened parent, in
// catalog order.
func (c *Catalog) FlattenChildren() []*Entry {
	if c.FlattenKey == "" {
		return nil
	}
	out := make([]*Entry, 0, len(c.Entries))
	for i := range c.Entries {
		if c.Entries[i].Path == c.FlattenKey {
			out = append(out, &c.Entries[i])
		}
	}
	return out
}

// IsDenied reports whether a key must never be stored. Kept as a function so
// call sites read as intent rather than as a map lookup.
func IsDenied(key string) bool { return DeniedKeys[key] }
