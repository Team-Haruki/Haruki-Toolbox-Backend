package story

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Glossary holds official CN names for JP terms. Characters and units are
// always given to the model; areas, song titles and speaker labels only when
// they occur in the episode, which keeps the prompt short.
type Glossary struct {
	Characters []string          // "白石杏（杏）→ 白石杏（杏）"
	Units      []string          // "Vivid BAD SQUAD → Vivid BAD SQUAD"
	Terms      map[string]string // area names, song titles
	Speakers   map[string]string // speaker label → official CN label
}

type glossaryCache struct {
	mu      sync.Mutex
	value   *Glossary
	builtAt time.Time
}

const glossaryTTL = 24 * time.Hour

// harvestEvents is how many of the newest events with official CN text are
// read to learn speaker labels.
const harvestEvents = 30

func (g *glossaryCache) get(ctx context.Context, f *fetcher) (*Glossary, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.value != nil && time.Since(g.builtAt) < glossaryTTL {
		return g.value, nil
	}
	v, err := buildGlossary(ctx, f)
	if err != nil {
		if g.value != nil {
			return g.value, nil // keep serving the last good one
		}
		return nil, err
	}
	g.value, g.builtAt = v, time.Now()
	return v, nil
}

type masterUnitProfile struct {
	Unit     string `json:"unit"`
	UnitName string `json:"unitName"`
}

type masterMusic struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
}

func buildGlossary(ctx context.Context, f *fetcher) (*Glossary, error) {
	g := &Glossary{Terms: map[string]string{}, Speakers: map[string]string{}}

	var jc, cc []masterCharacter
	if err := f.master(ctx, "jp", "gameCharacters", &jc); err != nil {
		return nil, err
	}
	if err := f.master(ctx, "cn", "gameCharacters", &cc); err != nil {
		return nil, err
	}
	cn := map[int]masterCharacter{}
	for _, c := range cc {
		cn[c.ID] = c
	}
	for _, j := range jc {
		c, ok := cn[j.ID]
		if !ok {
			continue
		}
		g.Characters = append(g.Characters, j.FirstName+j.GivenName+"（"+j.GivenName+"）→ "+c.FirstName+c.GivenName+"（"+c.GivenName+"）")
		// speaker labels are given names; the official text writes virtual
		// singers in full ("ミク" → "初音未来"). Harvested labels override.
		if j.GivenName != "" && c.GivenName != "" {
			label := c.GivenName
			if j.Unit == "piapro" {
				label = c.FirstName + c.GivenName
			}
			g.Speakers[j.GivenName] = label
		}
	}

	var ju, cu []masterUnitProfile
	if err := f.master(ctx, "jp", "unitProfiles", &ju); err != nil {
		return nil, err
	}
	if err := f.master(ctx, "cn", "unitProfiles", &cu); err != nil {
		return nil, err
	}
	cuByUnit := map[string]string{}
	for _, u := range cu {
		cuByUnit[u.Unit] = u.UnitName
	}
	for _, u := range ju {
		if name, ok := cuByUnit[u.Unit]; ok {
			g.Units = append(g.Units, u.UnitName+" → "+name)
		}
	}

	var ja, ca []masterArea
	if err := f.master(ctx, "jp", "areas", &ja); err == nil {
		if err := f.master(ctx, "cn", "areas", &ca); err == nil {
			addPairs(g.Terms, ja, ca, func(a masterArea) (int, string) { return a.ID, a.Name })
		}
	}
	var jm, cm []masterMusic
	if err := f.master(ctx, "jp", "musics", &jm); err == nil {
		if err := f.master(ctx, "cn", "musics", &cm); err == nil {
			addPairs(g.Terms, jm, cm, func(m masterMusic) (int, string) { return m.ID, m.Title })
		}
	}

	harvestSpeakers(ctx, f, g.Speakers)
	return g, nil
}

func addPairs[T any](dst map[string]string, jp, cn []T, key func(T) (int, string)) {
	byID := map[int]string{}
	for _, c := range cn {
		id, name := key(c)
		byID[id] = name
	}
	for _, j := range jp {
		id, name := key(j)
		if c, ok := byID[id]; ok && utf8.RuneCountInString(name) >= 2 && c != "" {
			dst[name] = c
		}
	}
}

// harvestSpeakers learns speaker labels from the newest events that CN has,
// reading JP and CN scenarios side by side (they align line by line).
func harvestSpeakers(ctx context.Context, f *fetcher, dst map[string]string) {
	cn, err := regionScenarios(ctx, f, "cn")
	if err != nil {
		return
	}
	var es []masterEventStory
	if err := f.master(ctx, "jp", "eventStories", &es); err != nil {
		return
	}
	sort.Slice(es, func(i, j int) bool { return es[i].EventID > es[j].EventID })
	// newest events CN lists; some are only JP placeholders, so look at a few
	// more than needed and keep the first harvestEvents that hold real text
	var candidates []masterEventStory
	for _, s := range es {
		if len(s.Episodes) == 0 {
			continue
		}
		if _, ok := cn[s.Episodes[0].ScenarioID]; ok {
			candidates = append(candidates, s)
		}
		if len(candidates) >= harvestEvents+20 {
			break
		}
	}
	type harvest struct {
		pairs [][2]string
		used  bool
	}
	results := make([]harvest, len(candidates))
	parallel(len(candidates), fetchParallelism, func(i int) {
		s := candidates[i]
		for _, e := range s.Episodes {
			p := "ondemand/event_story/" + s.AssetbundleName + "/scenario/" + e.ScenarioID + ".json"
			js, err1 := f.scenario(ctx, "jp", p)
			cs, err2 := f.scenario(ctx, "cn", p)
			if err1 != nil || err2 != nil || js == nil || cs == nil || len(js.TalkData) != len(cs.TalkData) {
				continue
			}
			if kanaRatio(cs) >= untranslatedKana {
				break // the event's CN text is still the JP placeholder
			}
			results[i].used = true
			for k := range js.TalkData {
				a, b := js.TalkData[k].WindowDisplayName, cs.TalkData[k].WindowDisplayName
				if a != "" && b != "" {
					results[i].pairs = append(results[i].pairs, [2]string{a, b})
				}
			}
		}
	})
	counts := map[[2]string]int{}
	read := 0
	for _, r := range results {
		if read >= harvestEvents {
			break
		}
		if !r.used {
			continue
		}
		read++
		for _, pair := range r.pairs {
			counts[pair]++
		}
	}
	best := map[string]int{}
	for k, n := range counts {
		if n > best[k[0]] {
			best[k[0]] = n
			dst[k[0]] = k[1]
		}
	}
}

// hitsFor returns the term and speaker entries relevant to an episode.
func (g *Glossary) hitsFor(text string, speakers []string) (terms, names []string) {
	for jp, cn := range g.Terms {
		if strings.Contains(text, jp) {
			terms = append(terms, jp+" → "+cn)
		}
	}
	seen := map[string]bool{}
	for _, s := range speakers {
		if seen[s] {
			continue
		}
		seen[s] = true
		if cn, ok := g.Speakers[s]; ok {
			names = append(names, s+" → "+cn)
		}
	}
	sort.Strings(terms)
	sort.Strings(names)
	return terms, names
}
