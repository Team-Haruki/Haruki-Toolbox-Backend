package nuverse

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/iancoleman/orderedmap"
)

func TestRestoreDictSimpleTuple(t *testing.T) {
	t.Parallel()

	keyStructure := []interface{}{
		"profile",
		map[string]interface{}{
			tupleKey: []interface{}{"uid", "name"},
		},
	}
	arrayData := []interface{}{json.Number("1001"), "alice"}

	result := RestoreDict(arrayData, keyStructure)
	profileRaw, ok := result.Get("profile")
	if !ok {
		t.Fatalf("profile key should exist in restored dict")
	}
	profile, ok := profileRaw.(*orderedmap.OrderedMap)
	if !ok {
		t.Fatalf("profile should be ordered map, got %T", profileRaw)
	}
	uid, _ := profile.Get("uid")
	name, _ := profile.Get("name")
	if uid != json.Number("1001") {
		t.Fatalf("uid mismatch: got %v", uid)
	}
	if name != "alice" {
		t.Fatalf("name mismatch: got %v", name)
	}
}

func TestRestoreCompactDataWithEnum(t *testing.T) {
	t.Parallel()

	enumState := orderedmap.New()
	enumState.SetEscapeHTML(false)
	enumState.Set("0", "inactive")
	enumState.Set("1", "active")

	enumOM := orderedmap.New()
	enumOM.SetEscapeHTML(false)
	enumOM.Set("state", enumState)

	data := orderedmap.New()
	data.SetEscapeHTML(false)
	data.Set("state", []interface{}{0, 1, "0", json.Number("1"), 99, nil})
	data.Set(enumKey, enumOM)

	rows := RestoreCompactData(data)
	if len(rows) != 6 {
		t.Fatalf("unexpected rows len: got %d, want 6", len(rows))
	}

	expect := []interface{}{"inactive", "active", "inactive", "active", 99, nil}
	for i, row := range rows {
		v, _ := row.Get("state")
		if expect[i] == nil {
			if v != nil {
				t.Fatalf("row %d expected nil state, got %v", i, v)
			}
			continue
		}
		if v != expect[i] {
			t.Fatalf("row %d state mismatch: got %v, want %v", i, v, expect[i])
		}
	}
}

func TestNuverseMasterRestorerStructuredData(t *testing.T) {
	t.Parallel()

	structPath := writeStructureFile(t, `{"cards":["cardId","name"]}`)

	masterData := orderedmap.New()
	masterData.SetEscapeHTML(false)
	masterData.Set("cards", []interface{}{
		[]interface{}{1, "first"},
		[]interface{}{2, "second"},
	})

	restored, err := NuverseMasterRestorer(masterData, structPath)
	if err != nil {
		t.Fatalf("NuverseMasterRestorer returned error: %v", err)
	}

	cardsRaw, ok := restored.Get("cards")
	if !ok {
		t.Fatalf("cards key should exist in restored output")
	}
	cards, ok := cardsRaw.([]*orderedmap.OrderedMap)
	if !ok {
		t.Fatalf("cards should be []*orderedmap.OrderedMap, got %T", cardsRaw)
	}
	if len(cards) != 2 {
		t.Fatalf("cards length mismatch: got %d, want 2", len(cards))
	}

	id1, _ := cards[0].Get("cardId")
	name1, _ := cards[0].Get("name")
	if toInt64(id1) != 1 || name1 != "first" {
		t.Fatalf("first card mismatch: id=%v name=%v", id1, name1)
	}
}

func TestNuverseMasterRestorerCompactEventCards(t *testing.T) {
	t.Parallel()

	structPath := writeStructureFile(t, `{}`)

	compactEventCards := orderedmap.New()
	compactEventCards.SetEscapeHTML(false)
	compactEventCards.Set("cardId", []interface{}{10, 20})
	compactEventCards.Set("name", []interface{}{"A", "B"})

	masterData := orderedmap.New()
	masterData.SetEscapeHTML(false)
	masterData.Set("compactEventCards", compactEventCards)

	restored, err := NuverseMasterRestorer(masterData, structPath)
	if err != nil {
		t.Fatalf("NuverseMasterRestorer returned error: %v", err)
	}

	if _, ok := restored.Get("compactEventCards"); !ok {
		t.Fatalf("compactEventCards should remain in restored output")
	}

	eventCardsRaw, ok := restored.Get(eventCardsKey)
	if !ok {
		t.Fatalf("eventCards should be generated from compactEventCards")
	}
	eventCards, ok := eventCardsRaw.([]*orderedmap.OrderedMap)
	if !ok {
		t.Fatalf("eventCards should be []*orderedmap.OrderedMap, got %T", eventCardsRaw)
	}
	if len(eventCards) != 2 {
		t.Fatalf("eventCards length mismatch: got %d, want 2", len(eventCards))
	}
}

func writeStructureFile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "structure.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write structure file: %v", err)
	}
	return path
}
