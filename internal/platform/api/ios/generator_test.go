package ios

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestURLRewriteSyntaxByApp(t *testing.T) {
	apps := []struct {
		name     string
		generate func(*ModuleRequest, *RuleSet) string
		format   string
	}{
		{"Surge", generateSurgeModule, "%s %s header"},
		{"Loon", generateLoonModule, "%s header %s"},
		{"QuantumultX", generateQuantumultXModule, "%s url 307 %s"},
		{"Stash", generateStashModule, "    - %s %s transparent"},
	}
	for _, dt := range []DataType{DataTypeSuite, DataTypeMysekai, DataTypeMysekaiForce, DataTypeMysekaiBirthdayParty} {
		for _, mode := range []UploadMode{UploadModeProxy, UploadModeScript} {
			rs := generateRulesForDataType("tw.example.com", "tw", dt, mode, "code", "https://toolbox.example", 1, "direct")
			for _, app := range apps {
				t.Run(app.name+"/"+string(dt)+"/"+string(mode), func(t *testing.T) {
					out := app.generate(&ModuleRequest{DataTypes: []DataType{dt}, Mode: mode}, rs)
					for _, rule := range rs.RewriteRules {
						if strings.Contains(rule.Target, " ") {
							t.Fatalf("target contains action: %q", rule.Target)
						}
						want := fmt.Sprintf(app.format, rule.Pattern, rule.Target) + "\n"
						if !strings.Contains(out, want) {
							t.Fatalf("missing %q in %s", want, out)
						}
					}
					if app.name != "QuantumultX" && strings.Contains(out, " 307") {
						t.Fatalf("unexpected redirect: %s", out)
					}
					if app.name == "Stash" && mode == UploadModeProxy {
						var parsed struct {
							HTTP struct {
								Rules []string `yaml:"url-rewrite"`
							} `yaml:"http"`
						}
						if err := yaml.Unmarshal([]byte(out), &parsed); err != nil {
							t.Fatal(err)
						}
						if len(parsed.HTTP.Rules) != len(rs.RewriteRules)+1 {
							t.Fatalf("missing Stash URL rules: %s", out)
						}
					}
				})
			}
		}
	}
}

func TestTransparentRewritePreservesCapturedPaths(t *testing.T) {
	for _, tc := range []struct {
		dt         DataType
		path, want string
	}{
		{DataTypeSuite, "/suite/user/123?isLogin=true", "/suite/user/123?isLogin=true"},
		{DataTypeMysekai, "/user/123/mysekai?isForceAllReloadOnlyMysekai=False", "/user/123/mysekai?isForceAllReloadOnlyMysekai=False"},
		{DataTypeMysekaiForce, "/user/123/mysekai?isForceAllReloadOnlyMysekai=False", "/user/123/mysekai?isForceAllReloadOnlyMysekai=True"},
		{DataTypeMysekaiBirthdayParty, "/user/123/mysekai/birthday-party/456/delivery", "/user/123/mysekai/birthday-party/456/delivery"},
	} {
		rs := generateRulesForDataType("tw.example.com", "tw", tc.dt, UploadModeProxy, "code", "https://toolbox.example", 1, "direct")
		rule := rs.RewriteRules[0]
		got := regexp.MustCompile(rule.Pattern).ReplaceAllString("https://tw.example.com/api"+tc.path, rule.Target)
		if want := "https://toolbox.example/ios/proxy/tw" + tc.want; got != want {
			t.Errorf("%s: got %q want %q", tc.dt, got, want)
		}
	}
}
