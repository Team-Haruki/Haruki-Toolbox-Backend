package sekai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// versionedGame is a game whose data version can move under a running job,
// in front of a version file that can lag behind it. Every game call except
// the executing inherit call is gated (when gated) on X-Data-Version and
// X-Asset-Version matching the game's; a mismatch answers 426, like the game
// does after a data update.
type versionedGame struct {
	*fakeGame
	mu sync.Mutex
	// game is what the game expects; file is what the version file says.
	gameData, gameAsset string
	fileData, fileAsset string
	// gameApp, when set, is the app version login demands (426 otherwise);
	// fileApp is the app version in the file.
	gameApp, fileApp string
	// bumpAfterLogin moves the game to the given versions right after the
	// first login: a data update in the middle of a job.
	bumpAfterLogin     [2]string
	logins             int
	gateExecute        bool
	upgradeBody        map[string]any
	versionNoCacheHits int
	appVersionSeen     []string
	dataVersionSeen    map[string][]string
}

func newVersionedGame(t *testing.T) (*versionedGame, *httptest.Server) {
	t.Helper()
	fg := &fakeGame{t: t, override: map[string]http.HandlerFunc{}, bodies: map[string]map[string]any{}}
	g := &versionedGame{fakeGame: fg, gameData: "3", gameAsset: "4", fileData: "3", fileAsset: "4", fileApp: "1", dataVersionSeen: map[string][]string{}}
	srv := httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(srv.Close)
	return g, srv
}

func (g *versionedGame) serve(w http.ResponseWriter, r *http.Request) {
	uid := strconv.FormatInt(testUserID, 10)
	g.mu.Lock()
	if r.URL.Path == "/version" {
		if r.Header.Get("Cache-Control") == "no-cache" {
			g.versionNoCacheHits++
		}
		body := `{"appVersion":"` + g.fileApp + `","appHash":"2","dataVersion":"` + g.fileData + `","assetVersion":"` + g.fileAsset + `"}`
		g.mu.Unlock()
		g.add("GET /version")
		_, _ = w.Write([]byte(body))
		return
	}
	isExecute := strings.HasPrefix(r.URL.Path, "/api/inherit/user/") && r.URL.Query().Get("isExecuteInherit") == "True"
	if strings.HasPrefix(r.URL.Path, "/api/") {
		g.appVersionSeen = append(g.appVersionSeen, r.Header.Get("X-App-Version"))
		g.dataVersionSeen[r.URL.Path] = append(g.dataVersionSeen[r.URL.Path], r.Header.Get("X-Data-Version"))
	}
	stale := r.Header.Get("X-Data-Version") != g.gameData || r.Header.Get("X-Asset-Version") != g.gameAsset
	gated := strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/user/"+uid+"/auth" && (!isExecute || g.gateExecute)
	if gated && stale {
		body := g.upgradeBody
		g.mu.Unlock()
		e := r.Method + " " + r.URL.Path
		if v := r.URL.Query().Get("isExecuteInherit"); v != "" {
			e += "?isExecuteInherit=" + v
		}
		g.add(e + " -> 426")
		w.WriteHeader(http.StatusUpgradeRequired)
		if body != nil {
			g.write(w, body)
		}
		return
	}
	if r.URL.Path == "/api/user/"+uid+"/auth" && g.gameApp != "" && r.Header.Get("X-App-Version") != g.gameApp {
		g.mu.Unlock()
		g.add("PUT /api/user/" + uid + "/auth -> 426")
		w.WriteHeader(http.StatusUpgradeRequired)
		return
	}
	if r.URL.Path == "/api/user/"+uid+"/auth" {
		g.logins++
		answer := map[string]any{
			"sessionToken": "session-token",
			"dataVersion":  g.gameData,
			"assetVersion": g.gameAsset,
			// The game also names an app version; it must never be adopted.
			"appVersion": "999.0.0",
		}
		if g.logins == 1 && g.bumpAfterLogin[0] != "" {
			g.gameData, g.gameAsset = g.bumpAfterLogin[0], g.bumpAfterLogin[1]
		}
		g.mu.Unlock()
		g.add("PUT /api/user/" + uid + "/auth")
		g.write(w, answer)
		return
	}
	g.mu.Unlock()
	g.fakeGame.serve(w, r)
}

func (g *versionedGame) count(event string) int {
	n := 0
	for _, e := range g.snapshot() {
		if e == event {
			n++
		}
	}
	return n
}

func (g *versionedGame) setFile(data, asset string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fileData, g.fileAsset = data, asset
}

func (g *versionedGame) setGame(data, asset string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.gameData, g.gameAsset = data, asset
}

type progressLog struct {
	mu     sync.Mutex
	events []ProgressEvent
}

