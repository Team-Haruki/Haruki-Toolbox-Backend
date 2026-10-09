# AGENTS.md

本文件是本仓库给编码代理（Claude Code、Codex、Copilot 等）的唯一权威说明。`CLAUDE.md` 只指向这里；`.github/copilot-instructions.md` 是给 Copilot 代码评审用的精简摘要，与本文件冲突时以本文件为准。

## 项目概览

Haruki Toolbox Backend（module `github.com/Team-Haruki/Haruki-Toolbox-Backend`）收集用户上传的 suite / mysekai 游戏数据并提供公开 API。项目基于 Go 1.27（版本以 `go.mod` 为准），核心技术栈包括：

- Fiber v3：HTTP 路由与中间件
- Ent：PostgreSQL schema 与 ORM
- 游戏数据：独立 PostgreSQL pool 存储 suite / mysekai；MongoDB 已退役，仅保留 BSON 兼容工具
- Redis：缓存、验证码状态、限流状态、会话辅助状态
- Ory Kratos：浏览器身份体系与自助认证流程
- Ory Hydra：OAuth2 / OIDC
- Ory Oathkeeper：浏览器受保护 API 的身份代理

默认本地配置文件为 `haruki-toolbox-configs.yaml`（YAML，支持 `${ENV_VAR}` 插值；可用 `HARUKI_CONFIG_PATH` 指定路径，未指定时从工作目录逐级向上查找）。全部字段见 `haruki-toolbox-configs.example.yaml`，环境变量覆盖见 `config/env.go`。

## 构建与运行

```bash
go build -o haruki-toolbox-backend ./main.go   # 构建
go run ./main.go                                # 运行（需要 haruki-toolbox-configs.yaml）
```

## 当前仓库结构

当前仓库以源码为中心，不再保留部署包快照、迁移临时目录或运行期产物。

主源码目录：

- `main.go`
- `api/`
- `cmd/`（辅助命令行工具，目前只有 `nuverse-restore-compare`）
- `config/`
- `data/`（suite 复原用的 Avro schema，随发布包和镜像分发）
- `ent/`
- `internal/`
- `utils/`
- `version/`（构建时由 `-ldflags -X` 注入版本信息）
- `.github/copilot-instructions.md`
- `docs/`（架构说明、Ory 文档、API 对接文档）
- `external/`（Kratos / Hydra / Oathkeeper 配置；`external/oathkeeper/` 含 access rules，`external/hydra/it/` 是真实 Hydra 集成测试用的 compose）

请不要在没有明确需求的情况下重新引入以下类型的内容：

- 部署快照目录（如 `deploy/`）
- 一次性迁移脚本目录
- 本地构建产物
- 调试缓存目录
- 临时导出数据目录

## 分层规则

- `main.go` 只负责加载配置并进入启动流程。
- `api/`（`api/route.go`）只负责注册路由，不承载业务逻辑。
- `internal/bootstrap/` 负责启动装配、依赖初始化、配置校验、Fiber 设置。
- `internal/modules/<name>/` 放业务处理逻辑（handler 与模块内逻辑）。
- `internal/platform/...` 放跨模块复用但偏业务的平台能力（`api`、`authheader`、`filtering`、`identity`、`mailnotify`、`oauth2`、`pagination`、`runtimeconfig`、`timeutil`、`upload`）。
- `utils/...` 放基础设施、通用 helper、外部系统适配器；`utils/codec/` 放通用编解码，`utils/game/` 放游戏协议和结构复原。
- `internal/platform/api`、`upload`、`oauth2` 分别承载共享 API/会话能力、上传编排和 OAuth2 集成；`utils` 不得反向依赖平台层（`internal/architecture` 的 `TestUtilsDoNotImportPlatform`）。
- handler 保持薄，复杂逻辑下沉到模块或 helper。

## 常用包

