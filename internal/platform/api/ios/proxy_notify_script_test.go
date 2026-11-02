package ios

import (
	json "encoding/json/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// proxyNotifyRuntime installs one app's script runtime into the sandbox: how it
// passes the notify switch, and its notification API.
type proxyNotifyRuntime struct {
	name string
	// argument returns the JS statements that hand the script the switch:
	// "true", "false", "unfilled" (the app left the placeholder) or "" (no
	// argument at all).
	argument func(setting string) string
	// notifyAPI is the JS statement that installs the notification API; it
	// pushes to run.notifications and throws when throwNotify is set.
	notifyAPI string
}

const (
	notificationPostAPI  = `sandbox.$notification = { post(title, subtitle, body) { run.notifications.push([title, subtitle, body]); if (throwNotify) throw new Error("refused"); } };`
	quantumultXNotifyAPI = `sandbox.$notify = function (title, subtitle, body) { run.notifications.push([title, subtitle, body]); if (throwNotify) throw new Error("refused"); };`
)

func stringArgument(unfilled string) func(string) string {
	return func(setting string) string {
		switch setting {
		case "":
			return ""
		case "unfilled":
			return "sandbox.$argument = " + strconv.Quote(unfilled) + ";"
		default:
			return "sandbox.$argument = " + strconv.Quote("notify="+setting) + ";"
		}
	}
}

// The runtimes as each app's docs describe them (see proxyNotifyScriptTemplate).
var proxyNotifyRuntimes = []proxyNotifyRuntime{
	{
		name: "surge",
		argument: func(setting string) string {
			return `sandbox.$environment = { "surge-version": "6.0", language: "zh-Hans" };` + stringArgument("notify={{{notify}}}")(setting)
		},
		notifyAPI: notificationPostAPI,
	},
	{name: "shadowrocket", argument: stringArgument("notify={{{notify}}}"), notifyAPI: notificationPostAPI},
	{
		name: "loon",
		argument: func(setting string) string {
			switch setting {
			case "":
				return ""
			case "unfilled":
				return `sandbox.$argument = "{notify}";`
			default:
				return "sandbox.$argument = { notify: " + setting + " };"
			}
		},
		notifyAPI: notificationPostAPI,
	},
	{
		name: "stash",
		argument: func(setting string) string {
			return `sandbox.$environment = { "stash-build": "1", "stash-version": "3.7.0", language: "zh-Hans", system: "iOS" };` + stringArgument("notify=${args.notify}")(setting)
		},
		notifyAPI: notificationPostAPI,
	},
	{
		// Quantumult X 1.5.6+: the fragment as $environment.variables.
		name: "quantumult x",
		argument: func(setting string) string {
			const path = "https://toolbox.example/ios/script/proxy-notify.js?v=1.1.0"
			switch setting {
			case "", "unfilled":
				return "sandbox.$environment = { sourcePath: " + strconv.Quote(path) + ", variables: {} };"
			default:
				return "sandbox.$environment = { sourcePath: " + strconv.Quote(path+"#notify="+setting) + `, variables: { notify: "` + setting + `" } };`
			}
		},
		notifyAPI: quantumultXNotifyAPI,
	},
	{
		// Older Quantumult X: only $environment.sourcePath.
		name: "quantumult x without variables",
		argument: func(setting string) string {
			const path = "https://toolbox.example/ios/script/proxy-notify.js?v=1.1.0&notify=false"
			switch setting {
			case "", "unfilled":
				return "sandbox.$environment = { sourcePath: " + strconv.Quote(path) + " };"
			default:
				return "sandbox.$environment = { sourcePath: " + strconv.Quote(path+"#notify="+setting) + " };"
			}
		},
		notifyAPI: quantumultXNotifyAPI,
	},
}

type proxyNotifyRun struct {
	notifyByDefault bool
	setting         string // see proxyNotifyRuntime.argument
	url             string
	headers         string // JS object literal for $response.headers
	throwNotify     bool
}

type proxyNotifyResult struct {
	Notifications [][]string `json:"notifications"`
	Done          int        `json:"done"`
	// DoneChanges are the keys of each $done value: anything there would
	// change the response.
	DoneChanges [][]string `json:"doneChanges"`
	// ResponseChanged: the script modified $response (status or headers)
	// in place.
	ResponseChanged bool `json:"responseChanged"`
}

// Runs the proxy-mode notification script under Node with one app's runtime
// (a body-less response: $request.url, $response.status and headers, no body).
func runProxyNotifyScript(t *testing.T, node string, app proxyNotifyRuntime, r proxyNotifyRun) proxyNotifyResult {
	t.Helper()
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "notify.js")
	if err := os.WriteFile(scriptPath, []byte(GenerateProxyNotifyScript(r.notifyByDefault)), 0o600); err != nil {
		t.Fatal(err)
	}
	harness := `
const vm = require("vm"), fs = require("fs");
const run = { notifications: [], done: 0, doneChanges: [], responseChanged: false };
const throwNotify = ` + strconv.FormatBool(r.throwNotify) + `;
const headers = ` + r.headers + `;
const sandbox = {
  console: { log() {} },
  $request: { url: ` + strconv.Quote(r.url) + `, method: "GET", headers: {} },
  $response: { status: 200, statusCode: 200, headers },
  $done(value) { run.done++; run.doneChanges.push(Object.keys(value === undefined ? { aborted: true } : value)); },
};
` + app.notifyAPI + `
` + app.argument(r.setting) + `
const responseBefore = JSON.stringify(sandbox.$response);
vm.runInNewContext(fs.readFileSync(process.argv[2], "utf8"), sandbox);
run.responseChanged = JSON.stringify(sandbox.$response) !== responseBefore;
process.stdout.write(JSON.stringify(run));
`
	harnessPath := filepath.Join(dir, "harness.js")
	if err := os.WriteFile(harnessPath, []byte(harness), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, harnessPath, scriptPath).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	var run proxyNotifyResult
	if err := json.Unmarshal(out, &run); err != nil {
		t.Fatalf("decode %s: %v", out, err)
	}
	return run
}

