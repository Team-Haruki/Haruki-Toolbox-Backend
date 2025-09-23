package upload

import (
	harukiMongo "haruki-suite/utils/mongo"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

func RegisterRoutes(app *fiber.App, manager *harukiMongo.MongoDBManager, client *redis.Client) {
	registerIOSRoutes(app, manager)
	registerGeneralRoutes(app, manager, client)
	registerInheritRoutes(app, manager, client)
}
