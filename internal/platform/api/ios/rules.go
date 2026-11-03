package ios

import (
	"fmt"
	"regexp"
	"strings"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
)

func getHarukiIOSMitMHostnameMapping() map[harukiUtils.SupportedDataUploadServer][]string {
	cfg := harukiConfig.Cfg.SekaiClient
	return map[harukiUtils.SupportedDataUploadServer][]string{
		harukiUtils.SupportedDataUploadServerJP: {cfg.JPServerAPIHost},
		harukiUtils.SupportedDataUploadServerEN: {cfg.ENServerAPIHost},
		harukiUtils.SupportedDataUploadServerTW: {cfg.TWServerAPIHost, cfg.TWServerAPIHost2},
		harukiUtils.SupportedDataUploadServerKR: {cfg.KRServerAPIHost, cfg.KRServerAPIHost2},
		harukiUtils.SupportedDataUploadServerCN: {cfg.CNServerAPIHost, cfg.CNServerAPIHost2},
	}
}

type Rule struct {
	Pattern     string
	Target      string
	RuleType    string
	Description string
}

type RuleSet struct {
	RewriteRules []Rule
	ScriptRules  []Rule
	Hostnames    []string
	// ProxyNotify, set in proxy mode, is a body-less response script that
	// reports the upload from the response headers of our upload URL. Its
	// pattern matches only our upload URLs (the rewrite targets), never the
	// game's, so it cannot take the place of another module's script on the
	// game API: Surge runs only the first matching http-response script per
	// request (https://manual.nssurge.com/scripting/http-response.html).
	// Every app sees our URL:
	//   - Loon (header rewrite): response scripts match the rewritten URL
	//     (https://nsloon.app/docs/Script/script_v2).
	//   - Surge and Shadowrocket (header rewrite, the same .sgmodule): URL
	//     Rewrite runs before script processing
	//     (https://manual.nssurge.com/http/overview.html); Shadowrocket has no
	//     official docs and follows Surge.
	//   - Stash (transparent rewrite): the docs do not say which URL a script
	//     matches after a transparent rewrite (https://stash.wiki/http-engine/rewrite);
	//     not verified on a device, so the frontend does not offer the switch.
	//   - Quantumult X (307 redirect): the game itself requests our URL; the
	//     module adds our host to its MitM hostnames, without which the script
	//     never sees the HTTPS response.
	ProxyNotify *Rule
}

func GetHostnames(regions []harukiUtils.SupportedDataUploadServer) []string {
	mapping := getHarukiIOSMitMHostnameMapping()
	var hostnames []string
	seen := make(map[string]bool)
	for _, region := range regions {
		hosts := mapping[region]
		for _, h := range hosts {
			h = strings.TrimSpace(h)
			if h == "" {
				continue
			}
			if !seen[h] {
				seen[h] = true
				hostnames = append(hostnames, h)
			}
		}
	}
	return hostnames
}

func GenerateRuleSet(req *ModuleRequest, endpoint string, endpointType string) *RuleSet {
	mapping := getHarukiIOSMitMHostnameMapping()
	rs := &RuleSet{
		Hostnames: GetHostnames(req.Regions),
	}
	for _, region := range req.Regions {
		hosts := mapping[region]
		for _, host := range hosts {
			host = strings.TrimSpace(host)
			if host == "" {
				continue
			}
			for _, dt := range req.DataTypes {
				rules := generateRulesForDataType(host, string(region), dt, req.Mode, req.UploadCode, endpoint, req.ChunkSizeMB, endpointType)
				rs.RewriteRules = append(rs.RewriteRules, rules.RewriteRules...)
				rs.ScriptRules = append(rs.ScriptRules, rules.ScriptRules...)
			}
		}
	}
	if req.Mode == UploadModeProxy {
		rs.ProxyNotify = &Rule{
			Pattern:  "^" + regexp.QuoteMeta(endpoint) + "/ios/proxy/",
			Target:   endpoint + ProxyNotifyScriptPath + "?v=" + ProxyNotifyScriptVersion,
			RuleType: "script",
		}
	}
	if req.NotifyOff {
		// The fallback for apps that pass the script no notify argument.
		for i := range rs.ScriptRules {
			rs.ScriptRules[i].Target += "&notify=false"
		}
		if rs.ProxyNotify != nil {
			rs.ProxyNotify.Target += "&notify=false"
		}
	}
	return rs
}