- `internal/platform/api/` — `SessionHandler`、`HarukiToolboxRouterHelpers`、路由中间件
- `internal/platform/oauth2/` — Hydra 客户端（`HydraConfig`，含 `DoWithoutRedirect`）、scope、bearer 中间件与共享的 `IntrospectAccessToken`
- `internal/modules/oauth2/` — Hydra 路由兼容层、令牌端点兼容层、设备授权（`hydra_device_*.go`，含回收器）与内部令牌校验 API
- `utils/database/` — `HarukiToolboxDBManager`，汇总下列客户端
- `utils/database/postgresql/` — Toolbox 主库 Ent 客户端（生成）
- `utils/database/neopg/` — Bot 数据库 Ent 客户端（生成）
- `utils/database/gamedata/` — suite / mysekai 游戏数据的独立 PostgreSQL pool 与读写（含上传字段名校验和大小上限）
- `utils/database/redis/` — Redis 客户端与 `KeyBuilder`
- `internal/modules/admincore/` — 管理端共享逻辑（含角色层级守卫 `EnsureAdminCanManageTargetUser`、`CurrentAdminActor`）
- `internal/modules/usercore/` — 用户端共享逻辑
- `internal/modules/harukibotneo/` — HarukiBot NEO 注册与凭据重置（状态、发信、注册/重置）
- `internal/modules/botsecurity/` — Haruki Cloud 推送的 bot 安全告警：内部接收（`bot_security.ingest_token_sha256`，未配置不注册路由）与 `/api/admin/bot-security` 管理端；主人 QQ 从 Bot 库按页批量解析，Bot 库不可用时置空
- `internal/modules/sponsor/` + `adminsponsor/` — 爱发电赞助墙（公开读取 + webhook；爱发电 webhook 无签名，真实性靠 URL secret 和/或经爱发电 API 回查订单）与管理端
- `utils/codec/msgpackcodec/` — 面向不可信上传数据的有界 MessagePack 解码与 JSON 转换（`ValidateMaxDepth` 校验深度、按长度封顶）；`utils/orderedmap/` 是其 OrderedMap 存储

优先复用 `SessionHandler`、`admincore`、`usercore` 与 `internal/platform/oauth2/`，不要另起平行 helper。

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

与 Kratos / Auth Proxy / 会话验证相关的改动，优先集中在 `internal/platform/api/session_*.go` 这一族文件：

- `session_handler.go`（`SessionHandler` 类型与配置）
- `session_verify.go`、`session_auth_proxy.go`、`session_kratos_*.go`（会话解析与身份解析）

避免在各业务模块里复制一套会话解析、Kratos whoami 查询、header 信任逻辑。

### 6. OAuth2 设备授权（RFC 8628）由后端中介

设备授权的代码集中在 `internal/modules/oauth2`（`hydra_device_*.go`、`hydra_token_endpoint.go`），架构见 `docs/ory-suite-usage.zh-CN.md` §10.5，配置与开关见 §10.6。改动时保持：

- Hydra 的 `/oauth2/device/*`、`/oauth2/fallbacks/device` 不加 Oathkeeper 规则；设备只调用后端的 `POST /api/oauth2/device/auth` 与 `POST /api/oauth2/token`，lookup / approve / deny 走 cookie_session 规则（`internal/architecture/oathkeeper_oauth2_device_rules_test.go`）
- `/api/oauth2/token` 是令牌端点兼容层：非设备授权许可的请求逐字节转发给 Hydra（`TestTokenShimNonDeviceGrantVerbatim`）；两份发现文档的 `token_endpoint` 与 `device_authorization_endpoint` 由 compose 中的 Hydra 环境变量指向后端（`TestOryDeviceFlowDeploymentContract`）
- Hydra 与 backend 的用户码字符集、长度、TTL 只来自 `.env` 的 `DEVICE_FLOW_USER_CODE_*`，不设 Hydra 的 user_code 熵预设
- 运行时总开关 `oauth2DeviceFlowEnabled` 字段缺失视为关闭，不要改成缺省开启
- 原始用户码、`hdc_…`、`ory_dc_…`、`dfh_…` 不进入 Redis 键名与日志：键名经 `KeyBuilder` 用 `user_system.session_sign_token` 做 HMAC，日志经 `utils/redact` 脱敏
- `/internal/*` 下供自有服务调用的内部路由只在 backend 端口上、不加 Oathkeeper 规则（`TestInternalAPINotRoutedByOathkeeper`）；它们的说明只放在私有运维文档中，不写进本仓库的任何文档

## 安全不变量

以下规则源自历次安全审计，**不得回退**：