func (p *progressLog) sink(e ProgressEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, e)
}

func (p *progressLog) refreshes() []ProgressEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []ProgressEvent
	for _, e := range p.events {
		if e.Step == SubStepVersionRefresh && e.Status != ProgressRunning {
			out = append(out, e)
		}
	}
	return out
}

var (
	executeCall = "POST /api/inherit/user/" + testInheritID + "?isExecuteInherit=True"
	previewCall = "POST /api/inherit/user/" + testInheritID + "?isExecuteInherit=False"
)

// The incident: the version file is stuck on an old data version while the
// game moves on (here between execute and login). Login reports the game's
// versions, so the suite works with no retry at all.
func TestLoginAdoptsTheGamesDataVersions(t *testing.T) {
	g, srv := newVersionedGame(t)
	recordPauses(t, g.fakeGame)
	s := newTestSession(t, ServerEN, srv.URL, nil)
	ctx := context.Background()
	if err := s.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Preview(ctx, testCode); err != nil {
		t.Fatal(err)
	}
	credential, err := s.Execute(ctx, testCode)
	if err != nil {
		t.Fatal(err)
	}
	g.setGame("7.1.0.45", "7.1.0.40")
	if err := s.Login(ctx, s.UserID(), credential); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FetchSuite(ctx); err != nil {
		t.Fatalf("FetchSuite: %v (calls %q)", err, g.snapshot())
	}
	uid := strconv.FormatInt(testUserID, 10)
	if got := g.count("GET /api/suite/user/" + uid + " -> 426"); got != 0 {
		t.Fatalf("suite answered 426 %d times; login versions were not adopted", got)
	}
	if data, asset := s.versionHeaders(); data != "7.1.0.45" || asset != "7.1.0.40" {
		t.Fatalf("headers data=%s asset=%s, want the login's", data, asset)
	}
	assertAppVersionFromFileOnly(t, g)
}

// A data update lands after login while the version file is stuck: the suite
// answers 426, the file is re-read past caches (still stale), a fresh login
// reports the new versions, and the suite is retried once and succeeds. The
// executing inherit call is made exactly once.
func TestSuite426RetriesWithTheVersionsALoginReports(t *testing.T) {
	g, srv := newVersionedGame(t)
	recordPauses(t, g.fakeGame)
	g.bumpAfterLogin = [2]string{"7.1.0.45", "7.1.0.45"}
	var progress progressLog
	runFullTransfer(t, newTestSession(t, ServerEN, srv.URL, progress.sink))

	uid := strconv.FormatInt(testUserID, 10)
	if got := g.count(executeCall); got != 1 {
		t.Fatalf("executing inherit call made %d times, want exactly 1", got)
	}
	if got := g.count("PUT /api/user/" + uid + "/auth"); got != 2 {
		t.Fatalf("logins = %d, want 2 (the job's and the refresh's)", got)
	}
	if g.versionNoCacheHits != 1 {
		t.Fatalf("version file re-read past caches %d times, want 1", g.versionNoCacheHits)
	}
	// The new-code request is the first call after the data bump: it gets the
	// 426 and is retried; the suite then already sends the new versions.
	if got := g.count("PUT /api/user/" + uid + "/inherit -> 426"); got != 1 {
		t.Fatalf("issue-code 426 count = %d (calls %q)", got, g.snapshot())
	}
	if got := g.count("PUT /api/user/" + uid + "/inherit"); got != 1 {
		t.Fatalf("issue-code successes = %d, want 1", got)
	}
	refreshes := progress.refreshes()
	if len(refreshes) != 1 || refreshes[0].Status != ProgressDone || !strings.HasPrefix(refreshes[0].Note, VersionSourceLogin+" data=7.1.0.45") {
		t.Fatalf("version refresh events = %+v", refreshes)
	}
	assertAppVersionFromFileOnly(t, g)
}

// The same, hitting the suite itself: the game moves after the new code was
// issued.
func TestSuiteItselfRetriesAfter426(t *testing.T) {
	g, srv := newVersionedGame(t)
	recordPauses(t, g.fakeGame)
	uid := strconv.FormatInt(testUserID, 10)
	s := newTestSession(t, ServerEN, srv.URL, nil)
	ctx := context.Background()
	if err := s.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Preview(ctx, testCode); err != nil {
		t.Fatal(err)
	}
	credential, err := s.Execute(ctx, testCode)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Login(ctx, s.UserID(), credential); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IssueInheritCode(ctx, "newpass1234"); err != nil {
		t.Fatal(err)
	}
	g.setGame("7.1.0.45", "7.1.0.45")
	if _, err := s.FetchSuite(ctx); err != nil {
		t.Fatalf("FetchSuite: %v", err)
	}
	seen := g.dataVersionSeen["/api/suite/user/"+uid]
	if len(seen) < 2 || seen[0] != "3" || seen[1] != "7.1.0.45" {
		t.Fatalf("suite data versions sent = %q, want the stale one then the login's", seen)
	}
	if got := g.count(executeCall); got != 1 {
		t.Fatalf("executing inherit call made %d times", got)
	}
}

