package bootstrap

import (
	"context"
	"fmt"

	db "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/uploadlog"
)

const uploadLogColumnsSQL = `SELECT column_name, is_nullable FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'upload_logs'`

func validateUploadLogSchema(ctx context.Context, client *db.Client) error {
	sqlDB := client.SQLDB()
	if sqlDB == nil {
		return fmt.Errorf("underlying SQL DB is unavailable")
	}
	rows, err := sqlDB.QueryContext(ctx, uploadLogColumnsSQL)
	if err != nil {
		return fmt.Errorf("inspect upload_logs: %w", err)
	}
	defer rows.Close()
	columns := map[string]string{}
	for rows.Next() {
		var name, nullable string
		if err := rows.Scan(&name, &nullable); err != nil {
			return err
		}
		columns[name] = nullable
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, name := range uploadlog.Columns {
		if _, ok := columns[name]; !ok {
			return fmt.Errorf("upload_logs.%s missing; apply docs/harukiproxy-v3-schema.sql or enable backend.auto_migrate", name)
		}
	}
	if columns[uploadlog.FieldGameUserID] != "YES" || columns[uploadlog.FieldToolboxUserID] != "YES" {
		return fmt.Errorf("upload_logs identity fields must be nullable; apply docs/harukiproxy-v3-schema.sql")
	}
	return nil
}
