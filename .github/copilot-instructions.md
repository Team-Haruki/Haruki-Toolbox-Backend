# Copilot Instructions

## 在正确的层里改代码

- `main.go` 只做配置加载和启动入口
- `api/` 只做路由注册
- `internal/bootstrap/` 处理启动装配
- `internal/modules/...` 放业务逻辑
- `internal/platform/...` 放跨模块平台能力
- `utils/...` 放通用基础设施与外部系统适配

## 当前仓库形态

- 当前仓库以源码为主
- 不要默认创建或修改部署快照、迁移临时目录、本地构建产物
- 如无明确需求，不要重新引入 `deploy/`、临时迁移脚本目录或一次性导出目录

## Ory 相关硬规则

- 浏览器身份体系默认是 `Kratos`
- OAuth2 提供者默认是 `Hydra`
- 浏览器受保护 API 默认通过 `Oathkeeper/Auth Proxy` 进入后端
- 不要重新引入旧本地浏览器登录/注册/找回密码流程
- 如果启用了 auth proxy，必须保留 trusted header 校验
- 如果启用了 auth proxy，必须保留 `user_system.auth_proxy_session_header`
- 管理员敏感操作的重认证必须使用“会话级标识”，不要只靠 `user_id` 或 `kratos_identity_id`

## 安全不变量（不得回退，源自历次安全审计）

- **Auth Proxy 身份只信 Oathkeeper 注入的 `X-Kratos-Identity-Id`，不信客户端 `X-User-Id`**；若客户端带了 `X-User-Id` 必须等于解析结果否则拒绝。后端只能经 Oathkeeper 访问，端口别发布到公网；信任密钥用 `subtle.ConstantTimeCompare` 比较且启动校验非占位/≥16 字符。
- 所有密钥/token/OTP/验证码用 `subtle.ConstantTimeCompare`，禁用 `==`/`!=`。
- 对象级鉴权防 IDOR：per-user 读写 scope 到本人/属主，不信 body/param id；管理员对目标用户的读取和写入都过 `admincore.EnsureAdminCanManageTargetUser`。
- 不可信上传：上传体用公开游戏密钥解密，解码前校验深度（`msgpackcodec.ValidateMaxDepth`），拒绝含 `.`/`$` 的 Mongo 字段名，按剩余长度封顶分配。
- 限流/计数用原子 `IncrementWithTTL`，禁用 GetCache 后 SetCache；`c.IP()` 仅在 `EnableIPValidation` + 收窄 `trusted_proxies` 时可信。
- webhook 等对用户 URL 的出站请求在 dial 时拒绝私网 IP 并 pin 已校验 IP（防 DNS rebinding）。
- OAuth2：introspection pin `access_token`，拒绝已禁用 client 的 token，禁用 client 时吊销其 token/consent。
- 公开端点不暴露金额/PII/凭据，错误信息/时序不区分「不存在」与「无权限」。

## 实现偏好

- 优先复用 `internal/platform/api/session_*.go`（会话/认证逻辑都在这族文件）
- 优先复用 `internal/platform/oauth2/...`
- 优先复用 `admincore`、`usercore`
- 保持 handler 薄，复杂逻辑下沉到模块或 helper

## 数据层规则

项目使用两个独立的 Ent 数据库：

- Toolbox（主库）：schema 在 `ent/toolbox/schema/`，生成到 `utils/database/postgresql/`，运行 `go generate ./ent/toolbox`
- Bot（HarukiBot NEO）：schema 在 `ent/bot/schema/`，生成到 `utils/database/neopg/`，运行 `go generate ./ent/bot`

Bot 数据库使用独立 DSN（`haruki_bot.db_url`）。不要随意手改生成文件。

## 测试与验证

- 先跑触达包测试
- Ory / Session / OAuth2 改动优先补：
  - `internal/platform/api/session_handler*_test.go`
  - `internal/platform/oauth2/*_test.go`
  - 对应模块测试
- 跨模块变更时运行 `go test ./...`

## 文档同步

涉及 Ory 行为、认证流程、Auth Proxy header、OAuth2 流程变化时，同步更新：

- `docs/ory-suite-usage.zh-CN.md`

涉及 OAuth2 客户端对接变化时，同步更新：

- `docs/oauth2-integration.zh-CN.md`

涉及 Webhook 对接变化时，同步更新：

- `docs/webhook-integration.zh-CN.md`

涉及爱发电赞助 webhook/同步变化时，同步更新：

- `docs/afdian-sponsor-integration.zh-CN.md`

新增或移除端点时，同步更新：

- `external/oathkeeper/access-rules.yml`（必要时 `external/oathkeeper/oathkeeper.yml` 的 header mutator）

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
