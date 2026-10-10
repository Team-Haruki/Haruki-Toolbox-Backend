package ios

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Stash parses $httpClient options strictly: a header value that is a number
// or a boolean fails with "invalid script HTTP request: invalid type: integer
// 0, expected a string". The template must hand over strings only.
func TestUploaderScriptSendsStringHeaders(t *testing.T) {
	script := GenerateScript("test-code", 2, "https://toolbox.example")

	for _, want := range []string{
		`"X-Chunk-Index": String(index)`,
		`"X-Total-Chunks": String(totalChunks)`,
		`options.headers['X-Surge-Skip-Scripting'] = 'false'`,
		`const version = "1.0.1";`,
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
	if err := os.WriteFile(scriptPath, []byte(GenerateScript("test-code", 1, "https://toolbox.example")), 0o600); err != nil {
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
