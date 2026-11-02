package ios

import (
	"os/exec"
	"regexp"
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
	if proxy := mustGenerateModule(t, notifyModuleRequest(ProxyAppSurge, UploadModeProxy, false)); strings.Contains(proxy, "#!arguments") {
		t.Fatalf("a proxy-mode module, which runs no script, declares arguments:\n%s", proxy)
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
	if proxy := mustGenerateModule(t, notifyModuleRequest(ProxyAppLoon, UploadModeProxy, false)); strings.Contains(proxy, "[Argument]") {
		t.Fatalf("a proxy-mode plugin declares arguments:\n%s", proxy)
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
	proxy := mustGenerateModule(t, notifyModuleRequest(ProxyAppStash, UploadModeProxy, false))
	if strings.Contains(proxy, "arguments") {
		t.Fatalf("a proxy-mode override declares arguments:\n%s", proxy)
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
