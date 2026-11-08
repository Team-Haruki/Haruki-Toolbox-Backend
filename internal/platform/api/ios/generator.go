package ios

import (
	json "encoding/json/v2"
	"fmt"
	"net/url"
	"slices"
	"strconv"
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

// The module argument that switches upload notifications. Each app hands it to
// the uploader script in its own way; the script reads both forms (see
// notifyEnabled in the template). Surge and Shadowrocket share the .sgmodule.
const (
	notifyArgumentName  = "notify"
	notifyArgumentTitle = "上传通知"
	notifyArgumentDesc  = "每次上传后弹出通知，告诉你上传成功还是失败"
)

// notifyArgumentDefault is the module argument's default value.
func notifyArgumentDefault(req *ModuleRequest) string {
	return strconv.FormatBool(!req.NotifyOff)
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
		return generateStashModule(req, ruleSet)
	default:
		return "", fmt.Errorf("unsupported proxy app: %s", req.App)
	}
}

// ScriptVersion is the uploader script's version. Bump it with every change to
// HarukiIOSJavaScriptTemplate: the modules load the script from a URL that ends
// in &v=<version>, and proxy apps cache a script by its URL, so updating a
// module only loads a changed script when the URL changes with it.
const ScriptVersion = "1.1.1"

// GenerateScript renders the uploader script. notifyByDefault applies when the
// proxy app passes the script no "notify" argument (see the template).
func GenerateScript(uploadCode string, chunkSizeMB int, uploadURL string, notifyByDefault bool) string {
	regionLabels, err := json.Marshal(scriptRegionLabels())
	if err != nil {
		regionLabels = []byte("{}")
	}
	return strings.NewReplacer(
		"{{UPLOAD_URL}}", uploadURL+"/ios/script/"+uploadCode+"/upload",
		"{{CHUNK_SIZE}}", fmt.Sprintf("%d", chunkSizeMB),
		"{{UPLOAD_CODE}}", uploadCode,
		"{{GENERATE_DATE}}", time.Now().Format("2006-01-02 15:04:05"),
		"{{SCRIPT_VERSION}}", ScriptVersion,
		"{{NOTIFY_DEFAULT}}", strconv.FormatBool(notifyByDefault),
		"{{REGION_LABELS}}", string(regionLabels),
		"{{NOTIFY_ARGUMENT_JS}}", notifyArgumentJS,
		"{{UPLOAD_ERROR_TEXT_JS}}", uploadErrorTextJS,
		"\\u0060", "`",
	).Replace(HarukiIOSJavaScriptTemplate)
}

// GenerateProxyNotifyScript renders the proxy-mode notification script.
// notifyByDefault applies when the app passes no notify argument.
func GenerateProxyNotifyScript(notifyByDefault bool) string {
	return strings.NewReplacer(
		"{{SCRIPT_VERSION}}", ProxyNotifyScriptVersion,
		"{{NOTIFY_DEFAULT}}", strconv.FormatBool(notifyByDefault),
		"{{NOTIFY_ARGUMENT_JS}}", notifyArgumentJS,
		"{{UPLOAD_ERROR_TEXT_JS}}", uploadErrorTextJS,
	).Replace(proxyNotifyScriptTemplate)
}

// scriptRegionLabels maps each configured game API host to the region name the
// notification shows.
func scriptRegionLabels() map[string]string {
	labels := make(map[string]string)
	for region, hosts := range getHarukiIOSMitMHostnameMapping() {
		for _, host := range hosts {
			if host = strings.ToLower(strings.TrimSpace(host)); host != "" {
				labels[host] = regionNames[region]
			}
		}
	}
	return labels
}

