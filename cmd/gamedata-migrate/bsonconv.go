package main

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func openMongo(ctx context.Context, cf commonFlags) (*mongo.Client, *mongo.Database, error) {
	cl, err := mongo.Connect(options.Client().ApplyURI(cf.mongoURI))
	if err != nil {
		return nil, nil, fmt.Errorf("connect mongo: %w", err)
	}
	if err := cl.Ping(ctx, nil); err != nil {
		return nil, nil, fmt.Errorf("ping mongo: %w", err)
	}
	return cl, cl.Database(cf.mongoDB), nil
}

// bsonToGo converts a decoded BSON value into the plain Go shapes the store
// encodes from.
//
// Integers are kept as int32/int64, never widened through float64: a game user
// id exceeds 2^53 (28808221489823746 is real) and one float hop corrupts it
// silently.
func bsonToGo(v any) any {
	switch t := v.(type) {
	case bson.M:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = bsonToGo(val)
		}
		return out
	case bson.D:
		out := make(map[string]any, len(t))
		for _, e := range t {
			out[e.Key] = bsonToGo(e.Value)
		}
		return out
	case bson.A:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = bsonToGo(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = bsonToGo(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = bsonToGo(val)
		}
		return out
	default:
		return v
	}
}

func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	case float64:
		// Only accept a float that is exactly an integer AND inside the range
		// float64 represents exactly; anything else has already lost precision.
		if n != float64(int64(n)) || n > 1<<53 || n < -(1<<53) {
			return 0, false
		}
		return int64(n), true
	default:
		return 0, false
	}
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }
