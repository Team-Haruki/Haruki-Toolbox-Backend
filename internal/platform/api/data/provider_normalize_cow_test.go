package data

import (
	"bytes"
	json "encoding/json/v2"
	"math"
	"reflect"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/jsonvalue"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/msgpackcodec"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestProviderJSONScalarContractAcrossPaths(t *testing.T) {
	for _, value := range []any{int64(9007199254740993), uint64(math.MaxUint64), float32(1.2), float64(1.5), float64(1e25), "123", "", false, nil} {
		for _, supplied := range []bool{false, true} {
			in := map[string]any{"userId": value}
			if supplied {
				in["userIdString"] = "existing"
			}
			payload := map[string]any{"userGamedata": in}
			packed, err := msgpackcodec.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			var direct bytes.Buffer
			if err := msgpackcodec.WriteJSON(&direct, packed, ProviderJSONOptions()); err != nil {
				t.Fatal(err)
			}
			normalized, err := json.Marshal(NormalizeProviderResponse(payload))
			if err != nil {
				t.Fatal(err)
			}
			var a, b any
			if err := json.Unmarshal(direct.Bytes(), &a, jsonvalue.Numbers); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(normalized, &b, jsonvalue.Numbers); err != nil {
				t.Fatal(err)
			}
			// The source float32 JSON spelling differs historically; this contract
			// aligns derived text without rewriting the original numeric field.
			left := a.(map[string]any)["userGamedata"].(map[string]any)
			right := b.(map[string]any)["userGamedata"].(map[string]any)
			if !reflect.DeepEqual(left["userIdString"], right["userIdString"]) {
				t.Fatalf("value=%#v supplied=%v direct=%s normalized=%s", value, supplied, direct.Bytes(), normalized)
			}
		}
	}
	for _, value := range []any{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, ok := providerIDString(value); ok {
			t.Fatalf("nonfinite value derived: %v", value)
		}
	}
}

func TestProviderNormalizationCopiesOnlyChangedBranches(t *testing.T) {
	stable := map[string]any{"cardId": int64(1)}
	cards := []any{stable}
	game := map[string]any{"userId": int64(9007199254740993), "userIdString": "stale"}
	original := map[string]any{"userCards": cards, "userGamedata": game}
	got := NormalizeProviderResponse(original).(map[string]any)
	if reflect.ValueOf(got).Pointer() == reflect.ValueOf(original).Pointer() {
		t.Fatal("changed root was not copied")
	}
	if got["userGamedata"].(map[string]any)["userIdString"] != "9007199254740993" || game["userIdString"] != "stale" {
		t.Fatal("derived field missing or input mutated")
	}
	if reflect.ValueOf(got["userCards"]).Pointer() != reflect.ValueOf(cards).Pointer() {
		t.Fatal("unchanged slice copied")
	}
	if reflect.ValueOf(got["userCards"].([]any)[0]).Pointer() != reflect.ValueOf(stable).Pointer() {
		t.Fatal("unchanged map copied")
	}
	again := NormalizeProviderResponse(got).(map[string]any)
	if reflect.ValueOf(again).Pointer() != reflect.ValueOf(got).Pointer() {
		t.Fatal("idempotent normalization copied root")
	}
	rows := []any{stable, original}
	out := NormalizeProviderResponse(rows).([]any)
	if reflect.ValueOf(rows).Pointer() == reflect.ValueOf(out).Pointer() {
		t.Fatal("changed slice shared")
	}
	if game["userIdString"] != "stale" {
		t.Fatal("nested normalization mutated input")
	}
}

func TestProviderNormalizationPreservesEmptyContainers(t *testing.T) {
	for _, value := range []any{map[string]any(nil), bson.M(nil), []any(nil), bson.A(nil), bson.D(nil), []bson.D(nil)} {
		raw, err := json.Marshal(NormalizeProviderResponse(value))
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != "{}" && string(raw) != "[]" {
			t.Fatalf("nil container changed representation: %T %s", value, raw)
		}
	}
}

func BenchmarkNormalizeProviderResponse(b *testing.B) {
	rows := make([]any, 10000)
	for i := range rows {
		rows[i] = map[string]any{"cardId": int64(i), "level": int64(60), "skills": []any{int64(1), int64(2)}}
	}
	payload := map[string]any{"userGamedata": map[string]any{"userId": int64(9007199254740993)}, "userCards": rows}
	b.ReportAllocs()
	for b.Loop() {
		_ = NormalizeProviderResponse(payload)
	}
}
