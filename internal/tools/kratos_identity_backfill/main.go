package main

import (
	"context"
	stdsql "database/sql"
	"flag"
	"fmt"
	"haruki-suite/config"
	platformIdentity "haruki-suite/internal/platform/identity"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

const (
	validateBackfillUserIDShapeSQL = `
SELECT
  COUNT(*) FILTER (WHERE id !~ '^[0-9]+$') AS non_numeric_count,
  COALESCE(MIN(LENGTH(id)), 0) AS min_len,
  COALESCE(MAX(LENGTH(id)), 0) AS max_len
FROM users
WHERE kratos_identity_id IS NULL;
`
)

func main() {
	apply := flag.Bool("apply", false, "apply updates to database (default: dry-run)")
	limit := flag.Int("limit", 0, "max users to scan (0 means no limit)")
	batchSize := flag.Int("batch-size", 200, "number of users fetched per DB batch")
	resumeFrom := flag.String("resume-from", "", "resume scan from user ID (exclusive)")
	requestTimeout := flag.Duration("request-timeout", 10*time.Second, "timeout per Kratos identity lookup request")
	retries := flag.Int("retries", 3, "retry times for transient Kratos lookup errors")
	retryDelay := flag.Duration("retry-delay", 500*time.Millisecond, "delay between retries for transient Kratos lookup errors")
	allowUnsafeLexicalCursor := flag.Bool("allow-unsafe-lexical-cursor", false, "allow resume cursor on variable/non-numeric user IDs")
	flag.Parse()
	if *batchSize <= 0 {
		fmt.Fprintln(os.Stderr, "batch-size must be greater than 0")
		os.Exit(1)
	}
	if *retries <= 0 {
		fmt.Fprintln(os.Stderr, "retries must be greater than 0")
		os.Exit(1)
	}
	if *requestTimeout < 0 {
		fmt.Fprintln(os.Stderr, "request-timeout must be >= 0")
		os.Exit(1)
	}
	if *retryDelay < 0 {
		fmt.Fprintln(os.Stderr, "retry-delay must be >= 0")
		os.Exit(1)
	}

	configPath, err := config.LoadGlobalFromEnvOrDefault()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config failed: %v\n", err)
		os.Exit(1)
	}
	cfg := config.Cfg
	if strings.TrimSpace(cfg.UserSystem.KratosAdminURL) == "" {
		fmt.Fprintln(os.Stderr, "kratos admin url is empty, abort")
		os.Exit(1)
	}

	dbClient, err := postgresql.Open(cfg.UserSystem.DBType, cfg.UserSystem.DBURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open database failed: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = dbClient.Close() }()

	sessionHandler := harukiAPIHelper.NewSessionHandler(nil, "")
	sessionHandler.ConfigureIdentityProvider(
		cfg.UserSystem.AuthProvider,
		cfg.UserSystem.KratosPublicURL,
		cfg.UserSystem.KratosAdminURL,
		cfg.UserSystem.KratosSessionHeader,
		cfg.UserSystem.KratosSessionCookie,
		cfg.UserSystem.KratosAutoLinkByEmail,
		cfg.UserSystem.KratosAutoProvisionUser,
		time.Duration(cfg.UserSystem.KratosRequestTimeout)*time.Second,
		dbClient,
	)

	ctx := context.Background()
	if err := ensureBackfillCursorSafety(ctx, dbClient, *allowUnsafeLexicalCursor); err != nil {
		fmt.Fprintf(os.Stderr, "validate backfill cursor safety failed: %v\n", err)
		os.Exit(1)
	}
	scanned := 0
	updated := 0
	wouldUpdate := 0
	notFound := 0
	skipped := 0
	failed := 0
	lastScannedID := strings.TrimSpace(*resumeFrom)

	fmt.Printf("Config: %s\n", configPath)
	if *apply {
		fmt.Println("Mode: APPLY")
	} else {
		fmt.Println("Mode: DRY-RUN")
	}
	fmt.Printf("Batch size: %d\n", *batchSize)
	fmt.Printf("Resume from: %q\n", lastScannedID)
	fmt.Printf("Limit: %d (0 means unlimited)\n", *limit)
	fmt.Printf("Request timeout: %s, retries: %d, retry delay: %s\n", requestTimeout.String(), *retries, retryDelay.String())
	fmt.Printf("Allow unsafe lexical cursor: %v\n", *allowUnsafeLexicalCursor)

	remaining := *limit
	for {
		currentBatchSize := *batchSize
		if remaining > 0 && remaining < currentBatchSize {
			currentBatchSize = remaining
		}
		batchQuery := dbClient.User.Query().
			Where(userSchema.KratosIdentityIDIsNil()).
			Select(userSchema.FieldID, userSchema.FieldEmail).
			Order(postgresql.Asc(userSchema.FieldID)).
			Limit(currentBatchSize)
		if lastScannedID != "" {
			batchQuery = batchQuery.Where(userSchema.IDGT(lastScannedID))
		}
		users, err := batchQuery.All(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "query users failed: %v\n", err)
			os.Exit(1)
		}
		if len(users) == 0 {
			break
		}

		for _, user := range users {
			lastScannedID = user.ID
			if remaining > 0 {
				remaining--
			}
			scanned++
			email := platformIdentity.NormalizeEmail(user.Email)
			if email == "" {
				skipped++
				fmt.Printf("[skip] user=%s reason=empty_email\n", user.ID)
				if remaining == 0 && *limit > 0 {
					break
				}
				continue
			}

			identityID, err := findKratosIdentityWithRetry(ctx, sessionHandler, email, *retries, *requestTimeout, *retryDelay)
			if err != nil {
				if harukiAPIHelper.IsKratosIdentityUnmappedError(err) {
					notFound++
					fmt.Printf("[miss] user=%s email=%s reason=identity_not_found\n", user.ID, email)
				} else {
					failed++
					fmt.Printf("[fail] user=%s email=%s err=%v\n", user.ID, email, err)
				}
				if remaining == 0 && *limit > 0 {
					break
				}
				continue
			}

			if !*apply {
				wouldUpdate++
				fmt.Printf("[plan] user=%s email=%s kratos_identity_id=%s\n", user.ID, email, identityID)
				if remaining == 0 && *limit > 0 {
					break
				}
				continue
			}

			if _, err := dbClient.User.UpdateOneID(user.ID).SetKratosIdentityID(identityID).Save(ctx); err != nil {
				failed++
				fmt.Printf("[fail] user=%s email=%s identity=%s update_err=%v\n", user.ID, email, identityID, err)
				if remaining == 0 && *limit > 0 {
					break
				}
				continue
			}
			updated++
			fmt.Printf("[ok] user=%s email=%s kratos_identity_id=%s\n", user.ID, email, identityID)
			if remaining == 0 && *limit > 0 {
				break
			}
		}
		if remaining == 0 && *limit > 0 {
			break
		}
	}

	fmt.Println("----- Summary -----")
	fmt.Printf("scanned=%d skipped=%d not_found=%d failed=%d", scanned, skipped, notFound, failed)
	if *apply {
		fmt.Printf(" updated=%d", updated)
	} else {
		fmt.Printf(" would_update=%d", wouldUpdate)
	}
	fmt.Printf(" last_scanned_id=%q\n", lastScannedID)
}