// For every app's runtime: one notification for a reported upload (or none),
// following the switch, and the response (status, headers, $done value)
// passed through untouched exactly once, also when there is nothing to report
// or posting the notification throws.
func TestProxyNotifyScriptReportsTheUploadResultHeader(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	const suiteURL = "https://toolbox.example/ios/proxy/jp/suite/user/1?isLogin=true"
	ok := `{ "X-Haruki-Upload-Result": "ok", "Content-Type": "application/octet-stream" }`
	failed := func(code string) string {
		return `{ "X-Haruki-Upload-Result": "failed", "X-Haruki-Upload-Error": "` + code + `" }`
	}
	type want struct {
		subtitle string // "" means no notification
		message  string
	}
	cases := []struct {
		name string
		run  proxyNotifyRun
		want want
	}{
		{"uploaded", proxyNotifyRun{notifyByDefault: true, url: suiteURL, headers: ok}, want{"日服 Suite 数据已上传", "工具箱已保存这份数据"}},
		{"lower-case header", proxyNotifyRun{notifyByDefault: true, url: suiteURL, headers: `{ "x-haruki-upload-result": "ok" }`}, want{"日服 Suite 数据已上传", ""}},
		{"mysekai on tw", proxyNotifyRun{notifyByDefault: true, url: "https://toolbox.example/ios/proxy/tw/user/1/mysekai?isForceAllReloadOnlyMysekai=True", headers: ok}, want{"台服 MySekai 数据已上传", ""}},
		{"birthday party", proxyNotifyRun{notifyByDefault: true, url: "https://toolbox.example/ios/proxy/cn/user/1/mysekai/birthday-party/2/delivery", headers: ok}, want{"国服 MySekai 生日派对 数据已上传", ""}},
		{"invalid data", proxyNotifyRun{notifyByDefault: true, url: suiteURL, headers: failed("invalid_upload_payload")}, want{"日服 Suite 数据上传失败", "无法解析这份数据"}},
		{"not allowed", proxyNotifyRun{notifyByDefault: true, url: suiteURL, headers: failed("upload_not_allowed")}, want{"日服 Suite 数据上传失败", "没有在 Haruki 工具箱绑定并验证"}},
		{"temporarily unavailable", proxyNotifyRun{notifyByDefault: true, url: suiteURL, headers: failed("temporarily_unavailable")}, want{"日服 Suite 数据上传失败", "暂时无法处理"}},
		{"internal error", proxyNotifyRun{notifyByDefault: true, url: suiteURL, headers: failed("internal_error")}, want{"日服 Suite 数据上传失败", "保存数据时出错"}},
		{"game server unreachable", proxyNotifyRun{notifyByDefault: true, url: suiteURL, headers: failed("game_server_unavailable")}, want{"日服 Suite 数据上传失败", "连不上游戏服务器"}},
		{"rate limited", proxyNotifyRun{notifyByDefault: true, url: suiteURL, headers: `{ "X-Haruki-Upload-Result": "failed", "X-Haruki-Upload-Error": "rate_limited", "Retry-After": "30" }`}, want{"日服 Suite 数据上传失败", "上传太频繁"}},
		{"failed without a code", proxyNotifyRun{notifyByDefault: true, url: suiteURL, headers: `{ "X-Haruki-Upload-Result": "failed" }`}, want{"日服 Suite 数据上传失败", "没有保存这份数据"}},
		{"unknown code", proxyNotifyRun{notifyByDefault: true, url: suiteURL, headers: failed("something_new")}, want{"日服 Suite 数据上传失败", "没有保存这份数据"}},
		{"nothing to upload", proxyNotifyRun{notifyByDefault: true, url: suiteURL, headers: `{ "X-Haruki-Upload-Result": "skipped" }`}, want{}},
		{"header absent", proxyNotifyRun{notifyByDefault: true, url: suiteURL, headers: `{ "Content-Type": "application/octet-stream" }`}, want{}},
		{"no headers", proxyNotifyRun{notifyByDefault: true, url: suiteURL, headers: `{}`}, want{}},
		{"switched off", proxyNotifyRun{notifyByDefault: true, setting: "false", url: suiteURL, headers: ok}, want{}},
		{"switched off, failure", proxyNotifyRun{notifyByDefault: true, setting: "false", url: suiteURL, headers: failed("internal_error")}, want{}},
		{"switched on, default off", proxyNotifyRun{setting: "true", url: suiteURL, headers: ok}, want{"日服 Suite 数据已上传", ""}},
		{"no argument, default on", proxyNotifyRun{notifyByDefault: true, url: suiteURL, headers: ok}, want{"日服 Suite 数据已上传", ""}},
		{"no argument, default off", proxyNotifyRun{url: suiteURL, headers: ok}, want{}},
		{"unfilled argument", proxyNotifyRun{notifyByDefault: true, setting: "unfilled", url: suiteURL, headers: ok}, want{"日服 Suite 数据已上传", ""}},
		{"notification throws", proxyNotifyRun{notifyByDefault: true, url: suiteURL, headers: ok, throwNotify: true}, want{"日服 Suite 数据已上传", ""}},
		{"notification throws on failure", proxyNotifyRun{notifyByDefault: true, url: suiteURL, headers: failed("rate_limited"), throwNotify: true}, want{"日服 Suite 数据上传失败", "上传太频繁"}},
	}
	for _, app := range proxyNotifyRuntimes {
		for _, tc := range cases {
			t.Run(app.name+"/"+tc.name, func(t *testing.T) {
				run := runProxyNotifyScript(t, node, app, tc.run)
				if run.Done != 1 {
					t.Errorf("$done called %d times, want 1", run.Done)
				}
				for _, changes := range run.DoneChanges {
					if len(changes) != 0 {
						t.Errorf("$done changes the response: %q", changes)
					}
				}
				if run.ResponseChanged {
					t.Error("the script modified the response status or headers")
				}
				if tc.want.subtitle == "" {
					if len(run.Notifications) != 0 {
						t.Fatalf("notifications = %q, want none", run.Notifications)
					}
					return
				}
				if len(run.Notifications) != 1 {
					t.Fatalf("notifications = %q, want one", run.Notifications)
				}
				n := run.Notifications[0]
				if n[0] != "Haruki 工具箱" || n[1] != tc.want.subtitle || !strings.Contains(n[2], tc.want.message) {
					t.Fatalf("notification = %q, want subtitle %q with %q", n, tc.want.subtitle, tc.want.message)
				}
			})
		}
	}
}

// An app with no notification API at all still releases the response.
func TestProxyNotifyScriptWithoutANotificationAPI(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	bare := proxyNotifyRuntime{name: "bare", argument: func(string) string { return "" }}
	run := runProxyNotifyScript(t, node, bare, proxyNotifyRun{notifyByDefault: true, url: "https://toolbox.example/ios/proxy/jp/suite/user/1", headers: `{ "X-Haruki-Upload-Result": "ok" }`})
	if run.Done != 1 || len(run.Notifications) != 0 || len(run.DoneChanges[0]) != 0 {
		t.Fatalf("run = %+v, want one untouched $done and no notification", run)
	}
}
