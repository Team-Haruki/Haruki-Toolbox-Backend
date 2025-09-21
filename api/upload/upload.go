package upload

import (
	harukiMongo "haruki-suite/utils/mongo"

	"github.com/gofiber/fiber/v2"
)

func RegisterRoutes(app *fiber.App, manager *harukiMongo.MongoDBManager, secret string) {
	registerIosRoutes(app, manager, secret)
	registerGeneralRoutes(app, manager, secret)
}
