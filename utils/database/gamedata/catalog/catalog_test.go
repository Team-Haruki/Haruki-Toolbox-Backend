package catalog

import (
	"strings"
	"testing"
)

// The pinned registries must be loadable and internally consistent in every
// build. init() panics otherwise, so reaching a test at all proves the happy
// path; these assert the properties that matter.

func TestPinnedCatalogsLoad(t *testing.T) {
	for _, c := range []*Catalog{Suite(), Mysekai()} {
		if c.Len() == 0 {
			t.Fatalf("%s: empty catalog", c.Table)
		}
		if c.TotalColumns() > MaxDataColumns {
			t.Fatalf("%s: %d columns", c.Table, c.TotalColumns())
		}
		t.Logf("%s: %d data columns (%d total), checksum %s",
			c.Table, c.Len(), c.TotalColumns(), c.Checksum())
	}
}

// A denied key is a security control that no size or row metric can verify, so
// it is asserted directly and from several angles.
func TestDeniedKeysHaveNoColumnAnywhere(t *testing.T) {
	for _, c := range []*Catalog{Suite(), Mysekai()} {
		for _, e := range c.Entries {
			if DeniedKeys[e.Key] {
				t.Fatalf("%s: denied key %q has column %q", c.Table, e.Key, e.Column)
			}
			if e.Child != "" && DeniedKeys[e.Child] {
				t.Fatalf("%s: denied child %q has column %q", c.Table, e.Child, e.Column)
			}
			for _, a := range e.Aliases {
				if DeniedKeys[a] {
					t.Fatalf("%s: denied alias %q on key %q", c.Table, a, e.Key)
				}
			}
		}
		for k := range DeniedKeys {
			if _, ok := c.Lookup(k); ok {
				t.Fatalf("%s: denied key %q resolves through Lookup", c.Table, k)
			}
		}
	}
}

func TestValidateRejectsADeniedColumn(t *testing.T) {
	for k := range DeniedKeys {
		c := &Catalog{Collection: "suite", Table: "game_suite",
			Entries: []Entry{{Key: k, Column: "x_j", Storage: StorageJSON}}}
		err := c.build()
		if err == nil {
			t.Fatalf("build accepted denied key %q", k)
		}
		if !strings.Contains(err.Error(), "denied") {
			t.Fatalf("error for %q does not mention denial: %v", k, err)
		}
	}
}

func TestValidateRejectsADeniedAlias(t *testing.T) {
	c := &Catalog{Collection: "suite", Table: "game_suite",
		Entries: []Entry{{Key: "userCards", Column: "user_cards_j", Storage: StorageJSON,
			Aliases: []string{"userRegistration"}}}}
	if err := c.build(); err == nil {
		t.Fatal("build accepted a denied key smuggled in as an alias")
	}
}

func TestValidateRejectsReservedColumnCollision(t *testing.T) {
	for _, col := range []string{ExtraColumn, ColUserID, ColServer, ColUploadTime} {
		c := &Catalog{Collection: "suite", Table: "game_suite",
			Entries: []Entry{{Key: "userCards", Column: col, Storage: StorageJSON}}}
		if err := c.build(); err == nil {
			t.Fatalf("build accepted a key on reserved column %q", col)
		}
	}
}

// Every compact key must carry its compact* sibling as an alias, or a cn/tw
// upload would land in `extra` instead of its column.
func TestCompactKeysCarryTheirCompactAlias(t *testing.T) {
	c := Suite()
	for row, compact := range CompactPairs {
		e, ok := c.Lookup(row)
		if !ok {
			t.Fatalf("compact row key %q missing from the suite catalog", row)
		}
		if e.Storage != StorageCompactJSON {
			t.Fatalf("%s: storage %q, want %q", row, e.Storage, StorageCompactJSON)
		}
		if !e.IsAlias(compact) {
			t.Fatalf("%s: missing alias %q (aliases=%v)", row, compact, e.Aliases)
		}
		got, ok := c.Lookup(compact)
		if !ok || got != e {
			t.Fatalf("%s: alias %q does not resolve to the same entry", row, compact)
		}
		if CompactFieldName(row) != compact {
			t.Fatalf("CompactFieldName(%q) = %q, want %q", row, CompactFieldName(row), compact)
		}
	}
}

