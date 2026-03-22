package ios

import (
	harukiConfig "haruki-suite/config"
	harukiUtils "haruki-suite/utils"
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
