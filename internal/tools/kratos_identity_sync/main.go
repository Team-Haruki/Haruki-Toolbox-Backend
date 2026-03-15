package main

import (
	"bytes"
	"context"
	stdsql "database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"haruki-suite/config"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/lib/pq"

	"haruki-suite/utils/database/postgresql"
)

type syncUserRow struct {
	ID               string
	Email            string
	Name             string
	EmailVerified    *bool
	KratosIdentityID string
}

type syncKratosIdentity struct {
	ID                  string                  `json:"id"`
	Traits              map[string]any          `json:"traits"`
	MetadataPublic      map[string]any          `json:"metadata_public"`
	VerifiableAddresses []syncVerifiableAddress `json:"verifiable_addresses"`
}

type syncVerifiableAddress struct {
	Value    string `json:"value"`
	Verified bool   `json:"verified"`
	Status   string `json:"status"`
}
type syncState struct {
	LastID    string `json:"last_id"`
	Processed int    `json:"processed"`
	Patched   int    `json:"patched"`
	Skipped   int    `json:"skipped"`
	Failed    int    `json:"failed"`
}

func main() {
	apply := flag.Bool("apply", false, "apply identity patches (default: dry-run)")
	batchSize := flag.Int("batch-size", 250, "number of users fetched per DB batch")
	limit := flag.Int("limit", 0, "max users to process (0 means unlimited)")
	resumeFrom := flag.String("resume-from", "", "resume from user ID (exclusive)")
	requestTimeout := flag.Duration("request-timeout", 10*time.Second, "timeout per Kratos request")
	stateFile := flag.String("state-file", ".codex-dev/kratos-identity-sync-state.json", "state file path for apply mode")
	flag.Parse()
	if *batchSize <= 0 || *requestTimeout <= 0 {
		fmt.Fprintln(os.Stderr, "invalid batch-size or request-timeout")
		os.Exit(1)
	}
	configPath, err := config.LoadGlobalFromEnvOrDefault()
	if err != nil {
		fatal(err)
	}
	cfg := config.Cfg
	if strings.TrimSpace(cfg.UserSystem.KratosAdminURL) == "" {
		fmt.Fprintln(os.Stderr, "user_system.kratos_admin_url is empty, abort")
		os.Exit(1)
	}
	dbClient, err := postgresql.Open(cfg.UserSystem.DBType, cfg.UserSystem.DBURL)
	if err != nil {
		fatal(err)
	}
	defer func() { _ = dbClient.Close() }()
	sqlDB := dbClient.SQLDB()
	if sqlDB == nil {
		fatal(fmt.Errorf("underlying SQL DB is not available"))
	}
	state := syncState{}
	if *apply {
		if loadedState, err := loadSyncState(*stateFile); err == nil {
			state = loadedState
		} else {
			fatal(err)
		}
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
	fmt.Printf("Batch size: %d\nResume from: %q\nLimit: %d (0 means unlimited)\nRequest timeout: %s\n", *batchSize, state.LastID, *limit, requestTimeout.String())
	remaining := *limit
	ctx := context.Background()
	for {
		currentBatchSize := *batchSize
		if remaining > 0 && remaining < currentBatchSize {
			currentBatchSize = remaining
		}
		rows, err := fetchSyncUsers(ctx, sqlDB, state.LastID, currentBatchSize)
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
			identityCtx, cancel := context.WithTimeout(ctx, *requestTimeout)
			identity, err := fetchKratosIdentity(identityCtx, cfg.UserSystem.KratosAdminURL, row.KratosIdentityID)
			cancel()
			if err != nil {
				state.Failed++
				fmt.Fprintf(os.Stderr, "[fail] user=%s identity=%s err=%v\n", row.ID, row.KratosIdentityID, err)
				if *apply {
					_ = saveSyncState(*stateFile, state)
				}
				if remaining == 0 && *limit > 0 {
					break
				}
				continue
			}
			patches := buildIdentityPatches(row, identity)
			if len(patches) == 0 {
				state.Skipped++
				fmt.Printf("[skip] user=%s identity=%s reason=no_changes\n", row.ID, row.KratosIdentityID)
				if *apply {
					_ = saveSyncState(*stateFile, state)
				}
				if remaining == 0 && *limit > 0 {
					break
				}
				continue
			}
			if !*apply {
				fmt.Printf("[plan] user=%s identity=%s patches=%d\n", row.ID, row.KratosIdentityID, len(patches))
				if remaining == 0 && *limit > 0 {
					break
				}
				continue
			}
			patchCtx, cancelPatch := context.WithTimeout(ctx, *requestTimeout)
			err = patchKratosIdentity(patchCtx, cfg.UserSystem.KratosAdminURL, row.KratosIdentityID, patches)
			cancelPatch()
			if err != nil {
				state.Failed++
				fmt.Fprintf(os.Stderr, "[fail] user=%s identity=%s patch_err=%v\n", row.ID, row.KratosIdentityID, err)
				_ = saveSyncState(*stateFile, state)
				if remaining == 0 && *limit > 0 {
					break
				}
				continue
			}
			state.Patched++
			fmt.Printf("[ok] user=%s identity=%s patches=%d\n", row.ID, row.KratosIdentityID, len(patches))
			_ = saveSyncState(*stateFile, state)
			if remaining == 0 && *limit > 0 {
				break
			}
		}
		if remaining == 0 && *limit > 0 {
			break
		}
	}
	fmt.Printf("Summary: processed=%d patched=%d skipped=%d failed=%d last_id=%q\n", state.Processed, state.Patched, state.Skipped, state.Failed, state.LastID)
}

