package main

import (
	"context"
	stdsql "database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"haruki-suite/config"
	"haruki-suite/utils/database/postgresql"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

type tableSpec struct {
	Table string
	File  string
}

type tableColumn struct {
	Name     string
	DataType string
}

var importOrder = []tableSpec{
	{Table: "users", File: "users.json"},
	{Table: "groups", File: "groups.json"},
	{Table: "group_lists", File: "group_lists.json"},
	{Table: "friend_links", File: "friend_links.json"},
	{Table: "social_platform_infos", File: "social_platform_infos.json"},
	{Table: "authorize_social_platform_infos", File: "authorize_social_platform_infos.json"},
	{Table: "game_account_bindings", File: "game_account_bindings.json"},
	{Table: "ios_script_codes", File: "ios_script_codes.json"},
	{Table: "upload_logs", File: "upload_logs.json"},
	{Table: "oauth_clients", File: "oauth_clients.json"},
}

func main() {
	dir := flag.String("dir", "db_export", "directory containing exported JSON files")
	batchSize := flag.Int("batch-size", 250, "max rows per insert batch")
	flag.Parse()

	if *batchSize <= 0 {
		fmt.Fprintln(os.Stderr, "batch-size must be positive")
		os.Exit(1)
	}

	configPath, err := config.LoadGlobalFromEnvOrDefault()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config failed: %v\n", err)
		os.Exit(1)
	}

	dbClient, err := postgresql.Open(config.Cfg.UserSystem.DBType, config.Cfg.UserSystem.DBURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open database failed: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = dbClient.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := dbClient.Schema.Create(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "create schema failed: %v\n", err)
		os.Exit(1)
	}

	sqlDB := dbClient.SQLDB()
	if sqlDB == nil {
		fmt.Fprintln(os.Stderr, "underlying SQL DB is not available")
		os.Exit(1)
	}

	if err := ensureLegacySchema(ctx, sqlDB); err != nil {
		fmt.Fprintf(os.Stderr, "ensure legacy schema failed: %v\n", err)
		os.Exit(1)
	}
	if err := truncateImportTables(ctx, sqlDB, importOrder); err != nil {
		fmt.Fprintf(os.Stderr, "truncate import tables failed: %v\n", err)
		os.Exit(1)
	}

	totalImported := 0
	for _, spec := range importOrder {
		path := filepath.Join(strings.TrimSpace(*dir), spec.File)
		rows, err := loadJSONRows(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "load %s failed: %v\n", path, err)
			os.Exit(1)
		}
		if spec.Table == "users" {
			if adjusted := normalizeUserEmailCollisions(rows); adjusted > 0 {
				fmt.Printf("[info] table=users deduplicated_casefold_emails=%d\n", adjusted)
			}
		}
		if len(rows) == 0 {
			fmt.Printf("[skip] table=%s file=%s rows=0\n", spec.Table, path)
			continue
		}
		imported, err := importTableRows(ctx, sqlDB, spec.Table, rows, *batchSize)
		if err != nil {
			fmt.Fprintf(os.Stderr, "import %s failed: %v\n", spec.Table, err)
			os.Exit(1)
		}
		totalImported += imported
		fmt.Printf("[ok] table=%s rows=%d\n", spec.Table, imported)
	}

	fmt.Printf("Config: %s\n", configPath)
	fmt.Printf("Summary: tables=%d rows=%d source=%s\n", len(importOrder), totalImported, strings.TrimSpace(*dir))
}

func ensureLegacySchema(ctx context.Context, sqlDB *stdsql.DB) error {
	statements := []string{
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS password_hash TEXT;`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS email_verified BOOLEAN;`,
		`CREATE TABLE IF NOT EXISTS oauth_clients (
			id BIGINT PRIMARY KEY,
			client_id TEXT NOT NULL UNIQUE,
			client_secret TEXT NOT NULL,
			name TEXT NOT NULL,
			client_type TEXT NOT NULL,
			redirect_uris JSONB NOT NULL DEFAULT '[]'::jsonb,
			scopes JSONB NOT NULL DEFAULT '[]'::jsonb,
			active BOOLEAN NOT NULL DEFAULT TRUE,
			created_at TIMESTAMPTZ NULL
		);`,
	}
	for _, statement := range statements {
		if _, err := sqlDB.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func truncateImportTables(ctx context.Context, sqlDB *stdsql.DB, specs []tableSpec) error {
	if len(specs) == 0 {
		return nil
	}
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, quoteIdentifier(spec.Table))
	}
	statement := "TRUNCATE TABLE " + strings.Join(names, ", ") + " RESTART IDENTITY CASCADE"
	_, err := sqlDB.ExecContext(ctx, statement)
	return err
}

