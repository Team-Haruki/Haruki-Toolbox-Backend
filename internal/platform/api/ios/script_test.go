package ios

import (
	"crypto/sha256"
	"encoding/hex"
	json "encoding/json/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
)

// Stash parses $httpClient options strictly: a header value that is a number
// or a boolean fails with "invalid script HTTP request: invalid type: integer
// 0, expected a string". The template must hand over strings only.
func TestUploaderScriptSendsStringHeaders(t *testing.T) {
	script := GenerateScript("test-code", 2, "https://toolbox.example", true)

	for _, want := range []string{
		`"X-Chunk-Index": String(index)`,
		`"X-Total-Chunks": String(totalChunks)`,
		`options.headers['X-Surge-Skip-Scripting'] = 'false'`,
		`const version = "` + ScriptVersion + `";`,
		`'HarukiScriptClient/v1.0.1'`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	// No header is assigned a bare number, boolean or unconverted variable.
	for _, bad := range []*regexp.Regexp{
		regexp.MustCompile(`headers\[[^\]]+\]\s*=\s*(true|false|\d+)\b`),
		regexp.MustCompile(`"X-(Chunk-Index|Total-Chunks)":\s*(index|totalChunks)\b`),
	} {
		if m := bad.FindString(script); m != "" {
			t.Errorf("non-string header value: %s", m)
		}
	}
	// Both helpers coerce every header before any platform call.
	for _, helper := range []string{"const get = (options, callback) => {", "const post = (options, callback) => {"} {
		i := strings.Index(script, helper)
		if i < 0 {
			t.Fatalf("helper %q missing", helper)
		}
		body := script[i:]
		coerce := strings.Index(body, "stringifyHeaders(options)")
		call := strings.Index(body, "$httpClient.")
		if coerce < 0 || call < 0 || coerce > call {
			t.Errorf("%s does not stringify headers before calling $httpClient", helper)
		}
	}
}

// Runs the rendered script under Node with a Stash-like $httpClient that
// rejects any non-string header value, the way Stash's options parser does.
// Skipped when Node is not installed.
func TestUploaderScriptRunsAgainstStrictHTTPClient(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "uploader.js")
	if err := os.WriteFile(scriptPath, []byte(GenerateScript("test-code", 1, "https://toolbox.example", true)), 0o600); err != nil {
		t.Fatal(err)
	}
	harness := `
const vm = require("vm"), fs = require("fs");
const posts = [];
const sandbox = {
  console: { log() {} },
  $request: { url: "https://jp.example.com/api/suite/user/1" },
  $response: { body: new Uint8Array(2.5 * 1024 * 1024) },
  $done() {},
  $httpClient: {
    post(options, cb) {
      for (const [k, v] of Object.entries(options.headers)) {
        if (typeof v !== "string") { cb("invalid type for header " + k + ": " + typeof v, null, null); posts.push("bad"); return; }
      }
      posts.push(options.headers["X-Chunk-Index"] + "/" + options.headers["X-Total-Chunks"]);
      cb(null, { status: 200 }, "{}");
    },
    get(options, cb) { cb(null, { status: 200 }, "{}"); },
  },
};
vm.runInNewContext(fs.readFileSync(process.argv[2], "utf8"), sandbox);
process.stdout.write(posts.join(","));
`
	harnessPath := filepath.Join(dir, "harness.js")
	if err := os.WriteFile(harnessPath, []byte(harness), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, harnessPath, scriptPath).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	if got, want := string(out), "0/3,1/3,2/3"; got != want {
		t.Fatalf("posted chunks = %q, want %q", got, want)
	}
}

// ★ A changed script must carry a new version, or no installed module loads it:
// the modules name the script by a URL ending in &v=<version>, and proxy apps
// cache scripts by URL (see ScriptVersion). The hash pins the template to its
// version. When this fails, bump ScriptVersion, then set both values here.
func TestUploaderScriptVersionChangesWithTheScript(t *testing.T) {
	const version, templateSHA256 = "1.1.0", "5628e245d8c481f561abb9f074631b9b0ea43ebb0bcd07c02b3e8a0df3c68bd0"
	sum := sha256.Sum256([]byte(HarukiIOSJavaScriptTemplate))
	if got := hex.EncodeToString(sum[:]); got != templateSHA256 || ScriptVersion != version {
		t.Fatalf("uploader script %s at version %s, pinned %s at %s: bump ScriptVersion for a changed script, then update this test",
			got, ScriptVersion, templateSHA256, version)
	}
}

