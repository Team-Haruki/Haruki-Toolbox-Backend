package database

import (
	gamedataManager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata"
	neopgManager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/neopg"
	dbManager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	redisManager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
)

type HarukiToolboxDBManager struct {
	DB    *dbManager.Client
	BotDB *neopgManager.Client
	Redis *redisManager.HarukiRedisManager
	// GameData owns the dedicated PostgreSQL pool for suite/mysekai data.
	GameData *gamedataManager.Service
}

func NewHarukiToolboxDBManager(
	db *dbManager.Client,
	redis *redisManager.HarukiRedisManager,
	gameData *gamedataManager.Service,
) *HarukiToolboxDBManager {
	return &HarukiToolboxDBManager{
		DB:       db,
		Redis:    redis,
		GameData: gameData,
	}
}
