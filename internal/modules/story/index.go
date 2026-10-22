package story

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// AssetIndex tells the story player where things are in the JP asset tree.
// Paths are relative to "<asset root>/jp-assets/".
type AssetIndex struct {
	Version     string                `json:"version"`
	GeneratedAt time.Time             `json:"generatedAt"`
	Models      map[string]ModelEntry `json:"models"`
	// Movies maps a scenario movie name to its mp4.
	Movies map[string]string `json:"movies"`
	// Se maps a shared scenario SE name to its mp3 (they are split over
	// several pack folders); event-only SEs sit next to the event's scenarios.
	Se map[string]string `json:"se"`
}

// ModelEntry is one Live2D costume.
type ModelEntry struct {
	Dir    string `json:"dir"`
	Model3 string `json:"model3"`
	// Physics is the exported physics file name (the model3 references
	// "<stem>.physics3.json", the exporter writes "<stem>.physics3"); empty
	// when the costume ships none.
	Physics    string `json:"physics,omitempty"`
	MotionBase string `json:"motionBase,omitempty"`
	// Motions lists the costume's own clips (lowercase names, without
	// ".motion3.json"), found in "<dir>/motions/".
	Motions []string `json:"motions,omitempty"`
}

const (
	modelPrefix  = "startapp/live2d/model/"
	motionPrefix = "startapp/live2d/motion/"
	moviePrefix  = "ondemand/scenario/movie/"
	movie2Prefix = "ondemand/movie/"
	sePrefix     = "startapp/sound/scenario/se/"
)

// listingPrefixes are the subtrees the index needs.
var listingPrefixes = []string{modelPrefix, motionPrefix, moviePrefix, movie2Prefix, sePrefix}