func TestScriptRulesLoadTheScriptByItsVersion(t *testing.T) {
	for _, dt := range []DataType{DataTypeSuite, DataTypeMysekai, DataTypeMysekaiForce, DataTypeMysekaiBirthdayParty} {
		rs := generateRulesForDataType("jp.example.com", "jp", dt, UploadModeScript, "code", "https://toolbox.example", 1, "direct")
		if len(rs.ScriptRules) != 1 {
			t.Fatalf("%s: %d script rules, want 1", dt, len(rs.ScriptRules))
		}
		if target := rs.ScriptRules[0].Target; !strings.HasSuffix(target, "&v="+ScriptVersion) {
			t.Errorf("%s: script URL %q does not name version %s", dt, target, ScriptVersion)
		}
	}
	script := GenerateScript("code", 1, "https://toolbox.example", true)
	if strings.Contains(script, "{{") {
		t.Fatal("the script keeps a placeholder")
	}
}

// moduleNotifyRun is one upload of the rendered script under Node, with a
// Surge-like runtime whose server answers every chunk with respond(index).
type moduleNotifyRun struct {
	Posts         []string   `json:"posts"`
	Notifications [][]string `json:"notifications"`
	Done          int        `json:"done"`
}

func runUploaderScript(t *testing.T, node string, notifyByDefault bool, url string, bodyBytes int, respond string) moduleNotifyRun {
	t.Helper()
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "uploader.js")
	if err := os.WriteFile(scriptPath, []byte(GenerateScript("test-code", 1, "https://toolbox.example", notifyByDefault)), 0o600); err != nil {
		t.Fatal(err)
	}
	harness := `
const vm = require("vm"), fs = require("fs");
const run = { posts: [], notifications: [], done: 0 };
const respond = ` + respond + `;
const sandbox = {
  console: { log() {} },
  $request: { url: process.argv[3] },
  $response: { body: new Uint8Array(Number(process.argv[4])) },
  $done() { run.done++; },
  $notification: { post(title, subtitle, body) { run.notifications.push([title, subtitle, body]); } },
  $httpClient: {
    post(options, cb) {
      const index = Number(options.headers["X-Chunk-Index"]);
      run.posts.push(index + "/" + options.headers["X-Total-Chunks"]);
      const r = respond(index);
      if (r.error) cb(r.error, null, null); else cb(null, { status: r.status, headers: r.headers || {} }, r.body || "{}");
    },
    get(options, cb) { cb(null, { status: 200, headers: {} }, "{}"); },
  },
};
vm.runInNewContext(fs.readFileSync(process.argv[2], "utf8"), sandbox);
process.stdout.write(JSON.stringify(run));
`
	harnessPath := filepath.Join(dir, "harness.js")
	if err := os.WriteFile(harnessPath, []byte(harness), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, harnessPath, scriptPath, url, strconv.Itoa(bodyBytes)).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	var run moduleNotifyRun
	if err := json.Unmarshal(out, &run); err != nil {
		t.Fatalf("decode %s: %v", out, err)
	}
	return run
}

