package main

import (
	"bytes"
	"context"
	stdsql "database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"haruki-suite/config"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

type importState struct {
	LastID    string `json:"last_id"`
	Processed int    `json:"processed"`
	Created   int    `json:"created"`
	Existing  int    `json:"existing"`
	Failed    int    `json:"failed"`
}

type importUserRow struct {
	ID           string
	Email        string
	PasswordHash string
}

type toolRuntimeConfig struct {
	DBType              string
	DBURL               string
	KratosPublicURL     string
	KratosAdminURL      string
	KratosSessionHeader string
	KratosSessionCookie string
}

func main() {
	apply := flag.Bool("apply", false, "apply import to Kratos (default: dry-run)")
	batchSize := flag.Int("batch-size", 200, "number of users fetched per DB batch")
	limit := flag.Int("limit", 0, "max users to process (0 means unlimited)")
	resumeFrom := flag.String("resume-from", "", "resume from user ID (exclusive)")
	requestTimeout := flag.Duration("request-timeout", 10*time.Second, "timeout per Kratos request")
	stateFile := flag.String("state-file", ".codex-dev/kratos-user-import-state.json", "state file path for apply mode")
	dbTypeFlag := flag.String("db-type", "", "override source DB type; when provided with the other required flags the tool can run without HARUKI_CONFIG_PATH")
	dbURLFlag := flag.String("db-url", "", "override source DB URL; when provided with the other required flags the tool can run without HARUKI_CONFIG_PATH")
	kratosPublicURLFlag := flag.String("kratos-public-url", "", "override Kratos public URL; optional when only admin lookup/import is needed")
	kratosAdminURLFlag := flag.String("kratos-admin-url", "", "override Kratos admin URL")
	kratosSessionHeaderFlag := flag.String("kratos-session-header", "", "override Kratos session header name")
	kratosSessionCookieFlag := flag.String("kratos-session-cookie", "", "override Kratos session cookie name")
	flag.Parse()

	if *batchSize <= 0 || *requestTimeout <= 0 {
		fmt.Fprintln(os.Stderr, "invalid batch-size or request-timeout")
		os.Exit(1)
	}

	runtimeCfg, configPath, err := resolveRuntimeConfig(*dbTypeFlag, *dbURLFlag, *kratosPublicURLFlag, *kratosAdminURLFlag, *kratosSessionHeaderFlag, *kratosSessionCookieFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve runtime config failed: %v\n", err)
		os.Exit(1)
	}

	dbClient, err := postgresql.Open(runtimeCfg.DBType, runtimeCfg.DBURL)
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

	sessionHandler := harukiAPIHelper.NewSessionHandler(nil, "")
	sessionHandler.ConfigureIdentityProvider("kratos", runtimeCfg.KratosPublicURL, runtimeCfg.KratosAdminURL, runtimeCfg.KratosSessionHeader, runtimeCfg.KratosSessionCookie, true, true, *requestTimeout, nil)

	state := importState{}
	if *apply {
		loadedState, err := loadImportState(*stateFile)
		if err != nil {
			fatal(err)
		}
		state = loadedState
	}
	if strings.TrimSpace(*resumeFrom) != "" {
		state.LastID = strings.TrimSpace(*resumeFrom)
	}

	fmt.Printf("Config: %s\n", configPath)
	if *apply {
		fmt.Println("Mode: APPLY")
	} else {
		fmt.Println("Mode: DRY-RUN")
	}
	fmt.Printf("Source DB: type=%s url=%s\n", runtimeCfg.DBType, redactDSN(runtimeCfg.DBURL))
	fmt.Printf("Kratos: admin=%s public=%s\n", runtimeCfg.KratosAdminURL, runtimeCfg.KratosPublicURL)
	fmt.Printf("Batch size: %d\nResume from: %q\nLimit: %d (0 means unlimited)\nRequest timeout: %s\n", *batchSize, state.LastID, *limit, requestTimeout.String())

	remaining := *limit
	ctx := context.Background()
	for {
		currentBatchSize := *batchSize
		if remaining > 0 && remaining < currentBatchSize {
			currentBatchSize = remaining
		}
		rows, err := fetchImportUsers(ctx, sqlDB, state.LastID, currentBatchSize)
		if err != nil {
			fatal(err)
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			state.LastID = row.ID
			state.Processed++
			if remaining > 0 {
				remaining--
			}
			if !*apply {
				fmt.Printf("[plan] user=%s email=%s\n", row.ID, row.Email)
				if remaining == 0 && *limit > 0 {
					break
				}
				continue
			}
			identityID, mode, err := importUserToKratos(ctx, sessionHandler, runtimeCfg.KratosAdminURL, *requestTimeout, row)
			if err != nil {
				state.Failed++
				fmt.Fprintf(os.Stderr, "[fail] user=%s email=%s err=%v\n", row.ID, row.Email, err)
				_ = saveImportState(*stateFile, state)
				if remaining == 0 && *limit > 0 {
					break
				}
				continue
			}
			if err := updateUserKratosIdentityID(ctx, sqlDB, row.ID, identityID); err != nil {
				state.Failed++
				fmt.Fprintf(os.Stderr, "[fail] user=%s email=%s update_err=%v\n", row.ID, row.Email, err)
				_ = saveImportState(*stateFile, state)
				if remaining == 0 && *limit > 0 {
					break
				}
				continue
			}
			if mode == "existing" {
				state.Existing++
			} else {
				state.Created++
			}
			fmt.Printf("[ok] mode=%s user=%s email=%s identity=%s\n", mode, row.ID, row.Email, identityID)
			_ = saveImportState(*stateFile, state)
			if remaining == 0 && *limit > 0 {
				break
			}
		}
		if remaining == 0 && *limit > 0 {
			break
		}
	}
	fmt.Printf("Summary: processed=%d created=%d existing=%d failed=%d last_id=%q\n", state.Processed, state.Created, state.Existing, state.Failed, state.LastID)
}

func fetchImportUsers(ctx context.Context, sqlDB *stdsql.DB, afterID string, limit int) ([]importUserRow, error) {
	query := "SELECT id, email, password_hash FROM users WHERE (kratos_identity_id IS NULL OR btrim(kratos_identity_id) = '') AND email IS NOT NULL AND btrim(email) <> '' AND password_hash IS NOT NULL AND btrim(password_hash) <> ''"
	args := make([]any, 0, 2)
	if strings.TrimSpace(afterID) != "" {
		query += " AND id > $1"
		args = append(args, strings.TrimSpace(afterID))
	}
	query += " ORDER BY id ASC"
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := sqlDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]importUserRow, 0, 128) // pre-allocate for batch processing
	for rows.Next() {
		var row importUserRow
		if err := rows.Scan(&row.ID, &row.Email, &row.PasswordHash); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func updateUserKratosIdentityID(ctx context.Context, sqlDB *stdsql.DB, userID, identityID string) error {
	_, err := sqlDB.ExecContext(ctx, "UPDATE users SET kratos_identity_id = $1 WHERE id = $2", strings.TrimSpace(identityID), strings.TrimSpace(userID))
	return err
}

func resolveRuntimeConfig(dbTypeRaw, dbURLRaw, kratosPublicURLRaw, kratosAdminURLRaw, kratosSessionHeaderRaw, kratosSessionCookieRaw string) (toolRuntimeConfig, string, error) {
	runtimeCfg := toolRuntimeConfig{
		DBType:              strings.TrimSpace(dbTypeRaw),
		DBURL:               strings.TrimSpace(dbURLRaw),
		KratosPublicURL:     strings.TrimSpace(kratosPublicURLRaw),
		KratosAdminURL:      strings.TrimSpace(kratosAdminURLRaw),
		KratosSessionHeader: strings.TrimSpace(kratosSessionHeaderRaw),
		KratosSessionCookie: strings.TrimSpace(kratosSessionCookieRaw),
	}

	needConfig := runtimeCfg.DBURL == "" || runtimeCfg.KratosAdminURL == ""
	configPath := "<flag-overrides>"
	if needConfig {
		loadedConfigPath, err := config.LoadGlobalFromEnvOrDefault()
		if err != nil {
			return toolRuntimeConfig{}, "", err
		}
		configPath = loadedConfigPath
		cfg := config.Cfg
		runtimeCfg.DBType = firstNonEmpty(runtimeCfg.DBType, cfg.UserSystem.DBType)
		runtimeCfg.DBURL = firstNonEmpty(runtimeCfg.DBURL, cfg.UserSystem.DBURL)
		runtimeCfg.KratosPublicURL = firstNonEmpty(runtimeCfg.KratosPublicURL, cfg.UserSystem.KratosPublicURL)
		runtimeCfg.KratosAdminURL = firstNonEmpty(runtimeCfg.KratosAdminURL, cfg.UserSystem.KratosAdminURL)
		runtimeCfg.KratosSessionHeader = firstNonEmpty(runtimeCfg.KratosSessionHeader, cfg.UserSystem.KratosSessionHeader)
		runtimeCfg.KratosSessionCookie = firstNonEmpty(runtimeCfg.KratosSessionCookie, cfg.UserSystem.KratosSessionCookie)
	}

	runtimeCfg.DBType = firstNonEmpty(runtimeCfg.DBType, "postgres")
	runtimeCfg.KratosSessionHeader = firstNonEmpty(runtimeCfg.KratosSessionHeader, "X-Session-Token")
	runtimeCfg.KratosSessionCookie = firstNonEmpty(runtimeCfg.KratosSessionCookie, "ory_kratos_session")

	if runtimeCfg.DBURL == "" {
		return toolRuntimeConfig{}, "", fmt.Errorf("source db url is empty; set --db-url or configure user_system.db_url")
	}
	if runtimeCfg.KratosAdminURL == "" {
		return toolRuntimeConfig{}, "", fmt.Errorf("kratos admin url is empty; set --kratos-admin-url or configure user_system.kratos_admin_url")
	}
	if runtimeCfg.KratosPublicURL == "" {
		runtimeCfg.KratosPublicURL = runtimeCfg.KratosAdminURL
	}

	return runtimeCfg, configPath, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func redactDSN(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return trimmed
	}
	if parsed.User != nil {
		username := parsed.User.Username()
		if username != "" {
			parsed.User = url.UserPassword(username, "***")
		}
	}
	return parsed.String()
}

func importUserToKratos(ctx context.Context, sessionHandler *harukiAPIHelper.SessionHandler, kratosAdminURL string, requestTimeout time.Duration, row importUserRow) (string, string, error) {
	lookupCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if identityID, err := sessionHandler.FindKratosIdentityIDByEmail(lookupCtx, row.Email); err == nil {
		return identityID, "existing", nil
	} else if !harukiAPIHelper.IsKratosIdentityUnmappedError(err) {
		return "", "", err
	}
	payload := map[string]any{"schema_id": "default", "external_id": row.ID, "traits": map[string]any{"email": row.Email}, "metadata_public": map[string]any{"public_user_id": row.ID}, "metadata_admin": map[string]any{"legacy_user_id": row.ID, "source": "haruki-toolbox-backend"}, "credentials": map[string]any{"password": map[string]any{"config": map[string]any{"hashed_password": row.PasswordHash}}}}
	createCtx, cancel2 := context.WithTimeout(ctx, requestTimeout)
	defer cancel2()
	identityID, err := createKratosIdentity(createCtx, kratosAdminURL, payload)
	if err == nil {
		return identityID, "created", nil
	}
	if !isHTTPStatusError(err, http.StatusConflict, http.StatusBadRequest) {
		return "", "", err
	}
	lookupCtx2, cancel3 := context.WithTimeout(ctx, requestTimeout)
	defer cancel3()
	identityID, lookupErr := sessionHandler.FindKratosIdentityIDByEmail(lookupCtx2, row.Email)
	if lookupErr != nil {
		return "", "", err
	}
	return identityID, "existing", nil
}

func createKratosIdentity(ctx context.Context, adminBaseURL string, payload map[string]any) (string, error) {
	endpoint, err := buildKratosAdminEndpoint(adminBaseURL, "/admin/identities")
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &httpStatusError{Status: resp.StatusCode, Message: strings.TrimSpace(string(respBody))}
	}
	var parsed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", err
	}
	if strings.TrimSpace(parsed.ID) == "" {
		return "", fmt.Errorf("kratos create identity response missing id")
	}
	return strings.TrimSpace(parsed.ID), nil
}

