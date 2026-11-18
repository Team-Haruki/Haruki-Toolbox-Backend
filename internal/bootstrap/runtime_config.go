package bootstrap

import (
	"context"
	"database/sql"
	"os"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	platformRuntimeConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/runtimeconfig"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
)

func runtimeConfigSnapshotFromConfig(cfg harukiConfig.Config) platformRuntimeConfig.Snapshot {
	webhookEnabled := cfg.Webhook.Enabled
	return platformRuntimeConfig.Snapshot{
		AllowedKeys:         append([]string(nil), cfg.Others.AllowedKeys...),
		PrivateAPIToken:     cfg.MongoDB.PrivateApiSecret,
		PrivateAPIUserAgent: cfg.MongoDB.PrivateApiUserAgent,
		WebhookJWTSecret:    cfg.Webhook.JWTSecret,
		WebhookEnabled:      &webhookEnabled,
	}
}

func newRuntimeConfigService(cfg harukiConfig.Config, redisClient *harukiRedis.HarukiRedisManager) *platformRuntimeConfig.Service {
	initial := runtimeConfigSnapshotFromConfig(cfg)
	store := platformRuntimeConfig.NewRedisStore(redisClient)
	return platformRuntimeConfig.New(initial, store)
}

func newDurableRuntimeConfigService(ctx context.Context, cfg harukiConfig.Config, db *sql.DB, redisClient *harukiRedis.HarukiRedisManager) (*platformRuntimeConfig.Service, error) {
	initial := runtimeConfigSnapshotFromConfig(cfg)
	cutoverConfirmed := os.Getenv("HARUKI_RUNTIME_CONFIG_CUTOVER") == "1"
	store, err := platformRuntimeConfig.NewPostgresStore(ctx, db, platformRuntimeConfig.NewRedisStore(redisClient), initial, cutoverConfirmed)
	if err != nil {
		return nil, err
	}
	return platformRuntimeConfig.New(platformRuntimeConfig.Snapshot{}, store), nil
}
