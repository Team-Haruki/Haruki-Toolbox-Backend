package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"reflect"
	"sort"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	harukiGameData "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
)

// runVerify re-reads both stores and compares them key by key.
//
// It compares CANONICALISED values, not serialised bytes. Object key order is
// already not preserved by the response path — NormalizeProviderResponse turns
// every bson.D into a map and the marshaller does not sort keys — so a byte
// comparison would report differences that production itself produces on two
// consecutive requests.
func runVerify(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	var cf commonFlags
	cf.bind(fs)
	maxReport := fs.Int("max-report", 20, "stop reporting after N differing documents")
	_ = fs.Parse(args)

	if cf.pgURL == "" {
		return errors.New("-pg is required")
	}
	mcl, mdb, err := openMongo(ctx, cf)
	if err != nil {
		return err
	}
	defer func() { _ = mcl.Disconnect(context.Background()) }()

	pool, err := harukiGameData.NewPool(ctx, harukiGameData.PoolConfig{URL: cf.pgURL, MaxConns: 8})
	if err != nil {
		return err
	}
	defer func() { _ = pool.Close() }()

	pairs, err := cf.collections()
	if err != nil {
		return err
	}
	failed := 0
	for _, p := range pairs {
		cat, ok := catalog.For(p[1])
		if !ok {
			return fmt.Errorf("no catalog for %q", p[1])
		}
		n, err := verifyCollection(ctx, mdb, pool, cat, p[0], cf, *maxReport)
		if err != nil {
			return err
		}
		failed += n
	}
	if failed > 0 {
		return fmt.Errorf("%d documents differ; do not cut over", failed)
	}
	fmt.Println("\nVERDICT: every compared document matches, modulo the intended denied-key drop.")
	return nil
}

func verifyCollection(
	ctx context.Context,
	mdb *mongo.Database,
	pool *harukiGameData.Pool,
	cat *catalog.Catalog,
	collection string,
	cf commonFlags,
	maxReport int,
) (int, error) {
	store := harukiGameData.NewStore(pool, cat)

	filter := bson.M{}
	if cf.server != "" {
		filter["server"] = cf.server
	}
	opt := options.Find().SetBatchSize(1)
	if cf.limit > 0 {
		opt = opt.SetLimit(cf.limit)
	}
	cur, err := mdb.Collection(collection).Find(ctx, filter, opt)
	if err != nil {
		return 0, err
	}
	defer func() { _ = cur.Close(context.Background()) }()

	checked, differing, deniedOnly, missing := 0, 0, 0, 0
	for cur.Next(ctx) {
		var doc bson.M
		if err := cur.Decode(&doc); err != nil {
			return 0, err
		}
		userID, server, q := identity(doc)
		if q != nil {
			continue // quarantined during migrate; nothing to compare against
		}
		row, err := store.Fetch(ctx, userID, server, nil)
		if err != nil {
			if errors.Is(err, harukiGameData.ErrNoRow) {
				missing++
				if missing <= maxReport {
					fmt.Printf("  MISSING in PostgreSQL: %d/%s\n", userID, server)
				}
				continue
			}
			return 0, err
		}
		checked++

		diffs, denied := compareDocument(cat, bsonToGo(doc).(map[string]any), row)
		switch {
		case len(diffs) == 0 && denied == 0:
		case len(diffs) == 0:
			deniedOnly++
		default:
			differing++
			if differing <= maxReport {
				fmt.Printf("  DIFFERS %d/%s: %v\n", userID, server, diffs)
			}
		}
		if cf.progress > 0 && checked%cf.progress == 0 {
			fmt.Printf("  ... %d compared\n", checked)
		}
	}
	if err := cur.Err(); err != nil {
		return 0, err
	}

	fmt.Printf("\n%s vs %s\n", collection, cat.Table)
	fmt.Printf("  compared            : %d\n", checked)
	fmt.Printf("  differing           : %d\n", differing)
	fmt.Printf("  missing in Postgres : %d\n", missing)
	fmt.Printf("  denied-key drop only: %d   (intended: those keys are never stored)\n", deniedOnly)
	return differing + missing, nil
}

