package adminstats

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	platform "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/upload"
	database "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	pg "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	manager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	"github.com/redis/go-redis/v9"
)

func TestAnalyticsHTTPTransaction(t *testing.T) {
	for _, fail := range []string{"", "begin", "summary", "groups", "migration", "failures", "commit"} {
		t.Run(fail, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			client := pg.NewClient(pg.Driver(entsql.OpenDB("postgres", db)))
			helper := &api.HarukiToolboxRouterHelpers{DBManager: &database.HarukiToolboxDBManager{DB: client}}
			failure := errors.New("database unavailable")
			b := mock.ExpectBegin()
			if fail == "begin" {
				b.WillReturnError(failure)
			} else {
				stages := []string{"summary", "groups", "migration", "failures"}
				for _, stage := range stages {
					patterns := map[string]string{"summary": "SELECT COUNT", "groups": "SELECT l.upload_method", "migration": "WITH scoped", "failures": "SELECT COALESCE"}
					q := mock.ExpectQuery(patterns[stage])
					if stage == fail {
						q.WillReturnError(failure)
						break
					}
					switch stage {
					case "summary":
						q.WillReturnRows(sqlmock.NewRows([]string{"attempts", "success", "accounts", "samples", "p50", "p95", "metadata"}).AddRow(2, 1, 1, 2, 10.0, 20.0, 2))
					case "groups":
						q.WillReturnRows(sqlmock.NewRows([]string{"method", "attempts", "success", "accounts", "samples", "p50", "p95", "metadata"}).AddRow("haruki_proxy", 2, 1, 1, 2, 10.0, 20.0, 2))
					case "migration":
						q.WillReturnRows(sqlmock.NewRows([]string{"v2", "v3", "unknown", "v2only"}).AddRow(0, 1, 0, 0))
					case "failures":
						q.WillReturnRows(sqlmock.NewRows([]string{"stage", "count"}).AddRow("decrypt", 1))
					}
				}
				if fail == "" {
					mock.ExpectCommit()
				} else if fail == "commit" {
					mock.ExpectCommit().WillReturnError(failure)
				} else {
					mock.ExpectRollback()
				}
			}
			app := fiber.New()
			app.Use(func(c fiber.Ctx) error {
				c.Locals("userID", "actor")
				c.Locals("userRole", "super_admin")
				return c.Next()
			})
			app.Get("/", handleUploadAnalytics(helper))
			resp, err := app.Test(httptest.NewRequest("GET", "/", nil))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			want := 200
			if fail != "" {
				want = 500
			}
			if resp.StatusCode != want {
				t.Fatalf("status %d want %d", resp.StatusCode, want)
			}
			if fail == "" {
				var body struct {
					Data uploadAnalyticsResponse `json:"updatedData"`
				}
				if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body.Data.Summary.AttemptCount != 2 || body.Data.Summary.FailureCount != 1 || body.Data.Migration.V3Share == nil || *body.Data.Migration.V3Share != 1 {
					t.Fatalf("unexpected metrics %+v", body)
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAnalyticsHTTPRejectsBeforeQuery(t *testing.T) {
	for _, tc := range []struct {
		role, url string
		status    int
	}{{"", "/", 401}, {"admin", "/?group_by=evil", 400}, {"admin", "/?platform=abcdefghijklmnopqrstuvwxyz", 400}} {
		app := fiber.New()
		app.Use(func(c fiber.Ctx) error {
			if tc.role != "" {
				c.Locals("userID", "actor")
				c.Locals("userRole", tc.role)
			}
			return c.Next()
		})
		app.Get("/", handleUploadAnalytics(nil))
		resp, err := app.Test(httptest.NewRequest("GET", tc.url, nil))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Fatal(resp.StatusCode)
		}
	}
}

func TestAnalyticsIngressAvailability(t *testing.T) {
	mini := miniredis.RunT(t)
	r := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	defer r.Close()
	helper := &api.HarukiToolboxRouterHelpers{DBManager: &database.HarukiToolboxDBManager{Redis: &manager.HarukiRedisManager{Redis: r}}}
	now := adminNow()
	from := now.Add(-time.Hour)
	result := queryProxyIngress(context.Background(), helper, from, now)
	if !result.Available || result.Complete || len(result.Items) == 0 {
		t.Fatalf("unexpected availability %+v", result)
	}
	key := platform.IngressKey(now, "3", "accepted")
	mini.Set(key, "12")
	result = queryProxyIngress(context.Background(), helper, from, now)
	var total int64
	for _, item := range result.Items {
		total += item.Count
	}
	if total != 12 {
		t.Fatal(total)
	}
	mini.Set(key, "invalid")
	result = queryProxyIngress(context.Background(), helper, from, now)
	if result.Available {
		t.Fatal("corrupt counter accepted")
	}
	mini.Close()
	result = queryProxyIngress(context.Background(), helper, from, now)
	if result.Available {
		t.Fatal("Redis failure advertised as available")
	}
}
