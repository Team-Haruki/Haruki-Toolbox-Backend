package ios

import (
	"fmt"
	"strings"
	"time"
)

// GenerateModule generates the complete module content for the given request
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

// GenerateScript generates the JavaScript upload script
func GenerateScript(uploadCode string, chunkSizeMB int, uploadURL string) string {
	script := IOSJavaScriptTemplate
	script = strings.ReplaceAll(script, "{{UPLOAD_URL}}", uploadURL+"/ios/script/"+uploadCode+"/upload")
	script = strings.ReplaceAll(script, "{{CHUNK_SIZE}}", fmt.Sprintf("%d", chunkSizeMB))
	script = strings.ReplaceAll(script, "{{UPLOAD_CODE}}", uploadCode)
	script = strings.ReplaceAll(script, "{{GENERATE_DATE}}", time.Now().Format("2006-01-02 15:04:05"))
	return script
}

func generateSurgeModule(req *ModuleRequest, rs *RuleSet) string {
	var sb strings.Builder

	sb.WriteString("#!name=Haruki工具箱数据上传模块\n")
	sb.WriteString("#!desc=自动获取选定区服与数据类型的数据，并上传至Haruki工具箱\n")
	sb.WriteString("#!homepage=https://haruki.seiunx.com/ios-modules\n")
	sb.WriteString("#!author=Haruki Dev Team\n")
	sb.WriteString(fmt.Sprintf("#!date=%s\n", time.Now().Format("2006-01-02")))
	sb.WriteString("\n")

	// MITM
	sb.WriteString("[MITM]\n")
	sb.WriteString(fmt.Sprintf("hostname = %%APPEND%% %s, submit.backtrace.io\n", strings.Join(rs.Hostnames, ", ")))
	sb.WriteString("\n")

	// URL Rewrite
	sb.WriteString("[URL Rewrite]\n")
	for _, rule := range rs.RewriteRules {
		if rule.RuleType == "redirect" {
			sb.WriteString(fmt.Sprintf("%s %s\n", rule.Pattern, rule.Target))
		} else if rule.RuleType == "rewrite" {
			sb.WriteString(fmt.Sprintf("%s %s 302\n", rule.Pattern, rule.Target))
		}
	}
	sb.WriteString("^https:\\/\\/submit\\.backtrace\\.io\\/ reject\n")
	sb.WriteString("\n")

	// Script (if any)
	if len(rs.ScriptRules) > 0 {
		sb.WriteString("[Script]\n")
		for i, rule := range rs.ScriptRules {
			sb.WriteString(fmt.Sprintf("haruki-upload-%d = type=http-response,pattern=%s,requires-body=1,max-size=0,script-path=%s\n",
				i+1, rule.Pattern, rule.Target))
		}
	}

	return sb.String()
}

func generateLoonModule(req *ModuleRequest, rs *RuleSet) string {
	var sb strings.Builder

	sb.WriteString("#!name=Haruki工具箱数据上传模块\n")
	sb.WriteString("#!desc=自动获取选定区服与数据类型的数据，并上传至Haruki工具箱\n")
	sb.WriteString("#!homepage=https://haruki.seiunx.com/ios-modules\n")
	sb.WriteString("#!author=Haruki Dev Team\n")
	sb.WriteString(fmt.Sprintf("#!date=%s\n", time.Now().Format("2006-01-02")))
	sb.WriteString("\n")

	// Rewrite
	sb.WriteString("[Rewrite]\n")
	for _, rule := range rs.RewriteRules {
		if rule.RuleType == "redirect" {
			sb.WriteString(fmt.Sprintf("%s %s\n", rule.Pattern, rule.Target))
		} else if rule.RuleType == "rewrite" {
			sb.WriteString(fmt.Sprintf("%s %s 302\n", rule.Pattern, rule.Target))
		}
	}
	sb.WriteString("^https:\\/\\/submit\\.backtrace\\.io\\/ reject\n")
	sb.WriteString("\n")

	// Script (if any)
	if len(rs.ScriptRules) > 0 {
		sb.WriteString("[Script]\n")
		for i, rule := range rs.ScriptRules {
			sb.WriteString(fmt.Sprintf("http-response %s script-path=%s, requires-body=true, tag=haruki-upload-%d\n",
				rule.Pattern, rule.Target, i+1))
		}
		sb.WriteString("\n")
	}

	// MITM
	sb.WriteString("[MITM]\n")
	sb.WriteString(fmt.Sprintf("hostname = %s, submit.backtrace.io\n", strings.Join(rs.Hostnames, ", ")))

	return sb.String()
}

