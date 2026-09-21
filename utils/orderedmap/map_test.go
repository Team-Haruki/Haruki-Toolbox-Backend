package orderedmap

import (
	json "encoding/json/v2"
	"fmt"
	"reflect"
	"testing"
)

func TestMapMutationAndPromotion(t *testing.T) {
	for _, capacity := range []int{0, 3, 32} {
		m := NewSize(capacity)
		for i := range 32 {
			m.Set(fmt.Sprint(i), i)
		}
		m.Set("3", "updated")
		m.Delete("0")
		m.Delete("15")
		m.Delete("missing")
		m.Set("0", "reinserted")
		var keys []string
		for key, value := range m.All() {
			keys = append(keys, key)
			if got, ok := m.Get(key); !ok || got != value {
				t.Fatalf("lookup disagrees with iteration for %s", key)
			}
		}
		if len(keys) != 31 || keys[0] != "1" || keys[len(keys)-1] != "0" {
			t.Fatalf("unexpected key order: %v", keys)
		}
		if got, ok := m.GetAs[string]("3"); !ok || got != "updated" {
			t.Fatal("typed lookup failed")
		}
		if _, ok := m.GetAs[int]("3"); ok {
			t.Fatal("typed lookup accepted wrong type")
		}
		snapshot := m.Keys()
		snapshot[0] = "corrupt"
		if !reflect.DeepEqual(keys, m.Keys()) {
			t.Fatal("key snapshot mutated map")
		}
	}
}

func TestMapJSONPreservesOrderAndIntegers(t *testing.T) {
	m := NewSize(2)
	nested := NewSize(1)
	nested.Set("id", uint64(18446744073709551615))
	m.Set("z", nested)
	m.Set("a", "<>&")
	got, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"z":{"id":18446744073709551615},"a":"<>&"}` {
		t.Fatalf("unexpected JSON: %s", got)
	}
	var zero OrderedMap
	got, err = json.Marshal(&zero)
	if err != nil || string(got) != "{}" {
		t.Fatalf("zero map: %s %v", got, err)
	}
}

func TestMapIterationStops(t *testing.T) {
	m := New()
	m.Set("a", 1)
	m.Set("b", 2)
	count := 0
	for range m.All() {
		count++
		break
	}
	if count != 1 {
		t.Fatal("iterator did not stop")
	}
}
