package bootstrap

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
	dbManager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"strings"
)

const (
	checkUsersTableExistsSQL                = `SELECT to_regclass('public.users')`
	checkUsersKratosIdentityColumnExistsSQL = `
SELECT EXISTS (
	SELECT 1
	FROM information_schema.columns
	WHERE table_schema = 'public'
		AND table_name = 'users'
		AND column_name = 'kratos_identity_id'
);
`
	checkUsersKratosIdentityUniqueIndexExistsSQL = `
SELECT EXISTS (
	SELECT 1
	FROM pg_indexes
	WHERE schemaname = 'public'
		AND tablename = 'users'
		AND indexdef ILIKE '%UNIQUE INDEX%'
		AND indexdef ILIKE '%(kratos_identity_id)%'
);
`
	checkUsersEmailLowerUniqueIndexExistsSQL = `
SELECT EXISTS (
	SELECT 1
	FROM pg_indexes
	WHERE schemaname = 'public'
		AND tablename = 'users'
		AND indexdef ILIKE '%UNIQUE INDEX%'
		AND (
			indexdef ILIKE '%lower((email)::text)%'
			OR indexdef ILIKE '%lower(email)%'
		)
);
`

	createUsersEmailLowerUniqueIndexSQL = `
CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower_unique_idx
ON users (LOWER(email));
`
	findUsersEmailLowerDuplicateSQL = `
SELECT LOWER(email) AS normalized_email, COUNT(*) AS cnt
FROM users
GROUP BY LOWER(email)
HAVING COUNT(*) > 1
LIMIT 1;
`
	createUsersKratosIdentityColumnSQL = `
ALTER TABLE users
ADD COLUMN IF NOT EXISTS kratos_identity_id TEXT;
`
	createUsersKratosIdentityUniqueIndexSQL = `
CREATE UNIQUE INDEX IF NOT EXISTS users_kratos_identity_id_unique_idx
ON users (kratos_identity_id);
`

	checkWebhookEndpointsTableExistsSQL         = `SELECT to_regclass('public.webhook_endpoints')`
	checkWebhookSubscriptionsTableExistsSQL     = `SELECT to_regclass('public.webhook_subscriptions')`
	checkWebhookEndpointsEnabledColumnExistsSQL = `
SELECT EXISTS (
	SELECT 1
	FROM information_schema.columns
	WHERE table_schema = 'public'
		AND table_name = 'webhook_endpoints'
		AND column_name = 'enabled'
);
`
)

func ensureUsersEmailLowerUniqueIndex(ctx context.Context, entClient *dbManager.Client) error {
	sqlDB := entClient.SQLDB()
	if sqlDB == nil {
		return fmt.Errorf("underlying SQL DB is not available")
	}

	var normalizedEmail string
	var duplicateCount int
	err := sqlDB.QueryRowContext(ctx, findUsersEmailLowerDuplicateSQL).Scan(&normalizedEmail, &duplicateCount)
	if err != nil && !errors.Is(err, stdsql.ErrNoRows) {
		return fmt.Errorf("query case-insensitive email duplicates: %w", err)
	}
	if err == nil {
		return fmt.Errorf("case-insensitive duplicate emails exist (normalized=%q, count=%d)", normalizedEmail, duplicateCount)
	}

	if _, err := sqlDB.ExecContext(ctx, createUsersEmailLowerUniqueIndexSQL); err != nil {
		return fmt.Errorf("create users lower(email) unique index: %w", err)
	}
	return nil
}

func ensureUsersKratosIdentityColumn(ctx context.Context, entClient *dbManager.Client) error {
	sqlDB := entClient.SQLDB()
	if sqlDB == nil {
		return fmt.Errorf("underlying SQL DB is not available")
	}

	if _, err := sqlDB.ExecContext(ctx, createUsersKratosIdentityColumnSQL); err != nil {
		return fmt.Errorf("add users.kratos_identity_id column: %w", err)
	}
	if _, err := sqlDB.ExecContext(ctx, createUsersKratosIdentityUniqueIndexSQL); err != nil {
		return fmt.Errorf("create users.kratos_identity_id unique index: %w", err)
	}
	return nil
}

