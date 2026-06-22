# AGENTS.md

## 项目概览

Haruki Toolbox Backend 是一个基于 Go 1.26 的后端项目，核心技术栈包括：

- Fiber：HTTP 路由与中间件
- Ent：PostgreSQL schema 与 ORM
- MongoDB：游戏数据、Webhook 等文档型数据存储
- Redis：缓存、验证码状态、限流状态、会话辅助状态
- Ory Kratos：浏览器身份体系与自助认证流程
- Ory Hydra：OAuth2 / OIDC

默认本地配置文件为 `haruki-toolbox-configs.yaml`。

## 当前仓库结构

当前仓库以源码为中心，不再保留部署包快照、迁移临时目录或运行期产物。

主源码目录：

- `main.go`
- `api/`
- `config/`
- `ent/`
- `internal/`
- `utils/`
- `.github/copilot-instructions.md`
- `docs/`（架构说明、Ory 文档、API 对接文档）
- `external/oathkeeper/`（Oathkeeper access rules）

请不要在没有明确需求的情况下重新引入以下类型的内容：

- 部署快照目录
- 一次性迁移脚本目录
- 本地构建产物
- 调试缓存目录
- 临时导出数据目录

## 分层规则

- `main.go` 只负责加载配置并进入启动流程。
- `api/` 只负责注册路由，不承载业务逻辑。
- `internal/bootstrap/` 负责启动装配、依赖初始化、配置校验。
- `internal/modules/...` 放业务处理逻辑。
- `internal/platform/...` 放跨模块复用但偏业务的平台能力。
- `utils/...` 放基础设施、通用 helper、外部系统适配器。

## Ory 相关工作准则

### 1. 身份体系默认前提

- 浏览器身份提供者只支持 `Kratos`
- OAuth2 提供者只支持 `Hydra`
- 浏览器受保护 API 的标准部署模式是 `Oathkeeper -> Backend`

### 2. 不要重新引入旧浏览器认证体系

当 `SessionHandler.UsesManagedBrowserAuth()` 成立时，旧浏览器认证入口应保持禁用：

- `/api/user/login`
- `/api/user/register`
- `/api/user/reset-password/send`
- `/api/user/reset-password`
- `/api/user/:toolbox_user_id/change-password`

这些入口在当前架构里应返回 `410 Gone` 或转为 Ory 驱动的实现，不要再把它们改回本地密码体系。

### 3. Auth Proxy 约束

如果启用了 `user_system.auth_proxy_enabled`：

- 必须保留 trusted header 校验
- 必须保留 `user_system.auth_proxy_session_header`
- 涉及管理员二次确认、敏感操作复用校验等逻辑时，必须使用“代理会话级标识”，不能只用 `user_id` 或 `kratos_identity_id`

当前实现对 `auth_proxy_session_header` 有启动期强校验，不能省略。

### 4. Hydra Subject 规则

Hydra subject 当前采用“优先 Kratos identity ID，兼容 fallback 本地 user ID”的策略：

- 新逻辑优先使用 `users.kratos_identity_id`
- 兼容逻辑仍允许旧 `users.id`

如果改动：

- `CurrentHydraSubject`
- `CurrentHydraSubjects`
- `HydraSubjectsForUser`
- OAuth2 introspection subject 映射

必须保证兼容过渡期行为不被破坏。

### 5. SessionHandler 是 Ory 集成核心

与 Kratos / Auth Proxy / 会话验证相关的改动，优先集中在 `utils/api/session_*.go` 这一族文件：

- `session_handler.go`（`SessionHandler` 类型与配置）
- `session_verify.go`、`session_auth_proxy.go`、`session_kratos_*.go`（会话解析与身份解析）

避免在各业务模块里复制一套会话解析、Kratos whoami 查询、header 信任逻辑。

## 安全不变量

以下规则源自历次安全审计，**不得回退**：

