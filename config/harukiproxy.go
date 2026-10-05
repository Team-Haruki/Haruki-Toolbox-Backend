package config

import (
	"fmt"
	"gopkg.in/yaml.v3"
)

type HarukiProxyClientPolicy struct {
	AllowedChannels []string          `yaml:"allowed_channels"`
	MinimumVersions map[string]string `yaml:"minimum_versions"`
}

func (p *HarukiProxyClientPolicy) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("v3_client_policy must be a mapping")
	}
	for i := 0; i < len(node.Content); i += 2 {
		switch node.Content[i].Value {
		case "allowed_channels", "minimum_versions":
		default:
			return fmt.Errorf("unknown v3_client_policy key %q", node.Content[i].Value)
		}
	}
	type plain HarukiProxyClientPolicy
	return node.Decode((*plain)(p))
}
