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

- Ent schema 在 `ent/schema/...`
- 生成结果在 `utils/database/postgresql/...`
- 修改 schema 后运行 `go generate ./ent`
- 不要随意手改生成文件

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
