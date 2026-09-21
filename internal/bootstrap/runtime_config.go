package bootstrap

import (
	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	platformRuntimeConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/runtimeconfig"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
)

func runtimeConfigSnapshotFromConfig(cfg harukiConfig.Config) platformRuntimeConfig.Snapshot {
	webhookEnabled := cfg.Webhook.Enabled
	return platformRuntimeConfig.Snapshot{
		AllowedKeys:          append([]string(nil), cfg.Others.AllowedKeys...),
		PrivateAPIToken:      cfg.MongoDB.PrivateApiSecret,
		PrivateAPIUserAgent:  cfg.MongoDB.PrivateApiUserAgent,
		HarukiProxyUserAgent: cfg.HarukiProxy.UserAgent,
		HarukiProxyVersion:   cfg.HarukiProxy.Version,
		HarukiProxySecret:    cfg.HarukiProxy.Secret,
		HarukiProxyUnpackKey: cfg.HarukiProxy.UnpackKey,
		WebhookJWTSecret:     cfg.Webhook.JWTSecret,
		WebhookEnabled:       &webhookEnabled,
	}
}

func newRuntimeConfigService(cfg harukiConfig.Config, redisClient *harukiRedis.HarukiRedisManager) *platformRuntimeConfig.Service {
	initial := runtimeConfigSnapshotFromConfig(cfg)
	store := platformRuntimeConfig.NewRedisStore(redisClient)
	return platformRuntimeConfig.New(initial, store)
}
