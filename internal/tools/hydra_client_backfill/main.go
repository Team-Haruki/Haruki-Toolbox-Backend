package main

import (
	"context"
	stdsql "database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"haruki-suite/config"
	oauth2Module "haruki-suite/internal/modules/oauth2"
	"haruki-suite/utils/database/postgresql"
	harukiOAuth2 "haruki-suite/utils/oauth2"
	"os"
	"strings"

	_ "github.com/lib/pq"
)

type emittedSecret struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	ClientType   string `json:"clientType"`
}

func main() {
	apply := flag.Bool("apply", false, "apply changes to Hydra (default: dry-run)")
	upsert := flag.Bool("upsert", true, "update Hydra client when client_id already exists")
	includeInactive := flag.Bool("include-inactive", true, "include inactive legacy clients")
	clientIDFilter := flag.String("client-id", "", "migrate a single client_id only")
	limit := flag.Int("limit", 0, "max number of clients to process (0 means unlimited)")
	secretsOut := flag.String("secrets-out", "", "write generated confidential client secrets to this JSON file")
	flag.Parse()

	configPath, err := config.LoadGlobalFromEnvOrDefault()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config failed: %v\n", err)
		os.Exit(1)
	}
	cfg := config.Cfg
	if strings.TrimSpace(cfg.OAuth2.HydraAdminURL) == "" {
		fmt.Fprintln(os.Stderr, "oauth2.hydra_admin_url is empty, abort")
		os.Exit(1)
	}

	dbClient, err := postgresql.Open(cfg.UserSystem.DBType, cfg.UserSystem.DBURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open database failed: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = dbClient.Close() }()

	ctx := context.Background()
	clients, err := fetchLegacyOAuthClients(ctx, dbClient.SQLDB(), *includeInactive, strings.TrimSpace(*clientIDFilter), *limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query oauth clients failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Config: %s\n", configPath)
	if *apply {
		fmt.Println("Mode: APPLY")
	} else {
		fmt.Println("Mode: DRY-RUN")
	}
	fmt.Printf("Client count: %d\n", len(clients))

	generatedSecrets := make([]emittedSecret, 0, len(clients))
	created := 0
	updated := 0
	skipped := 0
	failed := 0

	for _, legacyClient := range clients {
		clientID := strings.TrimSpace(legacyClient.ClientID)
		clientType := strings.TrimSpace(legacyClient.ClientType)
		if clientType == "" {
			clientType = "public"
		}
		input := oauth2Module.HydraOAuthClientUpsertInput{
			ClientID:     clientID,
			ClientName:   strings.TrimSpace(legacyClient.Name),
			ClientType:   clientType,
			RedirectURIs: append([]string(nil), legacyClient.RedirectURIs...),
			Scopes:       append([]string(nil), legacyClient.Scopes...),
			Active:       legacyClient.Active,
		}
		if clientType == "confidential" {
			plainSecret, err := harukiOAuth2.GenerateRandomToken(32)
			if err != nil {
				failed++
				fmt.Fprintf(os.Stderr, "[fail] client=%s generate secret err=%v\n", clientID, err)
				continue
			}
			input.ClientSecret = plainSecret
			generatedSecrets = append(generatedSecrets, emittedSecret{ClientID: clientID, ClientSecret: plainSecret, ClientType: clientType})
		}

		if !*apply {
			action := "create"
			if *upsert {
				action = "upsert"
			}
			fmt.Printf("[plan] action=%s client=%s type=%s active=%v redirectUris=%d scopes=%d\n", action, clientID, clientType, input.Active, len(input.RedirectURIs), len(input.Scopes))
			continue
		}

		_, err := oauth2Module.CreateHydraOAuthClient(ctx, input)
		if err == nil {
			created++
			fmt.Printf("[ok] action=create client=%s\n", clientID)
			continue
		}
		if oauth2Module.IsHydraConflictError(err) && *upsert {
			if _, updateErr := oauth2Module.UpdateHydraOAuthClient(ctx, clientID, input); updateErr != nil {
				failed++
				fmt.Fprintf(os.Stderr, "[fail] action=update client=%s err=%v\n", clientID, updateErr)
				continue
			}
			updated++
			fmt.Printf("[ok] action=update client=%s\n", clientID)
			continue
		}
		if oauth2Module.IsHydraConflictError(err) && !*upsert {
			skipped++
			fmt.Printf("[skip] action=create client=%s reason=already_exists\n", clientID)
			continue
		}
		failed++
		fmt.Fprintf(os.Stderr, "[fail] action=create client=%s err=%v\n", clientID, err)
	}

	if strings.TrimSpace(*secretsOut) != "" {
		payload, err := json.MarshalIndent(generatedSecrets, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "marshal secrets output failed: %v\n", err)
			os.Exit(1)
		}
		if err := os.WriteFile(strings.TrimSpace(*secretsOut), payload, 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "write secrets output failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Secrets written: %s\n", strings.TrimSpace(*secretsOut))
	}

	fmt.Printf("Summary: created=%d updated=%d skipped=%d failed=%d generatedSecrets=%d\n", created, updated, skipped, failed, len(generatedSecrets))
}

type legacyOAuthClientRow struct {
	ClientID     string
	Name         string
	ClientType   string
	Active       bool
	RedirectURIs []string
	Scopes       []string
}

func fetchLegacyOAuthClients(ctx context.Context, sqlDB *stdsql.DB, includeInactive bool, clientID string, limit int) ([]legacyOAuthClientRow, error) {
	if sqlDB == nil {
		return nil, fmt.Errorf("underlying SQL DB is not available")
	}
	var queryBuilder strings.Builder
	queryBuilder.WriteString("SELECT client_id, name, client_type, active, redirect_uris, scopes FROM oauth_clients")
	clauses := make([]string, 0, 2)
	args := make([]any, 0, 2)
	if !includeInactive {
		clauses = append(clauses, fmt.Sprintf("active = $%d", len(args)+1))
		args = append(args, true)
	}
	if strings.TrimSpace(clientID) != "" {
		clauses = append(clauses, fmt.Sprintf("client_id = $%d", len(args)+1))
		args = append(args, strings.TrimSpace(clientID))
	}
	if len(clauses) > 0 {
		queryBuilder.WriteString(" WHERE ")
		queryBuilder.WriteString(strings.Join(clauses, " AND "))
	}
	queryBuilder.WriteString(" ORDER BY client_id ASC")
	if limit > 0 {
		queryBuilder.WriteString(fmt.Sprintf(" LIMIT %d", limit))
	}
	query := queryBuilder.String()
	rows, err := sqlDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]legacyOAuthClientRow, 0, 64) // pre-allocate for common case
	for rows.Next() {
		var row legacyOAuthClientRow
		var redirectURIsRaw []byte
		var scopesRaw []byte
		if err := rows.Scan(&row.ClientID, &row.Name, &row.ClientType, &row.Active, &redirectURIsRaw, &scopesRaw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(redirectURIsRaw, &row.RedirectURIs); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(scopesRaw, &row.Scopes); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
