package ios

import (
	harukiConfig "haruki-suite/config"
	harukiUtils "haruki-suite/utils"
	"regexp"
	"strings"
	"testing"
)

func TestGetHostnamesReadsLatestConfig(t *testing.T) {
	originalCfg := harukiConfig.Cfg
	t.Cleanup(func() {
		harukiConfig.Cfg = originalCfg
	})

	harukiConfig.Cfg.SekaiClient.JPServerAPIHost = "jp-a.example.com"
	first := GetHostnames([]harukiUtils.SupportedDataUploadServer{harukiUtils.SupportedDataUploadServerJP})
	if len(first) != 1 || first[0] != "jp-a.example.com" {
		t.Fatalf("first hostnames = %#v, want [jp-a.example.com]", first)
	}

	harukiConfig.Cfg.SekaiClient.JPServerAPIHost = "jp-b.example.com"
	second := GetHostnames([]harukiUtils.SupportedDataUploadServer{harukiUtils.SupportedDataUploadServerJP})
	if len(second) != 1 || second[0] != "jp-b.example.com" {
		t.Fatalf("second hostnames = %#v, want [jp-b.example.com]", second)
	}
}

func TestGetHostnamesSkipsEmptyAndDeduplicates(t *testing.T) {
	originalCfg := harukiConfig.Cfg
	t.Cleanup(func() {
		harukiConfig.Cfg = originalCfg
	})

	harukiConfig.Cfg.SekaiClient.JPServerAPIHost = "jp.example.com"
	harukiConfig.Cfg.SekaiClient.TWServerAPIHost = ""
	harukiConfig.Cfg.SekaiClient.TWServerAPIHost2 = "tw.example.com"

	got := GetHostnames([]harukiUtils.SupportedDataUploadServer{
		harukiUtils.SupportedDataUploadServerJP,
		harukiUtils.SupportedDataUploadServerJP,
		harukiUtils.SupportedDataUploadServerTW,
	})
	if len(got) != 2 {
		t.Fatalf("len(hostnames) = %d, want 2; got=%#v", len(got), got)
	}
	if got[0] != "jp.example.com" || got[1] != "tw.example.com" {
		t.Fatalf("hostnames = %#v, want [jp.example.com tw.example.com]", got)
	}
}

func TestGenerateSuiteRulesMatchOptionalLoginQuery(t *testing.T) {
	t.Parallel()

	rules := generateRulesForDataType(
		"jp.example.com",
		"jp",
		DataTypeSuite,
		UploadModeProxy,
		"code",
		"https://toolbox.example",
		1,
		string(EndpointTypeDirect),
	)
	if len(rules.RewriteRules) != 1 {
		t.Fatalf("len(rewrite rules) = %d, want 1", len(rules.RewriteRules))
	}

	rule := rules.RewriteRules[0]
	re := regexp.MustCompile(rule.Pattern)
	for _, url := range []string{
		"https://jp.example.com/api/suite/user/123",
		"https://jp.example.com/api/suite/user/123?isLogin=true",
	} {
		if !re.MatchString(url) {
			t.Fatalf("suite pattern %q should match %q", rule.Pattern, url)
		}
	}
	if re.MatchString("https://jp.example.com/api/suite/user/123?foo=bar") {
		t.Fatalf("suite pattern %q should not match unsupported query", rule.Pattern)
	}
	if !strings.Contains(rule.Target, "/suite/user/$1$2 ") {
		t.Fatalf("suite proxy target should preserve optional query capture, got %q", rule.Target)
	}
}
