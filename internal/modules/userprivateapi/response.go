package userprivateapi

import (
	"haruki-suite/utils/api/data"
	"strings"

	"github.com/gofiber/fiber/v3"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func processRequestKeys(c fiber.Ctx, result bson.D) error {
	return c.JSON(buildPrivateDataResponse(c.Query("key"), result))
}

func buildPrivateDataResponse(requestKey string, result bson.D) any {
	if requestKey != "" {
		keys := strings.Split(requestKey, ",")
		if len(keys) == 1 {
			return data.NormalizeProviderResponse(bsonDGet(result, keys[0]))
		}
		filtered := make(bson.D, 0, len(keys))
		for _, k := range keys {
			filtered = append(filtered, bson.E{Key: k, Value: bsonDGet(result, k)})
		}
		return data.NormalizeProviderResponse(filtered)
	}
	return data.NormalizeProviderResponse(result)
}

func bsonDGet(d bson.D, key string) any {
	for _, elem := range d {
		if elem.Key == key {
			return elem.Value
		}
	}
	return nil
}