// Before execute there is no login to ask. When the re-read version file has
// moved on, the preview is retried with it.
func TestPreview426RetriesWithARefreshedVersionFile(t *testing.T) {
	g, srv := newVersionedGame(t)
	recordPauses(t, g.fakeGame)
	s := newTestSession(t, ServerEN, srv.URL, nil)
	ctx := context.Background()
	if err := s.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	g.setGame("7.1.0.45", "7.1.0.45")
	g.setFile("7.1.0.45", "7.1.0.45")
	if _, err := s.Preview(ctx, testCode); err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if got := g.count(previewCall + " -> 426"); got != 1 {
		t.Fatalf("preview 426 count = %d", got)
	}
	if got := g.count(previewCall); got != 1 {
		t.Fatalf("successful previews = %d", got)
	}
	if g.count(executeCall) != 0 {
		t.Fatal("preview made the executing call")
	}
}

// The 426 answer may name the versions the game expects; with a stale file
// and no login yet, those are used for the one retry. The app version is not.
func TestPreview426UsesTheVersionsTheAnswerNames(t *testing.T) {
	g, srv := newVersionedGame(t)
	recordPauses(t, g.fakeGame)
	var progress progressLog
	s := newTestSession(t, ServerEN, srv.URL, progress.sink)
	ctx := context.Background()
	if err := s.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	g.setGame("7.1.0.45", "7.1.0.40")
	g.upgradeBody = map[string]any{"dataVersion": "7.1.0.45", "assetVersion": "7.1.0.40", "appVersion": "999.0.0"}
	if _, err := s.Preview(ctx, testCode); err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if refreshes := progress.refreshes(); len(refreshes) != 1 || !strings.HasPrefix(refreshes[0].Note, VersionSourceResponse) {
		t.Fatalf("version refresh events = %+v", refreshes)
	}
	assertAppVersionFromFileOnly(t, g)
}

// With nothing newer anywhere, the preview fails once with a distinct error
// (not a 403, so not mistaken for a bad code) and is not retried pointlessly.
func TestPreview426WithNothingNewerFailsAsOutdated(t *testing.T) {
	g, srv := newVersionedGame(t)
	recordPauses(t, g.fakeGame)
	var progress progressLog
	s := newTestSession(t, ServerEN, srv.URL, progress.sink)
	ctx := context.Background()
	if err := s.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	g.setGame("7.1.0.45", "7.1.0.45")
	_, err := s.Preview(ctx, testCode)
	if !IsGameVersionOutdated(err) || APIStatus(err) != http.StatusUpgradeRequired {
		t.Fatalf("err = %v, want a game version outdated 426", err)
	}
	if got := g.count(previewCall + " -> 426"); got != 1 {
		t.Fatalf("preview attempts = %d, want 1 (no retry without newer versions)", got)
	}
	if refreshes := progress.refreshes(); len(refreshes) != 1 || refreshes[0].Status != ProgressFailed || refreshes[0].Note != versionRefreshNone {
		t.Fatalf("version refresh events = %+v", refreshes)
	}
	if strings.Contains(err.Error(), testInheritID) || strings.Contains(err.Error(), testInheritPassword) {
		t.Fatalf("error leaks the code: %v", err)
	}
}

// ⚠️ The executing call uses up the code: a 426 there is reported, never
// retried, and nothing refreshes versions in between.
func TestExecute426IsNeverRetried(t *testing.T) {
	g, srv := newVersionedGame(t)
	recordPauses(t, g.fakeGame)
	g.gateExecute = true
	var progress progressLog
	s := newTestSession(t, ServerEN, srv.URL, progress.sink)
	ctx := context.Background()
	if err := s.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Preview(ctx, testCode); err != nil {
		t.Fatal(err)
	}
	g.setGame("7.1.0.45", "7.1.0.45")
	g.setFile("7.1.0.45", "7.1.0.45")
	g.upgradeBody = map[string]any{"dataVersion": "7.1.0.45", "assetVersion": "7.1.0.45"}
	_, err := s.Execute(ctx, testCode)
	if !IsGameVersionOutdated(err) {
		t.Fatalf("err = %v, want a 426", err)
	}
	if got := g.count(executeCall + " -> 426"); got != 1 {
		t.Fatalf("executing call made %d times, want exactly 1", got)
	}
	if g.count(executeCall) != 0 || g.versionNoCacheHits != 0 || len(progress.refreshes()) != 0 {
		t.Fatalf("execute was retried or refreshed versions: calls %q", g.snapshot())
	}
}

