# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Haruki Toolbox Backend — a Go 1.26 backend (module name: `github.com/Team-Haruki/Haruki-Toolbox-Backend`) for collecting user-submitted game suite/mysekai data and providing public APIs. Built on Fiber v3, Ent (PostgreSQL ORM), MongoDB, Redis, and the Ory identity stack (Kratos + Hydra + Oathkeeper).

Config file: `haruki-toolbox-configs.yaml` (YAML with `${ENV_VAR}` interpolation). See `haruki-toolbox-configs.example.yaml` for all available fields.

## Build & Run

```bash
go build -o haruki-toolbox-backend ./main.go   # build
go run ./main.go                                # run (needs haruki-toolbox-configs.yaml)
```

## Testing

```bash
go test ./internal/modules/somemodule           # single package
go test ./internal/platform/api ./internal/platform/oauth2              # targeted packages
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
- Session/auth logic lives in the `internal/platform/api/session_*.go` family (`session_handler.go` holds `SessionHandler` + config; resolution is split into `session_verify.go`, `session_auth_proxy.go`, `session_kratos_*.go`) — do not duplicate session parsing or Kratos whoami calls in business modules.

## Security Invariants

These encode hard-won rules from prior security audits. Do **not** regress them.

- **Auth-proxy identity is derived from the vouched subject header, never from a client header.** In auth-proxy mode resolve the user ONLY from `X-Kratos-Identity-Id` (Oathkeeper-injected) via `resolveKratosIdentity`. Never trust a client-supplied `X-User-Id` as authoritative — if present it must equal the resolved user or the request is rejected. The backend must be unreachable except through Oathkeeper (never publish its port on a public interface; internal-only/Tailscale binding is fine). The trusted secret is the entire identity-forging boundary: compare it with `crypto/subtle.ConstantTimeCompare`; bootstrap refuses to start on the placeholder or a value < 16 chars; the oathkeeper mutator must inject every trusted header (incl. the session id) and clear `X-User-Id`.
- **Constant-time comparison for all secrets.** Compare every shared secret, token, OTP, and verification code with `crypto/subtle.ConstantTimeCompare`, never `==`/`!=`.
- **Object-level authorization (no IDOR).** Scope every per-user object read/write to the authenticated self or the owner resolved from the data, never to a body/param id. Admin **reads and** mutations on a target user must pass `admincore.EnsureAdminCanManageTargetUser` (role hierarchy) — reads too (detail, role, activity, system logs). Oathkeeper-bypassing token-gated endpoints (`/api/private/*`, harukiproxy, social verify, `/internal/*`) self-authorize per request and are the highest-value surfaces.
- **Untrusted upload parsing.** Upload payloads are decrypted with the *public* Project Sekai client key, so decoded content is attacker-controlled: validate nesting depth before decoding (`orderedmsgpack.ValidateMaxDepth`), reject Mongo field names containing `.` or `$`, and bound allocations by remaining input length. A Go stack overflow is fatal — never rely on `recover()`.
- **Atomic rate limits / counters.** Implement attempt counters and rate limits with atomic `IncrementWithTTL`, never GetCache-then-SetCache (a race defeats the cap). Trust `c.IP()` only with `EnableIPValidation` on and `trusted_proxies` scoped to the real edge proxy.
- **SSRF.** Outbound requests to user-supplied URLs (webhook callbacks) must re-resolve and reject private/link-local IPs at *dial* time and pin the validated IP, not only validate DNS up front (rebinding).
- **OAuth2 bearer.** Pin token type to `access_token` on introspection, reject tokens of disabled clients, and revoke a client's tokens/consent when disabling it (don't just flip metadata).
- **Public responses / enumeration.** Don't expose payment amounts, PII, or credentials on public endpoints, and don't let error messages or timing distinguish "not found" from "not permitted".

## Key Utility Packages

- `internal/platform/api/` — `SessionHandler`, `HarukiToolboxRouterHelpers`, route middleware
- `internal/platform/oauth2/` — OAuth2 scope handling, token helpers
- `utils/database/` — Database manager interfaces
- `utils/database/mongo/` — MongoDB client and operations
- `utils/database/redis/` — Redis client
- `utils/database/neopg/` — Bot database Ent client (generated)
- `internal/modules/admincore/` — Shared admin logic (incl. `EnsureAdminCanManageTargetUser` role-hierarchy guard, `CurrentAdminActor`)
- `internal/modules/usercore/` — Shared user logic
- `internal/modules/harukibotneo/` — HarukiBot NEO registration and credential reset (status, send-mail, register/reset)
- `internal/modules/sponsor/` + `adminsponsor/` — Afdian sponsor wall (public read + signed/verified webhook) and admin management
- `utils/orderedmsgpack/`, `utils/streamjson/` — bounded msgpack decoders for untrusted upload data (depth/length validated)

Prefer reusing `SessionHandler`, `admincore`, `usercore`, and `internal/platform/oauth2/` over building parallel helpers.

## Documentation

- `docs/README.md` is the index, grouped by audience. Add new docs there. Docs about our own systems are kept only while the work is unfinished — delete them once it lands and move anything the code cannot state into a comment next to the code; the history stays in git. Docs for external integrators are kept regardless.
- Links inside `docs/` must be repo-relative (`oauth2-integration.zh-CN.md`, `../internal/...`). Never commit an absolute path from your own machine.
- When changing Ory behavior, auth flows, OAuth2 flows, or auth proxy header conventions, update `docs/ory-suite-usage.zh-CN.md`.
- When changing OAuth2 client integration, update `docs/oauth2-integration.zh-CN.md` — one document covers public clients, confidential clients and the OAuth2 webhook.
- When changing webhook behavior, update `docs/webhook-integration.zh-CN.md` (public API webhook) and §8 of `docs/oauth2-integration.zh-CN.md` (OAuth2 webhook).
- When changing game account data grants or the accessible-accounts aggregate, update `docs/game-account-data-grants.zh-CN.md`, including the feature-to-capability table the frontend gates on.
- When changing Afdian sponsor webhook/sync behavior, update `docs/afdian-sponsor-integration.zh-CN.md`.
- Oathkeeper access rules live in `external/oathkeeper/access-rules.yml` — update when adding/removing public or protected endpoints. The backend's auth-proxy header conventions also live in `external/oathkeeper/oathkeeper.yml` (header mutator).

## Git commits

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
- **Agent attribution uses the standard Git `Co-authored-by:` trailer in the commit body, not a free-form `Agent:` line.** This makes GitHub render the co-author avatar on the commit page. The trailer must be on its own line, separated from the subject by a blank line, in the form `Co-authored-by: <Display Name> <email>`. Suggested values per agent:
  - Claude (any 4.x): `Co-authored-by: Claude Opus 4.8 <noreply@anthropic.com>` (substitute the actual model, e.g. `Claude Sonnet 4.6`, `Claude Haiku 4.5`)
  - Codex: `Co-authored-by: Codex <noreply@openai.com>`
  - Copilot: `Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>`

Examples from this repo's history:

```text
[Feat] Add owned game account data endpoint
[Fix] Keep birthday fixture drops
[Chore] Go mod tidy
[Docs] Update project docs with credential reset and missing doc references
```

## GitHub Actions workflows

CI reuses the shared templates in
[`seiunx-dev/ci-templates`](https://github.com/seiunx-dev/ci-templates) at `@v1`.
The files in `.github/workflows` are thin callers:

- `ci.yml` (`CI`) runs on `main` pushes, pull requests targeting `main`, and manual
  dispatch:
  - `go-ci`: `gofmt`, `go mod tidy -diff`, `go build` / `go vet` (`-mod=readonly`),
    `go tool staticcheck` (the `tool` directive in `go.mod`), then the tests **once**:
    `go test -race -count=1 ./...` with coverage. Go caches are written only from `main`.
  - `sonar` scans that coverage (skipped green on Dependabot/fork PRs); `SONAR_TOKEN` is
    only passed to the scan.
  - `docker` does not wait for the tests. PRs build only, and only when Go sources, `data/`,
    `go.mod`/`go.sum`, the Dockerfile, the example config or the workflows change. On
    `main` it runs in parallel with the tests and pushes the immutable
    `ghcr.io/team-haruki/haruki-toolbox-backend:sha-<full sha>` and `:sha-<7 chars>` as
    soon as the build finishes. The `Docker tags` job (`docker-retag.yml`, after `CI OK`)
    then moves `:main` to that digest without rebuilding, so `:main` only follows commits
    whose `CI OK` passed. Main images carry `VERSION=main-<sha7>`, `GIT_SHA` and
    `BUILD_DATE` = the commit time. Images are `linux/amd64` only. The registry
    `:buildcache` keeps the module download layer.
  - The aggregate job **`CI OK`** is the only required status check.
- `release.yml` (`Release`): bump `Version` in `version/version.go` in a PR (9.0.0 is in
  the rc phase: `v9.0.0-rc3`, `v9.0.0-rc4`, ...) → merge and wait for `CI OK` on `main` →
  push the same tag (`v9.0.0-rc4`). `release-gate` (`version-source: go`) refuses a tag
  that differs from `version/version.go` and waits for `CI OK` on the tagged commit; then
  `go-release` builds `HarukiToolboxBackend-linux-amd64.tar.gz` and
  `HarukiToolboxBackend-linux-arm64.tar.gz` (binary, `data/` and
  `haruki-toolbox-configs.example.yaml` at the archive root; CGO off, `-trimpath`,
  `Version=<tag>`, `Commit=<sha>`, `BuildDate=<commit time>`); the image is **built** (not
  promoted from `main`, because the version is compiled in) with `VERSION=<tag>` and tagged
  `:<version>` (e.g. `:9.0.0-rc3`, which production pulls; stable tags also get
  `:<major>.<minor>` and, for the highest one, `:latest`); and the GitHub Release is
  published with `SHA256SUMS-<tag>.txt` (`-rc` tags as pre-releases that never become
  "latest"). Manual dispatch is a dry run: it builds the binaries with the version from
  `version/version.go` and publishes nothing, also when started on a tag.
- Dockerfile: modules are downloaded in their own layer; `VERSION`, `GIT_SHA` and
  `BUILD_DATE` are declared right before `go build` (and after the runtime stage's `RUN`),
  so their per-commit values no longer invalidate the earlier layers; the builder
  cross-compiles for `TARGETOS`/`TARGETARCH`, so adding `linux/arm64` only needs the
  `platforms` input in `ci.yml` and `release.yml`.

Workflow maintenance rules:

- Use the shared templates first. Add custom jobs or steps only when a template
  genuinely cannot meet the project's needs, keep them in the thin caller files, and
  add a comment explaining why.
- Template bugs and missing features are fixed upstream in `seiunx-dev/ci-templates`
  (new `v1.x.y` tag), not worked around here.
- Keep top-level `permissions: contents: read`; grant `packages: write` / `contents: write`
  only on the job that needs it.
- Do not suppress `githubactions:S7637` (full-SHA pins) in `sonar-project.properties`: the
  template's `sonar.yml` already ignores it for the `@v1` references.
- Third-party actions in caller-side custom steps are pinned to a full commit SHA with a
  `# vX.Y.Z` comment; Dependabot (`github-actions`) updates them and the template refs.
- CI uses the Go version in `go.mod` exactly (`GOTOOLCHAIN=local`); keep the Dockerfile's
  `golang` image on the same version.
