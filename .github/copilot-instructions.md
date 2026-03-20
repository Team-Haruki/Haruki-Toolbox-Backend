# Copilot Instructions

## Work In The Right Place

- Put business logic in `internal/modules/...`
- Keep `api/` limited to route wiring
- Keep `main.go` limited to config load and bootstrap handoff
- Put reusable infra helpers in `utils/...`
- Put shared cross-module helpers in `internal/platform/...`

Do not edit `deploy/upload/haruki-server-package/` unless the task is explicitly about packaging or release artifacts. The root source tree is the source of truth.

## Follow Existing Project Patterns

- Keep handlers thin
- Reuse `admincore` and `usercore` helpers
- Reuse existing response helpers in `utils/api`
- Reuse existing audit-log patterns for admin and user mutations
- Add tests close to the changed code

## Auth Rules

- Assume Kratos is the managed auth provider
- Assume Hydra is the OAuth2 provider
- Do not add new legacy browser-auth flows
- Preserve trusted auth-proxy header checks
- For auth-proxy reauthentication or session-sensitive logic, use session-scoped identifiers, not only user ID or identity ID
- If auth proxy config is touched, remember `user_system.auth_proxy_session_header`

## Database Rules

- Ent schema source is in `ent/schema/...`
- Generated Ent output is in `utils/database/postgresql/...`
- If `ent/schema/...` changes, also run `go generate ./ent`
- Do not hand-edit generated Ent files unless explicitly needed

## Logging Rules

- Prefer project logger helpers
- Global loggers should respect current global log level and writer behavior
- Avoid patterns that freeze stale bootstrap logging config

## Validation

- Run focused `go test` commands for touched packages
- Run `go test ./...` for cross-cutting changes
- Run `go generate ./ent` after schema edits

## Avoid

- business logic in `api/`
- duplicate permission or session logic that already exists in helpers
- editing packaging snapshots instead of root source
- forgetting generated artifacts after schema changes
