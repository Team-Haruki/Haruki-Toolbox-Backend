package story

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"
	"unicode"
)

// Episode is one playable scenario with the context the translator needs.
type Episode struct {
	ScenarioID  string
	Kind        string // event | unit | card | special | area
	SeriesKey   string // episodes of one series share it (an event, a card, ...)
	SeriesTitle string
	Outline     string
	No          int    // order inside the series
	Label       string // "第1話", "前編", ...
	Title       string
	Paths       []string // candidate asset paths of the scenario JSON
	ReleasedAt  int64    // ms; 0 when unknown
	CharIDs     []int    // area talks: the participating game characters
}

// Catalog is every JP episode plus which scenario ids CN also has.
type Catalog struct {
	Episodes []*Episode
	ByID     map[string]*Episode
	Series   map[string][]*Episode // ordered by No
	CNHas    map[string]bool
}

// JPOnly reports whether the episode exists on JP but not on CN.
func (c *Catalog) JPOnly(id string) bool {
	_, ok := c.ByID[id]
	return ok && !c.CNHas[id]
}

type masterEventStory struct {
	ID              int    `json:"id"`
	EventID         int    `json:"eventId"`
	Outline         string `json:"outline"`
	AssetbundleName string `json:"assetbundleName"`
	Episodes        []struct {
		EpisodeNo  int    `json:"episodeNo"`
		Title      string `json:"title"`
		ScenarioID string `json:"scenarioId"`
	} `json:"eventStoryEpisodes"`
}

type masterEvent struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	StartAt int64  `json:"startAt"`
}

type masterUnitStory struct {
	Unit     string `json:"unit"`
	Chapters []struct {
		ChapterNo       int    `json:"chapterNo"`
		Title           string `json:"title"`
		AssetbundleName string `json:"assetbundleName"`
		Episodes        []struct {
			EpisodeNo      int    `json:"episodeNo"`
			EpisodeNoLabel string `json:"episodeNoLabel"`
			Title          string `json:"title"`
			ScenarioID     string `json:"scenarioId"`
		} `json:"episodes"`
	} `json:"chapters"`
}

type masterCardEpisode struct {
	Seq        int    `json:"seq"`
	CardID     int    `json:"cardId"`
	Title      string `json:"title"`
	ScenarioID string `json:"scenarioId"`
	PartType   string `json:"cardEpisodePartType"`
}

type masterCard struct {
	ID              int    `json:"id"`
	CharacterID     int    `json:"characterId"`
	Prefix          string `json:"prefix"`
	AssetbundleName string `json:"assetbundleName"`
	ReleaseAt       int64  `json:"releaseAt"`
}

type masterSpecialStory struct {
	ID              int    `json:"id"`
	Title           string `json:"title"`
	AssetbundleName string `json:"assetbundleName"`
	StartAt         int64  `json:"startAt"`
	Episodes        []struct {
		EpisodeNo       int    `json:"episodeNo"`
		Title           string `json:"title"`
		AssetbundleName string `json:"assetbundleName"`
		ScenarioID      string `json:"scenarioId"`
	} `json:"episodes"`
}

type masterActionSet struct {
	ID                 int    `json:"id"`
	AreaID             int    `json:"areaId"`
	ScenarioID         string `json:"scenarioId"`
	CharacterIDs       []int  `json:"characterIds"`
	ArchivePublishedAt int64  `json:"archivePublishedAt"`
}

type masterArea struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type masterCharacter struct {
	ID        int    `json:"id"`
	FirstName string `json:"firstName"`
	GivenName string `json:"givenName"`
	Unit      string `json:"unit"`
}

// plausibleTime drops placeholder timestamps (archivePublishedAt carries 2009
// dates on old rows).
func plausibleTime(ms int64) int64 {
	if ms < 1_577_836_800_000 { // 2020-01-01
		return 0
	}
	return ms
}