func loadJSONRows(path string) ([]map[string]any, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rows []map[string]any
	if err := json.Unmarshal(content, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func importTableRows(ctx context.Context, sqlDB *stdsql.DB, table string, rows []map[string]any, batchSize int) (int, error) {
	columns, err := loadTableColumns(ctx, sqlDB, table)
	if err != nil {
		return 0, err
	}
	insertColumns := selectImportColumns(columns, rows)
	if len(insertColumns) == 0 {
		return 0, fmt.Errorf("no matching columns found for table %s", table)
	}

	imported := 0
	for start := 0; start < len(rows); start += batchSize {
		end := start + batchSize
		if end > len(rows) {
			end = len(rows)
		}
		if err := insertBatch(ctx, sqlDB, table, insertColumns, rows[start:end]); err != nil {
			return imported, err
		}
		imported += end - start
	}
	if err := resetSerialSequence(ctx, sqlDB, table, columns); err != nil {
		return imported, err
	}
	return imported, nil
}

func loadTableColumns(ctx context.Context, sqlDB *stdsql.DB, table string) ([]tableColumn, error) {
	rows, err := sqlDB.QueryContext(ctx, `
SELECT column_name, data_type
FROM information_schema.columns
WHERE table_schema = 'public' AND table_name = $1
ORDER BY ordinal_position ASC
`, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var columns []tableColumn
	for rows.Next() {
		var column tableColumn
		if err := rows.Scan(&column.Name, &column.DataType); err != nil {
			return nil, err
		}
		columns = append(columns, column)
	}
	return columns, rows.Err()
}

func selectImportColumns(columns []tableColumn, rows []map[string]any) []tableColumn {
	present := make(map[string]struct{})
	for _, row := range rows {
		for key := range row {
			present[key] = struct{}{}
		}
	}
	selected := make([]tableColumn, 0, len(columns))
	for _, column := range columns {
		if _, ok := present[column.Name]; ok {
			selected = append(selected, column)
		}
	}
	return selected
}

func insertBatch(ctx context.Context, sqlDB *stdsql.DB, table string, columns []tableColumn, rows []map[string]any) error {
	if len(rows) == 0 {
		return nil
	}

	columnNames := make([]string, len(columns))
	for i, column := range columns {
		columnNames[i] = quoteIdentifier(column.Name)
	}

	var (
		valueGroups []string
		args        []any
	)
	args = make([]any, 0, len(rows)*len(columns))
	valueGroups = make([]string, 0, len(rows))
	placeholderIndex := 1
	for _, row := range rows {
		placeholders := make([]string, len(columns))
		for i, column := range columns {
			placeholders[i] = fmt.Sprintf("$%d", placeholderIndex)
			args = append(args, normalizeImportValue(row[column.Name], column.DataType))
			placeholderIndex++
		}
		valueGroups = append(valueGroups, "("+strings.Join(placeholders, ", ")+")")
	}

	statement := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES %s",
		quoteIdentifier(table),
		strings.Join(columnNames, ", "),
		strings.Join(valueGroups, ", "),
	)
	_, err := sqlDB.ExecContext(ctx, statement, args...)
	return err
}

func normalizeImportValue(value any, dataType string) any {
	if value == nil {
		return nil
	}

	switch typed := value.(type) {
	case map[string]any, []any:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return nil
		}
		return string(encoded)
	case float64:
		switch dataType {
		case "integer", "bigint", "smallint":
			return int64(typed)
		default:
			return typed
		}
	default:
		return typed
	}
}

func resetSerialSequence(ctx context.Context, sqlDB *stdsql.DB, table string, columns []tableColumn) error {
	for _, column := range columns {
		if column.Name != "id" {
			continue
		}
		switch column.DataType {
		case "integer", "bigint", "smallint":
			var sequenceName stdsql.NullString
			if err := sqlDB.QueryRowContext(ctx, "SELECT pg_get_serial_sequence($1, 'id')", table).Scan(&sequenceName); err != nil {
				return err
			}
			if !sequenceName.Valid || strings.TrimSpace(sequenceName.String) == "" {
				return nil
			}
			statement := fmt.Sprintf(
				"SELECT setval($1::regclass, COALESCE((SELECT MAX(id) FROM %s), 1), true)",
				quoteIdentifier(table),
			)
			if _, err := sqlDB.ExecContext(ctx, statement, sequenceName.String); err != nil {
				return err
			}
			return nil
		}
	}
	return nil
}

func normalizeUserEmailCollisions(rows []map[string]any) int {
	seen := make(map[string]struct{}, len(rows))
	adjusted := 0
	for _, row := range rows {
		rawEmail, ok := row["email"].(string)
		if !ok {
			continue
		}
		email := strings.TrimSpace(rawEmail)
		if email == "" {
			continue
		}
		normalized := strings.ToLower(email)
		if _, exists := seen[normalized]; !exists {
			seen[normalized] = struct{}{}
			row["email"] = email
			continue
		}
		userID := strings.TrimSpace(fmt.Sprint(row["id"]))
		candidate := buildDeduplicatedEmail(email, userID, len(seen))
		for {
			lowerCandidate := strings.ToLower(candidate)
			if _, exists := seen[lowerCandidate]; !exists {
				seen[lowerCandidate] = struct{}{}
				row["email"] = candidate
				adjusted++
				break
			}
			candidate = buildDeduplicatedEmail(email, userID, len(seen)+1)
		}
	}
	return adjusted
}

func buildDeduplicatedEmail(email string, userID string, salt int) string {
	email = strings.TrimSpace(email)
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return fmt.Sprintf("%s+dup-%s-%d", email, userID, salt)
	}
	localPart := email[:at]
	domain := email[at+1:]
	return fmt.Sprintf("%s+dup-%s-%d@%s", localPart, userID, salt, domain)
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(strings.TrimSpace(value), `"`, `""`) + `"`
}
