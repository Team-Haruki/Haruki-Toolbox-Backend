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
			return fmt.Errorf("upload_logs.%s missing; apply docs/harukiproxy-v3-schema.sql and docs/upload-write-grants-schema.sql or enable backend.auto_migrate", name)
		}
	}
	if columns[uploadlog.FieldGameUserID] != "YES" || columns[uploadlog.FieldToolboxUserID] != "YES" {
		return fmt.Errorf("upload_logs identity fields must be nullable; apply docs/harukiproxy-v3-schema.sql")
	}
	return nil
}

func validateUploadGrantSchema(ctx context.Context, client *db.Client) error {
	if client.SQLDB() == nil {
		return fmt.Errorf("underlying SQL DB unavailable")
	}
	var count int
	err := client.SQLDB().QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='game_account_data_grants' AND column_name IN ('can_read','can_write') AND data_type='boolean' AND is_nullable='NO'`).Scan(&count)
	if err != nil {
		return err
	}
	if count != 2 {
		return fmt.Errorf("apply docs/upload-write-grants-schema.sql before deploying")
	}
	return nil
}
