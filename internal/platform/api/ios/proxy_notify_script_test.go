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

type proxyNotifyRun struct {
	notifyByDefault bool
	argument        string // JS expression for $argument; "" leaves it undefined
	url             string
	headers         string // JS object literal for $response.headers
	throwNotify     bool
}

// Runs the proxy-mode notification script under Node with a Loon-like runtime
// (body-less response: $request.url, $response.status and headers).
func runProxyNotifyScript(t *testing.T, node string, r proxyNotifyRun) moduleNotifyRun {
	t.Helper()
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "notify.js")
	if err := os.WriteFile(scriptPath, []byte(GenerateProxyNotifyScript(r.notifyByDefault)), 0o600); err != nil {
		t.Fatal(err)
	}
	argument := ""
	if r.argument != "" {
		argument = "sandbox.$argument = " + r.argument + ";"
	}
	harness := `
const vm = require("vm"), fs = require("fs");
const run = { posts: [], notifications: [], done: 0 };
const throwNotify = ` + strconv.FormatBool(r.throwNotify) + `;
const sandbox = {
  console: { log() {} },
  $request: { url: ` + strconv.Quote(r.url) + ` },
  $response: { status: 200, headers: ` + r.headers + ` },
  $done(value) { if (value && value.body !== undefined) throw new Error("a body-less script returned a body"); run.done++; },
  $notification: { post(title, subtitle, body) { run.notifications.push([title, subtitle, body]); if (throwNotify) throw new Error("refused"); } },
};
` + argument + `
vm.runInNewContext(fs.readFileSync(process.argv[2], "utf8"), sandbox);
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
	var run moduleNotifyRun
	if err := json.Unmarshal(out, &run); err != nil {
		t.Fatalf("decode %s: %v", out, err)
	}
	return run
}

func TestProxyNotifyScriptReportsTheUploadResultHeader(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	const suiteURL = "https://toolbox.example/ios/proxy/jp/suite/user/1?isLogin=true"
	ok := `{ "X-Haruki-Upload-Result": "ok" }`
	for _, tc := range []struct {
		name         string
		run          proxyNotifyRun
		wantSubtitle string // "" means no notification
		wantMessage  string
	}{
		{"uploaded", proxyNotifyRun{notifyByDefault: true, url: suiteURL, headers: ok}, "日服 Suite 数据已上传", "工具箱已保存这份数据"},
		{"lower-case header", proxyNotifyRun{notifyByDefault: true, url: suiteURL, headers: `{ "x-haruki-upload-result": "ok" }`}, "日服 Suite 数据已上传", ""},
		{"mysekai on tw", proxyNotifyRun{notifyByDefault: true, url: "https://toolbox.example/ios/proxy/tw/user/1/mysekai?isForceAllReloadOnlyMysekai=True", headers: ok}, "台服 MySekai 数据已上传", ""},
		{"birthday party", proxyNotifyRun{notifyByDefault: true, url: "https://toolbox.example/ios/proxy/cn/user/1/mysekai/birthday-party/2/delivery", headers: ok}, "国服 MySekai 生日派对 数据已上传", ""},
		{"invalid data", proxyNotifyRun{notifyByDefault: true, url: suiteURL, headers: `{ "X-Haruki-Upload-Result": "failed", "X-Haruki-Upload-Error": "invalid_upload_payload" }`}, "日服 Suite 数据上传失败", "无法解析这份数据"},
		{"not allowed", proxyNotifyRun{notifyByDefault: true, url: suiteURL, headers: `{ "X-Haruki-Upload-Result": "failed", "X-Haruki-Upload-Error": "upload_not_allowed" }`}, "日服 Suite 数据上传失败", "没有在 Haruki 工具箱绑定并验证"},
		{"game server unreachable", proxyNotifyRun{notifyByDefault: true, url: suiteURL, headers: `{ "X-Haruki-Upload-Result": "failed", "X-Haruki-Upload-Error": "game_server_unavailable" }`}, "日服 Suite 数据上传失败", "连不上游戏服务器"},
		{"failed without a code", proxyNotifyRun{notifyByDefault: true, url: suiteURL, headers: `{ "X-Haruki-Upload-Result": "failed" }`}, "日服 Suite 数据上传失败", "没有保存这份数据"},
		{"nothing to upload", proxyNotifyRun{notifyByDefault: true, url: suiteURL, headers: `{ "X-Haruki-Upload-Result": "skipped" }`}, "", ""},
		{"header absent", proxyNotifyRun{notifyByDefault: true, url: suiteURL, headers: `{ "Content-Type": "application/octet-stream" }`}, "", ""},
		{"argument off", proxyNotifyRun{notifyByDefault: true, argument: `{ notify: false }`, url: suiteURL, headers: ok}, "", ""},
		{"argument off, failure", proxyNotifyRun{notifyByDefault: true, argument: `{ notify: false }`, url: suiteURL, headers: `{ "X-Haruki-Upload-Result": "failed" }`}, "", ""},
		{"argument on", proxyNotifyRun{argument: `{ notify: true }`, url: suiteURL, headers: ok}, "日服 Suite 数据已上传", ""},
		{"no argument, default off", proxyNotifyRun{url: suiteURL, headers: ok}, "", ""},
		{"unfilled argument", proxyNotifyRun{notifyByDefault: true, argument: `"{notify}"`, url: suiteURL, headers: ok}, "日服 Suite 数据已上传", ""},
		{"notification throws", proxyNotifyRun{notifyByDefault: true, url: suiteURL, headers: ok, throwNotify: true}, "日服 Suite 数据已上传", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := runProxyNotifyScript(t, node, tc.run)
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
