package database

import (
	gamedataManager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata"
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
	// GameData is the PostgreSQL store that REPLACES Mongo for suite/mysekai
	// game data. The two coexist only for the length of the cutover; `Mongo`
	// and this field are removed together once MongoDB is decommissioned.
	GameData *gamedataManager.Service
}

func NewHarukiToolboxDBManager(
	db *dbManager.Client,
	redis *redisManager.HarukiRedisManager,
	mongo *mongoManager.MongoDBManager,
	gameData *gamedataManager.Service,
) *HarukiToolboxDBManager {
	return &HarukiToolboxDBManager{
		DB:       db,
		Redis:    redis,
		Mongo:    mongo,
		GameData: gameData,
	}
}
