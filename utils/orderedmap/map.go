// Package orderedmap stores MessagePack objects in insertion order. Small
// records avoid a hash table; larger records index a contiguous entry slice.
package orderedmap

import "iter"

const linearLimit = 8

type entry struct {
	key   string
	value any
}

// OrderedMap is mutable and not safe for concurrent writes. Do not copy a map
// after its first mutation. Its zero value is an empty, usable map.
type OrderedMap struct {
	entries []entry
	index   map[string]int
}

func New() *OrderedMap { return NewSize(0) }

// NewSize preallocates entries. Decoders must validate untrusted sizes before
// calling it; capacity is a caller-owned allocation budget.
func NewSize(size int) *OrderedMap {
	// Combine the small map header and exact-capacity entries in one allocation.
	// Unlike an inline array in OrderedMap itself, wide maps carry no extra bytes.
	switch size {
	case 1:
		storage := new(struct {
			m       OrderedMap
			entries [1]entry
		})
		storage.m.entries = storage.entries[:0]
		return &storage.m
	case 2:
		storage := new(struct {
			m       OrderedMap
			entries [2]entry
		})
		storage.m.entries = storage.entries[:0]
		return &storage.m
	case 3:
		storage := new(struct {
			m       OrderedMap
			entries [3]entry
		})
		storage.m.entries = storage.entries[:0]
		return &storage.m
	case 4:
		storage := new(struct {
			m       OrderedMap
			entries [4]entry
		})
		storage.m.entries = storage.entries[:0]
		return &storage.m
	case 5:
		storage := new(struct {
			m       OrderedMap
			entries [5]entry
		})
		storage.m.entries = storage.entries[:0]
		return &storage.m
	case 6:
		storage := new(struct {
			m       OrderedMap
			entries [6]entry
		})
		storage.m.entries = storage.entries[:0]
		return &storage.m
	case 7:
		storage := new(struct {
			m       OrderedMap
			entries [7]entry
		})
		storage.m.entries = storage.entries[:0]
		return &storage.m
	case 8:
		storage := new(struct {
			m       OrderedMap
			entries [8]entry
		})
		storage.m.entries = storage.entries[:0]
		return &storage.m
	}
	m := &OrderedMap{entries: make([]entry, 0, size)}
	if size > linearLimit {
		m.index = make(map[string]int, size)
	}
	return m
}

func (m *OrderedMap) Len() int {
	if m == nil {
		return 0
	}
	return len(m.entries)
}

func (m *OrderedMap) position(key string) (int, bool) {
	if m == nil {
		return 0, false
	}
	if m.index != nil {
		i, ok := m.index[key]
		return i, ok
	}
	for i := range m.entries {
		if m.entries[i].key == key {
			return i, true
		}
	}
	return 0, false
}

func (m *OrderedMap) Get(key string) (any, bool) {
	i, ok := m.position(key)
	if !ok {
		return nil, false
	}
	return m.entries[i].value, true
}

// GetAs returns a value only when its dynamic type is T, without coercing
// numeric MessagePack values through float64.
func (m *OrderedMap) GetAs[T any](key string) (T, bool) {
	value, _ := m.Get(key)
	typed, ok := value.(T)
	return typed, ok
}

// Set replaces an existing value without moving its first-seen position.
func (m *OrderedMap) Set(key string, value any) {
	if i, ok := m.position(key); ok {
		m.entries[i].value = value
		return
	}
	if m.index == nil && len(m.entries) == linearLimit {
		m.index = make(map[string]int, 2*linearLimit)
		for i, e := range m.entries {
			m.index[e.key] = i
		}
	}
	if m.index != nil {
		m.index[key] = len(m.entries)
	}
	previous := m.entries
	m.entries = append(m.entries, entry{key, value})
	// The combined allocation stays alive with the map header. Clear abandoned
	// small entry storage on growth so it cannot retain overwritten values.
	if len(previous) == cap(previous) && cap(previous) <= linearLimit {
		clear(previous)
	}
}

func (m *OrderedMap) Delete(key string) {
	i, ok := m.position(key)
	if !ok {
		return
	}
	copy(m.entries[i:], m.entries[i+1:])
	m.entries[len(m.entries)-1] = entry{}
	m.entries = m.entries[:len(m.entries)-1]
	if m.index != nil {
		delete(m.index, key)
		for j := i; j < len(m.entries); j++ {
			m.index[m.entries[j].key] = j
		}
	}
}

// All iterates without allocating a key snapshot or hashing each key again.
// Mutating the map during iteration is unsupported.
func (m *OrderedMap) All() iter.Seq2[string, any] {
	return func(yield func(string, any) bool) {
		if m == nil {
			return
		}
		for _, e := range m.entries {
			if !yield(e.key, e.value) {
				return
			}
		}
	}
}

// Keys returns an independent snapshot; changing it cannot corrupt the map.
func (m *OrderedMap) Keys() []string {
	keys := make([]string, m.Len())
	if m != nil {
		for i, e := range m.entries {
			keys[i] = e.key
		}
	}
	return keys
}
