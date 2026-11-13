package costume3d

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
)

func TestCostumeShopBundleAllowsOnlyTheKernelFamilies(t *testing.T) {
	for _, name := range []string{
		"live_pv/model/characterv2/face/21/0001",
		"live_pv/model/character/head_optional/0019/a03",
		"live_pv/model/characterv2/color_variation/body/99/0691/ladies_ss/03",
		"live_pv/model/music_item/0007",
		"character/motion/costume_setting/ladies",
		"model3d/motion/result/34",
		"model3d/motion/common/default",
		"shader/live",
	} {
		if !costumeShopBundle(name) {
			t.Errorf("%s should be served", name)
		}
	}
	for _, name := range []string{
		"music/long/0610_01",
		"live_pv/mv_data/0610",
		"shader/unlit",
		"model3d/motion/common/other",
		"live_pv/model/characterv2/",
		"live_pv/model/characterv2/../../music/long/x",
		"/live_pv/model/characterv2/face/21/0001",
		"live_pv/model/characterv2/face/21/0001?x",
	} {
		if costumeShopBundle(name) {
			t.Errorf("%s should not be served", name)
		}
	}
}

// Character 21 with a preset borrowing character 1's hair; character 1 with a pattern of its head.
var fixtureTables = map[string]string{
	"gameCharacters":             `[{"id":1,"figure":"ladies"},{"id":21,"figure":"ladies"}]`,
	"gameCharacterUnits":         `[{"id":1,"gameCharacterId":1,"unit":"light_sound"},{"id":21,"gameCharacterId":21,"unit":"piapro"}]`,
	"gameCharacterUnit3dMotions": `[{"id":21,"gameCharacterUnitId":21,"motionType":"costume_setting","assetbundleName":"21_00"}]`,
	"character3ds": `[{"id":1,"characterId":1,"unit":"light_sound","bodyCostume3dId":1,"headCostume3dId":2,"hairCostume3dId":3},
		{"id":21,"characterId":21,"unit":"piapro","bodyCostume3dId":21,"headCostume3dId":22,"hairCostume3dId":3}]`,
	"costume3ds": `[{"id":1,"characterId":1,"partType":"body"},{"id":2,"characterId":1,"partType":"head"},{"id":3,"characterId":1,"partType":"hair"},
		{"id":21,"characterId":21,"partType":"body"},{"id":22,"characterId":21,"partType":"head"}]`,
	"costume3dModels":                    `[{"id":1,"costume3dId":1},{"id":3,"costume3dId":3},{"id":21,"costume3dId":21},{"id":22,"costume3dId":22,"unit":"piapro"}]`,
	"costume3dModelNotAvailablePatterns": `[{"unit":"piapro","headCostume3dId":22,"hairCostume3dId":99},{"unit":"light_sound","headCostume3dId":2,"hairCostume3dId":98}]`,
	"costume3dModelDefaultHairs":         `[]`,
}

func fakeRegistry(t *testing.T, hash *atomic.Value, fileRequests *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/master/jp/current":
			_ = json.NewEncoder(w).Encode(map[string]string{"contentHash": hash.Load().(string)})
		case strings.HasPrefix(r.URL.Path, "/v1/master/jp/files/"):
			fileRequests.Add(1)
			name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/master/jp/files/"), ".json")
			if r.URL.Query().Get("version") != hash.Load().(string) {
				t.Errorf("file %s asked with version %q", name, r.URL.Query().Get("version"))
			}
			_, _ = io.WriteString(w, fixtureTables[name])
		default:
			http.NotFound(w, r)
		}
	}))
}

func fakeMirror(bundleRequests *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/raw/current.json":
			_, _ = io.WriteString(w, `{"catalog":"catalogs/7.1.0.60.msgpack","assetVersion":"7.1.0.60"}`)
		case "/raw/catalogs/7.1.0.60.msgpack":
			_, _ = io.WriteString(w, "catalog-bytes")
		case "/raw/ui/archive-background/bg_sekai.png":
			_, _ = io.WriteString(w, "\x89PNG-bg")
		case "/raw/bundles/live_pv/model/characterv2/face/21/0001.bundle":
			bundleRequests.Add(1)
			_, _ = io.WriteString(w, "UnityFS\x00face")
		default:
			http.NotFound(w, r)
		}
	}))
}