- **Auth Proxy 身份只信 Oathkeeper 注入的 subject 头，不信客户端头。** auth-proxy 模式下身份必须经 `resolveKratosIdentity` 从 `X-Kratos-Identity-Id` 解析；**绝不**把客户端自带的 `X-User-Id` 当权威——若存在必须等于解析结果，否则拒绝。后端**只能**经 Oathkeeper 访问（不要把后端端口发布到公网，绑内网/Tailscale 接口可以）。信任密钥是整个身份伪造边界：用 `crypto/subtle.ConstantTimeCompare` 比较；启动时拒绝占位值或 <16 字符；oathkeeper mutator 必须注入全部信任头（含会话 id）并清除 `X-User-Id`。
- **所有密钥常量时间比较。** 共享密钥、token、OTP、验证码一律 `crypto/subtle.ConstantTimeCompare`，禁用 `==`/`!=`。
- **对象级鉴权（防 IDOR）。** 每个 per-user 对象的读写都要 scope 到已认证本人或由数据解析出的属主，绝不信 body/param 里的 id。管理员对目标用户的**读取和**写入都必须过 `admincore.EnsureAdminCanManageTargetUser`（角色层级）——读取也要（detail/role/activity/system-logs）。绕过 Oathkeeper 的 token 网关端点（`/api/private/*`、harukiproxy、社交验证、`/internal/*`）每请求自鉴权，是最高价值攻击面。
- **不可信上传解析。** 上传体用**公开**的 Project Sekai 客户端密钥解密，解出的内容即攻击者可控：解码前校验嵌套深度（`msgpackcodec.ValidateMaxDepth`），按剩余长度封顶分配；写入游戏数据库前拒绝含 `.`/`$`、NUL 或非法 UTF-8 的字段名（`gamedata.ValidateUploadFieldNames`，MongoDB 退役后仍保留），并按 `gamedata.Limits` 限制单键与整行大小。Go 栈溢出是 fatal，`recover()` 救不了。
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
- 凭据不进日志：项目 logger、access log（`redact.Writer`）、system log 的 path 与上传审计的错误信息都经 `utils/redact` 脱敏。引继 ID 与引继密码一律替换为 `<redacted>`；知道确切值的调用方（Sekai 客户端）把它传给 `redact.Text` / `redact.Error`
- 游戏用户 ID 一般可以出现在日志里（公开数据 URL 本身就带它，上传失败日志把它作为结构化字段）。例外是引继流程：引继解析出的 ID 在 Sekai 客户端的日志与 `APIError` 里经 `redact.MaskIDs` / `redact.IDsInError` 换成 `redact.Fingerprint`（进程内随机密钥的 HMAC，只能在同一进程内关联，不可逆），上传审计行另行记录原值

## 测试规则

优先跑最小必要验证：

- 触达包测试，例如：
  - `go test ./internal/modules/userauth ./internal/platform/api`
- 涉及 Ory 会话、OAuth2、Auth Proxy 的跨模块改动时：
  - `go test ./...`
- 涉及 Ent schema 时：
  - Toolbox: `go generate ./ent/toolbox`
  - Bot: `go generate ./ent/bot`
- 真实 Hydra 的设备授权测试（`internal/modules/oauth2/hydra_device_live_test.go`，build tag `hydra_live`）不在 `go test ./...` 里，需要真实 Hydra：本地步骤见 `docs/ory-suite-usage.zh-CN.md` §11.3，CI 见下文 `Device flow live`

Ory 相关改动尤其建议关注：

- `internal/platform/api/session_handler*_test.go`
- `internal/platform/oauth2/*_test.go`
- 对应业务模块的 route / managed identity 测试
- `internal/architecture/` 中的 Oathkeeper 规则与 Ory 部署契约测试（改 `access-rules.yml`、`docker-compose.yml`、`hydra.yml`、`.env.example` 时）
- `TestIntegrationDocGoSampleMatchesLiveTest`：`docs/oauth2-integration.zh-CN.md` §4A.8 Go 示例里的 `deviceLogin`、`authorizedAccountName`、`retryAfter`、`sleepContext`（连同注释）必须与 `internal/modules/oauth2/hydra_device_live_test.go` 中的同名函数逐字一致，改一处就要改另一处
- 设备授权或 `ORY_VERSION` 的改动：在待部署的 commit 上手动触发 `Device flow live`（见下文 GitHub Actions）

