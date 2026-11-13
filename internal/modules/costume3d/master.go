package costume3d

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/masterregistry"
)

// The master files one character's CostumeShop load reads (HarukiCostumeMasterData of the
// 3D kernel). The first three are small and go out whole with every character.
const (
	tableGameCharacters     = "gameCharacters"
	tableGameCharacterUnits = "gameCharacterUnits"
	tableUnit3dMotions      = "gameCharacterUnit3dMotions"
)

var (
	sharedTables    = []string{tableGameCharacters, tableGameCharacterUnits, tableUnit3dMotions}
	characterTables = []string{"character3ds", "costume3ds", "costume3dModels", "costume3dModelNotAvailablePatterns", "costume3dModelDefaultHairs"}
)

// characterRows is one character's master rows, as the game has them. Its fields are the
// 3D kernel's HarukiCostumeMasterData.
type characterRows struct {
	Version                            string            `json:"version"`
	GameCharacters                     []json.RawMessage `json:"gameCharacters"`
	GameCharacterUnits                 []json.RawMessage `json:"gameCharacterUnits"`
	GameCharacterUnit3dMotions         []json.RawMessage `json:"gameCharacterUnit3dMotions"`
	Character3ds                       []json.RawMessage `json:"character3ds"`
	Costume3ds                         []json.RawMessage `json:"costume3ds"`
	Costume3dModels                    []json.RawMessage `json:"costume3dModels"`
	Costume3dModelNotAvailablePatterns []json.RawMessage `json:"costume3dModelNotAvailablePatterns"`
	Costume3dModelDefaultHairs         []json.RawMessage `json:"costume3dModelDefaultHairs"`
}

// The row fields the slicing reads.
type (
	idRow struct {
		ID int `json:"id"`
	}
	costumeRow struct {
		ID          int `json:"id"`
		CharacterID int `json:"characterId"`
	}
	presetRow struct {
		CharacterID     int `json:"characterId"`
		BodyCostume3dID int `json:"bodyCostume3dId"`
		HeadCostume3dID int `json:"headCostume3dId"`
		HairCostume3dID int `json:"hairCostume3dId"`
	}
	modelRow struct {
		Costume3dID int `json:"costume3dId"`
	}
	patternRow struct {
		HeadCostume3dID int `json:"headCostume3dId"`
	}
)

// rows is a master table with each row's raw JSON and the fields the slicing reads.
type rows[T any] struct {
	raw    []json.RawMessage
	fields []T
}

// decodeRows reads the master file `name` of files.
func decodeRows[T any](files map[string][]byte, name string) (rows[T], error) {
	var r rows[T]
	if err := json.Unmarshal(files[name], &r.raw); err != nil {
		return r, fmt.Errorf("%s: %w", name, err)
	}
	r.fields = make([]T, len(r.raw))
	for i, row := range r.raw {
		if err := json.Unmarshal(row, &r.fields[i]); err != nil {
			return r, fmt.Errorf("%s row %d: %w", name, i, err)
		}
	}
	return r, nil
}

func (r rows[T]) filter(keep func(T) bool) []json.RawMessage {
	out := make([]json.RawMessage, 0)
	for i, f := range r.fields {
		if keep(f) {
			out = append(out, r.raw[i])
		}
	}
	return out
}

// masterTables is a region's master set, read once per registry content hash.
type masterTables struct {
	shared   map[string][]json.RawMessage
	presets  rows[presetRow]
	costumes rows[costumeRow]
	models   rows[modelRow]
	notAvail rows[patternRow]
	defaults rows[patternRow]
	chars    []idRow
}

func fetchMasterTables(ctx context.Context, registry *masterregistry.Client, region, version string) (*masterTables, error) {
	files := map[string][]byte{}
	for _, name := range append(append([]string{}, sharedTables...), characterTables...) {
		body, err := registry.File(ctx, region, name, version)
		if err != nil {
			return nil, err
		}
		files[name] = body
	}
	t := &masterTables{shared: map[string][]json.RawMessage{}}
	for _, name := range sharedTables {
		var raw []json.RawMessage
		if err := json.Unmarshal(files[name], &raw); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		t.shared[name] = raw
	}
	if err := json.Unmarshal(files[tableGameCharacters], &t.chars); err != nil {
		return nil, fmt.Errorf("%s: %w", tableGameCharacters, err)
	}
	var err error
	if t.presets, err = decodeRows[presetRow](files, "character3ds"); err != nil {
		return nil, err
	}
	if t.costumes, err = decodeRows[costumeRow](files, "costume3ds"); err != nil {
		return nil, err
	}
	if t.models, err = decodeRows[modelRow](files, "costume3dModels"); err != nil {
		return nil, err
	}
	if t.notAvail, err = decodeRows[patternRow](files, "costume3dModelNotAvailablePatterns"); err != nil {
		return nil, err
	}
	if t.defaults, err = decodeRows[patternRow](files, "costume3dModelDefaultHairs"); err != nil {
		return nil, err
	}
	return t, nil
}

// characterSlice is the kernel's characterMaster (src/original/costumeIndex.ts): the
// character's costumes, its character3ds presets and the costumes they borrow from other
// characters, those costumes' models, and the head / hair patterns of their heads.
func (t *masterTables) characterSlice(version string, characterID int) characterRows {
	ids := map[int]bool{}
	for _, c := range t.costumes.fields {
		if c.CharacterID == characterID {
			ids[c.ID] = true
		}
	}
	for _, p := range t.presets.fields {
		if p.CharacterID == characterID {
			ids[p.BodyCostume3dID], ids[p.HeadCostume3dID], ids[p.HairCostume3dID] = true, true, true
		}
	}
	return characterRows{
		Version:                            version,
		GameCharacters:                     t.shared[tableGameCharacters],
		GameCharacterUnits:                 t.shared[tableGameCharacterUnits],
		GameCharacterUnit3dMotions:         t.shared[tableUnit3dMotions],
		Character3ds:                       t.presets.filter(func(p presetRow) bool { return p.CharacterID == characterID }),
		Costume3ds:                         t.costumes.filter(func(c costumeRow) bool { return ids[c.ID] }),
		Costume3dModels:                    t.models.filter(func(m modelRow) bool { return ids[m.Costume3dID] }),
		Costume3dModelNotAvailablePatterns: t.notAvail.filter(func(p patternRow) bool { return ids[p.HeadCostume3dID] }),
		Costume3dModelDefaultHairs:         t.defaults.filter(func(p patternRow) bool { return ids[p.HeadCostume3dID] }),
	}
}

// gzipJSON is the gzip-encoded JSON of a value.
func gzipJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err := json.NewEncoder(zw).Encode(v); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// sliceAll gzips every character's slice; the full tables can then be dropped.
func (t *masterTables) sliceAll(version string) (map[int][]byte, error) {
	out := make(map[int][]byte, len(t.chars))
	for _, c := range t.chars {
		body, err := gzipJSON(t.characterSlice(version, c.ID))
		if err != nil {
			return nil, err
		}
		out[c.ID] = body
	}
	return out, nil
}
