package ios

import (
	"bytes"
	"regexp"
	"slices"
	"strings"
	"testing"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"gopkg.in/yaml.v3"
)

// Documented Stash override keys: metadata (https://stash.wiki/configuration/override),
// http.mitm (https://stash.wiki/http-engine/mitm), http.url-rewrite
// (https://stash.wiki/http-engine/rewrite), http.script
// (https://stash.wiki/script/rewrite-requests) and script-providers
// (https://stash.wiki/script/manage-script).
var (
	stashTopLevelKeys = []string{"name", "desc", "author", "homepage", "date", "http", "script-providers"}
	stashHTTPKeys     = []string{"mitm", "url-rewrite", "script"}
	stashScriptKeys   = []string{"match", "name", "type", "require-body", "binary-mode", "max-size", "timeout"}
	stashProviderKeys = []string{"url", "interval"}
)

func mapKeys(t *testing.T, v any) []string {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("expected a mapping, got %T", v)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func assertKeysAllowed(t *testing.T, where string, got, allowed []string) {
	t.Helper()
	for _, k := range got {
		if !slices.Contains(allowed, k) {
			t.Fatalf("%s: unexpected key %q (allowed %v)", where, k, allowed)
		}
	}
}

func useStashTestHosts(t *testing.T) {
	t.Helper()
	original := harukiConfig.Cfg
	t.Cleanup(func() { harukiConfig.Cfg = original })
	harukiConfig.Cfg.SekaiClient.JPServerAPIHost = "jp.example.com"
	harukiConfig.Cfg.SekaiClient.TWServerAPIHost = "tw.example.com"
	harukiConfig.Cfg.SekaiClient.TWServerAPIHost2 = "tw2.example.com"
}

func TestStashOverrideMatchesDocumentedStructure(t *testing.T) {
	useStashTestHosts(t)
	regions := []harukiUtils.SupportedDataUploadServer{harukiUtils.SupportedDataUploadServerJP, harukiUtils.SupportedDataUploadServerTW}
	allTypes := []DataType{DataTypeSuite, DataTypeMysekai, DataTypeMysekaiForce, DataTypeMysekaiBirthdayParty}
	cases := [][]DataType{{DataTypeSuite, DataTypeMysekaiForce, DataTypeMysekaiBirthdayParty}}
	for _, dt := range allTypes {
		cases = append(cases, []DataType{dt})
	}
	for _, mode := range []UploadMode{UploadModeProxy, UploadModeScript} {
		for _, dataTypes := range cases {
			req := &ModuleRequest{UploadCode: "code", Regions: regions, DataTypes: dataTypes, App: ProxyAppStash, Mode: mode, ChunkSizeMB: 2}
			t.Run(string(mode)+"/"+string(dataTypes[0])+"/"+string(rune('0'+len(dataTypes))), func(t *testing.T) {
				out, err := GenerateModule(req, "https://toolbox.example", "direct")
				if err != nil {
					t.Fatal(err)
				}
				rs := GenerateRuleSet(req, "https://toolbox.example", "direct")
				checkStashOverride(t, out, rs)
			})
		}
	}
}

func checkStashOverride(t *testing.T, out string, rs *RuleSet) {
	t.Helper()
	if strings.HasPrefix(out, "\uFEFF") || strings.Contains(out, "\r") {
		t.Fatalf("output must be UTF-8 without BOM and with LF line endings")
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "[") || strings.HasPrefix(line, "#!") {
			t.Fatalf("Surge/Loon-style line in Stash override: %q", line)
		}
	}

	var doc map[string]any
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output is not valid YAML: %v\n%s", err, out)
	}
	assertKeysAllowed(t, "top level", mapKeys(t, doc), stashTopLevelKeys)
	if name, _ := doc["name"].(string); name == "" {
		t.Fatalf("missing name: %s", out)
	}
	if desc, _ := doc["desc"].(string); desc == "" {
		t.Fatalf("missing desc: %s", out)
	}

	httpSection, ok := doc["http"].(map[string]any)
	if !ok {
		t.Fatalf("missing http mapping: %s", out)
	}
	assertKeysAllowed(t, "http", mapKeys(t, httpSection), stashHTTPKeys)

	var parsed struct {
		HTTP struct {
			MITM       []string `yaml:"mitm"`
			URLRewrite []string `yaml:"url-rewrite"`
			Script     []struct {
				Match       string `yaml:"match"`
				Name        string `yaml:"name"`
				Type        string `yaml:"type"`
				RequireBody bool   `yaml:"require-body"`
				BinaryMode  bool   `yaml:"binary-mode"`
				MaxSize     int    `yaml:"max-size"`
				Timeout     int    `yaml:"timeout"`
			} `yaml:"script"`
		} `yaml:"http"`
		ScriptProviders map[string]struct {
			URL      string `yaml:"url"`
			Interval int    `yaml:"interval"`
		} `yaml:"script-providers"`
	}
	if err := yaml.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatal(err)
	}

	wantMITM := append(append([]string{}, rs.Hostnames...), "submit.backtrace.io")
	if !slices.Equal(parsed.HTTP.MITM, wantMITM) {
		t.Fatalf("mitm = %v, want %v", parsed.HTTP.MITM, wantMITM)
	}
	for _, h := range parsed.HTTP.MITM {
		if strings.ContainsAny(h, " ,%") {
			t.Fatalf("mitm entries must be bare hostnames, got %q", h)
		}
	}

	var wantRewrites []string
	for _, rule := range rs.RewriteRules {
		wantRewrites = append(wantRewrites, rule.Pattern+" "+rule.Target+" transparent")
	}
	wantRewrites = append(wantRewrites, `^https:\/\/submit\.backtrace\.io\/ - reject`)
	if !slices.Equal(parsed.HTTP.URLRewrite, wantRewrites) {
		t.Fatalf("url-rewrite = %q, want %q", parsed.HTTP.URLRewrite, wantRewrites)
	}
	for _, entry := range parsed.HTTP.URLRewrite {
		fields := strings.Fields(entry)
		if len(fields) != 3 || !slices.Contains([]string{"transparent", "302", "307", "reject"}, fields[2]) {
			t.Fatalf("url-rewrite entry must be `regex target action`: %q", entry)
		}
		if _, err := regexp.Compile(fields[0]); err != nil {
			t.Fatalf("url-rewrite regex %q: %v", fields[0], err)
		}
	}

	if len(parsed.HTTP.Script) != len(rs.ScriptRules) {
		t.Fatalf("script entries = %d, want %d", len(parsed.HTTP.Script), len(rs.ScriptRules))
	}
	if len(rs.ScriptRules) == 0 {
		if _, ok := doc["script-providers"]; ok {
			t.Fatalf("script-providers without scripts: %s", out)
		}
		return
	}
	rawScripts, _ := httpSection["script"].([]any)
	for i, raw := range rawScripts {
		keys := mapKeys(t, raw)
		assertKeysAllowed(t, "http.script", keys, stashScriptKeys)
		if len(keys) != len(stashScriptKeys) {
			t.Fatalf("http.script[%d] keys = %v, want %v", i, keys, stashScriptKeys)
		}
	}
	rawProviders, ok := doc["script-providers"].(map[string]any)
	if !ok {
		t.Fatalf("script entries need a top-level script-providers mapping: %s", out)
	}
	for name, raw := range rawProviders {
		assertKeysAllowed(t, "script-providers."+name, mapKeys(t, raw), stashProviderKeys)
	}
	for i, script := range parsed.HTTP.Script {
		rule := rs.ScriptRules[i]
		if script.Match != rule.Pattern {
			t.Fatalf("script match = %q, want %q (YAML round trip)", script.Match, rule.Pattern)
		}
		if _, err := regexp.Compile(script.Match); err != nil {
			t.Fatalf("script match %q: %v", script.Match, err)
		}
		if script.Type != "response" || !script.RequireBody || !script.BinaryMode || script.MaxSize <= 0 || script.Timeout <= 0 {
			t.Fatalf("unexpected script options: %+v", script)
		}
		provider, ok := parsed.ScriptProviders[script.Name]
		if !ok {
			t.Fatalf("script name %q has no script-providers entry", script.Name)
		}
		if provider.URL != rule.Target || provider.Interval <= 0 {
			t.Fatalf("provider %q = %+v, want url %q", script.Name, provider, rule.Target)
		}
	}
}