// regionScenarios maps every story scenario id of a region to the time it
// opens there (ms; 0 for stories without a schedule: unit stories, area talks).
func regionScenarios(ctx context.Context, f *fetcher, region string) (map[string]int64, error) {
	ids := map[string]int64{}
	var events []masterEvent
	if err := f.master(ctx, region, "events", &events); err != nil {
		return nil, err
	}
	eventStart := map[int]int64{}
	for _, e := range events {
		eventStart[e.ID] = e.StartAt
	}
	var es []masterEventStory
	if err := f.master(ctx, region, "eventStories", &es); err != nil {
		return nil, err
	}
	for _, s := range es {
		for _, e := range s.Episodes {
			ids[e.ScenarioID] = eventStart[s.EventID]
		}
	}
	var us []masterUnitStory
	if err := f.master(ctx, region, "unitStories", &us); err != nil {
		return nil, err
	}
	for _, u := range us {
		for _, ch := range u.Chapters {
			for _, e := range ch.Episodes {
				ids[e.ScenarioID] = 0
			}
		}
	}
	var cards []masterCard
	if err := f.master(ctx, region, "cards", &cards); err != nil {
		return nil, err
	}
	cardRelease := map[int]int64{}
	for _, c := range cards {
		cardRelease[c.ID] = c.ReleaseAt
	}
	var ce []masterCardEpisode
	if err := f.master(ctx, region, "cardEpisodes", &ce); err != nil {
		return nil, err
	}
	for _, e := range ce {
		ids[e.ScenarioID] = cardRelease[e.CardID]
	}
	var ss []masterSpecialStory
	if err := f.master(ctx, region, "specialStories", &ss); err != nil {
		return nil, err
	}
	for _, s := range ss {
		for _, e := range s.Episodes {
			if e.ScenarioID != "" {
				ids[e.ScenarioID] = s.StartAt
			}
		}
	}
	var as []masterActionSet
	if err := f.master(ctx, region, "actionSets", &as); err != nil {
		return nil, err
	}
	for _, a := range as {
		if a.ScenarioID != "" {
			ids[a.ScenarioID] = 0
		}
	}
	return ids, nil
}

// kanaRatio is the share of kana among the letters of a scenario's dialogue.
// Regions ship upcoming stories ahead of time with the untranslated JP text
// (CN had events 188-200 that way, at about 0.75; real CN text is 0).
func kanaRatio(s *Scenario) float64 {
	letters, kana := 0, 0
	for _, td := range s.TalkData {
		for _, r := range td.Body {
			if !unicode.IsLetter(r) {
				continue
			}
			letters++
			if unicode.In(r, unicode.Hiragana, unicode.Katakana) {
				kana++
			}
		}
	}
	if letters == 0 {
		return 0
	}
	return float64(kana) / float64(letters)
}

// untranslatedKana is the kanaRatio above which a non-JP scenario is taken to
// be the JP placeholder text.
const untranslatedKana = 0.3

