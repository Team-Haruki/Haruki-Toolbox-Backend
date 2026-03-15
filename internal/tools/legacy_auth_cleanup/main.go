package main

import (
	"context"
	stdsql "database/sql"
	"flag"
	"fmt"
	"haruki-suite/config"
	"haruki-suite/utils/database/postgresql"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq"

	"golang.org/x/crypto/bcrypt"
)

func main() {
	apply := flag.Bool("apply", false, "apply cleanup changes (default: dry-run)")
	purgeLegacyOAuth := flag.Bool("purge-legacy-oauth", true, "delete rows from legacy oauth tables")
	dropLegacyOAuthTables := flag.Bool("drop-legacy-oauth-tables", false, "drop legacy oauth tables after purge")
	scrubManagedPasswords := flag.Bool("scrub-managed-passwords", true, "replace password_hash for Kratos-managed users with a placeholder hash")
	clearManagedEmailVerified := flag.Bool("clear-managed-email-verified", false, "set email_verified=NULL for Kratos-managed users")
	dropUserAuthColumns := flag.Bool("drop-user-auth-columns", false, "drop users.password_hash and users.email_verified after migration")
	placeholderSecret := flag.String("placeholder-secret", "legacy-auth-disabled", "placeholder secret used to generate replacement password hashes")
	requestTimeout := flag.Duration("request-timeout", 15*time.Second, "timeout for each cleanup statement")
	flag.Parse()

	configPath, err := config.LoadGlobalFromEnvOrDefault()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config failed: %v\n", err)
		os.Exit(1)
	}
	cfg := config.Cfg
	dbClient, err := postgresql.Open(cfg.UserSystem.DBType, cfg.UserSystem.DBURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open database failed: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = dbClient.Close() }()
	sqlDB := dbClient.SQLDB()
	if sqlDB == nil {
		fmt.Fprintln(os.Stderr, "underlying SQL DB is not available")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *requestTimeout)
	defer cancel()
	stats, err := queryLegacyStats(ctx, sqlDB)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query legacy stats failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Config: %s\n", configPath)
	if *apply {
		fmt.Println("Mode: APPLY")
	} else {
		fmt.Println("Mode: DRY-RUN")
	}
	fmt.Printf("legacy oauth_clients=%d oauth_authorizations=%d oauth_tokens=%d managed_users=%d managed_users_with_password_hash=%d\n",
		stats.OAuthClients, stats.OAuthAuthorizations, stats.OAuthTokens, stats.ManagedUsers, stats.ManagedUsersWithPasswordHash,
	)
	fmt.Printf("purge-legacy-oauth=%v drop-legacy-oauth-tables=%v scrub-managed-passwords=%v clear-managed-email-verified=%v drop-user-auth-columns=%v\n",
		*purgeLegacyOAuth, *dropLegacyOAuthTables, *scrubManagedPasswords, *clearManagedEmailVerified, *dropUserAuthColumns,
	)

	if !*apply {
		return
	}

	if *purgeLegacyOAuth {
		for _, tableName := range []string{"oauth_tokens", "oauth_authorizations", "oauth_clients"} {
			if err := execTableCleanup(sqlDB, *requestTimeout, tableName, "DELETE FROM "+tableName); err != nil {
				fatalf("purge %s failed: %v", tableName, err)
			}
		}
	}

	if *dropLegacyOAuthTables {
		for _, statement := range []string{
			"DROP TABLE IF EXISTS oauth_tokens",
			"DROP TABLE IF EXISTS oauth_authorizations",
			"DROP TABLE IF EXISTS oauth_clients",
		} {
			if err := execCleanup(sqlDB, *requestTimeout, statement); err != nil {
				fatalf("drop legacy oauth tables failed: %v", err)
			}
		}
	}

	if *scrubManagedPasswords {
		placeholderHash, err := bcrypt.GenerateFromPassword([]byte(strings.TrimSpace(*placeholderSecret)), bcrypt.DefaultCost)
		if err != nil {
			fatalf("generate placeholder hash failed: %v", err)
		}
		if err := execCleanup(sqlDB, *requestTimeout, fmt.Sprintf(
			"UPDATE users SET password_hash = '%s' WHERE kratos_identity_id IS NOT NULL AND btrim(kratos_identity_id) <> ''",
			escapeSQLString(string(placeholderHash)),
		)); err != nil {
			fatalf("scrub managed password hashes failed: %v", err)
		}
	}

	if *clearManagedEmailVerified {
		if err := execCleanup(sqlDB, *requestTimeout, "UPDATE users SET email_verified = NULL WHERE kratos_identity_id IS NOT NULL AND btrim(kratos_identity_id) <> ''"); err != nil {
			fatalf("clear managed email_verified failed: %v", err)
		}
	}

	if *dropUserAuthColumns {
		for _, statement := range []string{
			"ALTER TABLE users DROP COLUMN IF EXISTS password_hash",
			"ALTER TABLE users DROP COLUMN IF EXISTS email_verified",
		} {
			if err := execCleanup(sqlDB, *requestTimeout, statement); err != nil {
				fatalf("drop user auth columns failed: %v", err)
			}
		}
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), *requestTimeout)
	defer cancel2()
	stats, err = queryLegacyStats(ctx2, sqlDB)
	if err != nil {
		fatalf("query post-cleanup stats failed: %v", err)
	}
	fmt.Printf("Post-cleanup: legacy oauth_clients=%d oauth_authorizations=%d oauth_tokens=%d managed_users=%d managed_users_with_password_hash=%d\n",
		stats.OAuthClients, stats.OAuthAuthorizations, stats.OAuthTokens, stats.ManagedUsers, stats.ManagedUsersWithPasswordHash,
	)
}

