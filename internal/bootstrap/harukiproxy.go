package bootstrap

import (
	config "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	upload "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/upload"
)

func compileHarukiProxyPolicy(cfg config.Config) (*upload.ClientPolicy, error) {
	p := cfg.HarukiProxy.V3ClientPolicy
	if p == nil {
		return upload.DefaultClientPolicy(), nil
	}
	return upload.CompileClientPolicy(p.AllowedChannels, p.MinimumVersions)
}
