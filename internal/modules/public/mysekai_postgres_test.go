package public

import (
	json "encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
)

func TestPublicMysekaiFieldSelectionFromPostgres(t *testing.T) {
	dsn := os.Getenv("GAMEDATA_PUBLIC_TEST_PG")
	if dsn == "" {
		t.Skip("set GAMEDATA_PUBLIC_TEST_PG to a disposable database")
	}
	ctx := t.Context()
	pool, err := gamedata.NewPool(ctx, gamedata.PoolConfig{URL: dsn, MaxConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := gamedata.EnsureSchema(ctx, pool, true, catalog.Mysekai()); err != nil {
		t.Fatal(err)
	}
	app, helper, _ := newPublicReadTest(t)
	service := gamedata.NewService(pool)
	helper.DBManager.GameData = service
	stamp := time.Now().Add(-time.Minute).Unix()
	fixture := map[string]any{
		"upload_time": stamp,
		"updatedResources": map[string]any{
			"userMysekaiItems": []int{1, 2},
			"userMysekaiGates": []int{3},
		},
		"fixtureUnlistedField": true,
	}
	if _, err := service.Mysekai().Write(ctx, 123, "jp", fixture, gamedata.WriteMysekai, service.Limits()); err != nil {
		t.Fatal(err)
	}
	const resources = `"updatedResources":{"userMysekaiItems":[1,2],"userMysekaiGates":[3]}`
	const extra = `"fixtureUnlistedField":true`
	const path = "/api/public/jp/mysekai/123"
	whole := `{` + resources + `,` + extra + `,"upload_time":` + strconv.FormatInt(stamp, 10) + `}`
	for _, tc := range []struct {
		name, url, body string
		status          int
	}{
		{"full document", path, whole, http.StatusOK},
		{"legacy route", "/public/jp/mysekai/123", whole, http.StatusOK},
		{"nested object", path + "?key=updatedResources", `{` + resources + `}`, http.StatusOK},
		{"extra field", path + "?key=fixtureUnlistedField", `{` + extra + `}`, http.StatusOK},
		{"multiple fields", path + "?key=fixtureUnlistedField,updatedResources,missingField", `{` + extra + `,` + resources + `}`, http.StatusOK},
		{"missing field", path + "?key=missingField", "", http.StatusNotFound},
		{"nested path", path + "?key=updatedResources.userMysekaiItems", "", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response, err := app.Test(httptest.NewRequest(http.MethodGet, tc.url, nil))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil || response.StatusCode != tc.status {
				t.Fatalf("GET %s returned %d %s, %v, want %d", tc.url, response.StatusCode, body, err, tc.status)
			}
			if tc.status == http.StatusOK {
				var want, got map[string]any
				if err := json.Unmarshal([]byte(tc.body), &want); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(body, &got); err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("GET %s returned %s, %v, want %s", tc.url, body, err, tc.body)
				}
			}
		})
	}
}
