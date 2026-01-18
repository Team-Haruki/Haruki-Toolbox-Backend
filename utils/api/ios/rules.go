package ios

import (
	"fmt"
	harukiConfig "haruki-suite/config"
	harukiUtils "haruki-suite/utils"
	"strings"
)

var HarukiIOSMitMHostnameMapping = map[harukiUtils.SupportedDataUploadServer][]string{
	harukiUtils.SupportedDataUploadServerJP: {harukiConfig.Cfg.SekaiClient.JPServerAPIHost},
	harukiUtils.SupportedDataUploadServerEN: {harukiConfig.Cfg.SekaiClient.ENServerAPIHost},
	harukiUtils.SupportedDataUploadServerTW: {harukiConfig.Cfg.SekaiClient.TWServerAPIHost, harukiConfig.Cfg.SekaiClient.TWServerAPIHost2},
	harukiUtils.SupportedDataUploadServerKR: {harukiConfig.Cfg.SekaiClient.KRServerAPIHost, harukiConfig.Cfg.SekaiClient.KRServerAPIHost2},
	harukiUtils.SupportedDataUploadServerCN: {harukiConfig.Cfg.SekaiClient.CNServerAPIHost, harukiConfig.Cfg.SekaiClient.CNServerAPIHost2},
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
}

func GetHostnames(regions []harukiUtils.SupportedDataUploadServer) []string {
	var hostnames []string
	seen := make(map[string]bool)
	for _, region := range regions {
		hosts := HarukiIOSMitMHostnameMapping[region]
		for _, h := range hosts {
			if !seen[h] {
				seen[h] = true
				hostnames = append(hostnames, h)
			}
		}
	}
	return hostnames
}

func GenerateRuleSet(req *ModuleRequest, endpoint string, endpointType string) *RuleSet {
	rs := &RuleSet{
		Hostnames: GetHostnames(req.Regions),
	}
	for _, region := range req.Regions {
		hosts := HarukiIOSMitMHostnameMapping[region]
		for _, host := range hosts {
			for _, dt := range req.DataTypes {
				rules := generateRulesForDataType(host, string(region), dt, req.Mode, req.UploadCode, endpoint, req.ChunkSizeMB, endpointType)
				rs.RewriteRules = append(rs.RewriteRules, rules.RewriteRules...)
				rs.ScriptRules = append(rs.ScriptRules, rules.ScriptRules...)
			}
		}
	}
	return rs
}

func generateRulesForDataType(host, region string, dt DataType, mode UploadMode, uploadCode, endpoint string, chunkSizeMB int, endpointType string) *RuleSet {
	rs := &RuleSet{}
	escapedHost := strings.ReplaceAll(host, ".", "\\.")
	scriptURL := fmt.Sprintf("%s/ios/script/%s/haruki-toolbox.js?chunk=%d&endpoint=%s", endpoint, uploadCode, chunkSizeMB, endpointType)
	switch dt {
	case DataTypeSuite:
		pattern := fmt.Sprintf(`^https://%s/api/suite/user/(\d+)$`, escapedHost)
		if mode == UploadModeProxy {
			target := fmt.Sprintf("%s/ios/proxy/%s/suite/user/$1 307", endpoint, region)
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
			target := fmt.Sprintf("%s/ios/proxy/%s/user/$1/mysekai?isForceAllReloadOnlyMysekai=$2 307", endpoint, region)
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
			target := fmt.Sprintf("%s/ios/proxy/%s/user/$1/mysekai?isForceAllReloadOnlyMysekai=True 307", endpoint, region)
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
			target := fmt.Sprintf("%s/ios/proxy/%s/user/$1/mysekai/birthday-party/$2/delivery 307", endpoint, region)
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