## 文档规则

如果改动以下内容，应同步更新文档：

- Ory 架构
- 浏览器认证行为
- OAuth2 行为
- 受保护 API 接入方式
- Auth Proxy header 约定
- OAuth2 客户端对接
- Webhook 对接
- 新增或移除公开/受保护端点

具体落点：

- Ory 行为、认证流程、OAuth2 流程、Auth Proxy header 约定 → `docs/ory-suite-usage.zh-CN.md`（设备授权：架构 §10.5、配置与开关 §10.6、仓库内的部署配置 §11.3）
- OAuth2 客户端对接 → `docs/oauth2-integration.zh-CN.md`（一份文档覆盖公开客户端、保密客户端、设备授权 §4A 与 OAuth2 Webhook）；只做 OIDC 登录的 RP 能看到的变化（Discovery、端点、登出）另同步 `docs/oidc-provider.zh-CN.md`
- Webhook 行为 → `docs/webhook-integration.zh-CN.md`（Public API webhook）与 `docs/oauth2-integration.zh-CN.md` §8（OAuth2 webhook）
- 架构、依赖方向、模块边界 → `docs/backend-architecture.zh-CN.md`；JSON / MessagePack 编解码约定 → `docs/json-conventions.zh-CN.md`、`docs/msgpack-codec.zh-CN.md`
- 生产部署与上线步骤、监控告警、回滚、密钥轮换、数据库手工迁移、内部接口，以及站内前端契约（游戏账号数据授权、上传读写授权页面）、爱发电赞助、上传维护、MYSEKAI 复原、iOS 模块、游戏数据加密配置等运维资料 → 私有运维文档（不在本仓库；需要时向维护者索取）
- 新增或移除公开/受保护端点 → `external/oathkeeper/access-rules.yml`；auth-proxy header 约定同时在 `external/oathkeeper/oathkeeper.yml`（header mutator）

文档维护约定：

- 关于本项目自身系统的文档只在工作未完成时保留：落地后删除，代码无法表达的内容改为写在相关代码旁的注释里，历史留在 git。给外部集成方的文档始终保留。
- `docs/` 与本仓库其他 Markdown 都是公开的：不写 tailnet / 私网 IP、内部主机名、生产文件路径、生产容器或 compose 项目名、密钥及其存放位置、内部接口（`/internal/*`）及其访问方式、运维手册。集成方必须使用的公开域名（issuer、公开 API 基址）可以写。这类内容写进私有运维文档，公开文档只能说「运维资料另行维护」，不得链接私有仓库或路径。
- `docs/` 内的链接一律用仓库相对路径（`oauth2-integration.zh-CN.md`、`../internal/...`），不要提交本机绝对路径。
- 修改代理规则时先改本文件；`.github/copilot-instructions.md` 只是摘要，涉及其中条目时同步更新，`CLAUDE.md` 保持只指向本文件。

当前文档：