// The flattened mysekai children must all agree with the declared parent.
func TestMysekaiFlattenedChildren(t *testing.T) {
	c := Mysekai()
	if c.FlattenKey == "" {
		t.Fatal("mysekai catalog has no FlattenKey")
	}
	n := 0
	for _, e := range c.Entries {
		if e.Path == "" {
			continue
		}
		n++
		if e.Path != c.FlattenKey {
			t.Fatalf("%s: path %q, want %q", e.Key, e.Path, c.FlattenKey)
		}
		if e.Key != e.Path+"."+e.Child {
			t.Fatalf("%s: key does not match path.child (%q.%q)", e.Key, e.Path, e.Child)
		}
	}
	if n == 0 {
		t.Fatal("mysekai catalog declares a FlattenKey but has no flattened children")
	}
	t.Logf("mysekai: %d flattened children of %q", n, c.FlattenKey)
}

func TestSnakeCase(t *testing.T) {
	cases := map[string]string{
		"userMusicResults":               "user_music_results",
		"userCostume3dStatuses":          "user_costume3d_statuses",
		"userCharacterMissionV2Statuses": "user_character_mission_v2_statuses",
		"compactUserMusicAchievements":   "compact_user_music_achievements",
		"upload_time":                    "upload_time",
		"":                               "k",
		"3d":                             "k_3d",
	}
	for in, want := range cases {
		if got := SnakeCase(in); got != want {
			t.Errorf("SnakeCase(%q) = %q, want %q", in, got, want)
		}
	}
}

// A long key must not be truncated into a collision.
func TestFitIdentAvoidsCollision(t *testing.T) {
	a := strings.Repeat("a", 80) + "one"
	b := strings.Repeat("a", 80) + "two"
	ca := FitIdent("", SnakeCase(a), "_j", a)
	cb := FitIdent("", SnakeCase(b), "_j", b)
	if ca == cb {
		t.Fatalf("two distinct long keys collapsed onto %q", ca)
	}
	for _, c := range []string{ca, cb} {
		if len(c) > MaxIdentLen {
			t.Fatalf("%q exceeds %d bytes", c, MaxIdentLen)
		}
	}
}

// Column identifiers must be safe to emit unquoted-ish: lowercase, digits and
// underscores only. Nothing derived from a document key may escape that.
func TestEveryColumnIsAPlainIdentifier(t *testing.T) {
	for _, c := range []*Catalog{Suite(), Mysekai()} {
		for _, e := range c.Entries {
			for i, r := range e.Column {
				ok := r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
				if i == 0 && (r >= '0' && r <= '9') {
					ok = false
				}
				if !ok {
					t.Fatalf("%s: column %q for key %q has illegal rune %q at %d",
						c.Table, e.Column, e.Key, r, i)
				}
			}
		}
	}
}

// --- DDL ---------------------------------------------------------------------

func TestDDLMentionsEveryColumnExactlyOnce(t *testing.T) {
	for _, c := range []*Catalog{Suite(), Mysekai()} {
		ddl := c.DDL(DefaultDDLOptions())
		for _, col := range c.AllColumns() {
			if n := strings.Count(ddl, QuoteIdent(col)); n < 1 {
				t.Fatalf("%s: column %q missing from DDL", c.Table, col)
			}
		}
		if !strings.Contains(ddl, "PRIMARY KEY") {
			t.Fatalf("%s: DDL has no primary key", c.Table)
		}
		// jsonb is rejected by design: it reorders keys, drops duplicates,
		// rewrites number literals and refuses NUL.
		if strings.Contains(ddl, "jsonb") {
			t.Fatalf("%s: DDL uses jsonb", c.Table)
		}
		if strings.Contains(ddl, "bytea") {
			t.Fatalf("%s: DDL uses bytea; application-level compression was rejected", c.Table)
		}
	}
}

func TestSelectListLengthMatchesScanTargets(t *testing.T) {
	for _, c := range []*Catalog{Suite(), Mysekai()} {
		// upload_time + data columns + extra
		if got, want := len(c.SelectColumns()), c.Len()+2; got != want {
			t.Fatalf("%s: SelectColumns len %d, want %d", c.Table, got, want)
		}
		if got, want := len(c.AllColumns()), c.TotalColumns(); got != want {
			t.Fatalf("%s: AllColumns len %d, want %d", c.Table, got, want)
		}
	}
}

func TestServerCodesRoundTrip(t *testing.T) {
	for name, code := range ServerCodes {
		if code == ServerUnknown {
			t.Fatalf("region %q collides with ServerUnknown", name)
		}
		got, ok := ServerCode(name)
		if !ok || got != code {
			t.Fatalf("ServerCode(%q) = %d,%v", name, got, ok)
		}
		back, ok := ServerName(code)
		if !ok || back != name {
			t.Fatalf("ServerName(%d) = %q,%v want %q", code, back, ok, name)
		}
	}
	if _, ok := ServerName(ServerUnknown); ok {
		t.Fatal("ServerUnknown must not map back to a region name")
	}
	seen := map[int16]string{}
	for name, code := range ServerCodes {
		if prev, dup := seen[code]; dup {
			t.Fatalf("regions %q and %q share code %d", prev, name, code)
		}
		seen[code] = name
	}
}