func TestStashScriptMatchesRealRequestURLs(t *testing.T) {
	useStashTestHosts(t)
	req := &ModuleRequest{
		UploadCode: "code",
		Regions:    []harukiUtils.SupportedDataUploadServer{harukiUtils.SupportedDataUploadServerJP},
		DataTypes:  []DataType{DataTypeSuite, DataTypeMysekaiForce},
		App:        ProxyAppStash,
		Mode:       UploadModeScript,
	}
	out, err := GenerateModule(req, "https://toolbox.example", "direct")
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		HTTP struct {
			Script []struct {
				Match string `yaml:"match"`
			} `yaml:"script"`
		} `yaml:"http"`
	}
	if err := yaml.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v\n%s", err, out)
	}
	urls := []string{
		"https://jp.example.com/api/suite/user/123?isLogin=true",
		"https://jp.example.com/api/user/123/mysekai?isForceAllReloadOnlyMysekai=True",
	}
	for i, script := range parsed.HTTP.Script {
		if !regexp.MustCompile(script.Match).MatchString(urls[i]) {
			t.Fatalf("script %d match %q does not match %q", i, script.Match, urls[i])
		}
	}
}

func TestStashOverrideStrictDecode(t *testing.T) {
	useStashTestHosts(t)
	req := &ModuleRequest{
		UploadCode: "code",
		Regions:    []harukiUtils.SupportedDataUploadServer{harukiUtils.SupportedDataUploadServerTW},
		DataTypes:  []DataType{DataTypeSuite, DataTypeMysekaiForce, DataTypeMysekaiBirthdayParty},
		App:        ProxyAppStash,
		Mode:       UploadModeScript,
	}
	out, err := GenerateModule(req, "https://toolbox.example", "cdn")
	if err != nil {
		t.Fatal(err)
	}
	dec := yaml.NewDecoder(bytes.NewReader([]byte(out)))
	dec.KnownFields(true)
	var override stashOverride
	if err := dec.Decode(&override); err != nil {
		t.Fatalf("strict decode: %v\n%s", err, out)
	}
	if len(override.ScriptProviders) != 1 {
		t.Fatalf("one shared script provider expected, got %v", override.ScriptProviders)
	}
}

func TestStashServedAsStoverride(t *testing.T) {
	req := &ModuleRequest{
		Regions:   []harukiUtils.SupportedDataUploadServer{harukiUtils.SupportedDataUploadServerJP},
		DataTypes: []DataType{DataTypeSuite},
		App:       ProxyAppStash,
	}
	if got := req.FileName(); !strings.HasSuffix(got, ".stoverride") {
		t.Fatalf("file name = %q", got)
	}
	if got := ProxyAppStash.ContentType(); !strings.HasPrefix(got, "text/yaml") || !strings.Contains(got, "charset=utf-8") {
		t.Fatalf("content type = %q", got)
	}
}