// Runs the rendered script under Node for each way an upload ends and checks
// the one notification it posts (or that it posts none), and that the game's
// response is always released exactly once. Skipped when Node is not installed.
func TestUploaderScriptPostsOneModuleNotificationPerUpload(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	previous := harukiConfig.Cfg.SekaiClient
	t.Cleanup(func() { harukiConfig.Cfg.SekaiClient = previous })
	harukiConfig.Cfg.SekaiClient = harukiConfig.SekaiClientConfig{JPServerAPIHost: "jp.example.com", TWServerAPIHost: "tw-a.example.com", TWServerAPIHost2: "tw-b.example.com"}
	const suiteURL = "https://jp.example.com/api/suite/user/1"
	const mib = 1024 * 1024
	on := `() => ({ status: 200, headers: { "X-Haruki-Module-Notify": "1" } })`

	for _, tc := range []struct {
		name            string
		notifyByDefault bool
		url             string
		bodyBytes       int
		respond         string
		wantPosts       string
		wantSubtitle    string // "" means no notification
		wantMessage     string
	}{
		{"success", true, suiteURL, 2*mib + 1, on, "0/3,1/3,2/3", "日服 Suite 数据已上传", "工具箱已收到数据"},
		{"second tw host, mysekai", true, "https://tw-b.example.com/api/user/1/mysekai?isForceAllReloadOnlyMysekai=True", 10, on, "0/1", "台服 MySekai 数据已上传", ""},
		{"birthday party", true, "https://jp.example.com/api/user/1/mysekai/birthday-party/2/delivery", 10, on, "0/1", "日服 MySekai 生日派对 数据已上传", ""},
		{"turned off on the server", true, suiteURL, 10, `() => ({ status: 200, headers: { "x-haruki-module-notify": "0" } })`, "0/1", "", ""},
		{"turned on on the server", false, suiteURL, 10, on, "0/1", "日服 Suite 数据已上传", ""},
		{"no header, default on", true, suiteURL, 10, `() => ({ status: 200 })`, "0/1", "日服 Suite 数据已上传", ""},
		{"no header, default off", false, suiteURL, 10, `() => ({ status: 200 })`, "0/1", "", ""},
		{"network error stops the upload", true, suiteURL, 2*mib + 1, `(i) => i === 1 ? { error: "timed out" } : { status: 200, headers: { "X-Haruki-Module-Notify": "1" } }`, "0/3,1/3", "日服 Suite 数据上传失败", "连不上 Haruki 工具箱（timed out）"},
		{"network error, default off", false, suiteURL, 10, `() => ({ error: "timed out" })`, "0/1", "", ""},
		{"invalid upload code", true, suiteURL, 10, `() => ({ status: 401, body: '{"status":401,"message":"invalid upload code"}' })`, "0/1", "日服 Suite 数据上传失败", "上传码已失效"},
		{"not allowed", true, suiteURL, 10, `() => ({ status: 403, headers: { "X-Haruki-Module-Notify": "1" }, body: '{"status":403,"message":"upload not allowed"}' })`, "0/1", "日服 Suite 数据上传失败", "没有在 Haruki 工具箱绑定并验证"},
		{"not allowed, turned off", true, suiteURL, 10, `() => ({ status: 403, headers: { "X-Haruki-Module-Notify": "0" }, body: '{"status":403,"message":"upload not allowed"}' })`, "0/1", "", ""},
		{"html error page", true, suiteURL, 10, `() => ({ status: 502, body: "<html>bad gateway</html>" })`, "0/1", "日服 Suite 数据上传失败", "HTTP 502。"},
		{"other error", true, suiteURL, 10, `() => ({ status: 400, headers: { "X-Haruki-Module-Notify": "1" }, body: '{"status":400,"message":"Unknown upload type"}' })`, "0/1", "日服 Suite 数据上传失败", "HTTP 400：Unknown upload type。"},
		{"empty body", true, suiteURL, 0, on, "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := runUploaderScript(t, node, tc.notifyByDefault, tc.url, tc.bodyBytes, tc.respond)
			if got := strings.Join(run.Posts, ","); got != tc.wantPosts {
				t.Errorf("posts = %q, want %q", got, tc.wantPosts)
			}
			if run.Done != 1 {
				t.Errorf("$done called %d times, want 1", run.Done)
			}
			if tc.wantSubtitle == "" {
				if len(run.Notifications) != 0 {
					t.Fatalf("notifications = %q, want none", run.Notifications)
				}
				return
			}
			if len(run.Notifications) != 1 {
				t.Fatalf("notifications = %q, want one", run.Notifications)
			}
			n := run.Notifications[0]
			if n[0] != "Haruki 工具箱" || n[1] != tc.wantSubtitle || !strings.Contains(n[2], tc.wantMessage) {
				t.Fatalf("notification = %q, want subtitle %q with %q", n, tc.wantSubtitle, tc.wantMessage)
			}
		})
	}
}