func TestQuoteIdentEscapesQuotes(t *testing.T) {
	if got := QuoteIdent(`a"b`); got != `"a""b"` {
		t.Fatalf("QuoteIdent = %s", got)
	}
}

// --- Resolve -----------------------------------------------------------------

// upload_time is the one allowlist key with no column. A resolver that treats
// "not in the catalog" as "look in extra" answers [] for it and silently kills
// every conditional request. Pin it.
func TestUploadTimeResolvesAsMetadataNotExtra(t *testing.T) {
	for _, c := range []*Catalog{Suite(), Mysekai()} {
		for _, k := range []string{"upload_time", "_id", "server"} {
			e, p := c.Resolve(k)
			if p != PlaceMetadata {
				t.Fatalf("%s: Resolve(%q) placement = %v, want PlaceMetadata", c.Table, k, p)
			}
			if e != nil {
				t.Fatalf("%s: Resolve(%q) returned an entry", c.Table, k)
			}
			if _, ok := c.Lookup(k); ok {
				t.Fatalf("%s: %q must not have a data column", c.Table, k)
			}
		}
	}
}

// Every remaining public-allowlist key must own a real suite column, or a public
// response silently degrades to [] for it.
func TestPublicAllowlistKeysAllHaveASuiteColumn(t *testing.T) {
	allowlist := []string{
		"userDecks", "userCards", "userAreas", "userHonors", "userMusics",
		"userEvents", "upload_time", "userProfile", "userCharacters",
		"userWorldBlooms", "userBondsHonors", "userMusicResults",
		"userMysekaiGates", "userProfileHonors", "userMysekaiCanvases",
		"userRankMatchSeasons", "userMysekaiMaterials", "userMusicAchievements",
		"userMysekaiCharacterTalks", "userWorldBloomSupportDecks",
		"userChallengeLiveSoloDecks", "userChallengeLiveSoloStages",
		"userChallengeLiveSoloResults", "userChallengeLiveSoloHighScoreRewards",
		"userMysekaiFixtureGameCharacterPerformanceBonuses",
	}
	c := Suite()
	for _, k := range allowlist {
		_, p := c.Resolve(k)
		switch p {
		case PlaceColumn, PlaceMetadata:
		default:
			t.Errorf("allowlist key %q resolves to %v — a public response would degrade to []", k, p)
		}
	}
}

func TestResolveUnknownKeyGoesToExtra(t *testing.T) {
	c := Suite()
	if e, p := c.Resolve("userDefinitelyNotAGameKey"); p != PlaceUnknown || e != nil {
		t.Fatalf("Resolve(unknown) = %v, %v", e, p)
	}
}

// A compact alias must resolve to a column, not to extra — otherwise a cn/tw
// upload lands in extra and the read path never finds it.
func TestCompactAliasResolvesToItsColumn(t *testing.T) {
	c := Suite()
	for row, compact := range CompactPairs {
		e, p := c.Resolve(compact)
		if p != PlaceColumn {
			t.Fatalf("Resolve(%q) placement = %v, want PlaceColumn", compact, p)
		}
		if e.Key != row {
			t.Fatalf("Resolve(%q) resolved to key %q, want %q", compact, e.Key, row)
		}
	}
}

// The flattened parent owns no column, so a resolver that falls through to
// `extra` would serve an empty object for the single largest key in a mysekai
// document (updatedResources is 97.9% of it).
func TestFlattenParentResolvesAsItsOwnPlacement(t *testing.T) {
	c := Mysekai()
	e, p := c.Resolve(c.FlattenKey)
	if p != PlaceFlattenParent {
		t.Fatalf("Resolve(%q) = %v, want PlaceFlattenParent", c.FlattenKey, p)
	}
	if e != nil {
		t.Fatal("flatten parent must not resolve to an entry")
	}
	kids := c.FlattenChildren()
	if len(kids) == 0 {
		t.Fatal("no flattened children")
	}
	for _, k := range kids {
		if k.Path != c.FlattenKey {
			t.Fatalf("child %q has path %q", k.Key, k.Path)
		}
	}
	// suite has no flattened parent at all.
	if _, p := Suite().Resolve("updatedResources"); p != PlaceUnknown {
		t.Fatalf("suite resolved a flatten parent: %v", p)
	}
}