- **Auth Proxy 身份只信 Oathkeeper 注入的 subject 头，不信客户端头。** auth-proxy 模式下身份必须经 `resolveKratosIdentity` 从 `X-Kratos-Identity-Id` 解析；**绝不**把客户端自带的 `X-User-Id` 当权威——若存在必须等于解析结果，否则拒绝。后端**只能**经 Oathkeeper 访问（不要把后端端口发布到公网，绑内网/Tailscale 接口可以）。信任密钥是整个身份伪造边界：用 `crypto/subtle.ConstantTimeCompare` 比较；启动时拒绝占位值或 <16 字符；oathkeeper mutator 必须注入全部信任头（含会话 id）并清除 `X-User-Id`。
- **所有密钥常量时间比较。** 共享密钥、token、OTP、验证码一律 `crypto/subtle.ConstantTimeCompare`，禁用 `==`/`!=`。
- **对象级鉴权（防 IDOR）。** 每个 per-user 对象的读写都要 scope 到已认证本人或由数据解析出的属主，绝不信 body/param 里的 id。管理员对目标用户的**读取和**写入都必须过 `admincore.EnsureAdminCanManageTargetUser`（角色层级）——读取也要（detail/role/activity/system-logs）。绕过 Oathkeeper 的 token 网关端点（`/api/private/*`、harukiproxy、社交验证、`/internal/*`）每请求自鉴权，是最高价值攻击面。
- **不可信上传解析。** 上传体用**公开**的 Project Sekai 客户端密钥解密，解出的内容即攻击者可控：解码前校验嵌套深度（`orderedmsgpack.ValidateMaxDepth`），拒绝含 `.`/`$` 的 Mongo 字段名，按剩余长度封顶分配。Go 栈溢出是 fatal，`recover()` 救不了。
- **限流/计数原子化。** attempt 计数与限流用原子 `IncrementWithTTL`，禁用 GetCache 后 SetCache（竞态会绕过上限）。`c.IP()` 只在 `EnableIPValidation` 开启且 `trusted_proxies` 收窄到真实边缘代理时才可信。
- **SSRF。** 对用户提供 URL 的出站请求（webhook 回调）必须在 **dial 时**重新解析并拒绝私网/链路本地 IP、pin 已校验 IP，而不只是事前校验 DNS（防 rebinding）。
- **OAuth2 bearer。** introspection 固定 `access_token` 类型，拒绝已禁用 client 的 token，禁用 client 时要真正吊销其 token/consent（不只改 metadata）。
- **公开响应/枚举。** 公开端点不暴露付费金额、PII、凭据；错误信息或时序不得区分「不存在」与「无权限」。

## 数据与模型规则

项目使用两个独立的 Ent 数据库：

| 数据库 | Schema 目录 | 生成命令 | 生成产物 |
|--------|------------|---------|---------|
| Toolbox（主库） | `ent/toolbox/schema/` | `go generate ./ent/toolbox` | `utils/database/postgresql/` |
| Bot（HarukiBot NEO） | `ent/bot/schema/` | `go generate ./ent/bot` | `utils/database/neopg/` |

- Bot 数据库使用独立的 DSN（配置项 `haruki_bot.db_url`）
- 不要手改生成文件，除非任务明确要求

## 日志规则

- 优先使用项目 logger helper
- 全局 logger 应遵循当前启动时设置的 log level 和 writer
- 避免在包级提前固化旧日志配置

## 测试规则

优先跑最小必要验证：

- 触达包测试，例如：
  - `go test ./internal/modules/userauth ./utils/api`
- 涉及 Ory 会话、OAuth2、Auth Proxy 的跨模块改动时：
  - `go test ./...`
- 涉及 Ent schema 时：
  - Toolbox: `go generate ./ent/toolbox`
  - Bot: `go generate ./ent/bot`

Ory 相关改动尤其建议关注：

- `utils/api/session_handler*_test.go`
- `utils/oauth2/*_test.go`
- 对应业务模块的 route / managed identity 测试

## 文档规则

如果改动以下内容，应同步更新文档：

- Ory 架构
- 浏览器认证行为
- OAuth2 行为
- 受保护 API 接入方式
- Auth Proxy header 约定
- HarukiBot NEO 注册/凭据重置流程
- OAuth2 客户端对接
- Webhook 对接
- 新增或移除公开/受保护端点

当前文档：

- `docs/ory-suite-usage.zh-CN.md` — Ory 总体说明
- `docs/haruki-bot-neo-registration.zh-CN.md` — HarukiBot NEO 注册与凭据重置 API 对接
- `docs/oauth2-client-integration.zh-CN.md` — OAuth2 公开客户端对接
- `docs/oauth2-confidential-client-integration.zh-CN.md` — OAuth2 机密客户端对接
- `docs/webhook-integration.zh-CN.md` — Webhook 对接
- `docs/afdian-sponsor-integration.zh-CN.md` — 爱发电赞助 webhook/同步对接
- `external/oathkeeper/access-rules.yml` — Oathkeeper 访问规则
- `external/oathkeeper/oathkeeper.yml` — Oathkeeper auth-proxy header mutator 约定

## 提交前检查清单

提交前至少确认：

- 代码放在正确层级
- 没有把 Ory 逻辑分散到重复 helper
- 没有重新引入旧本地浏览器认证流程
- `auth_proxy_session_header` 相关行为仍然成立
- Hydra subject 兼容逻辑未被破坏
- 未回退「安全不变量」（鉴权 scope、常量时间比较、不可信上传校验、原子限流、SSRF、客户端头信任等）
- 测试已覆盖触达变更
- 若改动了 Ory 行为，文档已同步

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
