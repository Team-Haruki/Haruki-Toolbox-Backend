package bootstrap

import (
	"context"
	"fmt"

	db "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/uploadlog"
)

const uploadLogColumnsSQL = `SELECT column_name, is_nullable FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'upload_logs'`

// The upload checks below run after the optional Ent auto-migration, so with
// backend.auto_migrate enabled they pass on their own: Schema.Create adds the
// missing columns and relaxes NOT NULL (ent applies column modifications and
// only skips drops). With it disabled, the operator applies the HarukiProxy v3
// and upload-grants schema migrations by hand; their SQL lives in the
// operations docs, not in this repository.
const (
	uploadSchemaMigrationHint = "apply the HarukiProxy v3 and upload-grants schema migrations (see the operations docs) or enable backend.auto_migrate"
	proxyV3MigrationHint      = "apply the HarukiProxy v3 schema migration (see the operations docs) or enable backend.auto_migrate"
	uploadGrantMigrationHint  = "apply the upload-grants schema migration (see the operations docs) or enable backend.auto_migrate"
)

// validateUploadLogSchema requires every upload_logs column this build knows
// and nullable game_user_id / toolbox_user_id.
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
			return fmt.Errorf("upload_logs.%s column is missing; %s", name, uploadSchemaMigrationHint)
		}
	}
	if columns[uploadlog.FieldGameUserID] != "YES" || columns[uploadlog.FieldToolboxUserID] != "YES" {
		return fmt.Errorf("upload_logs.%s and upload_logs.%s must be nullable; %s",
			uploadlog.FieldGameUserID, uploadlog.FieldToolboxUserID, proxyV3MigrationHint)
	}
	return nil
}

// validateUploadGrantSchema requires game_account_data_grants.can_read and
// can_write as NOT NULL boolean columns.
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
		return fmt.Errorf("game_account_data_grants.can_read and can_write must be NOT NULL boolean columns; %s", uploadGrantMigrationHint)
	}
	return nil
}