func fetchSyncUsers(ctx context.Context, sqlDB *stdsql.DB, afterID string, limit int) ([]syncUserRow, error) {
	query := "SELECT id, email, COALESCE(name, ''), email_verified, kratos_identity_id FROM users WHERE kratos_identity_id IS NOT NULL AND btrim(kratos_identity_id) <> ''"
	args := make([]any, 0, 1)
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
	result := make([]syncUserRow, 0)
	for rows.Next() {
		var row syncUserRow
		var emailVerified stdsql.NullBool
		if err := rows.Scan(&row.ID, &row.Email, &row.Name, &emailVerified, &row.KratosIdentityID); err != nil {
			return nil, err
		}
		if emailVerified.Valid {
			value := emailVerified.Bool
			row.EmailVerified = &value
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func fetchKratosIdentity(ctx context.Context, adminBaseURL, identityID string) (*syncKratosIdentity, error) {
	endpoint, err := buildIdentityEndpoint(adminBaseURL, identityID)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("kratos get identity status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var identity syncKratosIdentity
	if err := json.Unmarshal(body, &identity); err != nil {
		return nil, err
	}
	return &identity, nil
}
func patchKratosIdentity(ctx context.Context, adminBaseURL, identityID string, patches []map[string]any) error {
	endpoint, err := buildIdentityEndpoint(adminBaseURL, identityID)
	if err != nil {
		return err
	}
	body, err := json.Marshal(patches)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("kratos patch identity status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}
func buildIdentityPatches(row syncUserRow, identity *syncKratosIdentity) []map[string]any {
	if identity == nil {
		return nil
	}
	patches := make([]map[string]any, 0, 6)
	if identity.MetadataPublic == nil {
		patches = append(patches, map[string]any{"op": "add", "path": "/metadata_public", "value": map[string]any{}})
		identity.MetadataPublic = map[string]any{}
	}
	if currentUserID := strings.TrimSpace(stringValue(identity.MetadataPublic["public_user_id"])); currentUserID != strings.TrimSpace(row.ID) {
		op := "add"
		if currentUserID != "" {
			op = "replace"
		}
		patches = append(patches, map[string]any{"op": op, "path": "/metadata_public/public_user_id", "value": row.ID})
	}
	if identity.Traits == nil {
		patches = append(patches, map[string]any{"op": "add", "path": "/traits", "value": map[string]any{}})
		identity.Traits = map[string]any{}
	}
	currentName := strings.TrimSpace(stringValue(identity.Traits["name"]))
	targetName := strings.TrimSpace(row.Name)
	if targetName != "" && currentName != targetName {
		op := "add"
		if currentName != "" {
			op = "replace"
		}
		patches = append(patches, map[string]any{"op": op, "path": "/traits/name", "value": targetName})
	}
	if row.EmailVerified != nil && *row.EmailVerified {
		targetEmail := strings.ToLower(strings.TrimSpace(row.Email))
		for idx, address := range identity.VerifiableAddresses {
			if strings.ToLower(strings.TrimSpace(address.Value)) != targetEmail {
				continue
			}
			if !address.Verified {
				patches = append(patches, map[string]any{"op": "replace", "path": fmt.Sprintf("/verifiable_addresses/%d/verified", idx), "value": true})
			}
			if strings.ToLower(strings.TrimSpace(address.Status)) != "completed" {
				patches = append(patches, map[string]any{"op": "replace", "path": fmt.Sprintf("/verifiable_addresses/%d/status", idx), "value": "completed"})
			}
			break
		}
	}
	return patches
}
func buildIdentityEndpoint(baseURL, identityID string) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	identityID = strings.TrimSpace(identityID)
	if baseURL == "" || identityID == "" {
		return "", fmt.Errorf("kratos admin url or identity id is empty")
	}
	parsedBase, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	parsedBase.Path = strings.TrimRight(parsedBase.Path, "/") + "/admin/identities/" + url.PathEscape(identityID)
	return parsedBase.String(), nil
}
func stringValue(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}
func loadSyncState(path string) (syncState, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return syncState{}, nil
	}
	content, err := os.ReadFile(trimmed)
	if err != nil {
		if os.IsNotExist(err) {
			return syncState{}, nil
		}
		return syncState{}, err
	}
	var state syncState
	if err := json.Unmarshal(content, &state); err != nil {
		return syncState{}, err
	}
	return state, nil
}
func saveSyncState(path string, state syncState) error {
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