// compareDocument reports the keys whose values differ, and separately how many
// differences are the intended denied-key drop.
func compareDocument(cat *catalog.Catalog, src map[string]any, row *harukiGameData.Row) (diffs []string, denied int) {
	for key, want := range src {
		if key == "_id" || key == catalog.ColServer || key == catalog.ColUploadTime {
			continue
		}
		if catalog.IsDenied(key) {
			// Intended: the key must be absent on the PostgreSQL side. If it is
			// present, that IS a defect and is reported.
			if _, ok, _ := row.RawValue(key); ok {
				diffs = append(diffs, key+" (denied key was stored!)")
			} else {
				denied++
			}
			continue
		}
		if cat.FlattenKey != "" && key == cat.FlattenKey {
			sub, ok := want.(map[string]any)
			if !ok {
				continue
			}
			for child, cw := range sub {
				if catalog.IsDenied(child) {
					denied++
					continue
				}
				if !sameValue(cat, cw, row, cat.FlattenKey+"."+child) {
					diffs = append(diffs, cat.FlattenKey+"."+child)
				}
			}
			continue
		}
		// A document can carry BOTH spellings of a compact key with DIFFERENT
		// values. The row form takes the column (production's precedence) and
		// the compact form is parked in `extra`, so auditing the compact
		// spelling against the column would always report a difference that is
		// in fact the precedence rule working.
		if isCompactSpelling(cat, key) {
			if rowKey, ok := rowSpelling(cat, key); ok {
				if _, both := src[rowKey]; both {
					if parked, found := row.ExtraValue(key); !found || !sameBytes(want, parked) {
						diffs = append(diffs, key+" (parked copy)")
					}
					continue
				}
			}
		}
		if !sameValue(cat, want, row, key) {
			diffs = append(diffs, key)
		}
	}
	sort.Strings(diffs)
	return diffs, denied
}

// sameValue compares one key canonically.
//
// Both sides are reduced to the ROW form before comparing. RawValue already
// expands a compact column on the way out, so a source key spelled
// `compactUserX` — the columnar form cn/tw clients send — has to be expanded
// too, or every such document reports all six compact keys as differing.
// Measured on the real corpus, not doing this made 6,572 of 10,928 documents
// look wrong.
//
// The expansion is gated on the KEY being a compact alias, never on the value
// merely looking like one. A first pass tested `IsCompactValue(wantBytes)`,
// which is true of ANY JSON object, so it ran the columnar restore over every
// object-valued key — userProfile, userConfig, userGamedata and 20 more — and
// produced a second, larger wave of false differences.
func sameValue(cat *catalog.Catalog, want any, row *harukiGameData.Row, key string) bool {
	raw, ok, err := row.RawValue(key)
	if err != nil || !ok {
		return false
	}
	got, err := canonical(raw)
	if err != nil {
		return false
	}
	wantBytes, err := json.Marshal(want)
	if err != nil {
		return false
	}
	if isCompactSpelling(cat, key) && harukiGameData.IsCompactValue(wantBytes) {
		expanded, expErr := harukiGameData.ExpandCompactJSON(wantBytes)
		if expErr != nil {
			return false
		}
		wantBytes = expanded
	}
	wantCanon, err := canonical(wantBytes)
	if err != nil {
		return false
	}
	return reflect.DeepEqual(got, wantCanon)
}

// rowSpelling maps a compact alias back to the row key that owns the column.
func rowSpelling(cat *catalog.Catalog, key string) (string, bool) {
	e, place := cat.Resolve(key)
	if place != catalog.PlaceColumn || !e.IsAlias(key) {
		return "", false
	}
	return e.Key, true
}

// sameBytes compares a source value against stored JSON bytes, canonically.
func sameBytes(want any, stored []byte) bool {
	wantBytes, err := json.Marshal(want)
	if err != nil {
		return false
	}
	gotCanon, err := canonical(stored)
	if err != nil {
		return false
	}
	wantCanon, err := canonical(wantBytes)
	if err != nil {
		return false
	}
	return reflect.DeepEqual(gotCanon, wantCanon)
}

// isCompactSpelling reports whether key is the compact* alias of a
// compact-class column, i.e. a key whose stored value is legitimately columnar.
func isCompactSpelling(cat *catalog.Catalog, key string) bool {
	e, place := cat.Resolve(key)
	return place == catalog.PlaceColumn &&
		e.Storage == catalog.StorageCompactJSON &&
		e.IsAlias(key)
}

// canonical decodes with UseNumber so a game user id above 2^53 is compared as
// its literal rather than through float64.
func canonical(b []byte) (any, error) {
	dec := json.NewDecoder(bytesReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}
