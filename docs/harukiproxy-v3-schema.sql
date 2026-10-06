-- HarukiProxy v3 additive schema upgrade (PostgreSQL).
-- Review and run against the Toolbox database before deploying the backend.
-- No row rewrites or historical identity backfill. Preserve columns on rollback.
BEGIN;
ALTER TABLE upload_logs ALTER COLUMN game_user_id DROP NOT NULL;
ALTER TABLE upload_logs ALTER COLUMN toolbox_user_id DROP NOT NULL;
ALTER TABLE upload_logs ADD COLUMN IF NOT EXISTS client_name varchar(64);
ALTER TABLE upload_logs ADD COLUMN IF NOT EXISTS client_version varchar(128);
ALTER TABLE upload_logs ADD COLUMN IF NOT EXISTS client_channel varchar(32);
ALTER TABLE upload_logs ADD COLUMN IF NOT EXISTS client_metadata_format varchar(16);
ALTER TABLE upload_logs ADD COLUMN IF NOT EXISTS protocol_version varchar(16);
ALTER TABLE upload_logs ADD COLUMN IF NOT EXISTS platform varchar(16);
ALTER TABLE upload_logs ADD COLUMN IF NOT EXISTS os_version varchar(64);
ALTER TABLE upload_logs ADD COLUMN IF NOT EXISTS os_build varchar(64);
ALTER TABLE upload_logs ADD COLUMN IF NOT EXISTS os_arch varchar(16);
ALTER TABLE upload_logs ADD COLUMN IF NOT EXISTS app_arch varchar(16);
ALTER TABLE upload_logs ADD COLUMN IF NOT EXISTS failure_stage varchar(32);
ALTER TABLE upload_logs ADD COLUMN IF NOT EXISTS error_code varchar(64);
ALTER TABLE upload_logs ADD COLUMN IF NOT EXISTS request_id varchar(36);
ALTER TABLE upload_logs ADD COLUMN IF NOT EXISTS claimed_game_user_id varchar(30);
ALTER TABLE upload_logs ADD COLUMN IF NOT EXISTS oauth_client_id varchar(255);
ALTER TABLE upload_logs ADD COLUMN IF NOT EXISTS processing_duration_ms bigint;
ALTER TABLE upload_logs ADD COLUMN IF NOT EXISTS request_bytes bigint;
ALTER TABLE upload_logs ADD COLUMN IF NOT EXISTS identity_verified boolean;
ALTER TABLE upload_logs ADD COLUMN IF NOT EXISTS received_at timestamptz;
COMMIT;

-- Run outside a transaction. CONCURRENTLY avoids blocking uploads on large tables.
CREATE INDEX CONCURRENTLY IF NOT EXISTS uploadlog_received_at ON upload_logs (received_at);
CREATE INDEX CONCURRENTLY IF NOT EXISTS uploadlog_request_id ON upload_logs (request_id);
CREATE INDEX CONCURRENTLY IF NOT EXISTS uploadlog_upload_method_protocol_version_received_at ON upload_logs (upload_method, protocol_version, received_at);
CREATE INDEX CONCURRENTLY IF NOT EXISTS uploadlog_upload_method_client_channel_received_at ON upload_logs (upload_method, client_channel, received_at);
CREATE INDEX CONCURRENTLY IF NOT EXISTS uploadlog_upload_method_platform_received_at ON upload_logs (upload_method, platform, received_at);
