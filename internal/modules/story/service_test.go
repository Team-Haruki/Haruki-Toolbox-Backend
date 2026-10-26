package story

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/storytranslation"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"
)

var storyDBSeq atomic.Int64

func newStoryDB(t *testing.T) *postgresql.Client {
	t.Helper()
	driver, err := entsql.Open("sqlite3", fmt.Sprintf("file:story-%d-%d?mode=memory&cache=shared&_fk=1", time.Now().UnixNano(), storyDBSeq.Add(1)))
	if err != nil {
		t.Fatal(err)
	}
	// SQLite permits one writer. Serialize database access while the test's
	// translation workers still run concurrently, avoiding SQLITE_LOCKED.
	driver.DB().SetMaxOpenConns(1)
	db := enttest.NewClient(t, enttest.WithOptions(postgresql.Driver(driver)))
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func testLogger() *harukiLogger.Logger {
	return harukiLogger.NewLogger("StoryTest", "DEBUG", io.Discard)
}

func talk(lines ...[2]string) map[string]any {
	var td []map[string]any
	for _, l := range lines {
		td = append(td, map[string]any{"WindowDisplayName": l[0], "Body": l[1]})
	}
	return map[string]any{"TalkData": td}
}

var (
	past   = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	future = time.Now().Add(365 * 24 * time.Hour).UnixMilli()
)

// sekaiFixture is a small JP + CN game: event 1 is out on CN, event 2 is
// listed on CN but its files hold the JP placeholder text, event 3 is not
// open on CN yet but already translated, the special story is JP-only.
func sekaiFixture() (masters map[string]map[string]any, assets map[string]any) {
	ev := func(id, eventID int, bundle string, eps ...string) map[string]any {
		var list []map[string]any
		for i, sid := range eps {
			list = append(list, map[string]any{"episodeNo": i + 1, "title": fmt.Sprintf("Ep%d", i+1), "scenarioId": sid})
		}
		return map[string]any{"id": id, "eventId": eventID, "outline": "outline", "assetbundleName": bundle, "eventStoryEpisodes": list}
	}
	common := func(chars []map[string]any, events []map[string]any, areaName string, special []map[string]any) map[string]any {
		return map[string]any{
			"gameCharacters": chars,
			"events":         events,
			"eventStories":   []map[string]any{ev(1, 1, "ev1", "event_01_01"), ev(2, 2, "ev2", "event_02_01", "event_02_02"), ev(3, 3, "ev3", "event_03_01")},
			"unitStories": []map[string]any{{"unit": "street", "chapters": []map[string]any{{
				"chapterNo": 1, "title": "Chapter", "assetbundleName": "street-story-chapter",
				"episodes": []map[string]any{{"episodeNo": 1, "episodeNoLabel": "1話", "title": "Start", "scenarioId": "street_01_01"}},
			}}}},
			"cards":          []map[string]any{{"id": 100, "characterId": 9, "prefix": "Hi", "assetbundleName": "res009_no100", "releaseAt": past}},
			"cardEpisodes":   []map[string]any{{"seq": 1, "cardId": 100, "title": "Card", "scenarioId": "card_100_01", "cardEpisodePartType": "first_part"}},
			"specialStories": special,
			"areas":          []map[string]any{{"id": 1, "name": areaName}},
			"actionSets":     []map[string]any{{"id": 101, "areaId": 1, "scenarioId": "areatalk_101", "characterIds": []int{9, 10}, "archivePublishedAt": past}},
			"unitProfiles":   []map[string]any{{"unit": "street", "unitName": "Vivid BAD SQUAD"}},
			"musics":         []map[string]any{{"id": 1, "title": "Ready Steady"}},
		}
	}
	masters = map[string]map[string]any{
		"jp": common(
			[]map[string]any{
				{"id": 9, "firstName": "小豆沢", "givenName": "こはね", "unit": "street"},
				{"id": 10, "firstName": "白石", "givenName": "杏", "unit": "street"},
				{"id": 21, "firstName": "初音", "givenName": "ミク", "unit": "piapro"},
			},
			[]map[string]any{{"id": 1, "name": "One", "startAt": past}, {"id": 2, "name": "Two", "startAt": past + 2}, {"id": 3, "name": "Three", "startAt": past + 1}},
			"シブヤ",
			[]map[string]any{{"id": 5, "title": "SPECIAL", "assetbundleName": "sp5", "startAt": past - 1, "episodes": []map[string]any{
				{"episodeNo": 1, "title": "S1", "assetbundleName": "sp5_01", "scenarioId": "sp_05_01"},
			}}},
		),
		"cn": common(
			[]map[string]any{
				{"id": 9, "firstName": "小豆沢", "givenName": "心羽", "unit": "street"},
				{"id": 10, "firstName": "白石", "givenName": "杏", "unit": "street"},
				{"id": 21, "firstName": "初音", "givenName": "未来", "unit": "piapro"},
			},
			[]map[string]any{{"id": 1, "name": "一", "startAt": past}, {"id": 2, "name": "二", "startAt": future}, {"id": 3, "name": "三", "startAt": future}},
			"涩谷",
			[]map[string]any{},
		),
	}
	jpEv2 := talk([2]string{"こはね", "セカイに行こう"}, [2]string{"ミク", "ようこそ"})
	assets = map[string]any{
		"jp-assets/ondemand/event_story/ev1/scenario/event_01_01.json": talk([2]string{"こはね", "セカイに行こう"}, [2]string{"杏・こはね", "うん！"}),
		"cn-assets/ondemand/event_story/ev1/scenario/event_01_01.json": talk([2]string{"心羽", "去「世界」吧"}, [2]string{"杏&心羽", "嗯！"}),
		"jp-assets/ondemand/event_story/ev2/scenario/event_02_01.json": jpEv2,
		"cn-assets/ondemand/event_story/ev2/scenario/event_02_01.json": jpEv2, // placeholder
		"jp-assets/ondemand/event_story/ev2/scenario/event_02_02.json": talk([2]string{"杏", "行こう！"}),
		"jp-assets/ondemand/event_story/ev3/scenario/event_03_01.json": talk([2]string{"杏", "まだ"}),
		"cn-assets/ondemand/event_story/ev3/scenario/event_03_01.json": talk([2]string{"杏", "还没"}),
		"jp-assets/startapp/scenario/special/sp5_01/sp_05_01.json":     talk([2]string{"ミク", "スペシャル"}),
	}
	return masters, assets
}

// fakeSekai serves the registry (current + files) and the asset tree.
func fakeSekai(t *testing.T) *httptest.Server {
	masters, assets := sekaiFixture()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if rest, ok := strings.CutPrefix(p, "/v1/master/"); ok {
			region, tail, _ := strings.Cut(rest, "/")
			if tail == "current" {
				_, _ = fmt.Fprint(w, `{"contentHash":"h-`+region+`"}`)
				return
			}
			name := strings.TrimSuffix(strings.TrimPrefix(tail, "files/"), ".json")
			if v, ok := masters[region][name]; ok {
				_ = json.NewEncoder(w).Encode(v)
				return
			}
			http.NotFound(w, r)
			return
		}
		if v, ok := assets[strings.TrimPrefix(p, "/")]; ok {
			_ = json.NewEncoder(w).Encode(v)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// recordingLLM "translates" every line to "zh:<text>" and records the user
// prompts; a prompt containing failOn gets a 400.
func recordingLLM(t *testing.T, failOn string) (*httptest.Server, func() []string) {
	var mu sync.Mutex
	var prompts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []chatMessage `json:"messages"`
		}
		_ = json.Unmarshal(body, &req)
		user := req.Messages[1].Content
		mu.Lock()
		prompts = append(prompts, user)
		mu.Unlock()
		if failOn != "" && strings.Contains(user, failOn) {
			http.Error(w, `{"error":"bad"}`, http.StatusBadRequest)
			return
		}
		var lines []promptLine
		_ = json.Unmarshal([]byte(user[strings.LastIndex(user, "【待翻译台词】\n")+len("【待翻译台词】\n"):]), &lines)
		out := modelOutput{Names: map[string]string{"杏": "杏"}, Summary: "概括。"}
		for _, l := range lines {
			out.Lines = append(out.Lines, struct {
				I    int    `json:"i"`
				Text string `json:"text"`
			}{l.I, "zh:" + l.Text})
		}
		raw, _ := json.Marshal(out)
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": string(raw)}}}})
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n", chunk)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), prompts...)
	}
}

func testConfig(sekai, llm string) config.StoryConfig {
	return config.StoryConfig{
		Enabled: true, RegistryBaseURL: sekai, AssetBaseURL: sekai,
		Translation: config.StoryTranslateConfig{
			Enabled: llm != "", LLMBaseURL: llm, LLMAPIKey: "k", LLMModel: "m", LLMTimeoutSeconds: 5,
		},
	}
}

func TestNewValidatesConfig(t *testing.T) {
	if s, err := New(config.StoryConfig{}, nil, testLogger()); s != nil || err != nil {
		t.Fatalf("disabled: %v %v", s, err)
	}
	if _, err := New(config.StoryConfig{Enabled: true}, nil, testLogger()); err == nil {
		t.Fatal("missing URLs accepted")
	}
	cfg := testConfig("http://x", "")
	cfg.Translation.Enabled = true
	if _, err := New(cfg, nil, testLogger()); err == nil {
		t.Fatal("translation without an LLM accepted")
	}
	s, err := New(testConfig("http://x", ""), nil, testLogger())
	if err != nil || s.tr != nil {
		t.Fatalf("translation should be off: %+v %v", s, err)
	}
}

func TestBuildCatalogCountsPlaceholdersAsMissing(t *testing.T) {
	sekai := fakeSekai(t)
	f := newFetcher(sekai.URL, sekai.URL, "test", sekai.Client())
	c, err := buildCatalog(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"event_01_01":  false, // out on CN
		"event_02_01":  true,  // CN file is the JP placeholder
		"event_02_02":  true,
		"event_03_01":  false, // not open on CN yet, but translated
		"sp_05_01":     true,  // CN has no such story
		"street_01_01": false, "card_100_01": false, "areatalk_101": false,
	}
	for id, jpOnly := range want {
		if c.JPOnly(id) != jpOnly {
			t.Errorf("JPOnly(%s) = %v, want %v", id, c.JPOnly(id), jpOnly)
		}
	}
	if ep := c.ByID["sp_05_01"]; ep.Kind != "special" || len(ep.Paths) != 2 {
		t.Errorf("special episode = %+v", ep)
	}
	if ep := c.ByID["areatalk_101"]; ep.Title != "小豆沢こはね・白石杏" || ep.Paths[0] != "startapp/scenario/actionset/group1/areatalk_101.json" {
		t.Errorf("area episode = %+v", ep)
	}
	if c.ByID["card_100_01"].SeriesTitle != "【Hi】小豆沢こはね" {
		t.Errorf("card series = %q", c.ByID["card_100_01"].SeriesTitle)
	}
}

func TestGlossaryNamesAndHarvestedSpeakers(t *testing.T) {
	sekai := fakeSekai(t)
	f := newFetcher(sekai.URL, sekai.URL, "test", sekai.Client())
	var gc glossaryCache
	g, err := gc.get(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := gc.get(context.Background(), f); again != g {
		t.Fatal("glossary not cached")
	}
	if g.Speakers["こはね"] != "心羽" || g.Speakers["ミク"] != "初音未来" || g.Speakers["杏・こはね"] != "杏&心羽" {
		t.Fatalf("speakers = %v", g.Speakers)
	}
	if g.Terms["シブヤ"] != "涩谷" || len(g.Units) != 1 || len(g.Characters) != 3 {
		t.Fatalf("terms = %v units = %v", g.Terms, g.Units)
	}
	terms, names := g.hitsFor("シブヤで会おう", []string{"こはね", "こはね", "モブ"})
	if strings.Join(terms, ",") != "シブヤ → 涩谷" || strings.Join(names, ",") != "こはね → 心羽" {
		t.Fatalf("hits = %v %v", terms, names)
	}
}

func TestServiceTranslatesJPOnlyEpisodesAndServesThem(t *testing.T) {
	sekai := fakeSekai(t)
	llm, prompts := recordingLLM(t, "SPECIAL")
	db := newStoryDB(t)
	s, err := New(testConfig(sekai.URL, llm.URL), db, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	s.tr.llm.backoff = time.Millisecond
	ctx := context.Background()

	s.translateRun(ctx)
	rows, err := db.StoryTranslation.Query().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	for _, r := range rows {
		status[r.ScenarioID] = string(r.Status)
	}
	if fmt.Sprint(status) != "map[event_02_01:done event_02_02:done sp_05_01:failed]" {
		t.Fatalf("rows = %v", status)
	}
	// newest series first, in episode order; episode 2 sees episode 1's summary
	p := prompts()
	if len(p) != 3 || !strings.Contains(p[1], "第1話「Ep1」：概括。") || !strings.Contains(p[0], "初音未来") {
		t.Fatalf("prompts = %d\n%s", len(p), strings.Join(p, "\n----\n"))
	}
	// failed episodes wait; done ones are not redone
	s.translateRun(ctx)
	if len(prompts()) != 3 {
		t.Fatalf("second run called the model again: %d", len(prompts()))
	}

	app := fiber.New()
	RegisterRoutes(&harukiAPIHelper.HarukiToolboxRouterHelpers{Router: app}, s)
	app.Post("/retranslate/:scenario_id", s.handleRetranslate)
	get := func(path string) (*http.Response, map[string]any) {
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return resp, body
	}

	resp, body := get("/api/misc/story/translations/event_02_01")
	if resp.StatusCode != http.StatusOK || body["names"].(map[string]any)["ミク"] != "初音未来" ||
		body["lines"].([]any)[0] != "zh:セカイに行こう" {
		t.Fatalf("translation = %d %v", resp.StatusCode, body)
	}
	if resp, body = get("/api/misc/story/translations/sp_05_01"); resp.StatusCode != http.StatusNotFound || body["status"] != "pending" {
		t.Fatalf("failed JP-only = %d %v", resp.StatusCode, body)
	}
	if resp, body = get("/api/misc/story/translations/event_01_01"); resp.StatusCode != http.StatusNotFound || body["status"] != "unavailable" {
		t.Fatalf("CN episode = %d %v", resp.StatusCode, body)
	}
	if resp, _ = get("/api/misc/story/translations/event_02_01?lang=en"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("other language = %d", resp.StatusCode)
	}
	if resp, body = get("/api/misc/story/translations"); resp.StatusCode != http.StatusOK || len(body["items"].([]any)) != 2 {
		t.Fatalf("list = %d %v", resp.StatusCode, body)
	}

	// re-translate: the row goes away and the next run redoes it
	req := httptest.NewRequest(http.MethodPost, "/retranslate/event_02_02", nil)
	if resp, err := app.Test(req); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("retranslate = %v %v", resp, err)
	}
	if n, _ := db.StoryTranslation.Query().Where(storytranslation.ScenarioID("event_02_02")).Count(ctx); n != 0 {
		t.Fatalf("row kept after retranslate")
	}
	s.translateRun(ctx)
	if len(prompts()) != 4 {
		t.Fatalf("retranslate did not run once more: %d", len(prompts()))
	}
}

func TestStartRunsLoopsUntilCancelled(t *testing.T) {
	sekai := fakeSekai(t)
	s, err := New(testConfig(sekai.URL, ""), newStoryDB(t), testLogger())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	wait := s.Start(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for s.currentCatalog() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	s.Kick()
	s.Kick() // a second kick while one is queued is dropped
	cancel()
	wait()
	if s.currentCatalog() == nil {
		t.Fatal("the loop did not run")
	}
}

func TestParallelSeriesKeepEpisodeOrder(t *testing.T) {
	sekai := fakeSekai(t)
	llm, prompts := recordingLLM(t, "")
	db := newStoryDB(t)
	cfg := testConfig(sekai.URL, llm.URL)
	cfg.Translation.Concurrency = 3
	s, err := New(cfg, db, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	s.translateRun(context.Background())
	if n, _ := db.StoryTranslation.Query().Where(storytranslation.StatusEQ(storytranslation.StatusDone)).Count(context.Background()); n != 3 {
		t.Fatalf("done rows = %d, want 3", n)
	}
	for _, p := range prompts() {
		if strings.Contains(p, "「Ep2」（本话）") && !strings.Contains(p, "第1話「Ep1」：概括。") {
			t.Fatalf("episode 2 ran without episode 1's summary:\n%s", p)
		}
	}
}
