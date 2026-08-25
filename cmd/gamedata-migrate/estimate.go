package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
)

// runEstimate sizes the job without writing anything.
func runEstimate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("estimate", flag.ExitOnError)
	var cf commonFlags
	cf.bind(fs)
	sample := fs.Int64("sample", 200, "documents to sample for the size distribution (0 = skip)")
	_ = fs.Parse(args)

	mcl, mdb, err := openMongo(ctx, cf)
	if err != nil {
		return err
	}
	defer func() { _ = mcl.Disconnect(context.Background()) }()

	pairs, err := cf.collections()
	if err != nil {
		return err
	}
	for _, p := range pairs {
		cat, ok := catalog.For(p[1])
		if !ok {
			return fmt.Errorf("no catalog for %q", p[1])
		}
		if err := estimateOne(ctx, mdb, cat, p[0], *sample); err != nil {
			return err
		}
	}
	return nil
}

func estimateOne(ctx context.Context, mdb *mongo.Database, cat *catalog.Catalog, collection string, sample int64) error {
	coll := mdb.Collection(collection)

	countCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	total, err := coll.EstimatedDocumentCount(countCtx)
	if err != nil {
		return fmt.Errorf("count %s: %w", collection, err)
	}

	fmt.Printf("\n%s -> %s\n", collection, cat.Table)
	fmt.Printf("  documents            : %d\n", total)
	fmt.Printf("  catalog columns      : %d data (%d total), checksum %s\n",
		cat.Len(), cat.TotalColumns(), cat.Checksum())

	if sample <= 0 {
		return nil
	}
	// $sample, never a full-collection scan: an unsampled $bsonSize aggregation
	// reads every document and evicts the WiredTiger cache on a live primary.
	cur, err := coll.Aggregate(ctx, mongo.Pipeline{
		{{Key: "$sample", Value: bson.D{{Key: "size", Value: sample}}}},
		{{Key: "$project", Value: bson.D{{Key: "sz", Value: bson.D{{Key: "$bsonSize", Value: "$$ROOT"}}}}}},
	})
	if err != nil {
		return fmt.Errorf("sample %s: %w", collection, err)
	}
	defer func() { _ = cur.Close(context.Background()) }()

	var sizes []int64
	for cur.Next(ctx) {
		var row struct {
			Sz int64 `bson:"sz"`
		}
		if err := cur.Decode(&row); err != nil {
			return err
		}
		sizes = append(sizes, row.Sz)
	}
	if err := cur.Err(); err != nil {
		return err
	}
	if len(sizes) == 0 {
		return errors.New("sample returned no documents")
	}
	sortInt64(sizes)
	fmt.Printf("  sampled              : %d documents\n", len(sizes))
	fmt.Printf("  size p50 / p90 / max : %.2f / %.2f / %.2f MiB\n",
		mib(percentile(sizes, 50)), mib(percentile(sizes, 90)), mib(sizes[len(sizes)-1]))
	fmt.Printf("  NOTE: a sample understates the tail; p99 and max are lower bounds.\n")
	return nil
}

func mib(n int64) float64 { return float64(n) / (1 << 20) }

func percentile(sorted []int64, p int) int64 {
	if len(sorted) == 0 {
		return 0
	}
	i := (len(sorted) - 1) * p / 100
	return sorted[i]
}

func sortInt64(v []int64) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j-1] > v[j]; j-- {
			v[j-1], v[j] = v[j], v[j-1]
		}
	}
}
