package usersponsor

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/jsoncodec"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	"github.com/gofiber/fiber/v3"
)

func replaceFile(t *testing.T, filename string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filename+".tmp", data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filename+".tmp", filename); err != nil {
		t.Fatal(err)
	}
}
func writeLiveFrames(t *testing.T, dir string, frames []Frame) {
	t.Helper()
	data, err := jsoncodec.Marshal(frames)
	if err != nil {
		t.Fatal(err)
	}
	replaceFile(t, filepath.Join(dir, "catalog.json"), data)
}
func testPreview(t *testing.T, value uint8) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: value, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func liveCatalogFixture(t *testing.T) (string, Frame, *Catalog) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "previews"), 0700); err != nil {
		t.Fatal(err)
	}
	f := frameCatalog()[0]
	f.ID = "dynamic_one"
	f.Thumbnail = "panel.png"
	f.PanelPreview = "panel.png"
	f.ProfilePreview = "profile.png"
	replaceFile(t, filepath.Join(dir, "previews/panel.png"), testPreview(t, 10))
	replaceFile(t, filepath.Join(dir, "previews/profile.png"), testPreview(t, 20))
	writeLiveFrames(t, dir, []Frame{f})
	c, err := NewCatalog(dir, "https://api.example")
	if err != nil {
		t.Fatal(err)
	}
	return dir, f, c
}

func TestRuntimeCatalogAddModifyRemoveWithoutRestart(t *testing.T) {
	dir, f, c := liveCatalogFixture(t)
	e := newEnv(t)
	e.h.catalog = c
	e.db.User.UpdateOneID("alice").SetSponsorAccessOverride(user.SponsorAccessOverrideGranted).ExecX(t.Context())
	e.db.GameAccountBinding.Create().SetServer("cn").SetGameUserID("12345").SetUserID("alice").SetVerified(true).SaveX(t.Context())
	f.ID = "new_frame_after_startup"
	f.Name["zh-CN"] = "新头像框"
	writeLiveFrames(t, dir, []Frame{f})
	status, body := e.request(t, "PUT", "/api/user/alice/sponsor-frames/cn/12345", "alice", "session", map[string]string{"frameId": f.ID})
	if status != 200 || !bytes.Contains(body, []byte(f.ID)) || !bytes.Contains(body, []byte("https://api.example/api/misc/sponsor-frames/panel.png?v=")) {
		t.Fatalf("hot-added frame unavailable: %d %s", status, body)
	}
	f.Horizontal.Base = "static_images/custom_frames/changed/frame_base.png"
	writeLiveFrames(t, dir, []Frame{f})
	status, body = e.request(t, "GET", "/game-frame/cn/12345", "", "", nil)
	if status != 200 || !bytes.Contains(body, []byte(f.Horizontal.Base)) {
		t.Fatalf("private selection did not reload: %d %s", status, body)
	}
	replaceFile(t, filepath.Join(dir, "catalog.json"), []byte("{"))
	if got := c.snapshot(); len(got) != 1 || got[0].Horizontal.Base != f.Horizontal.Base {
		t.Fatal("invalid update replaced valid snapshot")
	}
	writeLiveFrames(t, dir, []Frame{})
	status, body = e.request(t, "GET", "/game-frame/cn/12345", "", "", nil)
	if status != 200 || string(body) != `{"frame":null}` {
		t.Fatalf("removed frame still selected: %d %s", status, body)
	}
	status, _ = e.request(t, "PUT", "/api/user/alice/sponsor-frames/cn/12345", "alice", "session", map[string]string{"frameId": f.ID})
	if status != 400 {
		t.Fatal("removed frame accepted")
	}
}

func TestRuntimePreviewsReloadAndConditionalRequest(t *testing.T) {
	dir, _, c := liveCatalogFixture(t)
	app := fiber.New()
	app.Get(previewRoute+":filename", c.preview)
	before := c.snapshot()[0].PanelPreview
	req := httptest.NewRequest("GET", previewRoute+"panel.png", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	etag := resp.Header.Get("ETag")
	if resp.StatusCode != 200 || !bytes.Equal(data, testPreview(t, 10)) || etag == "" {
		t.Fatal("invalid preview response")
	}
	req.Header.Set("If-None-Match", etag)
	resp, err = app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 304 {
		t.Fatal("unchanged preview did not revalidate")
	}
	replaceFile(t, filepath.Join(dir, "previews/panel.png"), testPreview(t, 99))
	if c.snapshot()[0].PanelPreview == before {
		t.Fatal("preview URL did not change after replacing image")
	}
	resp, err = app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Equal(data, testPreview(t, 99)) || resp.Header.Get("ETag") == etag {
		t.Fatal("stale preview after hot replacement")
	}
	resp, err = app.Test(httptest.NewRequest("GET", previewRoute+"catalog.json", nil))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatal("non-preview file exposed")
	}
}

func TestRuntimeCatalogRejectsUnsafeOrIncompleteUpdates(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Frame)
	}{
		{"reserved", func(f *Frame) { f.ID = "icon_user_avatar_frame_endfield_1" }},
		{"parts escape", func(f *Frame) { f.Horizontal.Base = "static_images/custom_frames/../../secret.png" }},
		{"URL parts", func(f *Frame) { f.Horizontal.Base = "https://example.com/frame.png" }},
		{"preview escape", func(f *Frame) { f.PanelPreview = "../secret.png" }},
		{"missing preview", func(f *Frame) { f.PanelPreview = "missing.png" }},
		{"empty name", func(f *Frame) { f.Name = map[string]string{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, f, c := liveCatalogFixture(t)
			tc.mutate(&f)
			writeLiveFrames(t, dir, []Frame{f})
			if c.snapshot()[0].ID != "dynamic_one" {
				t.Fatal("unsafe update accepted")
			}
			if _, err := NewCatalog(dir, "https://api.example"); err == nil {
				t.Fatal("invalid startup accepted")
			}
		})
	}
	dir, f, c := liveCatalogFixture(t)
	writeLiveFrames(t, dir, []Frame{f, f})
	if len(c.snapshot()) != 1 {
		t.Fatal("duplicate accepted")
	}
	if _, err := NewCatalog(dir, "http://api.example"); err == nil {
		t.Fatal("unsafe origin accepted")
	}
	if _, err := NewCatalog(dir, ""); err == nil {
		t.Fatal("incomplete config accepted")
	}
	if got, err := NewCatalog("", ""); err != nil || got != nil {
		t.Fatal("embedded fallback unavailable")
	}
}

func TestRuntimeCatalogRejectsEscapingSymlinksAndConcurrentReload(t *testing.T) {
	dir, f, c := liveCatalogFixture(t)
	outside := filepath.Join(t.TempDir(), "outside.png")
	replaceFile(t, outside, testPreview(t, 45))
	if err := os.Symlink(outside, filepath.Join(dir, "previews/escape.png")); err != nil {
		t.Fatal(err)
	}
	f.PanelPreview = "escape.png"
	writeLiveFrames(t, dir, []Frame{f})
	if strings.Contains(c.snapshot()[0].PanelPreview, "escape") {
		t.Fatal("escaping symlink accepted")
	}
	f.PanelPreview = "panel.png"
	writeLiveFrames(t, dir, []Frame{f})
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			for range 10 {
				if len(c.snapshot()) != 1 {
					t.Error("inconsistent snapshot")
				}
			}
		})
	}
	wg.Wait()
}