func querySchemaExists(ctx context.Context, entClient *dbManager.Client, query string) (bool, error) {
	sqlDB := entClient.SQLDB()
	if sqlDB == nil {
		return false, fmt.Errorf("underlying SQL DB is not available")
	}

	var exists bool
	if err := sqlDB.QueryRowContext(ctx, query).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func validateUsersEmailLowerUniqueIndex(ctx context.Context, entClient *dbManager.Client) error {
	exists, err := querySchemaExists(ctx, entClient, checkUsersEmailLowerUniqueIndexExistsSQL)
	if err != nil {
		return fmt.Errorf("check users lower(email) unique index: %w", err)
	}
	if !exists {
		return fmt.Errorf("users lower(email) unique index is missing; run schema migration or enable backend.auto_migrate")
	}
	return nil
}

func validateUsersKratosIdentityColumn(ctx context.Context, entClient *dbManager.Client) error {
	columnExists, err := querySchemaExists(ctx, entClient, checkUsersKratosIdentityColumnExistsSQL)
	if err != nil {
		return fmt.Errorf("check users.kratos_identity_id column: %w", err)
	}
	if !columnExists {
		return fmt.Errorf("users.kratos_identity_id column is missing; run schema migration or enable backend.auto_migrate")
	}

	indexExists, err := querySchemaExists(ctx, entClient, checkUsersKratosIdentityUniqueIndexExistsSQL)
	if err != nil {
		return fmt.Errorf("check users.kratos_identity_id unique index: %w", err)
	}
	if !indexExists {
		return fmt.Errorf("users.kratos_identity_id unique index is missing; run schema migration or enable backend.auto_migrate")
	}
	return nil
}

func ensureUsersSchemaCompatibility(ctx context.Context, entClient *dbManager.Client, autoMigrate bool) error {
	if autoMigrate {
		if err := ensureUsersEmailLowerUniqueIndex(ctx, entClient); err != nil {
			return err
		}
		if err := ensureUsersKratosIdentityColumn(ctx, entClient); err != nil {
			return err
		}
		return nil
	}

	if err := validateUsersEmailLowerUniqueIndex(ctx, entClient); err != nil {
		return err
	}
	if err := validateUsersKratosIdentityColumn(ctx, entClient); err != nil {
		return err
	}
	return nil
}

func usersTableExists(ctx context.Context, entClient *dbManager.Client) (bool, error) {
	return tableExists(ctx, entClient, checkUsersTableExistsSQL)
}

func tableExists(ctx context.Context, entClient *dbManager.Client, query string) (bool, error) {
	sqlDB := entClient.SQLDB()
	if sqlDB == nil {
		return false, fmt.Errorf("underlying SQL DB is not available")
	}

	var regclassName stdsql.NullString
	if err := sqlDB.QueryRowContext(ctx, query).Scan(&regclassName); err != nil {
		return false, err
	}
	return regclassName.Valid && strings.TrimSpace(regclassName.String) != "", nil
}

func validateTableExists(ctx context.Context, entClient *dbManager.Client, query, tableName string) error {
	exists, err := tableExists(ctx, entClient, query)
	if err != nil {
		return fmt.Errorf("check %s table existence: %w", tableName, err)
	}
	if !exists {
		return fmt.Errorf("%s table is missing; run schema migration or enable backend.auto_migrate", tableName)
	}
	return nil
}

func ensureWebhookSchemaCompatibility(ctx context.Context, entClient *dbManager.Client, autoMigrate bool) error {
	if autoMigrate {
		return nil
	}
	if err := validateTableExists(ctx, entClient, checkWebhookEndpointsTableExistsSQL, "webhook_endpoints"); err != nil {
		return err
	}
	if err := validateTableExists(ctx, entClient, checkWebhookSubscriptionsTableExistsSQL, "webhook_subscriptions"); err != nil {
		return err
	}
	columnExists, err := querySchemaExists(ctx, entClient, checkWebhookEndpointsEnabledColumnExistsSQL)
	if err != nil {
		return fmt.Errorf("check webhook_endpoints.enabled column: %w", err)
	}
	if !columnExists {
		return fmt.Errorf("webhook_endpoints.enabled column is missing; run schema migration or enable backend.auto_migrate")
	}
	return nil
}
