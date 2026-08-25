package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"sort"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	harukiGameData "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
)

// quarantine records a document that could not be loaded.
//
// A per-document data defect must NOT abort the run. Production contains
// documents that predate upload validation — one suite document had `_id: null`
// — and a backfill that dies on the first one strands the whole collection
// mid-window, with loaded and unloaded rows mixed together.
type quarantine struct {
	Reason string
	ID     string
}

type migrateStats struct {
	Docs          int
	Quarantined   []quarantine
	DeniedDropped map[string]int
	ExtraKeys     map[string]int
	AliasConflict map[string]int
	Bytes         int64
	Elapsed       time.Duration
}

func runMigrate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	var cf commonFlags
	cf.bind(fs)
	apply := fs.Bool("apply", false, "actually write; without it the run is a DRY RUN")
	createSchema := fs.Bool("create-schema", false, "create the tables if they do not exist")
	_ = fs.Parse(args)

	if cf.pgURL == "" {
		return errors.New("-pg is required")
	}
	if !*apply {
		fmt.Println("DRY RUN — nothing will be written. Re-run with -apply to migrate.")
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

	if *createSchema && *apply {
		cats := make([]*catalog.Catalog, 0, len(pairs))
		for _, p := range pairs {
			c, _ := catalog.For(p[1])
			cats = append(cats, c)
		}
		if _, err := harukiGameData.EnsureSchema(ctx, pool, true, cats...); err != nil {
			return err
		}
	}

	blocking := false
	for _, p := range pairs {
		cat, ok := catalog.For(p[1])
		if !ok {
			return fmt.Errorf("no catalog for %q", p[1])
		}
		st, err := migrateCollection(ctx, mdb, pool, cat, p[0], cf, *apply)
		if err != nil {
			return err
		}
		reportMigrate(p[0], cat, st, *apply)
		if len(st.Quarantined) > 0 {
			blocking = true
		}
	}

	if blocking {
		// The cutover's go/no-go requires a zero quarantine count: a document
		// that could not be loaded is a document the new store does not have.
		return errors.New("quarantined documents were not loaded; resolve them in the source before cutting over")
	}
	return nil
}

func migrateCollection(
	ctx context.Context,
	mdb *mongo.Database,
	pool *harukiGameData.Pool,
	cat *catalog.Catalog,
	collection string,
	cf commonFlags,
	apply bool,
) (*migrateStats, error) {
	store := harukiGameData.NewStore(pool, cat)
	st := &migrateStats{DeniedDropped: map[string]int{}, ExtraKeys: map[string]int{}, AliasConflict: map[string]int{}}

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
		return nil, fmt.Errorf("find %s: %w", collection, err)
	}
	defer func() { _ = cur.Close(context.Background()) }()

	// No limit on the read side: MongoDB's 16 MB cap applies to writes only, so
	// an oversized document can still be read out and must be.
	limits := harukiGameData.Limits{}

	start := time.Now()
	for cur.Next(ctx) {
		var doc bson.M
		if err := cur.Decode(&doc); err != nil {
			return nil, fmt.Errorf("decode from %s: %w", collection, err)
		}
		userID, server, qerr := identity(doc)
		if qerr != nil {
			st.Quarantined = append(st.Quarantined, *qerr)
			continue
		}
		data := bsonToGo(doc).(map[string]any)

		// A dry run still encodes every document — an upload that cannot be
		// represented then surfaces before the maintenance window rather than
		// inside it — and it accumulates the SAME statistics as a real run.
		// Reporting only on -apply would make the dry run print
		// "denied keys dropped: none observed", which reads as "the drop ran and
		// found nothing" when it means "the counter was thrown away".
		var ws harukiGameData.WriteStats
		var werr error
		if apply {
			ws, werr = store.Write(ctx, userID, server, data, harukiGameData.WriteMigrate, limits)
		} else {
			ws, werr = store.EncodeOnly(data, harukiGameData.WriteMigrate, limits)
		}
		if werr != nil {
			st.Quarantined = append(st.Quarantined, quarantine{
				Reason: werr.Error(), ID: fmt.Sprintf("%d/%s", userID, server)})
			continue
		}
		for k, n := range ws.DeniedDropped {
			st.DeniedDropped[k] += n
		}
		for _, k := range ws.ExtraKeys {
			st.ExtraKeys[k]++
		}
		for k, n := range ws.AliasConflicts {
			st.AliasConflict[k] += n
		}
		st.Bytes += int64(ws.Bytes)
		st.Docs++
		if cf.progress > 0 && st.Docs%cf.progress == 0 {
			fmt.Printf("  ... %d documents\n", st.Docs)
		}
		if cf.verbose {
			fmt.Printf("  %d/%s\n", userID, server)
		}
	}
	st.Elapsed = time.Since(start)
	return st, cur.Err()
}

