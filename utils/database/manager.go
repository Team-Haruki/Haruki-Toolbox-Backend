package database

import (
	mongoManager "haruki-suite/utils/database/mongo"
	neopgManager "haruki-suite/utils/database/neopg"
	dbManager "haruki-suite/utils/database/postgresql"
	redisManager "haruki-suite/utils/database/redis"
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