type httpStatusError struct {
	Status  int
	Message string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("http status %d: %s", e.Status, e.Message)
}
func isHTTPStatusError(err error, statuses ...int) bool {
	httpErr, ok := err.(*httpStatusError)
	if !ok {
		return false
	}
	for _, s := range statuses {
		if httpErr.Status == s {
			return true
		}
	}
	return false
}
func buildKratosAdminEndpoint(baseURL, endpointPath string) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "", fmt.Errorf("kratos admin url is empty")
	}
	parsedBase, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid kratos admin url: %w", err)
	}
	if parsedBase.Scheme == "" || parsedBase.Host == "" {
		return "", fmt.Errorf("invalid kratos admin url")
	}
	cleanPath := endpointPath
	if cleanPath == "" {
		cleanPath = "/"
	}
	if !strings.HasPrefix(cleanPath, "/") {
		cleanPath = "/" + cleanPath
	}
	parsedBase.Path = strings.TrimRight(parsedBase.Path, "/") + cleanPath
	return parsedBase.String(), nil
}
func loadImportState(path string) (importState, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return importState{}, nil
	}
	content, err := os.ReadFile(trimmed)
	if err != nil {
		if os.IsNotExist(err) {
			return importState{}, nil
		}
		return importState{}, err
	}
	var state importState
	if err := json.Unmarshal(content, &state); err != nil {
		return importState{}, err
	}
	return state, nil
}
func saveImportState(path string, state importState) error {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(trimmed), 0o755); err != nil {
		return err
	}
	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(trimmed, content, 0o600)
}
func fatal(err error) { fmt.Fprintf(os.Stderr, "%v\n", err); os.Exit(1) }
