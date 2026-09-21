package config

import (
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"gopkg.in/yaml.v3"
	"testing"
)

func TestCryptoRegionYAMLAndIndependentCopies(t *testing.T) {
	var c Config
	if err := yaml.Unmarshal([]byte("crypto:\n  cn: {key: cn-key, iv: cn-iv}\n  tw: {key: tw-key, iv: tw-iv}\n"), &c); err != nil {
		t.Fatal(err)
	}
	if err := validateCryptoConfig(c); err != nil {
		t.Fatal(err)
	}
	regions := c.CryptoRegions()
	if regions["cn"].Key != "cn-key" || regions["tw"].IV != "tw-iv" {
		t.Fatal("region YAML not parsed")
	}
	regions["cn"] = utils.CryptoMaterial{}
	if c.Crypto["cn"].Key != "cn-key" {
		t.Fatal("runtime map shares mutable config")
	}
	if _, ok := regions["kr"]; ok {
		t.Fatal("missing region was filled implicitly")
	}
}
func TestCryptoRejectsPartialAndUnknownRegion(t *testing.T) {
	for _, regions := range []map[string]utils.CryptoMaterial{{"cn": {Key: "key"}}, {"cn": {IV: "iv"}}, {"other": {Key: "key", IV: "iv"}}} {
		if err := validateCryptoConfig(Config{Crypto: regions}); err == nil {
			t.Fatal("invalid crypto configuration accepted")
		}
	}
}