// buildAssetIndex turns key listings (relative to the region prefix) into the
// index. Motion libraries are matched to costumes by the longest
// "<name>_motion_base" whose name prefixes the costume; a v2 costume without
// its own library falls back to the v1 one ("v2_clb01_21miku" →
// "clb01_21miku_motion_base"), and a "…_back" costume takes the matching
// "<name>_back" library.
func buildAssetIndex(keys []string, now time.Time) *AssetIndex {
	type modelDir struct {
		model3, physics []string
		motions         []string
	}
	dirs := map[string]*modelDir{}
	bases := map[string]string{} // library name prefix → dir
	movies := map[string]string{}
	se := map[string]string{}
	for _, k := range keys {
		switch {
		case strings.HasPrefix(k, modelPrefix):
			dir, file := path.Split(k)
			dir = strings.TrimSuffix(dir, "/")
			if path.Base(dir) == "motions" && strings.HasSuffix(file, ".motion3.json") {
				owner := path.Dir(dir)
				d := dirs[owner]
				if d == nil {
					d = &modelDir{}
					dirs[owner] = d
				}
				d.motions = append(d.motions, strings.ToLower(strings.TrimSuffix(file, ".motion3.json")))
				continue
			}
			if strings.HasSuffix(file, ".model3") || strings.HasSuffix(file, ".physics3") {
				d := dirs[dir]
				if d == nil {
					d = &modelDir{}
					dirs[dir] = d
				}
				if strings.HasSuffix(file, ".model3") {
					d.model3 = append(d.model3, file)
				} else {
					d.physics = append(d.physics, file)
				}
			}
		case strings.HasPrefix(k, motionPrefix):
			for p := path.Dir(k); p != "." && strings.HasPrefix(p+"/", motionPrefix); p = path.Dir(p) {
				if name := path.Base(p); strings.HasSuffix(name, "_motion_base") {
					bases[strings.TrimSuffix(name, "_motion_base")] = p
					break
				}
			}
		case strings.HasPrefix(k, sePrefix):
			if name, ok := strings.CutSuffix(path.Base(k), ".mp3"); ok {
				// packs sort "se" < "se_pack00001" < "se_pack00001_b"; first wins
				if old, dup := se[name]; !dup || k < old {
					se[name] = k
				}
			}
		case strings.HasPrefix(k, moviePrefix), strings.HasPrefix(k, movie2Prefix):
			if strings.HasSuffix(k, ".mp4") {
				name := path.Base(path.Dir(k))
				if _, ok := movies[name]; !ok || strings.HasPrefix(k, moviePrefix) {
					movies[name] = k
				}
			}
		}
	}
	prefixes := make([]string, 0, len(bases))
	for p := range bases {
		prefixes = append(prefixes, p)
	}
	sort.Slice(prefixes, func(i, j int) bool {
		if len(prefixes[i]) != len(prefixes[j]) {
			return len(prefixes[i]) > len(prefixes[j])
		}
		return prefixes[i] < prefixes[j]
	})
	matchPrefix := func(costume string) string {
		for _, c := range []string{costume, strings.TrimPrefix(costume, "v2_")} {
			for _, p := range prefixes {
				if strings.HasPrefix(c, p) {
					return p
				}
			}
		}
		return ""
	}
	matchBase := func(costume string) string {
		// back-view models ("v2_20mizuki_culture_back") use the character's
		// back library ("v2_20mizuki_back_motion_base")
		if front, ok := strings.CutSuffix(costume, "_back"); ok {
			if p := matchPrefix(front); p != "" {
				if dir, ok := bases[p+"_back"]; ok {
					return dir
				}
			}
		}
		if p := matchPrefix(costume); p != "" {
			return bases[p]
		}
		return ""
	}
	idx := &AssetIndex{GeneratedAt: now.UTC(), Models: map[string]ModelEntry{}, Movies: movies, Se: se}
	for dir, d := range dirs {
		if len(d.model3) == 0 {
			continue
		}
		// re-exports leave the previous version next to the current one
		// ("…_t04.model3" + "…_t05.model3"); the newest is the one in use
		model3 := slices.MaxFunc(d.model3, naturalCompare)
		physics := ""
		if want := strings.TrimSuffix(model3, ".model3") + ".physics3"; slices.Contains(d.physics, want) {
			physics = want
		}
		costume := path.Base(dir)
		sort.Strings(d.motions)
		idx.Models[costume] = ModelEntry{
			Dir: dir, Model3: model3, Physics: physics,
			MotionBase: matchBase(costume), Motions: d.motions,
		}
	}
	raw, _ := json.Marshal(struct {
		M map[string]ModelEntry
		V map[string]string
		S map[string]string
	}{idx.Models, idx.Movies, idx.Se})
	sum := sha256.Sum256(raw)
	idx.Version = hex.EncodeToString(sum[:8])
	return idx
}

// naturalCompare orders strings with digit runs compared by value
// ("_t9" < "_t10").
func naturalCompare(a, b string) int {
	for a != "" && b != "" {
		da, db := digitPrefix(a), digitPrefix(b)
		if da != "" && db != "" {
			na, nb := strings.TrimLeft(da, "0"), strings.TrimLeft(db, "0")
			if c := cmp.Compare(len(na), len(nb)); c != 0 {
				return c
			}
			if c := strings.Compare(na, nb); c != 0 {
				return c
			}
			a, b = a[len(da):], b[len(db):]
			continue
		}
		if a[0] != b[0] {
			return cmp.Compare(a[0], b[0])
		}
		a, b = a[1:], b[1:]
	}
	return cmp.Compare(len(a), len(b))
}

func digitPrefix(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}

// indexBuilder keeps the current index and rebuilds it from the bucket.
type indexBuilder struct {
	lister *s3Lister
	prefix string // region prefix in the bucket, e.g. "jp-assets/"

	mu      sync.RWMutex
	current *AssetIndex
	body    []byte // JSON of current
}

func (b *indexBuilder) refresh(ctx context.Context) error {
	var keys []string
	for _, p := range listingPrefixes {
		got, err := b.lister.listAll(ctx, b.prefix+p)
		if err != nil {
			return err
		}
		for _, k := range got {
			keys = append(keys, strings.TrimPrefix(k, b.prefix))
		}
	}
	idx := buildAssetIndex(keys, time.Now())
	body, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.current, b.body = idx, body
	b.mu.Unlock()
	return nil
}

func (b *indexBuilder) snapshot() (*AssetIndex, []byte) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.current, b.body
}
