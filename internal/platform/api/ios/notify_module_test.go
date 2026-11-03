package ios

import (
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"gopkg.in/yaml.v3"
)

func notifyModuleRequest(app ProxyApp, mode UploadMode, notifyOff bool) *ModuleRequest {
	return &ModuleRequest{
		UploadCode:  "code",
		Regions:     []harukiUtils.SupportedDataUploadServer{harukiUtils.SupportedDataUploadServerJP},
		DataTypes:   []DataType{DataTypeSuite, DataTypeMysekai},
		App:         app,
		Mode:        mode,
		ChunkSizeMB: 1,
		NotifyOff:   notifyOff,
	}
}

func mustGenerateModule(t *testing.T, req *ModuleRequest) string {
	t.Helper()
	out, err := GenerateModule(req, "https://toolbox.example", "direct")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Surge (and Shadowrocket, which installs the same .sgmodule) declares the
// notify argument with #!arguments and hands it to every upload script as
// argument="notify={{{notify}}}"
// (https://manual.nssurge.com/profile/module.html).
func TestSurgeModuleDeclaresTheNotifyArgument(t *testing.T) {
	useStashTestHosts(t)
	for _, tc := range []struct {
		notifyOff bool
		want      string
	}{{false, "true"}, {true, "false"}} {
		module := mustGenerateModule(t, notifyModuleRequest(ProxyAppSurge, UploadModeScript, tc.notifyOff))
		if !strings.Contains(module, "#!arguments=notify:"+tc.want+"\n") {
			t.Fatalf("NotifyOff=%t: module lacks #!arguments=notify:%s:\n%s", tc.notifyOff, tc.want, module)
		}
		scripts := regexp.MustCompile(`(?m)^haruki-upload-\d+ = .*$`).FindAllString(module, -1)
		if len(scripts) == 0 {
			t.Fatal("no script lines")
		}
		for _, line := range scripts {
			if !strings.HasSuffix(line, `,argument="notify={{{notify}}}"`) {
				t.Errorf("script line does not pass the argument: %s", line)
			}
			if strings.Contains(line, "&notify=false") != tc.notifyOff {
				t.Errorf("NotifyOff=%t: script URL fallback wrong: %s", tc.notifyOff, line)
			}
		}
	}
}

// Loon declares a switch in [Argument] (Build 733+) and passes it with the
// script's argument={notify}, so the script gets $argument.notify
// (https://nsloon.app/docs/Plugin/, https://nsloon.app/docs/Script/script_v2).
func TestLoonPluginDeclaresTheNotifyArgument(t *testing.T) {
	useStashTestHosts(t)
	for _, tc := range []struct {
		notifyOff bool
		want      string
	}{{false, "true"}, {true, "false"}} {
		plugin := mustGenerateModule(t, notifyModuleRequest(ProxyAppLoon, UploadModeScript, tc.notifyOff))
		if !strings.Contains(plugin, "[Argument]\nnotify = switch,"+tc.want+",tag=上传通知,desc=") {
			t.Fatalf("NotifyOff=%t: plugin lacks the switch:\n%s", tc.notifyOff, plugin)
		}
		if strings.Index(plugin, "[Argument]") > strings.Index(plugin, "[Script]") {
			t.Fatal("[Argument] comes after [Script]")
		}
		scripts := regexp.MustCompile(`(?m)^http-response .*$`).FindAllString(plugin, -1)
		if len(scripts) == 0 {
			t.Fatal("no script lines")
		}
		for _, line := range scripts {
			if !strings.HasSuffix(line, ", argument={notify}") {
				t.Errorf("script line does not pass the argument: %s", line)
			}
			// Commas separate Loon options; the description must not add any.
			if desc := plugin[strings.Index(plugin, "desc="):]; strings.Contains(desc[:strings.Index(desc, "\n")], ",") {
				t.Errorf("the argument description contains a comma")
			}
		}
	}
}

// proxyNotifyLine is the notification script one proxy-mode module declares:
// its URL pattern and its script URL, as the app reads them.
type proxyNotifyLine struct {
	pattern, scriptURL string
}

// Each app's proxy-mode module declares exactly one body-less notification
// script and the notify switch, and runs no other script. Shadowrocket installs
// the Surge module. The per-app docs are cited at RuleSet.ProxyNotify.
var proxyNotifyModuleChecks = map[ProxyApp]func(t *testing.T, module, wantDefault string) proxyNotifyLine{
	ProxyAppSurge: func(t *testing.T, module, wantDefault string) proxyNotifyLine {
		if !strings.Contains(module, "#!arguments=notify:"+wantDefault+"\n") {
			t.Fatalf("module lacks #!arguments=notify:%s:\n%s", wantDefault, module)
		}
		if n := strings.Count(module, "type=http-response"); n != 1 {
			t.Fatalf("%d http-response scripts, want the one notification script:\n%s", n, module)
		}
		m := regexp.MustCompile(`(?m)^haruki-upload-notify = type=http-response,pattern=([^,]+),requires-body=0,timeout=10,script-path=([^,]+),argument="notify=\{\{\{notify\}\}\}"$`).FindStringSubmatch(module)
		if m == nil {
			t.Fatalf("no body-less notification script line:\n%s", module)
		}
		return proxyNotifyLine{m[1], m[2]}
	},
	ProxyAppLoon: func(t *testing.T, module, wantDefault string) proxyNotifyLine {
		if !strings.Contains(module, "[Argument]\nnotify = switch,"+wantDefault+",") {
			t.Fatalf("plugin lacks the switch:\n%s", module)
		}
		lines := regexp.MustCompile(`(?m)^http-response .*$`).FindAllString(module, -1)
		if len(lines) != 1 {
			t.Fatalf("script lines = %q, want the one notification script", lines)
		}
		m := regexp.MustCompile(`^http-response (\S+) script-path=([^,]+), requires-body=false, timeout=10, tag=haruki-upload-notify, argument=\{notify\}$`).FindStringSubmatch(lines[0])
		if m == nil {
			t.Fatalf("unexpected notification script line: %s", lines[0])
		}
		return proxyNotifyLine{m[1], m[2]}
	},
	ProxyAppQuantumultX: func(t *testing.T, module, wantDefault string) proxyNotifyLine {
		lines := regexp.MustCompile(`(?m)^\S+ url script-.*$`).FindAllString(module, -1)
		if len(lines) != 1 {
			t.Fatalf("script lines = %q, want the one notification script", lines)
		}
		m := regexp.MustCompile(`^(\S+) url script-response-header (\S+)$`).FindStringSubmatch(lines[0])
		if m == nil {
			t.Fatalf("not a script-response-header line: %s", lines[0])
		}
		// The switch rides in the fragment, which the script reads.
		scriptURL, fragment, ok := strings.Cut(m[2], "#")
		if !ok || fragment != "notify="+wantDefault {
			t.Fatalf("script URL %s lacks #notify=%s", m[2], wantDefault)
		}
		// The game follows the 307 to our host over HTTPS: Quantumult X
		// must decrypt it to run the script.
		if !strings.Contains(module, "\nhostname = jp.example.com, toolbox.example, submit.backtrace.io\n") {
			t.Fatalf("hostname line lacks the toolbox host:\n%s", module)
		}
		return proxyNotifyLine{m[1], scriptURL}
	},
	ProxyAppStash: func(t *testing.T, module, wantDefault string) proxyNotifyLine {
		var doc struct {
			ArgumentsVersion int `yaml:"arguments-version"`
			Arguments        map[string]struct {
				Type    string `yaml:"type"`
				Default bool   `yaml:"default"`
			} `yaml:"arguments"`
			HTTP struct {
				MITM   []string `yaml:"mitm"`
				Script []struct {
					Match       string `yaml:"match"`
					Name        string `yaml:"name"`
					Type        string `yaml:"type"`
					RequireBody *bool  `yaml:"require-body"`
					Timeout     int    `yaml:"timeout"`
					Argument    string `yaml:"argument"`
				} `yaml:"script"`
			} `yaml:"http"`
			ScriptProviders map[string]struct {
				URL string `yaml:"url"`
			} `yaml:"script-providers"`
		}
		if err := yaml.Unmarshal([]byte(module), &doc); err != nil {
			t.Fatal(err)
		}
		arg, ok := doc.Arguments["notify"]
		if doc.ArgumentsVersion != 1 || !ok || arg.Type != "switch" || strconv.FormatBool(arg.Default) != wantDefault {
			t.Fatalf("arguments = %d %+v, want a notify switch defaulting to %s", doc.ArgumentsVersion, doc.Arguments, wantDefault)
		}
		if len(doc.HTTP.Script) != 1 {
			t.Fatalf("scripts = %+v, want the one notification script", doc.HTTP.Script)
		}
		s := doc.HTTP.Script[0]
		if s.Type != "response" || s.RequireBody == nil || *s.RequireBody || s.Timeout != 10 || s.Argument != "notify=${args.notify}" {
			t.Fatalf("unexpected notification script: %+v", s)
		}
		if slices.Contains(doc.HTTP.MITM, "toolbox.example") {
			t.Fatalf("the transparent rewrite needs no MitM of the toolbox host: %v", doc.HTTP.MITM)
		}
		return proxyNotifyLine{s.Match, doc.ScriptProviders[s.Name].URL}
	},
}

// In proxy mode every module runs no upload script, only the body-less
// notification script on our upload URL, switched by the notify argument. Its
// pattern matches every rewrite target and nothing else: in particular not the
// game API, where it could take the place of another module's script (Surge
// runs one http-response script per request).
func TestProxyModulesNotifyFromOurUploadURL(t *testing.T) {
	useStashTestHosts(t)
	for app, check := range proxyNotifyModuleChecks {
		for _, notifyOff := range []bool{false, true} {
			t.Run(string(app)+"/notifyOff="+strconv.FormatBool(notifyOff), func(t *testing.T) {
				req := notifyModuleRequest(app, UploadModeProxy, notifyOff)
				req.DataTypes = []DataType{DataTypeSuite, DataTypeMysekaiForce, DataTypeMysekaiBirthdayParty}
				line := check(t, mustGenerateModule(t, req), strconv.FormatBool(!notifyOff))
				wantURL := "https://toolbox.example" + ProxyNotifyScriptPath + "?v=" + ProxyNotifyScriptVersion
				if notifyOff {
					wantURL += "&notify=false"
				}
				if line.scriptURL != wantURL {
					t.Errorf("script URL = %s, want %s", line.scriptURL, wantURL)
				}
				pattern, err := regexp.Compile(line.pattern)
				if err != nil {
					t.Fatalf("pattern %s: %v", line.pattern, err)
				}
				rs := GenerateRuleSet(req, "https://toolbox.example", "direct")
				if len(rs.RewriteRules) == 0 {
					t.Fatal("no rewrite rules")
				}
				for _, rule := range rs.RewriteRules {
					target := strings.NewReplacer("$1", "123", "$2", "").Replace(rule.Target)
					if !pattern.MatchString(target) {
						t.Errorf("pattern %s misses the rewrite target %s", pattern, target)
					}
				}
				for _, other := range []string{
					"https://jp.example.com/api/suite/user/1",
					"https://jp.example.com/api/user/1/mysekai?isForceAllReloadOnlyMysekai=True",
					"https://toolbox.example/ios/script/code/haruki-toolbox.js",
					"https://toolbox.example/ios/script/proxy-notify.js?v=1.1.0",
					"https://toolboxXexample/ios/proxy/jp/suite/user/1",
					"https://evil.example/?https://toolbox.example/ios/proxy/jp/suite/user/1",
				} {
					if pattern.MatchString(other) {
						t.Errorf("pattern %s also matches %s", pattern, other)
					}
				}
			})
		}
	}
}

// Stash declares an override argument (arguments-version 1, a switch) and
// passes it as the script argument "notify=${args.notify}"; a reference inside
// other text becomes a string (https://stash.wiki/configuration/override).
func TestStashOverrideDeclaresTheNotifyArgument(t *testing.T) {
	useStashTestHosts(t)
	for _, notifyOff := range []bool{false, true} {
		var doc struct {
			ArgumentsVersion int `yaml:"arguments-version"`
			Arguments        map[string]struct {
				Type    string `yaml:"type"`
				Title   string `yaml:"title"`
				Default bool   `yaml:"default"`
			} `yaml:"arguments"`
			HTTP struct {
				Script []struct {
					Argument string `yaml:"argument"`
				} `yaml:"script"`
			} `yaml:"http"`
		}
		if err := yaml.Unmarshal([]byte(mustGenerateModule(t, notifyModuleRequest(ProxyAppStash, UploadModeScript, notifyOff))), &doc); err != nil {
			t.Fatal(err)
		}
		arg, ok := doc.Arguments["notify"]
		if doc.ArgumentsVersion != 1 || !ok || arg.Type != "switch" || arg.Title == "" || arg.Default != !notifyOff {
			t.Fatalf("NotifyOff=%t: arguments = %d %+v", notifyOff, doc.ArgumentsVersion, doc.Arguments)
		}
		if len(doc.HTTP.Script) == 0 {
			t.Fatal("no scripts")
		}
		for _, s := range doc.HTTP.Script {
			if s.Argument != "notify=${args.notify}" {
				t.Errorf("script argument = %q", s.Argument)
			}
		}
	}
}

// End to end for each app: what the app substitutes into the module reaches
// the script as $argument, and the script follows it.
func TestModuleArgumentsReachTheUploaderScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	useStashTestHosts(t)
	const suiteURL = "https://jp.example.com/api/suite/user/1"
	for _, tc := range []struct {
		name     string
		argument string // $argument as the app passes it
		want     int    // notifications
	}{
		{"surge default", `"notify=true"`, 1},
		{"surge turned off", `"notify=false"`, 0},
		{"surge without argument support", `"notify={{{notify}}}"`, 1},
		{"loon default", `{ notify: true }`, 1},
		{"loon turned off", `{ notify: false }`, 0},
		{"loon without [Argument] support", `"{notify}"`, 1},
		{"stash default", `"notify=true"`, 1},
		{"stash turned off", `"notify=false"`, 0},
		{"stash without argument support", `"notify=${args.notify}"`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := runUploaderScript(t, node, uploaderRun{notifyByDefault: true, argument: tc.argument, url: suiteURL, bodyBytes: 10, respond: `() => ({ status: 200 })`})
			if len(run.Notifications) != tc.want || run.Done != 1 {
				t.Fatalf("notifications = %q, done = %d; want %d and 1", run.Notifications, run.Done, tc.want)
			}
		})
	}
}