// A stale file read during a refresh must not undo the versions a login
// reported: its data and asset apply only when the file itself moved.
func TestStaleFileDoesNotUndoLoginVersions(t *testing.T) {
	_, srv := newVersionedGame(t)
	s := newTestSession(t, ServerEN, srv.URL, nil)
	ctx := context.Background()
	if err := s.fetchVersion(ctx, false); err != nil {
		t.Fatal(err)
	}
	s.adoptDataVersions(map[string]any{"dataVersion": "9", "assetVersion": "9"})
	if err := s.fetchVersion(ctx, true); err != nil {
		t.Fatal(err)
	}
	if data, asset := s.versionHeaders(); data != "9" || asset != "9" {
		t.Fatalf("stale file overwrote login versions: data=%s asset=%s", data, asset)
	}
	if s.headers[headerAppVersion] != "1" || s.headers[headerAppHash] != "2" {
		t.Fatalf("app version/hash not from the file: %q %q", s.headers[headerAppVersion], s.headers[headerAppHash])
	}
}

func assertAppVersionFromFileOnly(t *testing.T, g *versionedGame) {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, v := range g.appVersionSeen {
		if v != "1" {
			t.Fatalf("a game call sent app version %q; it must only come from the version file", v)
		}
	}
}

// A 426 on login means the app version moved. The re-read file has the new
// app version (its data and asset unchanged): that counts as a refresh, and
// login is retried with it.
func TestLogin426RetriesWithTheFilesNewAppVersion(t *testing.T) {
	g, srv := newVersionedGame(t)
	recordPauses(t, g.fakeGame)
	s := newTestSession(t, ServerEN, srv.URL, nil)
	ctx := context.Background()
	if err := s.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Preview(ctx, testCode); err != nil {
		t.Fatal(err)
	}
	credential, err := s.Execute(ctx, testCode)
	if err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	g.gameApp, g.fileApp = "1.1", "1.1"
	g.mu.Unlock()
	if err := s.Login(ctx, s.UserID(), credential); err != nil {
		t.Fatalf("Login: %v (calls %q)", err, g.snapshot())
	}
	uid := strconv.FormatInt(testUserID, 10)
	if g.count("PUT /api/user/"+uid+"/auth -> 426") != 1 || g.count("PUT /api/user/"+uid+"/auth") != 1 {
		t.Fatalf("login calls = %q", g.snapshot())
	}
	if s.headers[headerAppVersion] != "1.1" {
		t.Fatalf("app version = %q, want the file's", s.headers[headerAppVersion])
	}
}

// A file that moved but is still behind what a login reported does not
// replace it; a file that moved past it does.
func TestMovedButOlderFileDoesNotReplaceAdoptedVersions(t *testing.T) {
	g, srv := newVersionedGame(t)
	s := newTestSession(t, ServerEN, srv.URL, nil)
	ctx := context.Background()
	g.setFile("7.1.0.40", "7.1.0.40")
	if err := s.fetchVersion(ctx, false); err != nil {
		t.Fatal(err)
	}
	s.adoptDataVersions(map[string]any{"dataVersion": "7.1.0.45", "assetVersion": "7.1.0.45"})
	g.setFile("7.1.0.42", "7.1.0.42")
	if err := s.fetchVersion(ctx, true); err != nil {
		t.Fatal(err)
	}
	if data, asset := s.versionHeaders(); data != "7.1.0.45" || asset != "7.1.0.45" {
		t.Fatalf("an older file replaced adopted versions: data=%s asset=%s", data, asset)
	}
	g.setFile("7.1.0.46", "7.1.0.46")
	if err := s.fetchVersion(ctx, true); err != nil {
		t.Fatal(err)
	}
	if data, asset := s.versionHeaders(); data != "7.1.0.46" || asset != "7.1.0.46" {
		t.Fatalf("a newer file did not apply: data=%s asset=%s", data, asset)
	}
}

func TestCompareVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		cmp  int
		ok   bool
	}{
		{"7.1.0.45", "7.1.0.45", 0, true},
		{"7.1.0.45", "7.1.0.9", 1, true},
		{"7.1.0", "7.1.0.1", -1, true},
		{"7.2", "7.1.9.9", 1, true},
		{"7.1.0-x", "7.1.0", 0, false},
		{"", "1", 0, false},
	} {
		cmp, ok := compareVersions(tc.a, tc.b)
		if ok != tc.ok || (ok && cmp != tc.cmp) {
			t.Errorf("compareVersions(%q, %q) = %d, %v; want %d, %v", tc.a, tc.b, cmp, ok, tc.cmp, tc.ok)
		}
	}
}
