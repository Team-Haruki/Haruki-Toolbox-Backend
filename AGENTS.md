# AGENTS.md

## 项目概览

Haruki Toolbox Backend 是一个基于 Go 1.26 的后端项目，核心技术栈包括：

- Fiber：HTTP 路由与中间件
- Ent：PostgreSQL schema 与 ORM
- MongoDB：游戏数据、Webhook 等文档型数据存储
- Redis：缓存、验证码状态、限流状态、会话辅助状态
- Ory Kratos：浏览器身份体系与自助认证流程
- Ory Hydra：OAuth2 / OIDC

默认本地配置文件为 `haruki-suite-configs.yaml`。

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

与 Kratos / Auth Proxy / 会话验证相关的改动，优先集中在：

- `utils/api/session_handler.go`

避免在各业务模块里复制一套会话解析、Kratos whoami 查询、header 信任逻辑。

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
- `external/oathkeeper/access-rules.yml` — Oathkeeper 访问规则

## 提交前检查清单

提交前至少确认：

- 代码放在正确层级
- 没有把 Ory 逻辑分散到重复 helper
- 没有重新引入旧本地浏览器认证流程
- `auth_proxy_session_header` 相关行为仍然成立
- Hydra subject 兼容逻辑未被破坏
- 测试已覆盖触达变更
- 若改动了 Ory 行为，文档已同步