func generateSurgeModule(req *ModuleRequest, rs *RuleSet) string {
	var sb strings.Builder
	name, desc := generateModuleNameAndDesc(req)
	sb.WriteString(fmt.Sprintf("#!name=%s\n", name))
	sb.WriteString(fmt.Sprintf("#!desc=%s\n", desc))
	sb.WriteString("#!homepage=https://haruki.seiunx.com/ios-modules\n")
	sb.WriteString("#!author=Haruki Dev Team\n")
	sb.WriteString(fmt.Sprintf("#!date=%s\n", time.Now().Format("2006-01-02")))
	if len(rs.ScriptRules) > 0 || rs.ProxyNotify != nil {
		// Surge replaces {{{notify}}} with the user's value, or this default.
		sb.WriteString(fmt.Sprintf("#!arguments=%s:%s\n", notifyArgumentName, notifyArgumentDefault(req)))
		sb.WriteString(fmt.Sprintf("#!arguments-desc=%s：%s。true 开启，false 关闭。\n", notifyArgumentName, notifyArgumentDesc))
	}
	sb.WriteString("\n")
	sb.WriteString("[MITM]\n")
	sb.WriteString(fmt.Sprintf("hostname = %%APPEND%% %s, submit.backtrace.io\n", strings.Join(rs.Hostnames, ", ")))
	sb.WriteString("\n")
	sb.WriteString("[URL Rewrite]\n")
	for _, rule := range rs.RewriteRules {
		if rule.RuleType == "redirect" || rule.RuleType == "rewrite" {
			sb.WriteString(fmt.Sprintf("%s %s header\n", rule.Pattern, rule.Target))
		}
	}
	sb.WriteString("^https:\\/\\/submit\\.backtrace\\.io\\/ reject\n")
	sb.WriteString("\n")
	if len(rs.ScriptRules) > 0 {
		sb.WriteString("[Script]\n")
		for i, rule := range rs.ScriptRules {
			sb.WriteString(fmt.Sprintf("haruki-upload-%d = type=http-response,pattern=%s,requires-body=1,binary-body-mode=1,max-size=100000000,timeout=60,script-path=%s,argument=\"%s={{{%s}}}\"\n",
				i+1, rule.Pattern, rule.Target, notifyArgumentName, notifyArgumentName))
		}
	}
	if rs.ProxyNotify != nil {
		// Proxy mode: no upload script, only the body-less notification
		// script on our upload URL, which runs at the response header stage.
		sb.WriteString("[Script]\n")
		sb.WriteString(fmt.Sprintf("haruki-upload-notify = type=http-response,pattern=%s,requires-body=0,timeout=%d,script-path=%s,argument=\"%s={{{%s}}}\"\n",
			rs.ProxyNotify.Pattern, proxyNotifyTimeout, rs.ProxyNotify.Target, notifyArgumentName, notifyArgumentName))
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
		if rule.RuleType == "redirect" || rule.RuleType == "rewrite" {
			sb.WriteString(fmt.Sprintf("%s header %s\n", rule.Pattern, rule.Target))
		}
	}
	sb.WriteString("^https:\\/\\/submit\\.backtrace\\.io\\/ reject\n")
	sb.WriteString("\n")
	if rs.ProxyNotify != nil {
		// Proxy mode: no upload script, only the body-less notification
		// script on the rewritten URL, switched by the same argument.
		sb.WriteString("[Argument]\n")
		sb.WriteString(fmt.Sprintf("%s = switch,%s,tag=%s,desc=%s\n", notifyArgumentName, notifyArgumentDefault(req), notifyArgumentTitle, notifyArgumentDesc))
		sb.WriteString("\n")
		sb.WriteString("[Script]\n")
		sb.WriteString(fmt.Sprintf("http-response %s script-path=%s, requires-body=false, timeout=%d, tag=haruki-upload-notify, argument={%s}\n",
			rs.ProxyNotify.Pattern, rs.ProxyNotify.Target, proxyNotifyTimeout, notifyArgumentName))
		sb.WriteString("\n")
	}
	if len(rs.ScriptRules) > 0 {
		// A switch the user flips in the plugin's settings; the script gets
		// it as $argument.notify.
		sb.WriteString("[Argument]\n")
		sb.WriteString(fmt.Sprintf("%s = switch,%s,tag=%s,desc=%s\n", notifyArgumentName, notifyArgumentDefault(req), notifyArgumentTitle, notifyArgumentDesc))
		sb.WriteString("\n")
		sb.WriteString("[Script]\n")
		for i, rule := range rs.ScriptRules {
			sb.WriteString(fmt.Sprintf("http-response %s script-path=%s, requires-body=true, binary-body-mode=true, max-size=100000000, timeout=60, tag=haruki-upload-%d, argument={%s}\n",
				rule.Pattern, rule.Target, i+1, notifyArgumentName))
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
		if rule.RuleType == "redirect" || rule.RuleType == "rewrite" {
			sb.WriteString(fmt.Sprintf("%s url 307 %s\n", rule.Pattern, target))
		}
	}
	sb.WriteString("^https:\\/\\/submit\\.backtrace\\.io\\/ url reject\n")
	hostnames := rs.Hostnames
	if rs.ProxyNotify != nil {
		// Quantumult X has no module arguments: the switch rides in the
		// script URL's fragment, which is never sent to the server and which
		// the script reads from $environment
		// (https://github.com/crossutility/Quantumult-X/blob/master/sample-task.js).
		// Updating the module from a new URL changes it.
		sb.WriteString(fmt.Sprintf("%s url script-response-header %s#%s=%s\n",
			rs.ProxyNotify.Pattern, rs.ProxyNotify.Target, notifyArgumentName, notifyArgumentDefault(req)))
		// The 307 sends the game to our URL over a new HTTPS connection, and
		// Quantumult X only runs scripts on hosts it decrypts ("Only the TLS
		// SNI or destination address in hostname will be handled by MitM",
		// https://github.com/crossutility/Quantumult-X/blob/master/sample.conf).
		if host := endpointHostname(rs.ProxyNotify.Target); host != "" && !slices.Contains(hostnames, host) {
			hostnames = append(slices.Clone(hostnames), host)
		}
	}
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("hostname = %s, submit.backtrace.io\n", strings.Join(hostnames, ", ")))
	return sb.String()
}

// proxyNotifyTimeout is the notification script's timeout in seconds. It reads
// two headers and posts one notification, so a stuck run only delays the game's
// response by this much.
const proxyNotifyTimeout = 10

// endpointHostname is the host name of an absolute URL, or "" if it has none.
func endpointHostname(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}
