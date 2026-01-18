package ios

import (
	"fmt"
	"strings"
	"time"
)

func generateModuleNameAndDesc(req *ModuleRequest) (name, desc string) {
	var regionParts []string
	for _, r := range req.Regions {
		if n, ok := regionNames[r]; ok {
			regionParts = append(regionParts, n)
		}
	}
	regionsStr := strings.Join(regionParts, "/")
	var dataTypeParts []string
	for _, dt := range req.DataTypes {
		if n, ok := dataTypeNames[dt]; ok {
			dataTypeParts = append(dataTypeParts, n)
		}
	}
	dataTypesStr := strings.Join(dataTypeParts, "/")
	name = fmt.Sprintf("Haruki工具箱数据上传模块（%s）", regionsStr)
	desc = fmt.Sprintf("自动获取%s的%s数据，并上传至Haruki工具箱", regionsStr, dataTypesStr)
	return
}

func GenerateModule(req *ModuleRequest, endpoint string, endpointType string) (string, error) {
	ruleSet := GenerateRuleSet(req, endpoint, endpointType)
	switch req.App {
	case ProxyAppSurge:
		return generateSurgeModule(req, ruleSet), nil
	case ProxyAppLoon:
		return generateLoonModule(req, ruleSet), nil
	case ProxyAppQuantumultX:
		return generateQuantumultXModule(req, ruleSet), nil
	case ProxyAppStash:
		return generateStashModule(req, ruleSet), nil
	default:
		return "", fmt.Errorf("unsupported proxy app: %s", req.App)
	}
}

func GenerateScript(uploadCode string, chunkSizeMB int, uploadURL string) string {
	script := HarukiIOSJavaScriptTemplate
	script = strings.ReplaceAll(script, "{{UPLOAD_URL}}", uploadURL+"/ios/script/"+uploadCode+"/upload")
	script = strings.ReplaceAll(script, "{{CHUNK_SIZE}}", fmt.Sprintf("%d", chunkSizeMB))
	script = strings.ReplaceAll(script, "{{UPLOAD_CODE}}", uploadCode)
	script = strings.ReplaceAll(script, "{{GENERATE_DATE}}", time.Now().Format("2006-01-02 15:04:05"))
	script = strings.ReplaceAll(script, "\\u0060", "`")
	return script
}

func generateSurgeModule(req *ModuleRequest, rs *RuleSet) string {
	var sb strings.Builder
	name, desc := generateModuleNameAndDesc(req)
	sb.WriteString(fmt.Sprintf("#!name=%s\n", name))
	sb.WriteString(fmt.Sprintf("#!desc=%s\n", desc))
	sb.WriteString("#!homepage=https://haruki.seiunx.com/ios-modules\n")
	sb.WriteString("#!author=Haruki Dev Team\n")
	sb.WriteString(fmt.Sprintf("#!date=%s\n", time.Now().Format("2006-01-02")))
	sb.WriteString("\n")
	sb.WriteString("[MITM]\n")
	sb.WriteString(fmt.Sprintf("hostname = %%APPEND%% %s, submit.backtrace.io\n", strings.Join(rs.Hostnames, ", ")))
	sb.WriteString("\n")
	sb.WriteString("[URL Rewrite]\n")
	for _, rule := range rs.RewriteRules {
		if rule.RuleType == "redirect" {
			sb.WriteString(fmt.Sprintf("%s %s\n", rule.Pattern, rule.Target))
		} else if rule.RuleType == "rewrite" {
			sb.WriteString(fmt.Sprintf("%s %s 307\n", rule.Pattern, rule.Target))
		}
	}
	sb.WriteString("^https:\\/\\/submit\\.backtrace\\.io\\/ reject\n")
	sb.WriteString("\n")
	if len(rs.ScriptRules) > 0 {
		sb.WriteString("[Script]\n")
		for i, rule := range rs.ScriptRules {
			sb.WriteString(fmt.Sprintf("haruki-upload-%d = type=http-response,pattern=%s,requires-body=1,binary-body-mode=1,max-size=100000000,timeout=60,script-path=%s\n",
				i+1, rule.Pattern, rule.Target))
		}
	}
	return sb.String()
}

