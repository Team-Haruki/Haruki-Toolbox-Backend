-- Apply after harukiproxy-v3-schema.sql, before the OAuth2/write-grant backend.
-- Do not roll back to software that treats every grant as read permission.
BEGIN;
SET LOCAL lock_timeout = '5s';
ALTER TABLE game_account_data_grants ADD COLUMN IF NOT EXISTS can_read boolean NOT NULL DEFAULT true;
ALTER TABLE game_account_data_grants ADD COLUMN IF NOT EXISTS can_write boolean NOT NULL DEFAULT false;
ALTER TABLE upload_logs ADD COLUMN IF NOT EXISTS actor_user_id varchar;
ALTER TABLE upload_logs ADD COLUMN IF NOT EXISTS auth_method varchar;
ALTER TABLE upload_logs ADD COLUMN IF NOT EXISTS grant_id bigint;
ALTER TABLE upload_logs ADD COLUMN IF NOT EXISTS authorization_source varchar;
COMMIT;
