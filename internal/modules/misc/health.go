package misc

import (
	"context"
	"fmt"
	harukiAPIHelper "haruki-suite/utils/api"
	harukiHandler "haruki-suite/utils/handler"
	"time"

	"github.com/gofiber/fiber/v3"
)

const dependencyHealthTimeout = 2 * time.Second

func handleHealth(apiHelpers ...*harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	var apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers
	if len(apiHelpers) > 0 {
		apiHelper = apiHelpers[0]
	}

	return func(c fiber.Ctx) error {
		loadedRegions, failedRegions := harukiHandler.GetSuiteRestorerLoadStatus()
		dependencies := buildDependencyHealth(c.Context(), apiHelper)

		status := "ok"
		httpStatus := fiber.StatusOK
		if len(dependencies) > 0 && hasDependencyFailure(dependencies) {
			status = "unhealthy"
			httpStatus = fiber.StatusServiceUnavailable
		} else if len(failedRegions) > 0 {
			status = "degraded"
		}

		payload := fiber.Map{
			"status": status,
			"time":   time.Now().Unix(),
			"suiteRestorer": fiber.Map{
				"loadedRegions": loadedRegions,
				"failedRegions": failedRegions,
			},
		}
		if len(dependencies) > 0 {
			payload["dependencies"] = dependencies
		}

		return c.Status(httpStatus).JSON(payload)
	}
}

func buildDependencyHealth(parent context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Map {
	if apiHelper == nil {
		return nil
	}
	return fiber.Map{
		"postgresql": dependencyHealthEntry(pingPostgreSQL(parent, apiHelper)),
		"redis":      dependencyHealthEntry(pingRedis(parent, apiHelper)),
		"mongo":      dependencyHealthEntry(pingMongo(parent, apiHelper)),
	}
}

func dependencyHealthEntry(err error) fiber.Map {
	if err != nil {
		return fiber.Map{"status": "down"}
	}
	return fiber.Map{"status": "up"}
}

func hasDependencyFailure(dependencies fiber.Map) bool {
	for _, value := range dependencies {
		entry, ok := value.(fiber.Map)
		if !ok {
			return true
		}
		if entry["status"] != "up" {
			return true
		}
	}
	return false
}

func pingPostgreSQL(parent context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) error {
	if apiHelper == nil || apiHelper.DBManager == nil || apiHelper.DBManager.DB == nil {
		return fmt.Errorf("postgresql client is not initialized")
	}
	sqlDB := apiHelper.DBManager.DB.SQLDB()
	if sqlDB == nil {
		return fmt.Errorf("postgresql sql db is not available")
	}
	ctx, cancel := context.WithTimeout(parent, dependencyHealthTimeout)
	defer cancel()
	return sqlDB.PingContext(ctx)
}

func pingRedis(parent context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) error {
	if apiHelper == nil || apiHelper.DBManager == nil || apiHelper.DBManager.Redis == nil || apiHelper.DBManager.Redis.Redis == nil {
		return fmt.Errorf("redis client is not initialized")
	}
	ctx, cancel := context.WithTimeout(parent, dependencyHealthTimeout)
	defer cancel()
	return apiHelper.DBManager.Redis.Redis.Ping(ctx).Err()
}

func pingMongo(parent context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) error {
	if apiHelper == nil || apiHelper.DBManager == nil || apiHelper.DBManager.Mongo == nil {
		return fmt.Errorf("mongo client is not initialized")
	}
	ctx, cancel := context.WithTimeout(parent, dependencyHealthTimeout)
	defer cancel()
	return apiHelper.DBManager.Mongo.Ping(ctx)
}
