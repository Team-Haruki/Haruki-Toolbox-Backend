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

## 实现偏好

- 优先复用 `utils/api/session_handler.go`
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

新增或移除端点时，同步更新：

- `external/oathkeeper/access-rules.yml`

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
