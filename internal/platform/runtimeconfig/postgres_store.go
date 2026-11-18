package runtimeconfig

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	json "encoding/json/v2"
)

const runtimeConfigurationID = 1

var errMissingRuntimeConfiguration = errors.New("durable runtime configuration is missing")
var ErrRuntimeConfigCutoverRequired = errors.New("stop all Redis-backed backend instances and set HARUKI_RUNTIME_CONFIG_CUTOVER=1 for the initial runtime configuration import")

type postgresStore struct{ db *sql.DB }

// NewPostgresStore imports the last Redis snapshot after an explicit offline
// cutover. PostgreSQL is authoritative thereafter, including on restarts.
func NewPostgresStore(ctx context.Context, db *sql.DB, legacy Store, initial Snapshot, cutoverConfirmed bool) (Store, error) {
	s := &postgresStore{db: db}
	if _, found, err := s.load(ctx); err != nil || found {
		return s, err
	}
	if !cutoverConfirmed {
		return nil, ErrRuntimeConfigCutoverRequired
	}
	// There is no way to establish whether startup credentials were previously
	// rotated when no legacy snapshot survives. Require an explicit update.
	initial.PrivateAPIToken = ""
	initial.WebhookJWTSecret = ""
	if legacy != nil {
		snapshot, found, err := legacy.Load(ctx)
		if err != nil {
			return nil, fmt.Errorf("load legacy runtime settings: %w", err)
		}
		if found {
			initial = snapshot
		}
	}
	payload, err := marshalSnapshot(initial)
	if err != nil {
		return nil, err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO runtime_configurations (id, snapshot)
		VALUES ($1, $2::jsonb) ON CONFLICT (id) DO NOTHING`, runtimeConfigurationID, string(payload))
	return s, err
}

func (s *postgresStore) Load(ctx context.Context) (Snapshot, bool, error) {
	snapshot, found, err := s.load(ctx)
	if !found && err == nil {
		err = errMissingRuntimeConfiguration
	}
	return snapshot, found, err
}

func (s *postgresStore) load(ctx context.Context) (Snapshot, bool, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT snapshot FROM runtime_configurations WHERE id = $1`, runtimeConfigurationID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, false, nil
	}
	if err != nil {
		return Snapshot{}, false, err
	}
	var snapshot Snapshot
	err = unmarshalSnapshot(raw, &snapshot)
	return snapshot, err == nil, err
}

func (s *postgresStore) Apply(ctx context.Context, update Update, _ Snapshot) (Snapshot, error) {
	patch := make(map[string]any)
	for key, value := range map[string]*string{
		"privateApiToken": update.PrivateAPIToken, "privateApiUserAgent": update.PrivateAPIUserAgent, "webhookJwtSecret": update.WebhookJWTSecret,
	} {
		if value != nil {
			patch[key] = *value
		}
	}
	if update.AllowedKeys != nil {
		patch["publicApiAllowedKeys"] = *update.AllowedKeys
	}
	if update.WebhookEnabled != nil {
		patch["webhookEnabled"] = *update.WebhookEnabled
	}
	if update.OAuth2DeviceFlowEnabled != nil {
		patch["oauth2DeviceFlowEnabled"] = *update.OAuth2DeviceFlowEnabled
	}
	encoded, err := json.Marshal(patch)
	if err != nil {
		return Snapshot{}, err
	}
	var raw []byte
	err = s.db.QueryRowContext(ctx, `UPDATE runtime_configurations SET snapshot = snapshot || $2::jsonb
		WHERE id = $1 RETURNING snapshot`, runtimeConfigurationID, string(encoded)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, errMissingRuntimeConfiguration
	}
	if err != nil {
		return Snapshot{}, err
	}
	var next Snapshot
	err = unmarshalSnapshot(raw, &next)
	return next, err
}
