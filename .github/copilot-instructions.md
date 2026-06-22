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
- 不可信上传：上传体用公开游戏密钥解密，解码前校验深度（`orderedmsgpack.ValidateMaxDepth`），拒绝含 `.`/`$` 的 Mongo 字段名，按剩余长度封顶分配。
- 限流/计数用原子 `IncrementWithTTL`，禁用 GetCache 后 SetCache；`c.IP()` 仅在 `EnableIPValidation` + 收窄 `trusted_proxies` 时可信。
- webhook 等对用户 URL 的出站请求在 dial 时拒绝私网 IP 并 pin 已校验 IP（防 DNS rebinding）。
- OAuth2：introspection pin `access_token`，拒绝已禁用 client 的 token，禁用 client 时吊销其 token/consent。
- 公开端点不暴露金额/PII/凭据，错误信息/时序不区分「不存在」与「无权限」。

## 实现偏好

- 优先复用 `utils/api/session_*.go`（会话/认证逻辑都在这族文件）
- 优先复用 `utils/oauth2/...`
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
  - `utils/api/session_handler*_test.go`
  - `utils/oauth2/*_test.go`
  - 对应模块测试
- 跨模块变更时运行 `go test ./...`

## 文档同步

涉及 Ory 行为、认证流程、Auth Proxy header、OAuth2 流程变化时，同步更新：

- `docs/ory-suite-usage.zh-CN.md`

涉及 HarukiBot NEO 注册/凭据重置流程变化时，同步更新：

- `docs/haruki-bot-neo-registration.zh-CN.md`

涉及 OAuth2 客户端对接变化时，同步更新：

- `docs/oauth2-client-integration.zh-CN.md`
- `docs/oauth2-confidential-client-integration.zh-CN.md`

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

Use the standardized workflow layout in `.github/workflows`:

- `ci.yml` runs on `main` pushes, pull requests targeting `main`, and manual dispatch.
- Go CI order: `gofmt`, `go build ./...`, `go vet ./...`, `staticcheck ./...`, then `go test -race -count=1 ./...`.
- `release.yml` is the standard release build entrypoint. It runs on `v*` tags and manual dispatch, builds release artifacts, uploads them with `actions/upload-artifact`, and publishes GitHub Release assets on tag pushes.
- `docker.yml` is the standard Docker entrypoint. It runs on `main` pushes, `v*` tags, PRs that touch Docker/build inputs, and manual dispatch. PRs build only; non-PR runs push GHCR images with lowercase image names and Docker metadata tags.

Workflow maintenance rules:

- Keep workflow filenames and top-level names aligned: `CI`, `Release`, `Docker`, and optional package-specific names.
- Use `actions/checkout@v6`, `actions/setup-go@v6`, `actions/upload-artifact@v7`, `actions/download-artifact@v8`, `softprops/action-gh-release@v3`, and current Docker actions (`setup-buildx@v4`, `login@v4`, `metadata@v6`, `build-push@v7`).
- Keep `permissions` minimal: `contents: read` for CI/Docker build-only work, `contents: write` for release publishing, and `packages: write` only when pushing container images.
- Use workflow `concurrency` keyed by workflow name and ref, with release jobs using `release-${{ github.ref_name }}` and `cancel-in-progress: false`.
- Do not reintroduce legacy workflow names such as `rust-ci.yml`, `build.yml`, `release-build.yml`, `docker-build.yml`, or `docker-release.yml` unless a package-specific workflow already exists and is intentionally preserved.