func get(t *testing.T, app *fiber.App, path string, gzipOK bool) (*http.Response, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if gzipOK {
		req.Header.Set("Accept-Encoding", "gzip")
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ = io.ReadAll(zr)
	}
	return resp, body
}

func TestServesMasterRowsCatalogAndBundlesInTheMirrorLayout(t *testing.T) {
	var hash atomic.Value
	hash.Store("h1")
	var fileRequests, bundleRequests atomic.Int32
	registry := fakeRegistry(t, &hash, &fileRequests)
	defer registry.Close()
	mirror := fakeMirror(&bundleRequests)
	defer mirror.Close()

	s, err := New(config.Costume3DConfig{
		Enabled:         true,
		RegistryBaseURL: registry.URL,
		MirrorBaseURLs:  map[string]string{"jp": mirror.URL + "/raw"},
	}, harukiLogger.NewLogger("test", "ERROR", nil))
	if err != nil || s == nil {
		t.Fatalf("New: %v", err)
	}
	app := fiber.New()
	s.mount(app.Group(routePrefix))

	// Master rows of character 21: its costumes, the hair its preset borrows, their models and patterns.
	resp, body := get(t, app, routePrefix+"/jp/master/characters/21.json", true)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("master: %d %q", resp.StatusCode, resp.Header.Get("Content-Encoding"))
	}
	var rows struct {
		Version        string           `json:"version"`
		GameCharacters []map[string]any `json:"gameCharacters"`
		Character3ds   []map[string]any `json:"character3ds"`
		Costume3ds     []struct {
			ID int `json:"id"`
		} `json:"costume3ds"`
		Costume3dModels []struct {
			ID int `json:"id"`
		} `json:"costume3dModels"`
		NotAvailable []map[string]any `json:"costume3dModelNotAvailablePatterns"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("master body: %v", err)
	}
	ids := func(list []struct {
		ID int `json:"id"`
	}) []int {
		out := []int{}
		for _, r := range list {
			out = append(out, r.ID)
		}
		return out
	}
	if rows.Version != "h1" || len(rows.GameCharacters) != 2 || len(rows.Character3ds) != 1 || len(rows.NotAvailable) != 1 {
		t.Fatalf("master rows: %+v", rows)
	}
	if got := ids(rows.Costume3ds); len(got) != 3 || got[0] != 3 || got[1] != 21 || got[2] != 22 {
		t.Fatalf("costume3ds %v, want [3 21 22]", got)
	}
	if got := ids(rows.Costume3dModels); len(got) != 3 {
		t.Fatalf("costume3dModels %v, want 3 rows", got)
	}
	etag := resp.Header.Get("ETag")

	// Plain JSON for a client without gzip; the same content hash is not fetched again.
	if resp, body = get(t, app, routePrefix+"/jp/master/characters/1.json", false); resp.StatusCode != http.StatusOK || !json.Valid(body) {
		t.Fatalf("master without gzip: %d", resp.StatusCode)
	}
	if fileRequests.Load() != 8 {
		t.Fatalf("%d master file requests, want 8", fileRequests.Load())
	}
	req := httptest.NewRequest(http.MethodGet, routePrefix+"/jp/master/characters/21.json", nil)
	req.Header.Set("If-None-Match", etag)
	if resp, _ := app.Test(req); resp.StatusCode != http.StatusNotModified {
		t.Fatalf("If-None-Match: %d", resp.StatusCode)
	}
	if resp, _ = get(t, app, routePrefix+"/jp/master/characters/99.json", true); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown character: %d", resp.StatusCode)
	}

	// The mirror's current.json and catalog.
	if resp, body = get(t, app, routePrefix+"/jp/current.json", true); resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "7.1.0.60.msgpack") ||
		resp.Header.Get("Cache-Control") != cacheRevalidate {
		t.Fatalf("current.json: %d %s", resp.StatusCode, body)
	}
	if resp, body = get(t, app, routePrefix+"/jp/catalogs/7.1.0.60.msgpack", true); resp.StatusCode != http.StatusOK || string(body) != "catalog-bytes" {
		t.Fatalf("catalog: %d %q", resp.StatusCode, body)
	}
	if resp, _ = get(t, app, routePrefix+"/jp/catalogs/..%2Fsecret", true); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("bad catalog name: %d", resp.StatusCode)
	}

	// A CostumeShop bundle streams through; others and other regions are not served.
	resp, body = get(t, app, routePrefix+"/jp/bundles/live_pv/model/characterv2/face/21/0001.bundle?h=abc", false)
	if resp.StatusCode != http.StatusOK || string(body) != "UnityFS\x00face" || resp.Header.Get("Cache-Control") != cacheLong {
		t.Fatalf("bundle: %d %q %q", resp.StatusCode, body, resp.Header.Get("Cache-Control"))
	}
	if resp, _ = get(t, app, routePrefix+"/jp/bundles/live_pv/model/characterv2/face/21/0001.bundle", false); resp.Header.Get("Cache-Control") != cacheRevalidate {
		t.Fatalf("bundle without hash: %q", resp.Header.Get("Cache-Control"))
	}
	for _, path := range []string{
		"/jp/bundles/music/long/0610_01.bundle",
		"/jp/bundles/live_pv/model/characterv2/face/21/missing.bundle",
		"/tw/bundles/live_pv/model/characterv2/face/21/0001.bundle",
		"/tw/current.json",
	} {
		if resp, _ = get(t, app, routePrefix+path, false); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", path, resp.StatusCode)
		}
	}
	// The archive background textures, and nothing else under ui/.
	if resp, body = get(t, app, routePrefix+"/jp/ui/archive-background/bg_sekai.png", false); resp.StatusCode != http.StatusOK ||
		string(body) != "\x89PNG-bg" || resp.Header.Get("Content-Type") != "image/png" || resp.Header.Get("Cache-Control") != cacheDay {
		t.Fatalf("archive background: %d %q %q", resp.StatusCode, body, resp.Header.Get("Content-Type"))
	}
	for _, path := range []string{"/jp/ui/archive-background/other.png", "/jp/ui/archive-background/..%2Fcurrent.json", "/tw/ui/archive-background/bg_sekai.png"} {
		if resp, _ = get(t, app, routePrefix+path, false); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", path, resp.StatusCode)
		}
	}
	if bundleRequests.Load() != 2 {
		t.Fatalf("%d bundle requests reached the mirror, want 2", bundleRequests.Load())
	}
}

func TestDisabledServiceRegistersNothing(t *testing.T) {
	s, err := New(config.Costume3DConfig{}, nil)
	if err != nil || s != nil {
		t.Fatalf("disabled: %v %v", s, err)
	}
	if _, err := New(config.Costume3DConfig{Enabled: true}, nil); err == nil {
		t.Fatal("enabled without a registry should fail")
	}
}

func TestServesANuverseRegionsCatalogName(t *testing.T) {
	tw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/raw/current.json":
			_, _ = io.WriteString(w, `{"catalog":"catalogs/6.4.0-r22.msgpack","assetVersion":"6.4.0-r22"}`)
		case "/raw/catalogs/6.4.0-r22.msgpack":
			_, _ = io.WriteString(w, "tw-catalog")
		default:
			http.NotFound(w, r)
		}
	}))
	defer tw.Close()
	s, err := New(config.Costume3DConfig{
		Enabled:         true,
		RegistryBaseURL: "http://registry.invalid",
		MirrorBaseURLs:  map[string]string{"tw": tw.URL + "/raw"},
	}, harukiLogger.NewLogger("test", "ERROR", nil))
	if err != nil || s == nil {
		t.Fatalf("New: %v", err)
	}
	app := fiber.New()
	s.mount(app.Group(routePrefix))
	if resp, body := get(t, app, routePrefix+"/tw/catalogs/6.4.0-r22.msgpack", true); resp.StatusCode != http.StatusOK || string(body) != "tw-catalog" {
		t.Fatalf("nuverse catalog: %d %q", resp.StatusCode, body)
	}
	for _, name := range []string{"6.4.0-r.msgpack", "6.4.0-x22.msgpack", "r22.msgpack"} {
		if catalogFilePattern.MatchString(name) {
			t.Errorf("%s should not be a catalog name", name)
		}
	}
}
