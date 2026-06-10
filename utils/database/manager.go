package database

import (
	mongoManager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/mongo"
	neopgManager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/neopg"
	dbManager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	redisManager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
)

type HarukiToolboxDBManager struct {
	DB    *dbManager.Client
	BotDB *neopgManager.Client
	Redis *redisManager.HarukiRedisManager
	Mongo *mongoManager.MongoDBManager
}

func NewHarukiToolboxDBManager(db *dbManager.Client, redis *redisManager.HarukiRedisManager, mongo *mongoManager.MongoDBManager) *HarukiToolboxDBManager {
	return &HarukiToolboxDBManager{
		DB:    db,
		Redis: redis,
		Mongo: mongo,
	}
}
