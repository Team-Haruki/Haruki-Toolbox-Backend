package upload

import (
	harukiMongo "haruki-suite/utils/mongo"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

func RegisterRoutes(app *fiber.App, mongoDBManager *harukiMongo.MongoDBManager, redisClient *redis.Client, harukiProxyUA, harukiProxyVersion, harukiProxySecret *string) {
	registerIOSRoutes(app, mongoDBManager, redisClient)
	registerGeneralRoutes(app, mongoDBManager, redisClient)
	registerInheritRoutes(app, mongoDBManager, redisClient)
	registerHarukiProxyRoutes(app, mongoDBManager, redisClient, harukiProxyUA, harukiProxyVersion, harukiProxySecret)
}