// buildCatalog lists every JP episode with its series context.
func buildCatalog(ctx context.Context, f *fetcher) (*Catalog, error) {
	c := &Catalog{ByID: map[string]*Episode{}, Series: map[string][]*Episode{}}
	add := func(e *Episode) {
		if e.ScenarioID == "" || c.ByID[e.ScenarioID] != nil {
			return
		}
		c.ByID[e.ScenarioID] = e
		c.Episodes = append(c.Episodes, e)
		c.Series[e.SeriesKey] = append(c.Series[e.SeriesKey], e)
	}

	var events []masterEvent
	if err := f.master(ctx, "jp", "events", &events); err != nil {
		return nil, err
	}
	eventByID := map[int]masterEvent{}
	for _, e := range events {
		eventByID[e.ID] = e
	}
	var es []masterEventStory
	if err := f.master(ctx, "jp", "eventStories", &es); err != nil {
		return nil, err
	}
	for _, s := range es {
		ev := eventByID[s.EventID]
		for _, e := range s.Episodes {
			add(&Episode{
				ScenarioID: e.ScenarioID, Kind: "event", SeriesKey: "event:" + strconv.Itoa(s.EventID),
				SeriesTitle: ev.Name, Outline: s.Outline, No: e.EpisodeNo, Label: fmt.Sprintf("第%d話", e.EpisodeNo),
				Title: e.Title, ReleasedAt: plausibleTime(ev.StartAt),
				Paths: []string{"ondemand/event_story/" + s.AssetbundleName + "/scenario/" + e.ScenarioID + ".json"},
			})
		}
	}

	var us []masterUnitStory
	if err := f.master(ctx, "jp", "unitStories", &us); err != nil {
		return nil, err
	}
	for _, u := range us {
		for _, ch := range u.Chapters {
			for _, e := range ch.Episodes {
				add(&Episode{
					ScenarioID: e.ScenarioID, Kind: "unit", SeriesKey: "unit:" + u.Unit + ":" + strconv.Itoa(ch.ChapterNo),
					SeriesTitle: ch.Title, No: e.EpisodeNo, Label: e.EpisodeNoLabel, Title: e.Title,
					Paths: []string{"startapp/scenario/unitstory/" + ch.AssetbundleName + "/" + e.ScenarioID + ".json"},
				})
			}
		}
	}

	var chars []masterCharacter
	if err := f.master(ctx, "jp", "gameCharacters", &chars); err != nil {
		return nil, err
	}
	charName := map[int]string{}
	for _, ch := range chars {
		charName[ch.ID] = ch.FirstName + ch.GivenName
	}
	var cards []masterCard
	if err := f.master(ctx, "jp", "cards", &cards); err != nil {
		return nil, err
	}
	cardByID := map[int]masterCard{}
	for _, cd := range cards {
		cardByID[cd.ID] = cd
	}
	var ce []masterCardEpisode
	if err := f.master(ctx, "jp", "cardEpisodes", &ce); err != nil {
		return nil, err
	}
	for _, e := range ce {
		cd, ok := cardByID[e.CardID]
		if !ok {
			continue
		}
		label := "前編"
		if e.PartType == "second_part" {
			label = "後編"
		}
		add(&Episode{
			ScenarioID: e.ScenarioID, Kind: "card", SeriesKey: "card:" + strconv.Itoa(cd.ID),
			SeriesTitle: "【" + cd.Prefix + "】" + charName[cd.CharacterID], No: e.Seq, Label: label, Title: e.Title,
			ReleasedAt: plausibleTime(cd.ReleaseAt), CharIDs: []int{cd.CharacterID},
			Paths: []string{"startapp/character/member/" + cd.AssetbundleName + "/" + e.ScenarioID + ".json"},
		})
	}

	var ss []masterSpecialStory
	if err := f.master(ctx, "jp", "specialStories", &ss); err != nil {
		return nil, err
	}
	for _, s := range ss {
		for _, e := range s.Episodes {
			add(&Episode{
				ScenarioID: e.ScenarioID, Kind: "special", SeriesKey: "special:" + strconv.Itoa(s.ID),
				SeriesTitle: s.Title, No: e.EpisodeNo, Label: strconv.Itoa(e.EpisodeNo), Title: e.Title,
				ReleasedAt: plausibleTime(s.StartAt),
				Paths: []string{
					"startapp/scenario/special/" + e.AssetbundleName + "/" + e.ScenarioID + ".json",
					"startapp/scenario/special/" + s.AssetbundleName + "/" + e.ScenarioID + ".json",
				},
			})
		}
	}

	var areas []masterArea
	if err := f.master(ctx, "jp", "areas", &areas); err != nil {
		return nil, err
	}
	areaName := map[int]string{}
	for _, a := range areas {
		areaName[a.ID] = a.Name
	}
	var as []masterActionSet
	if err := f.master(ctx, "jp", "actionSets", &as); err != nil {
		return nil, err
	}
	for _, a := range as {
		if a.ScenarioID == "" {
			continue
		}
		title := ""
		for i, id := range a.CharacterIDs {
			if i > 0 {
				title += "・"
			}
			title += charName[id]
		}
		add(&Episode{
			ScenarioID: a.ScenarioID, Kind: "area", SeriesKey: "area:" + a.ScenarioID,
			SeriesTitle: areaName[a.AreaID] + "のエリア会話", No: 1, Label: "", Title: title,
			ReleasedAt: plausibleTime(a.ArchivePublishedAt), CharIDs: a.CharacterIDs,
			Paths: []string{"startapp/scenario/actionset/group" + strconv.Itoa(a.ID/100) + "/" + a.ScenarioID + ".json"},
		})
	}

	for _, list := range c.Series {
		sort.SliceStable(list, func(i, j int) bool { return list[i].No < list[j].No })
	}
	cn, err := regionScenarios(ctx, f, "cn")
	if err != nil {
		return nil, err
	}
	// opened on CN → official text; listed but not opened yet → only when its
	// file already holds Chinese (checked once per series)
	now := time.Now().UnixMilli()
	c.CNHas = map[string]bool{}
	seriesOK := map[string]bool{}
	for id, at := range cn {
		ep := c.ByID[id]
		if at <= now || ep == nil {
			c.CNHas[id] = true
			continue
		}
		ok, seen := seriesOK[ep.SeriesKey]
		if !seen {
			ok = f.localized(ctx, "cn", c.Series[ep.SeriesKey][0])
			seriesOK[ep.SeriesKey] = ok
		}
		if ok {
			c.CNHas[id] = true
		}
	}
	return c, nil
}

// pendingOrder returns the JP-only episodes to translate: newest series
// first, and inside a series in episode order so earlier summaries exist.
func (c *Catalog) pendingOrder(done func(id string) bool) []*Episode {
	type series struct {
		key    string
		latest int64
		eps    []*Episode
	}
	byKey := map[string]*series{}
	var keys []string
	for _, e := range c.Episodes {
		if c.CNHas[e.ScenarioID] || done(e.ScenarioID) {
			continue
		}
		s := byKey[e.SeriesKey]
		if s == nil {
			s = &series{key: e.SeriesKey}
			byKey[e.SeriesKey] = s
			keys = append(keys, e.SeriesKey)
		}
		if e.ReleasedAt > s.latest {
			s.latest = e.ReleasedAt
		}
		s.eps = append(s.eps, e)
	}
	sort.SliceStable(keys, func(i, j int) bool {
		a, b := byKey[keys[i]], byKey[keys[j]]
		if a.latest != b.latest {
			return a.latest > b.latest
		}
		return a.key > b.key
	})
	var out []*Episode
	for _, k := range keys {
		eps := byKey[k].eps
		sort.SliceStable(eps, func(i, j int) bool { return eps[i].No < eps[j].No })
		out = append(out, eps...)
	}
	return out
}
