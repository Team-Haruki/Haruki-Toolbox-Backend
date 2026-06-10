package ios

import (
	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	iosGen "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api/ios"
	"regexp"
)

var modulePathPattern = regexp.MustCompile(`^([a-z-]+)-haruki-toolbox-([a-z_-]+)\.(\w+)$`)

func getEndpoint(endpointType iosGen.EndpointType) string {
	if endpointType == iosGen.EndpointTypeCDN && harukiConfig.Cfg.Backend.BackendCDNURL != "" {
		return harukiConfig.Cfg.Backend.BackendCDNURL
	}
	return harukiConfig.Cfg.Backend.BackendURL
}
