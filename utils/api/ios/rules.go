package ios

import (
	"fmt"
	harukiUtils "haruki-suite/utils"
	"strings"
)

// Rule represents a single rewrite/script rule
type Rule struct {
	Pattern     string // Regex pattern
	Target      string // Redirect target or script URL
	RuleType    string // "redirect", "script", "reject"
	Description string // Optional comment
}

// RuleSet represents all rules for a module
type RuleSet struct {
	RewriteRules []Rule
	ScriptRules  []Rule
	Hostnames    []string
}

// GetHostnames returns hostnames for given regions
func GetHostnames(regions []Region) []string {
	var hostnames []string
	seen := make(map[string]bool)

	for _, region := range regions {
		hosts := IOSMitMHostnameMapping[harukiUtils.SupportedDataUploadServer(region)]
		for _, h := range hosts {
			if !seen[h] {
				seen[h] = true
				hostnames = append(hostnames, h)
			}
		}
	}
	return hostnames
}

// GenerateRuleSet generates all rules for the given request
func GenerateRuleSet(req *ModuleRequest, endpoint string) *RuleSet {
	rs := &RuleSet{
		Hostnames: GetHostnames(req.Regions),
	}

	for _, region := range req.Regions {
		hosts := IOSMitMHostnameMapping[harukiUtils.SupportedDataUploadServer(region)]
		for _, host := range hosts {
			for _, dt := range req.DataTypes {
				rules := generateRulesForDataType(host, string(region), dt, req.Mode, req.UploadCode, endpoint, req.ChunkSizeMB)
				rs.RewriteRules = append(rs.RewriteRules, rules.RewriteRules...)
				rs.ScriptRules = append(rs.ScriptRules, rules.ScriptRules...)
			}
		}
	}

	return rs
}

func generateRulesForDataType(host, region string, dt DataType, mode UploadMode, uploadCode, endpoint string, chunkSizeMB int) *RuleSet {
	rs := &RuleSet{}
	escapedHost := strings.ReplaceAll(host, ".", "\\.")

	// Build script URL with chunk size
	scriptURL := fmt.Sprintf("%s/ios/script/%s/haruki-toolbox.js?chunk=%d", endpoint, uploadCode, chunkSizeMB)

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
		// For script mode: two-step process
		// Step 1: Rewrite False -> True
		patternFalse := fmt.Sprintf(`^https://%s/api/user/(\d+)/mysekai\?isForceAllReloadOnlyMysekai=False`, escapedHost)
		targetTrue := fmt.Sprintf("https://%s/api/user/$1/mysekai?isForceAllReloadOnlyMysekai=True", host)

		if mode == UploadModeProxy {
			// Proxy mode: direct 307 redirect
			pattern := fmt.Sprintf(`^https://%s/api/user/(\d+)/mysekai\?isForceAllReloadOnlyMysekai=(True|False)`, escapedHost)
			target := fmt.Sprintf("%s/ios/proxy/%s/user/$1/mysekai?isForceAllReloadOnlyMysekai=True 307", endpoint, region)
			rs.RewriteRules = append(rs.RewriteRules, Rule{
				Pattern:     pattern,
				Target:      target,
				RuleType:    "redirect",
				Description: "mysekai_force: redirect to backend",
			})
		} else {
			// Script mode: Step 1 - rewrite False to True
			rs.RewriteRules = append(rs.RewriteRules, Rule{
				Pattern:     patternFalse,
				Target:      targetTrue,
				RuleType:    "rewrite",
				Description: "mysekai_force: rewrite False to True",
			})
			// Script mode: Step 2 - capture True and upload
			patternTrue := fmt.Sprintf(`^https://%s/api/user/(\d+)/mysekai\?isForceAllReloadOnlyMysekai=True`, escapedHost)
			rs.ScriptRules = append(rs.ScriptRules, Rule{
				Pattern:     patternTrue,
				Target:      scriptURL,
				RuleType:    "script",
				Description: "mysekai_force: upload on True",
			})
		}

	case DataTypeMysekaiBirthday:
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
