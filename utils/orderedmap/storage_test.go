package orderedmap

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"testing"
)

func TestSmallStorageGrowthReleasesOldReferences(t *testing.T) {
	for size := 1; size <= linearLimit; size++ {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := NewSize(size)
			if cap(m.entries) != size {
				t.Fatal("constructor changed requested capacity")
			}
			for i := range size {
				m.Set(fmt.Sprint(i), &struct{ data [1024]byte }{})
			}
			previous := m.entries
			m.Set("grow", true)
			// The map header keeps the combined allocation alive after growth.
			// Its abandoned entries must not pin keys/values that are later replaced.
			for _, e := range previous {
				if e.key != "" || e.value != nil {
					t.Fatal("abandoned storage retains references")
				}
			}
			for i := range size {
				k := fmt.Sprint(i)
				if _, ok := m.Get(k); !ok {
					t.Fatal("growth lost a key")
				}
				m.Set(k, i)
			}
			for i := range size {
				m.Delete(fmt.Sprint(i))
			}
			if !reflect.DeepEqual(m.Keys(), []string{"grow"}) {
				t.Fatal("replacement/deletion changed order")
			}
		})
	}
}

func TestSmallMapMutationDifferential(t *testing.T) {
	for capacity := 0; capacity <= 16; capacity++ {
		m := NewSize(capacity)
		values := map[string]int{}
		var keys []string
		rng := rand.New(rand.NewPCG(uint64(capacity), 19))
		for step := range 2000 {
			key := fmt.Sprint(rng.IntN(40))
			if rng.IntN(4) == 0 {
				m.Delete(key)
				if _, ok := values[key]; ok {
					delete(values, key)
					for i, k := range keys {
						if k == key {
							keys = append(keys[:i], keys[i+1:]...)
							break
						}
					}
				}
			} else {
				if _, ok := values[key]; !ok {
					keys = append(keys, key)
				}
				values[key] = step
				m.Set(key, step)
			}
			if m.Len() != len(keys) {
				t.Fatal("length differs")
			}
			position := 0
			for k, v := range m.All() {
				if k != keys[position] || v != values[k] {
					t.Fatalf("capacity %d: iteration differs", capacity)
				}
				got, ok := m.GetAs[int](k)
				if !ok || got != values[k] {
					t.Fatal("indexed lookup differs")
				}
				position++
			}
		}
	}
}

var constructedMap *OrderedMap

func BenchmarkNewSize(b *testing.B) {
	for _, size := range []int{1, 4, 8, 16} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				constructedMap = NewSize(size)
			}
		})
	}
}