type legacyStats struct {
	OAuthClients                 int
	OAuthAuthorizations          int
	OAuthTokens                  int
	ManagedUsers                 int
	ManagedUsersWithPasswordHash int
}

func queryLegacyStats(ctx context.Context, sqlDB *stdsql.DB) (*legacyStats, error) {
	oauthClientsCount, err := countTableRows(ctx, sqlDB, "oauth_clients")
	if err != nil {
		return nil, err
	}
	oauthAuthorizationsCount, err := countTableRows(ctx, sqlDB, "oauth_authorizations")
	if err != nil {
		return nil, err
	}
	oauthTokensCount, err := countTableRows(ctx, sqlDB, "oauth_tokens")
	if err != nil {
		return nil, err
	}
	managedUsersCount, err := countQueryRows(ctx, sqlDB, "SELECT COUNT(*) FROM users WHERE kratos_identity_id IS NOT NULL AND btrim(kratos_identity_id) <> ''")
	if err != nil {
		return nil, err
	}
	managedUsersWithPasswordHash, err := countQueryRows(ctx, sqlDB, "SELECT COUNT(*) FROM users WHERE kratos_identity_id IS NOT NULL AND btrim(kratos_identity_id) <> '' AND password_hash IS NOT NULL AND btrim(password_hash) <> ''")
	if err != nil {
		return nil, err
	}
	return &legacyStats{
		OAuthClients:                 oauthClientsCount,
		OAuthAuthorizations:          oauthAuthorizationsCount,
		OAuthTokens:                  oauthTokensCount,
		ManagedUsers:                 managedUsersCount,
		ManagedUsersWithPasswordHash: managedUsersWithPasswordHash,
	}, nil
}

func countTableRows(ctx context.Context, sqlDB *stdsql.DB, tableName string) (int, error) {
	exists, err := tableExists(ctx, sqlDB, tableName)
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, nil
	}
	return countQueryRows(ctx, sqlDB, "SELECT COUNT(*) FROM "+tableName)
}

func countQueryRows(ctx context.Context, sqlDB *stdsql.DB, statement string) (int, error) {
	var count int
	if err := sqlDB.QueryRowContext(ctx, statement).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func tableExists(ctx context.Context, sqlDB *stdsql.DB, tableName string) (bool, error) {
	var regclassName stdsql.NullString
	if err := sqlDB.QueryRowContext(ctx, "SELECT to_regclass($1)", tableName).Scan(&regclassName); err != nil {
		return false, err
	}
	return regclassName.Valid && strings.TrimSpace(regclassName.String) != "", nil
}

func execTableCleanup(sqlDB *stdsql.DB, timeout time.Duration, tableName string, statement string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	exists, err := tableExists(ctx, sqlDB, tableName)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	_, err = sqlDB.ExecContext(ctx, statement)
	return err
}

func execCleanup(sqlDB *stdsql.DB, timeout time.Duration, statement string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, err := sqlDB.ExecContext(ctx, statement)
	return err
}

func escapeSQLString(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