func findKratosIdentityWithRetry(
	ctx context.Context,
	sessionHandler *harukiAPIHelper.SessionHandler,
	email string,
	retries int,
	requestTimeout time.Duration,
	retryDelay time.Duration,
) (string, error) {
	if retries < 1 {
		retries = 1
	}
	var lastErr error
	for attempt := 1; attempt <= retries; attempt++ {
		lookupCtx := ctx
		cancel := func() {}
		if requestTimeout > 0 {
			lookupCtx, cancel = context.WithTimeout(ctx, requestTimeout)
		}
		identityID, err := sessionHandler.FindKratosIdentityIDByEmail(lookupCtx, email)
		cancel()
		if err == nil {
			return identityID, nil
		}
		if harukiAPIHelper.IsKratosIdentityUnmappedError(err) || harukiAPIHelper.IsKratosInvalidInputError(err) || harukiAPIHelper.IsKratosIdentityConflictError(err) {
			return "", err
		}
		lastErr = err
		if attempt >= retries {
			break
		}
		if retryDelay > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(retryDelay):
			}
		}
	}
	return "", lastErr
}

func ensureBackfillCursorSafety(ctx context.Context, dbClient *postgresql.Client, allowUnsafeLexicalCursor bool) error {
	sqlDB := dbClient.SQLDB()
	if sqlDB == nil {
		return fmt.Errorf("underlying SQL DB is not available")
	}

	var nonNumericCount int64
	var minLen int
	var maxLen int
	if err := sqlDB.QueryRowContext(ctx, validateBackfillUserIDShapeSQL).Scan(&nonNumericCount, &minLen, &maxLen); err != nil {
		if err == stdsql.ErrNoRows {
			return nil
		}
		return err
	}

	if (nonNumericCount > 0 || minLen != maxLen) && !allowUnsafeLexicalCursor {
		return fmt.Errorf(
			"unsafe ID shape detected for keyset cursor (non_numeric=%d, min_len=%d, max_len=%d). "+
				"Either normalize IDs or rerun with --allow-unsafe-lexical-cursor",
			nonNumericCount, minLen, maxLen,
		)
	}
	return nil
}
