# Copilot Instructions

本仓库的权威说明是根目录的 [`AGENTS.md`](../AGENTS.md)（`CLAUDE.md` 也只指向它）。下面只是给 Copilot（尤其是代码评审，它只读本文件开头的一部分）用的硬规则摘要；与 `AGENTS.md` 冲突时以 `AGENTS.md` 为准，修改规则时先改 `AGENTS.md` 再同步这里。

## 分层

- `main.go` 只做配置加载和启动入口；`api/` 只做路由注册；`internal/bootstrap/` 处理启动装配与配置校验
- `internal/modules/<name>/` 放业务逻辑，handler 保持薄；`internal/platform/...` 放跨模块平台能力；`utils/...` 放基础设施与外部系统适配，`utils` 不得 import `internal/platform`
- 优先复用 `internal/platform/api/session_*.go`（`SessionHandler`）、`internal/platform/oauth2/...`、`admincore`、`usercore`，不要另起平行 helper
- 不要重新引入部署快照（如 `deploy/`）、一次性迁移脚本目录、本地构建产物或临时导出目录
- 不要手改 Ent 生成代码（`utils/database/postgresql/`、`utils/database/neopg/`），改 `ent/*/schema/` 后 `go generate`

## Ory

- 浏览器身份只用 Kratos，OAuth2 只用 Hydra，受保护浏览器 API 走 Oathkeeper → backend
- 不要恢复旧的本地登录/注册/找回密码流程（保持 410 或交给 Ory）
- 启用 auth proxy 时保留 trusted header 校验与 `user_system.auth_proxy_session_header`；管理员敏感操作用代理会话级标识，不要只靠 `user_id` 或 `kratos_identity_id`
- 不破坏 Hydra subject 兼容（优先 `kratos_identity_id`，兼容旧 `users.id`）
- 设备授权（RFC 8628）由后端中介：Hydra 的 `/oauth2/device/*` 不加 Oathkeeper 规则；`/api/oauth2/token` 对非设备授权许可逐字节转发；用户码配置只来自 `DEVICE_FLOW_USER_CODE_*`；`oauth2DeviceFlowEnabled` 缺失即关闭；原始用户码与 `hdc_…`、`ory_dc_…`、`dfh_…` 不进 Redis 键名与日志
- `POST /internal/oauth2/introspect` 不加 Oathkeeper 规则，不写进对外接入文档

## 安全不变量（不得回退）

- auth proxy 身份只信 Oathkeeper 注入的 `X-Kratos-Identity-Id`；客户端 `X-User-Id` 若存在必须等于解析结果，否则拒绝
- 所有密钥、token、OTP、验证码用 `crypto/subtle.ConstantTimeCompare`，禁用 `==`/`!=`
- 防 IDOR：per-user 读写 scope 到本人或数据属主，不信 body/param id；管理员对目标用户的读取和写入都过 `admincore.EnsureAdminCanManageTargetUser`
- 不可信上传：解码前 `msgpackcodec.ValidateMaxDepth`，写库前 `gamedata.ValidateUploadFieldNames`，分配按长度封顶
- 限流/计数用原子 `IncrementWithTTL`，禁用 GetCache 后 SetCache
- 对用户 URL 的出站请求在 dial 时拒绝私网 IP 并 pin 已校验 IP
- introspection 固定 `access_token`，拒绝已禁用 client 的 token，禁用 client 时吊销其 token/consent
- 公开端点不暴露金额、PII、凭据；错误与时序不区分「不存在」与「无权限」

## 测试与文档

- 先跑触达包测试，跨模块改动跑 `go test ./...`
- 改 `access-rules.yml`、`docker-compose.yml`、`hydra.yml`、`.env.example` 时关注 `internal/architecture/` 的契约测试
- `docs/oauth2-integration.zh-CN.md` §4A.8 的 Go 示例必须与 `internal/modules/oauth2/hydra_device_live_test.go` 同名函数逐字一致
- 改 Ory / OAuth2 / Webhook / 端点行为时同步 `docs/` 与 `external/oathkeeper/`，具体落点见 `AGENTS.md`「文档规则」
- 提交标题格式 `[Feat|Fix|Chore|Docs] Imperative description`；代理署名用正文末尾的 `Co-authored-by:` trailer（Copilot：`Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>`）
- CI 唯一必需检查是 `CI OK`；工作流约定见 `AGENTS.md`「GitHub Actions workflows」
