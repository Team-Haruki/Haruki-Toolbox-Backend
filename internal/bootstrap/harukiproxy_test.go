package bootstrap

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	config "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/uploadlog"
)

func TestCompileProxyPolicy(t *testing.T) {
	p, err := compileHarukiProxyPolicy(config.Config{})
	if err != nil || p == nil {
		t.Fatal(err)
	}
	cfg := config.Config{}
	cfg.HarukiProxy.V3ClientPolicy = &config.HarukiProxyClientPolicy{}
	if _, err := compileHarukiProxyPolicy(cfg); err == nil {
		t.Fatal("empty policy accepted")
	}
	cfg.HarukiProxy.V3ClientPolicy = &config.HarukiProxyClientPolicy{AllowedChannels: []string{"preview"}, MinimumVersions: map[string]string{"preview": "3.0.0-beta.1"}}
	if _, err := compileHarukiProxyPolicy(cfg); err == nil {
		t.Fatal("wrong channel floor accepted")
	}
}
func TestUploadSchemaValidation(t *testing.T) {
	for _, tc := range []string{"valid", "missing", "not-null"} {
		t.Run(tc, func(t *testing.T) {
			client, mock := newBootstrapSQLMockClient(t)
			rows := sqlmock.NewRows([]string{"column_name", "is_nullable"})
			for _, name := range uploadlog.Columns {
				if tc == "missing" && name == "client_version" {
					continue
				}
				nullable := "YES"
				if tc == "not-null" && name == "game_user_id" {
					nullable = "NO"
				}
				rows.AddRow(name, nullable)
			}
			mock.ExpectQuery(regexp.QuoteMeta(uploadLogColumnsSQL)).WillReturnRows(rows)
			err := validateUploadLogSchema(context.Background(), client)
			if (err == nil) != (tc == "valid") {
				t.Fatalf("unexpected schema validation: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWriteGrantSchemaRequiresBothPermissions(t *testing.T) {
	for _, count := range []int{0, 1, 2} {
		client, mock := newBootstrapSQLMockClient(t)
		mock.ExpectQuery("SELECT count").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(count))
		err := validateUploadGrantSchema(context.Background(), client)
		if (err == nil) != (count == 2) {
			t.Fatalf("columns %d: %v", count, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	}
}
