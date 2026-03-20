# AGENTS.md

## Project Summary

Haruki Toolbox Backend is a Go 1.26 backend built around:

- Fiber for HTTP routing
- Ent for PostgreSQL schema and ORM
- MongoDB for uploaded game data and webhook storage
- Redis for sessions, cache-style state, and rate-limit style state
- Ory Kratos for managed identity flows
- Ory Hydra for OAuth2

Default local config file: `haruki-suite-configs.yaml`

## Source Of Truth

Primary application source lives in:

- `main.go`
- `api/`
- `config/`
- `ent/`
- `internal/`
- `utils/`

Do not treat these as primary runtime source unless explicitly asked:

- `deploy/upload/haruki-server-package/`
- `db_export/`
- `.codex-dev/`
- local binaries like `kratos_identity_sync` and `kratos_user_import`
- test data files such as `mysekai_test_file` and `suite_test_file`

`deploy/upload/haruki-server-package/` contains packaging snapshots and mirrored code. In normal feature work, edit the root source tree, not the packaged copy.

## Repository Layout

- `main.go`: config load and bootstrap entry only
- `api/`: thin route assembly only
- `internal/bootstrap/`: startup assembly and dependency wiring
- `internal/modules/`: business handlers and module logic
- `internal/platform/`: reusable cross-module helpers
- `utils/`: infrastructure adapters and shared helpers
- `ent/schema/`: Ent schema source
- `utils/database/postgresql/`: generated Ent code
- `docs/`: architecture, development, auth migration, API notes
- `scripts/`: local development and Ory stack helpers

## Architectural Rules

- Keep `api/` thin. Do not put business logic there.
- Keep `main.go` thin. Startup wiring belongs in `internal/bootstrap/`.
- Keep handlers thin: parse input, call module logic, return response.
- Prefer extending existing helpers in `admincore`, `usercore`, `internal/platform`, and `utils/...` over introducing parallel helpers.
- Put domain behavior in `internal/modules/...`.
- Put generic infrastructure behavior in `utils/...`.

## Auth And Security Rules

- Kratos is the supported managed browser auth provider.
- Hydra is the supported OAuth2 provider.
- Do not introduce new legacy browser-auth flows.
- If auth proxy is enabled, preserve trusted-header checks.
- Session-sensitive admin flows must remain session-scoped, not just identity-scoped.
- Auth proxy deployments now rely on `user_system.auth_proxy_session_header`; do not silently fall back to identity-only markers for reauthentication-sensitive behavior.

## Database And Schema Rules

- If you edit `ent/schema/...`, run:
  - `go generate ./ent`
- After schema edits, verify generated files under `utils/database/postgresql/` changed as expected.
- Do not hand-edit generated Ent files unless explicitly required.
- Production commonly runs with `backend.auto_migrate: false`; avoid changes that only work when auto-migrate is enabled.
- If startup schema compatibility checks are touched, keep both auto-migrate and manual-schema paths working.

## Logging Rules

- Prefer project logger helpers over ad hoc logging.
- Global loggers should respect current global log level and writer configuration.
- Avoid package-level logger construction that freezes stale bootstrap settings unless the logger implementation is explicitly dynamic.

## API And Docs Rules

- If changing routes, payloads, or response shapes, check whether `openapi.yaml` or docs need updates.
- Reuse response helpers in `utils/api`.
- Preserve existing JSON field naming and error response patterns in the touched module.

## Audit And Permission Rules

- Admin write paths should usually emit admin audit logs.
- User write paths should usually emit user audit logs.
- Reuse `admincore` and `usercore` for actor resolution, permission checks, and shared middleware.

## Testing Expectations

Prefer the smallest meaningful validation set first:

- touched-package tests, for example:
  - `go test ./internal/modules/admin ./utils/api`
- cross-cutting verification:
  - `go test ./...`
- Ent generation after schema changes:
  - `go generate ./ent`

Add tests near the changed module or utility.

## Useful Commands

- Run locally:
  - `HARUKI_CONFIG_PATH=./haruki-suite-configs.yaml go run ./main.go`
- Run all tests:
  - `go test ./...`
- Regenerate Ent:
  - `go generate ./ent`
- Ory stack helper:
  - `scripts/ory-stack.sh help`
  - `scripts/ory-stack.sh bootstrap`

## Change Checklist

Before finishing, verify:

- code was added in the correct layer
- tests were added or updated near the changed logic
- Ent was regenerated if schema changed
- auth/session logic is still session-safe
- docs or OpenAPI were updated if behavior changed
- packaged mirror code under `deploy/upload/haruki-server-package/` was not edited by accident