func generateLoonModule(req *ModuleRequest, rs *RuleSet) string {
	var sb strings.Builder
	name, desc := generateModuleNameAndDesc(req)
	sb.WriteString(fmt.Sprintf("#!name=%s\n", name))
	sb.WriteString(fmt.Sprintf("#!desc=%s\n", desc))
	sb.WriteString("#!homepage=https://haruki.seiunx.com/ios-modules\n")
	sb.WriteString("#!author=Haruki Dev Team\n")
	sb.WriteString(fmt.Sprintf("#!date=%s\n", time.Now().Format("2006-01-02")))
	sb.WriteString("\n")
	sb.WriteString("[Rewrite]\n")
	for _, rule := range rs.RewriteRules {
		if rule.RuleType == "redirect" {
			sb.WriteString(fmt.Sprintf("%s %s\n", rule.Pattern, rule.Target))
		} else if rule.RuleType == "rewrite" {
			sb.WriteString(fmt.Sprintf("%s %s 307\n", rule.Pattern, rule.Target))
		}
	}
	sb.WriteString("^https:\\/\\/submit\\.backtrace\\.io\\/ reject\n")
	sb.WriteString("\n")
	if len(rs.ScriptRules) > 0 {
		sb.WriteString("[Script]\n")
		for i, rule := range rs.ScriptRules {
			sb.WriteString(fmt.Sprintf("http-response %s script-path=%s, requires-body=true, binary-body-mode=true, max-size=100000000, timeout=60, tag=haruki-upload-%d\n",
				rule.Pattern, rule.Target, i+1))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("[MITM]\n")
	sb.WriteString(fmt.Sprintf("hostname = %s, submit.backtrace.io\n", strings.Join(rs.Hostnames, ", ")))
	return sb.String()
}

func generateQuantumultXModule(req *ModuleRequest, rs *RuleSet) string {
	var sb strings.Builder
	name, desc := generateModuleNameAndDesc(req)
	sb.WriteString(fmt.Sprintf("; %s\n", name))
	sb.WriteString(fmt.Sprintf("; %s\n", desc))
	sb.WriteString(fmt.Sprintf("; Date: %s\n", time.Now().Format("2006-01-02")))
	sb.WriteString("\n")
	for _, rule := range rs.RewriteRules {
		target := rule.Target
		target = strings.TrimSuffix(target, " 307")
		if rule.RuleType == "redirect" || rule.RuleType == "rewrite" {
			sb.WriteString(fmt.Sprintf("%s url 307 %s\n", rule.Pattern, target))
		}
	}
	sb.WriteString("^https:\\/\\/submit\\.backtrace\\.io\\/ url reject\n")
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("hostname = %s, submit.backtrace.io\n", strings.Join(rs.Hostnames, ", ")))
	return sb.String()
}

func generateStashModule(req *ModuleRequest, rs *RuleSet) string {
	var sb strings.Builder
	name, desc := generateModuleNameAndDesc(req)
	sb.WriteString(fmt.Sprintf("name: %s\n", name))
	sb.WriteString(fmt.Sprintf("desc: %s\n", desc))
	sb.WriteString(fmt.Sprintf("# date: %s\n", time.Now().Format("2006-01-02")))
	sb.WriteString("\n")
	sb.WriteString("http:\n")
	if len(rs.RewriteRules) > 0 || len(rs.ScriptRules) > 0 {
		sb.WriteString("  rewrite:\n")
		for _, rule := range rs.RewriteRules {
			target := rule.Target
			target = strings.TrimSuffix(target, " 307")
			if rule.RuleType == "redirect" || rule.RuleType == "rewrite" {
				sb.WriteString(fmt.Sprintf("    - %s %s 307\n", rule.Pattern, target))
			}
		}
		sb.WriteString("    - ^https:\\/\\/submit\\.backtrace\\.io\\/ - reject\n")
	}
	if len(rs.ScriptRules) > 0 {
		sb.WriteString("  script:\n")
		for i, rule := range rs.ScriptRules {
			sb.WriteString(fmt.Sprintf("    - match: \"%s\"\n", rule.Pattern))
			sb.WriteString(fmt.Sprintf("      name: haruki-upload-%d\n", i+1))
			sb.WriteString("      type: response\n")
			sb.WriteString("      require-body: true\n")
			sb.WriteString("      binary-body-mode: true\n")
			sb.WriteString("      max-size: 100000000\n")
			sb.WriteString("      timeout: 60\n")
			sb.WriteString(fmt.Sprintf("      script-path: \"%s\"\n", rule.Target))
		}
	}
	sb.WriteString("  mitm:\n")
	for _, h := range rs.Hostnames {
		sb.WriteString(fmt.Sprintf("    - \"%s\"\n", h))
	}
	sb.WriteString("    - \"submit.backtrace.io\"\n")
	return sb.String()
}
