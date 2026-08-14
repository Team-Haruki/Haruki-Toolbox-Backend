package ios

import (
	"regexp"

	iosGen "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api/ios"
)

var modulePathPattern = regexp.MustCompile(`^([a-z-]+)-haruki-toolbox-([a-z_-]+)\.(\w+)$`)

// EndpointConfigOptions contains the startup URLs consumed by iOS module and
// script generation.
type EndpointConfigOptions struct {
	BackendURL    string
	BackendCDNURL string
}

// EndpointConfig is an immutable view of the iOS generation endpoints. URL
// values are preserved verbatim to keep the existing path-joining behavior.
type EndpointConfig struct {
	backendURL    string
	backendCDNURL string
}

func NewEndpointConfig(options EndpointConfigOptions) EndpointConfig {
	return EndpointConfig{
		backendURL:    options.BackendURL,
		backendCDNURL: options.BackendCDNURL,
	}
}

func (config EndpointConfig) endpoint(endpointType iosGen.EndpointType) string {
	if endpointType == iosGen.EndpointTypeCDN && config.backendCDNURL != "" {
		return config.backendCDNURL
	}
	return config.backendURL
}