func generateRulesForDataType(host, region string, dt DataType, mode UploadMode, uploadCode, endpoint string, chunkSizeMB int, endpointType string) *RuleSet {
	rs := &RuleSet{}
	escapedHost := strings.ReplaceAll(host, ".", "\\.")
	// v names the script version, so a module update after a script change
	// loads the new script instead of the copy the proxy app cached by URL.
	scriptURL := fmt.Sprintf("%s/ios/script/%s/haruki-toolbox.js?chunk=%d&endpoint=%s&v=%s", endpoint, uploadCode, chunkSizeMB, endpointType, ScriptVersion)
	switch dt {
	case DataTypeSuite:
		pattern := fmt.Sprintf(`^https://%s/api/suite/user/(\d+)(\?isLogin=true)?$`, escapedHost)
		if mode == UploadModeProxy {
			target := fmt.Sprintf("%s/ios/proxy/%s/suite/user/$1$2", endpoint, region)
			rs.RewriteRules = append(rs.RewriteRules, Rule{
				Pattern:  pattern,
				Target:   target,
				RuleType: "redirect",
			})
		} else {
			rs.ScriptRules = append(rs.ScriptRules, Rule{
				Pattern:  pattern,
				Target:   scriptURL,
				RuleType: "script",
			})
		}
	case DataTypeMysekai:
		pattern := fmt.Sprintf(`^https://%s/api/user/(\d+)/mysekai\?isForceAllReloadOnlyMysekai=(True|False)`, escapedHost)
		if mode == UploadModeProxy {
			target := fmt.Sprintf("%s/ios/proxy/%s/user/$1/mysekai?isForceAllReloadOnlyMysekai=$2", endpoint, region)
			rs.RewriteRules = append(rs.RewriteRules, Rule{
				Pattern:  pattern,
				Target:   target,
				RuleType: "redirect",
			})
		} else {
			rs.ScriptRules = append(rs.ScriptRules, Rule{
				Pattern:  pattern,
				Target:   scriptURL,
				RuleType: "script",
			})
		}
	case DataTypeMysekaiForce:
		patternFalse := fmt.Sprintf(`^https://%s/api/user/(\d+)/mysekai\?isForceAllReloadOnlyMysekai=False`, escapedHost)
		targetTrue := fmt.Sprintf("https://%s/api/user/$1/mysekai?isForceAllReloadOnlyMysekai=True", host)
		if mode == UploadModeProxy {
			pattern := fmt.Sprintf(`^https://%s/api/user/(\d+)/mysekai\?isForceAllReloadOnlyMysekai=(True|False)`, escapedHost)
			target := fmt.Sprintf("%s/ios/proxy/%s/user/$1/mysekai?isForceAllReloadOnlyMysekai=True", endpoint, region)
			rs.RewriteRules = append(rs.RewriteRules, Rule{
				Pattern:     pattern,
				Target:      target,
				RuleType:    "redirect",
				Description: "mysekai_force: redirect to backend",
			})
		} else {
			rs.RewriteRules = append(rs.RewriteRules, Rule{
				Pattern:     patternFalse,
				Target:      targetTrue,
				RuleType:    "rewrite",
				Description: "mysekai_force: rewrite False to True",
			})
			patternTrue := fmt.Sprintf(`^https://%s/api/user/(\d+)/mysekai\?isForceAllReloadOnlyMysekai=True`, escapedHost)
			rs.ScriptRules = append(rs.ScriptRules, Rule{
				Pattern:     patternTrue,
				Target:      scriptURL,
				RuleType:    "script",
				Description: "mysekai_force: upload on True",
			})
		}
	case DataTypeMysekaiBirthdayParty:
		pattern := fmt.Sprintf(`^https://%s/api/user/(\d+)/mysekai/birthday-party/(\d+)/delivery`, escapedHost)
		if mode == UploadModeProxy {
			target := fmt.Sprintf("%s/ios/proxy/%s/user/$1/mysekai/birthday-party/$2/delivery", endpoint, region)
			rs.RewriteRules = append(rs.RewriteRules, Rule{
				Pattern:  pattern,
				Target:   target,
				RuleType: "redirect",
			})
		} else {
			rs.ScriptRules = append(rs.ScriptRules, Rule{
				Pattern:  pattern,
				Target:   scriptURL,
				RuleType: "script",
			})
		}
	}
	return rs
}
