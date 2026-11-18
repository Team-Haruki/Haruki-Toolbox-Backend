package misc

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiHandler "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/upload"

	"github.com/gofiber/fiber/v3"
)

const dependencyHealthTimeout = 2 * time.Second
const dependencyHealthCacheTTL = 5 * time.Second

type healthSnapshot struct {
	dependencies fiber.Map
	expires      time.Time
}

func handleHealth(
	apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	suiteRestoreService *harukiHandler.SuiteRestoreService,
) fiber.Handler {
	var snapshot atomic.Pointer[healthSnapshot]
	var refresh sync.Mutex
	return func(c fiber.Ctx) error {
		loadedRegions, failedRegions := suiteRestoreService.LoadStatus()
		for region := range failedRegions {
			failedRegions[region] = "schema unavailable"
		}
		current := snapshot.Load()
		if (current == nil || time.Now().After(current.expires)) && refresh.TryLock() {
			latest := snapshot.Load()
			if latest == nil || time.Now().After(latest.expires) {
				latest = &healthSnapshot{
					dependencies: buildDependencyHealth(c.Context(), apiHelper),
					expires:      time.Now().Add(dependencyHealthCacheTTL),
				}
				snapshot.Store(latest)
			}
			current = latest
			refresh.Unlock()
		}
		if current == nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"status": "initializing"})
		}
		dependencies := current.dependencies

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
		"game_data":  dependencyHealthEntry(pingGameData(parent, apiHelper)),
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

func pingGameData(parent context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) error {
	if apiHelper == nil || apiHelper.DBManager == nil || apiHelper.DBManager.GameData == nil {
		return fmt.Errorf("game data client is not initialized")
	}
	ctx, cancel := context.WithTimeout(parent, dependencyHealthTimeout)
	defer cancel()
	return apiHelper.DBManager.GameData.Ping(ctx)
}
