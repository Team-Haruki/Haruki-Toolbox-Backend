package config

import (
	"fmt"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"maps"
)

// CryptoRegions returns an independent copy for runtime dependency assembly.
func (c Config) CryptoRegions() map[string]utils.CryptoMaterial {
	return maps.Clone(c.Crypto)
}
func validateCryptoConfig(c Config) error {
	for region, pair := range c.Crypto {
		switch region {
		case "jp", "en", "cn", "tw", "kr":
		default:
			return fmt.Errorf("crypto: unsupported region %q", region)
		}
		if (pair.Key == "") != (pair.IV == "") {
			return fmt.Errorf("crypto.%s: key and iv must be configured together", region)
		}
	}
	return nil
}
