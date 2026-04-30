# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Haruki Toolbox Backend — a Go 1.26 backend (module name: `haruki-suite`) for collecting user-submitted game suite/mysekai data and providing public APIs. Built on Fiber v3, Ent (PostgreSQL ORM), MongoDB, Redis, and the Ory identity stack (Kratos + Hydra + Oathkeeper).

Config file: `haruki-suite-configs.yaml` (YAML with `${ENV_VAR}` interpolation). See `haruki-suite-configs.example.yaml` for all available fields.

## Build & Run

```bash
go build -o haruki-toolbox-backend ./main.go   # build
go run ./main.go                                # run (needs haruki-suite-configs.yaml)
```

## Testing

```bash
go test ./internal/modules/somemodule           # single package
go test ./utils/api ./utils/oauth2              # targeted packages
go test ./...                                   # full suite (use for cross-module changes)
```

## Code Generation (Ent)

Ent schemas are split into two databases:

| Database | Schemas | Generate command | Output |
|----------|---------|-----------------|--------|
| Toolbox (main) | `ent/toolbox/schema/` | `go generate ./ent/toolbox` | `utils/database/postgresql/` |
| Bot (HarukiBot NEO) | `ent/bot/schema/` | `go generate ./ent/bot` | `utils/database/neopg/` |

Do not hand-edit generated files. The bot database uses a separate DSN (`haruki_bot.db_url` in config).

## Architecture & Layering

| Layer | Location | Responsibility |
|---|---|---|
| Entry point | `main.go` | Load config, start bootstrap |
| Bootstrap | `internal/bootstrap/` | Dependency init, config validation, Fiber setup |
| Routing | `api/route.go` | Route registration only — no business logic |
| Business modules | `internal/modules/<name>/` | Handlers + module-scoped logic |
| Platform services | `internal/platform/` | Cross-module business capabilities (auth header, filtering, identity, pagination, timeutil) |
| Utilities | `utils/` | Infrastructure, external adapters, helpers |

Handlers should stay thin. Complex logic belongs in the module or a helper, not in the handler itself.

## Ory Integration Rules

- Browser identity: **Kratos only**. OAuth2 provider: **Hydra only**.
- Protected browser APIs go through Oathkeeper (auth proxy) to the backend.
- **Do not** re-introduce legacy local browser login/register/password-reset flows. Those endpoints should stay `410 Gone` or delegate to Ory.
- When `auth_proxy_enabled` is true: trusted header validation and `auth_proxy_session_header` must be preserved. Sensitive admin ops must use proxy-session-level identity, not just `user_id` or `kratos_identity_id`.
- Hydra subject strategy: prefer `kratos_identity_id`, fallback to local `users.id`. Do not break backward compatibility of `CurrentHydraSubject`, `CurrentHydraSubjects`, `HydraSubjectsForUser`, or introspection subject mapping.
- Session/auth logic is centralized in `utils/api/session_handler.go` — do not duplicate session parsing or Kratos whoami calls in business modules.

## Key Utility Packages

- `utils/api/` — `SessionHandler`, `HarukiToolboxRouterHelpers`, route middleware
- `utils/oauth2/` — OAuth2 scope handling, token helpers
- `utils/database/` — Database manager interfaces
- `utils/database/mongo/` — MongoDB client and operations
- `utils/database/redis/` — Redis client
- `utils/database/neopg/` — Bot database Ent client (generated)
- `internal/modules/admincore/` — Shared admin logic
- `internal/modules/usercore/` — Shared user logic
- `internal/modules/harukibotneo/` — HarukiBot NEO registration and credential reset (status, send-mail, register/reset)

Prefer reusing `SessionHandler`, `admincore`, `usercore`, and `utils/oauth2/` over building parallel helpers.

## Git Commits

All commit subjects must follow:

```text
[Type] Short description starting with capital letter
```

Allowed types:

| Type      | Usage                                                 |
|-----------|-------------------------------------------------------|
| `[Feat]`  | New feature or capability                             |
| `[Fix]`   | Bug fix                                               |
| `[Chore]` | Maintenance, refactoring, dependency or build changes |
| `[Docs]`  | Documentation-only changes                            |

Rules:

- Description starts with a capital letter.
- Use imperative mood: `Add ...`, not `Added ...`.
- No trailing period.
- Keep the subject at or below roughly 70 characters.
- **Agent attribution uses the standard Git `Co-authored-by:` trailer in
  the commit body, not a free-form `Agent:` line.** This makes GitHub
  render the co-author avatar on the commit page. The trailer must be on
  its own line, separated from the subject by a blank line, in the form
  `Co-authored-by: <Display Name> <email>`. Suggested values per agent:
  - Claude (any 4.x): `Co-authored-by: Claude Opus 4.7 <noreply@anthropic.com>`
    (substitute the actual model, e.g. `Claude Sonnet 4.6`)
  - Codex: `Co-authored-by: Codex <noreply@openai.com>`
  - Copilot: `Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>`

Project examples:

```text
[Feat] Add sendBase64Image config support
[Fix] Normalize Haruki Cloud user agent version
[Chore] Move Rust modules to flat files
[Docs] Document full obfuscated release builds
```

Agent-authored commit example:

```text
[Docs] Add agent commit guidelines

Co-authored-by: Codex <noreply@openai.com>
```

## Documentation

- When changing Ory behavior, auth flows, OAuth2 flows, or auth proxy header conventions, update `docs/ory-suite-usage.zh-CN.md`.
- When changing HarukiBot NEO registration/credential reset flow, update `docs/haruki-bot-neo-registration.zh-CN.md`.
- When changing OAuth2 client integration, update `docs/oauth2-client-integration.zh-CN.md` or `docs/oauth2-confidential-client-integration.zh-CN.md`.
- When changing webhook behavior, update `docs/webhook-integration.zh-CN.md`.
- Oathkeeper access rules live in `external/oathkeeper/access-rules.yml` — update when adding/removing public or protected endpoints.
