package adminsyslog

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	platformTime "haruki-suite/internal/platform/timeutil"
	harukiAPIHelper "haruki-suite/utils/api"
	"sort"
	"time"

	"github.com/gofiber/fiber/v3"
)

type categoryCount struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type groupedFieldCount struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

func normalizeCategoryCounts(rows []groupedFieldCount) []categoryCount {
	out := make([]categoryCount, 0, len(rows))
	for _, row := range rows {
		out = append(out, categoryCount(row))
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Key < out[j].Key
		}
		return out[i].Count > out[j].Count
	})

	return out
}

func resolveUploadLogTimeRange(fromRaw, toRaw string, now time.Time) (time.Time, time.Time, error) {
	return platformTime.ResolveUploadLogTimeRange(fromRaw, toRaw, now)
}

func respondFiberOrBadRequest(c fiber.Ctx, err error, fallbackMessage string) error {
	return adminCoreModule.RespondFiberOrBadRequest(c, err, fallbackMessage)
}

var adminNow = time.Now

func adminNowUTC() time.Time {
	return adminNow().UTC()
}

func requireAdmin(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return adminCoreModule.RequireAdmin(apiHelper)
}