// identity extracts the primary key, quarantining anything that cannot supply
// one rather than failing the run.
func identity(doc bson.M) (int64, string, *quarantine) {
	rawID, ok := doc["_id"]
	if !ok {
		return 0, "", &quarantine{Reason: "document has no _id", ID: "?"}
	}
	userID, ok := toInt64(rawID)
	if !ok {
		return 0, "", &quarantine{
			Reason: fmt.Sprintf("_id is not an integer (%T)", rawID),
			ID:     fmt.Sprintf("%v", rawID),
		}
	}
	server, _ := doc["server"].(string)
	if server == "" {
		// Parked under catalog.ServerUnknown rather than quarantined. Production
		// contains such documents (the mysekai upsert only started setting
		// `server` later), and they are UNADDRESSABLE — every read filters on a
		// region, so nothing can ask for them. Quarantining would therefore lose
		// a row for no benefit, and would block the cutover's zero-quarantine
		// gate on a document that is expected and harmless.
		return userID, "", nil
	}
	if _, ok := catalog.ServerCode(server); !ok {
		return 0, "", &quarantine{
			Reason: fmt.Sprintf("unknown server %q", server),
			ID:     fmt.Sprintf("%d", userID),
		}
	}
	return userID, server, nil
}

func reportMigrate(collection string, cat *catalog.Catalog, st *migrateStats, apply bool) {
	mode := "DRY RUN"
	if apply {
		mode = "APPLIED"
	}
	fmt.Printf("\n%s  %s -> %s  (%s)\n", mode, collection, cat.Table, st.Elapsed.Round(time.Millisecond))
	fmt.Printf("  documents loaded : %d\n", st.Docs)
	if st.Bytes > 0 {
		fmt.Printf("  bytes written    : %.2f MiB\n", float64(st.Bytes)/(1<<20))
	}

	// Always print the denied line, even at zero. A silent absence reads as
	// "the drop ran and found nothing" exactly like "the drop never ran", and
	// these keys are too small for any size metric to tell the difference.
	if len(st.DeniedDropped) == 0 {
		fmt.Println("  denied keys dropped: none observed")
	} else {
		total := 0
		keys := make([]string, 0, len(st.DeniedDropped))
		for k, n := range st.DeniedDropped {
			keys = append(keys, k)
			total += n
		}
		sort.Strings(keys)
		fmt.Printf("  denied keys dropped: %d occurrences (never stored, not even in `extra`)\n", total)
		for _, k := range keys {
			fmt.Printf("    %-42s %d\n", k, st.DeniedDropped[k])
		}
	}

	if len(st.AliasConflict) > 0 {
		keys := make([]string, 0, len(st.AliasConflict))
		total := 0
		for k, n := range st.AliasConflict {
			keys = append(keys, k)
			total += n
		}
		sort.Strings(keys)
		fmt.Printf("  both spellings present: %d occurrences (row form stored, compact form kept in `extra`)\n", total)
		for _, k := range keys {
			fmt.Printf("    %-42s %d\n", k, st.AliasConflict[k])
		}
	}

	if len(st.ExtraKeys) > 0 {
		keys := make([]string, 0, len(st.ExtraKeys))
		for k := range st.ExtraKeys {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Printf("  unknown keys parked in `extra`: %d distinct\n", len(keys))
		for _, k := range keys {
			fmt.Printf("    %-42s %d documents\n", k, st.ExtraKeys[k])
		}
	}

	if len(st.Quarantined) > 0 {
		fmt.Printf("\n  QUARANTINED %d documents (NOT loaded):\n", len(st.Quarantined))
		byReason := map[string]int{}
		for _, q := range st.Quarantined {
			byReason[q.Reason]++
		}
		reasons := make([]string, 0, len(byReason))
		for r := range byReason {
			reasons = append(reasons, r)
		}
		sort.Strings(reasons)
		for _, r := range reasons {
			fmt.Printf("    %-56s %d\n", r, byReason[r])
		}
		shown := st.Quarantined
		if len(shown) > 10 {
			shown = shown[:10]
		}
		for _, q := range shown {
			fmt.Printf("      e.g. %s: %s\n", q.ID, q.Reason)
		}
	}
}