- `docs/README.md` — 文档索引，按读者分组；新增文档在这里登记
- `docs/ory-suite-usage.zh-CN.md` — Ory 总体说明（含设备授权架构 §10.5、配置与开关 §10.6、部署配置 §11.3）
- `docs/oauth2-integration.zh-CN.md` — OAuth2 / OIDC 接入(公开 + 保密客户端 + 设备授权 §4A + Webhook)
- `docs/oidc-provider.zh-CN.md` — 只做 OIDC 登录的外部 RP 接入（令牌端点地址变化见 §1）
- `docs/webhook-integration.zh-CN.md` — Webhook 对接
- `docs/harukiproxy-v3-client-integration.zh-CN.md` — HarukiProxy v3 客户端对接（OAuth2 上传）
- `docs/backend-architecture.zh-CN.md`、`docs/json-conventions.zh-CN.md`、`docs/msgpack-codec.zh-CN.md` — 贡献者的代码约定
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
  - Claude: `Co-authored-by: Claude Opus 5.5 <noreply@anthropic.com>` (substitute the actual model, e.g. `Claude Opus 4.8`, `Claude Sonnet 4.6`)
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
  - `Go` (`go-ci` template, jobs `Lint` and `Test`): `gofmt`, `go mod tidy -diff`,
    `go build` / `go vet` (`-mod=readonly`), `go tool staticcheck` (the `tool` directive in
    `go.mod`), then the tests **once**: `go test -race -count=1 ./...` with coverage. Go
    caches are written only from `main`.
  - `Sonar` scans that coverage (skipped green on Dependabot/fork PRs); `SONAR_TOKEN` is
    only passed to the scan.
  - `Docker` does not wait for the tests. PRs build only, and only when Go sources, `data/`,
    `external/`, `go.mod`/`go.sum`, the Dockerfile, `.dockerignore`, the example config or
    the workflows change (`pr-paths` in `ci.yml`). On
    `main` it runs in parallel with the tests and pushes the immutable
    `ghcr.io/team-haruki/haruki-toolbox-backend:sha-<full sha>` and `:sha-<7 chars>` as
    soon as the build finishes. The `Docker tags` job (`docker-retag.yml`, after `CI OK`)
    then moves `:main` to that digest without rebuilding, so `:main` only follows commits
    whose `CI OK` passed. Main images carry `VERSION=main-<sha7>`, `GIT_SHA` and
    `BUILD_DATE` = the commit time. Images are `linux/amd64` only. The registry
    `:buildcache` keeps the module download layer.
  - `Workflow lint` runs the `actionlint` template on the workflow files.
  - The aggregate job **`CI OK`** (needs `Go`, `Sonar`, `Docker`, `Workflow lint`) is the
    single status that sums up CI. `main` has no branch protection or rulesets, so GitHub
    does not block a merge on it: confirm it is green before merging. It is enforced only
    downstream, by `release-gate` on tags and by the `Docker tags` job that moves `:main`.
- `release.yml` (`Release`): bump `Version` in `version/version.go` in a PR (9.0.0 is in
  the rc phase: `v9.0.0-rcN`, the current one is in `version/version.go`) → merge and wait
  for `CI OK` on `main` → push the same tag (e.g. `v9.0.0-rc6`). `release-gate` (`version-source: go`) refuses a tag
  that differs from `version/version.go` and waits for `CI OK` on the tagged commit; then
  `go-release` builds `HarukiToolboxBackend-linux-amd64.tar.gz` and
  `HarukiToolboxBackend-linux-arm64.tar.gz` (binary, `data/` and
  `haruki-toolbox-configs.example.yaml` at the archive root; CGO off, `-trimpath`,
  `Version=<tag>`, `Commit=<sha>`, `BuildDate=<commit time>`); the image is **built** (not
  promoted from `main`, because the version is compiled in) with `VERSION=<tag>` and tagged
  `:<version>` (e.g. `:9.0.0-rc6`, which production pulls; stable tags also get
  `:<major>.<minor>` and, for the highest one, `:latest`); and the GitHub Release is
  published with `SHA256SUMS-<tag>.txt` (`-rc` tags as pre-releases that never become
  "latest"). Manual dispatch is a dry run: it builds the binaries with the version from
  `version/version.go` and publishes nothing, also when started on a tag.
- `device-flow-live.yml` (`Device flow live`) is manual dispatch only and not part of
  `CI OK`: a custom matrix job (the `go-ci` template cannot start Hydra) that brings up
  `external/hydra/it/docker-compose.device-it.yml` with Hydra `v25.4.0` and `v26.2.0` and runs
  the `hydra_live`-tagged OAuth2 device-flow test against each. Run it on the commit to
  deploy and before changing `ORY_VERSION`; the local recipe is in
  `docs/ory-suite-usage.zh-CN.md` §11.3.
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

## Release notes

Release notes follow the org standard
[RELEASE_NOTES.md](https://github.com/seiunx-dev/ci-templates/blob/main/RELEASE_NOTES.md)
and are written in English.

- The release title is the tag only (e.g. `v9.0.0-rc6`), with no prefix.
- Tags with an `-alpha`, `-beta` or `-rc` suffix are pre-releases; every other tag is a
  regular release, and every tag gets a release.
- Omit empty sections, and end every item with its PR number `(#123)` (the short commit
  SHA when there is no PR).
- `Release` publishes auto-generated notes; once it has published, rewrite them to the
  standard with `gh release edit <tag> --notes-file <file>`.