func generateQuantumultXModule(req *ModuleRequest, rs *RuleSet) string {
	var sb strings.Builder

	sb.WriteString("; Haruki工具箱数据上传模块\n")
	sb.WriteString("; 自动获取选定区服与数据类型的数据，并上传至Haruki工具箱\n")
	sb.WriteString(fmt.Sprintf("; Date: %s\n", time.Now().Format("2006-01-02")))
	sb.WriteString("\n")

	// Rewrite - QX format: pattern 307 target
	sb.WriteString("[rewrite_local]\n")
	for _, rule := range rs.RewriteRules {
		if rule.RuleType == "redirect" {
			// QX format: pattern 307 target (not url 307)
			sb.WriteString(fmt.Sprintf("%s %s\n", rule.Pattern, rule.Target))
		} else if rule.RuleType == "rewrite" {
			sb.WriteString(fmt.Sprintf("%s 302 %s\n", rule.Pattern, rule.Target))
		}
	}
	sb.WriteString("^https:\\/\\/submit\\.backtrace\\.io\\/ reject\n")
	sb.WriteString("\n")

	// Note: QX does not support script upload mode

	// MITM
	sb.WriteString("[mitm]\n")
	sb.WriteString(fmt.Sprintf("hostname = %s, submit.backtrace.io\n", strings.Join(rs.Hostnames, ", ")))

	return sb.String()
}

func generateStashModule(req *ModuleRequest, rs *RuleSet) string {
	var sb strings.Builder

	sb.WriteString("name: \"Haruki工具箱数据上传模块\"\n")
	sb.WriteString("desc: \"自动获取选定区服与数据类型的数据，并上传至Haruki工具箱\"\n")
	sb.WriteString(fmt.Sprintf("# date: %s\n", time.Now().Format("2006-01-02")))
	sb.WriteString("\n")

	sb.WriteString("http:\n")

	// Rewrite rules
	if len(rs.RewriteRules) > 0 {
		sb.WriteString("  rewrite:\n")
		for _, rule := range rs.RewriteRules {
			if rule.RuleType == "redirect" {
				// Target format is "url 307", split it
				parts := strings.Split(rule.Target, " ")
				if len(parts) >= 2 {
					sb.WriteString(fmt.Sprintf("    - \"%s %s %s\"\n", rule.Pattern, parts[1], parts[0]))
				}
			} else if rule.RuleType == "rewrite" {
				sb.WriteString(fmt.Sprintf("    - \"%s 302 %s\"\n", rule.Pattern, rule.Target))
			}
		}
		sb.WriteString("    - \"^https:\\/\\/submit\\.backtrace\\.io\\/ reject\"\n")
	}

	// Script rules (if any)
	if len(rs.ScriptRules) > 0 {
		sb.WriteString("  script:\n")
		for i, rule := range rs.ScriptRules {
			sb.WriteString(fmt.Sprintf("    - match: \"%s\"\n", rule.Pattern))
			sb.WriteString(fmt.Sprintf("      name: haruki-upload-%d\n", i+1))
			sb.WriteString("      type: response\n")
			sb.WriteString("      require-body: true\n")
			sb.WriteString("      timeout: 60\n")
			sb.WriteString(fmt.Sprintf("      script-path: \"%s\"\n", rule.Target))
		}
	}

	// MITM
	sb.WriteString("  mitm:\n")
	for _, h := range rs.Hostnames {
		sb.WriteString(fmt.Sprintf("    - \"%s\"\n", h))
	}
	sb.WriteString("    - \"submit.backtrace.io\"\n")

	return sb.String()
}
