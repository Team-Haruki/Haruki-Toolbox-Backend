package ios

import (
	"bytes"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// Stash overrides (.stoverride) are YAML documents that Stash merges into the
// active configuration (https://stash.wiki/configuration/override). They are
// marshalled rather than hand-written so that regex patterns always come out
// with valid YAML quoting: a hand-written double-quoted "\." or "\d" is an
// invalid YAML escape and makes Stash reject the whole file.
//
// A script entry does not carry its own script URL. Its `name` refers to an
// entry under the top-level `script-providers` key, which holds the URL
// (https://stash.wiki/script/manage-script). Script options follow
// https://stash.wiki/script/rewrite-requests.

const (
	stashScriptProviderName     = "haruki-toolbox-upload"
	stashScriptProviderInterval = 86400
	stashScriptMaxSize          = 100000000
	stashScriptTimeout          = 60
)

type stashOverride struct {
	Name            string                         `yaml:"name"`
	Desc            string                         `yaml:"desc"`
	Author          string                         `yaml:"author"`
	Homepage        string                         `yaml:"homepage"`
	Date            string                         `yaml:"date"`
	HTTP            stashHTTP                      `yaml:"http"`
	ScriptProviders map[string]stashScriptProvider `yaml:"script-providers,omitempty"`
}

type stashHTTP struct {
	MITM       []string      `yaml:"mitm"`
	URLRewrite []string      `yaml:"url-rewrite,omitempty"`
	Script     []stashScript `yaml:"script,omitempty"`
}

type stashScript struct {
	Match       string `yaml:"match"`
	Name        string `yaml:"name"`
	Type        string `yaml:"type"`
	RequireBody bool   `yaml:"require-body"`
	BinaryMode  bool   `yaml:"binary-mode"`
	MaxSize     int    `yaml:"max-size"`
	Timeout     int    `yaml:"timeout"`
}

type stashScriptProvider struct {
	URL      string `yaml:"url"`
	Interval int    `yaml:"interval"`
}

func generateStashModule(req *ModuleRequest, rs *RuleSet) (string, error) {
	name, desc := generateModuleNameAndDesc(req)
	override := stashOverride{
		Name:     name,
		Desc:     desc,
		Author:   "Haruki Dev Team",
		Homepage: "https://haruki.seiunx.com/ios-modules",
		Date:     time.Now().Format("2006-01-02"),
	}
	override.HTTP.MITM = append(append([]string{}, rs.Hostnames...), "submit.backtrace.io")
	for _, rule := range rs.RewriteRules {
		if rule.RuleType == "redirect" || rule.RuleType == "rewrite" {
			override.HTTP.URLRewrite = append(override.HTTP.URLRewrite,
				fmt.Sprintf("%s %s transparent", rule.Pattern, rule.Target))
		}
	}
	override.HTTP.URLRewrite = append(override.HTTP.URLRewrite, `^https:\/\/submit\.backtrace\.io\/ - reject`)

	providerByURL := make(map[string]string)
	for _, rule := range rs.ScriptRules {
		provider, ok := providerByURL[rule.Target]
		if !ok {
			provider = stashScriptProviderName
			if len(providerByURL) > 0 {
				provider = fmt.Sprintf("%s-%d", stashScriptProviderName, len(providerByURL)+1)
			}
			providerByURL[rule.Target] = provider
			if override.ScriptProviders == nil {
				override.ScriptProviders = make(map[string]stashScriptProvider)
			}
			override.ScriptProviders[provider] = stashScriptProvider{
				URL:      rule.Target,
				Interval: stashScriptProviderInterval,
			}
		}
		override.HTTP.Script = append(override.HTTP.Script, stashScript{
			Match:       rule.Pattern,
			Name:        provider,
			Type:        "response",
			RequireBody: true,
			BinaryMode:  true,
			MaxSize:     stashScriptMaxSize,
			Timeout:     stashScriptTimeout,
		})
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&override); err != nil {
		return "", fmt.Errorf("encode stash override: %w", err)
	}
	if err := enc.Close(); err != nil {
		return "", fmt.Errorf("encode stash override: %w", err)
	}
	return buf.String(), nil
}
