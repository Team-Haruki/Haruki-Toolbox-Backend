package main

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
)

// A game user id exceeds 2^53. Any float64 hop corrupts it, so the BSON
// conversion must keep integers as integers.
func TestBsonToGoKeepsLargeIntegersExact(t *testing.T) {
	const big = int64(28808221489823746)
	got := bsonToGo(bson.M{"userId": big}).(map[string]any)
	if got["userId"] != any(big) {
		t.Fatalf("userId = %#v, want int64(%d)", got["userId"], big)
	}
}

func TestBsonToGoConvertsNestedShapes(t *testing.T) {
	in := bson.M{
		"a": bson.A{bson.M{"b": int32(1)}, bson.D{{Key: "c", Value: int64(2)}}},
	}
	got := bsonToGo(in).(map[string]any)
	arr, ok := got["a"].([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("a = %#v", got["a"])
	}
	if _, ok := arr[0].(map[string]any); !ok {
		t.Fatalf("bson.M element not converted: %T", arr[0])
	}
	d, ok := arr[1].(map[string]any)
	if !ok {
		t.Fatalf("bson.D element not converted: %T", arr[1])
	}
	if d["c"] != any(int64(2)) {
		t.Fatalf("c = %#v", d["c"])
	}
}

// A float that has already lost precision must be REFUSED rather than silently
// truncated into a plausible-looking id.
func TestToInt64RefusesImpreciseFloats(t *testing.T) {
	if _, ok := toInt64(float64(int64(1) << 54)); ok {
		t.Fatal("accepted a float past 2^53")
	}
	if _, ok := toInt64(1.5); ok {
		t.Fatal("accepted a non-integral float")
	}
	if v, ok := toInt64(float64(42)); !ok || v != 42 {
		t.Fatalf("rejected an exactly-representable float: %d %v", v, ok)
	}
	for _, in := range []any{int32(7), int64(7), 7} {
		if v, ok := toInt64(in); !ok || v != 7 {
			t.Fatalf("toInt64(%T) = %d, %v", in, v, ok)
		}
	}
	if _, ok := toInt64("7"); ok {
		t.Fatal("accepted a string id")
	}
	if _, ok := toInt64(nil); ok {
		t.Fatal("accepted a nil id")
	}
}

// Every per-document defect must QUARANTINE rather than abort: production
// contains documents that predate upload validation, and a backfill that dies on
// the first one strands the collection mid-window.
func TestIdentityQuarantinesRatherThanFailing(t *testing.T) {
	cases := []struct {
		name string
		doc  bson.M
	}{
		{"no _id", bson.M{"server": "jp"}},
		{"null _id", bson.M{"_id": nil, "server": "jp"}},
		{"objectid _id", bson.M{"_id": bson.NewObjectID(), "server": "jp"}},
		// note: a document with no `server` is PARKED, not quarantined —
		// see TestServerlessDocumentIsParkedNotQuarantined.
		{"unknown server", bson.M{"_id": int64(1), "server": "zz"}},
	}
	for _, c := range cases {
		_, _, q := identity(c.doc)
		if q == nil {
			t.Errorf("%s was accepted", c.name)
			continue
		}
		if q.Reason == "" {
			t.Errorf("%s quarantined with no reason", c.name)
		}
	}
	id, server, q := identity(bson.M{"_id": int64(28808221489823746), "server": "jp"})
	if q != nil {
		t.Fatalf("a valid document was quarantined: %v", q)
	}
	if id != 28808221489823746 || server != "jp" {
		t.Fatalf("identity = %d/%s", id, server)
	}
}

// A document with no `server` field must be PARKED, not quarantined.
//
// Production contains such documents and they are unaddressable — every read
// filters on a region, so nothing can ask for them. Quarantining would lose the
// row for no benefit AND would block the cutover's zero-quarantine gate on a
// document that is expected and harmless. Found by running the CLI against the
// real corpus: it stopped the whole dry run on one such document.
func TestServerlessDocumentIsParkedNotQuarantined(t *testing.T) {
	id, server, q := identity(bson.M{"_id": int64(493365101741342728)})
	if q != nil {
		t.Fatalf("a server-less document was quarantined: %v", q)
	}
	if id != 493365101741342728 {
		t.Fatalf("id = %d", id)
	}
	if server != "" {
		t.Fatalf("server = %q, want the empty parking value", server)
	}
}

// Only a compact ALIAS may be expanded on the source side. Gating on the value
// shape instead ("does it start with {") matches every object-valued key, which
// ran the columnar restore over userProfile / userConfig / userGamedata and 20
// more and produced a wave of false differences on the real corpus.
func TestOnlyCompactAliasesAreTreatedAsColumnar(t *testing.T) {
	c := catalog.Suite()
	for row, compact := range catalog.CompactPairs {
		if !isCompactSpelling(c, compact) {
			t.Errorf("%q is a compact alias but was not recognised", compact)
		}
		// The ROW spelling holds row form; expanding it would be wrong.
		if isCompactSpelling(c, row) {
			t.Errorf("%q is the row spelling and must not be treated as columnar", row)
		}
	}
	// Object-valued keys that merely look like an object must never qualify.
	for _, k := range []string{
		"userProfile", "userConfig", "userGamedata", "userAutoLive",
		"userBoost", "userTutorial", "userMysekaiGamedata",
	} {
		if isCompactSpelling(c, k) {
			t.Errorf("%q was treated as columnar", k)
		}
	}
	if isCompactSpelling(c, "userNotAKeyAtAll") {
		t.Error("an unknown key was treated as columnar")
	}
}
