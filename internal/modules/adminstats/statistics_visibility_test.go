package adminstats

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"
)

func TestDashboardAndTimeseriesUseSameAdminVisibility(t *testing.T) {
	db := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer db.Close()
	ctx := t.Context()
	now := time.Now().UTC().Add(-time.Minute)
	for id, role := range map[string]user.Role{"actor": user.RoleAdmin, "visible": user.RoleUser, "hidden": user.RoleSuperAdmin} {
		db.User.Create().SetID(id).SetName(id).SetEmail(id + "@example.test").SetRole(role).SetCreatedAt(now).ExecX(ctx)
		db.GameAccountBinding.Create().SetServer("jp").SetGameUserID(id).SetVerified(true).SetUserID(id).ExecX(ctx)
		db.UploadLog.Create().SetServer("jp").SetGameUserID(id).SetDataType("suite").SetUploadMethod("manual").
			SetSuccess(true).SetUploadTime(now).SetIdentityVerified(true).SetToolboxUserID(id).ExecX(ctx)
	}
	db.UploadLog.Create().SetServer("jp").SetGameUserID("visible").SetDataType("suite").SetUploadMethod("manual").
		SetSuccess(false).SetUploadTime(now).SetIdentityVerified(true).SetToolboxUserID("visible").SetActorUserID("hidden").ExecX(ctx)

	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{DBManager: &database.HarukiToolboxDBManager{DB: db}}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", "actor")
		c.Locals("userRole", c.Get("Test-Role"))
		return c.Next()
	})
	app.Get("/dashboard", handleGetDashboardStatistics(helper))
	app.Get("/timeseries", handleGetStatisticsTimeseries(helper))
	read := func(role, path string, out any) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Test-Role", role)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s returned %d", path, resp.StatusCode)
		}
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []struct {
		role                        string
		users, uploads, superAdmins int
	}{
		{"admin", 1, 1, 0},
		{"super_admin", 3, 4, 1},
	} {
		var dashboard struct {
			Data dashboardStatisticsResponse `json:"updatedData"`
		}
		read(want.role, "/dashboard", &dashboard)
		stats := dashboard.Data
		if stats.Users.Total != want.users || stats.Users.SuperAdmin != want.superAdmins ||
			stats.Bindings.Total != want.users || stats.Uploads.Total != want.uploads {
			t.Fatalf("%s dashboard has incorrect scope: %+v", want.role, stats)
		}
		var timeseries struct {
			Data statisticsTimeseriesResponse `json:"updatedData"`
		}
		read(want.role, "/timeseries", &timeseries)
		var users, uploads int
		for _, point := range timeseries.Data.Points {
			users += point.Registrations
			uploads += point.Uploads
		}
		if users != want.users || uploads != want.uploads {
			t.Fatalf("%s timeseries: got %d users and %d uploads", want.role, users, uploads)
		}
	}
}

func TestTimeseriesSQLBindsActorScopeForBothAggregates(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	from := time.Now().UTC().Add(-time.Hour)
	to := from.Add(time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta(registrationCountsHourSQL)).WithArgs(from, to, false, "actor", "UTC").
		WillReturnRows(sqlmock.NewRows([]string{"bucket_unix", "count"}).AddRow(from.Unix(), 1))
	mock.ExpectQuery(regexp.QuoteMeta(uploadCountsHourSQL)).WithArgs(from, to, false, "actor", "UTC").
		WillReturnRows(sqlmock.NewRows([]string{"bucket_unix", "total", "success"}).AddRow(from.Unix(), 2, 1))
	if rows, err := queryRegistrationCountsRawSQL(t.Context(), db, from, to, "hour", "UTC", "actor", false); err != nil || rows[from.Unix()] != 1 {
		t.Fatalf("registration counts = %v, %v", rows, err)
	}
	if rows, err := queryUploadCountsRawSQL(t.Context(), db, from, to, "hour", "UTC", "actor", false); err != nil || rows[from.Unix()].Failure != 1 {
		t.Fatalf("upload counts = %v, %v", rows, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTimeseriesFailsClosedOnUnavailablePostgresAggregate(t *testing.T) {
	for _, stage := range []string{"registration", "upload"} {
		t.Run(stage, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			client := postgresql.NewClient(postgresql.Driver(entsql.OpenDB(dialect.Postgres, db)))
			defer client.Close()
			failure := errors.New("test aggregate unavailable")
			if stage == "registration" {
				mock.ExpectQuery(regexp.QuoteMeta(registrationCountsHourSQL)).WillReturnError(failure)
			} else {
				mock.ExpectQuery(regexp.QuoteMeta(registrationCountsHourSQL)).
					WillReturnRows(sqlmock.NewRows([]string{"bucket_unix", "count"}))
				mock.ExpectQuery(regexp.QuoteMeta(uploadCountsHourSQL)).WillReturnError(failure)
			}
			helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{DBManager: &database.HarukiToolboxDBManager{DB: client}}
			app := fiber.New()
			app.Get("/", func(c fiber.Ctx) error {
				c.Locals("userID", "actor")
				c.Locals("userRole", "admin")
				return c.Next()
			}, handleGetStatisticsTimeseries(helper))
			res, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if res.StatusCode != http.StatusInternalServerError {
				t.Fatalf("failed aggregate returned %d", res.StatusCode)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
