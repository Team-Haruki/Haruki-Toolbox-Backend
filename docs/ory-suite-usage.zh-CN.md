# Haruki Toolbox Backend 中 Ory 套件的使用说明

本文档说明当前项目如何使用 Ory Kratos、Ory Hydra 与 Ory Oathkeeper，以及这些组件在代码中的职责边界、配置入口、请求路径、数据映射关系与注意事项。

## 1. 总体架构

当前项目把 Ory 套件拆成三层职责：

- `Kratos`：负责浏览器身份、自助认证流程、邮箱验证、找回密码、设置流、浏览器 session
- `Oathkeeper`：负责浏览器受保护 API 的反向代理与会话鉴权，把可信 header 注入给后端
- `Hydra`：负责 OAuth2 / OIDC 客户端、授权码流程、token / revoke / consent / login challenge
- `Backend`：负责本地业务数据、用户映射、管理员能力、OAuth2 兼容入口、Kratos/Hydra 与业务数据库之间的桥接

代码入口：

- 启动装配：`internal/bootstrap/run.go`
- 路由总装配：`api/route.go`
- Kratos / Auth Proxy 会话处理：`utils/api/session_handler.go`
- Hydra 路由兼容层：`internal/modules/oauth2/hydra_routes.go`
- Hydra token introspection：`utils/oauth2/middleware.go`

## 2. 项目为什么要用 Ory

项目没有把“用户表 + 本地密码校验 + 本地 OAuth2 授权服务器”继续全部放在后端内部，而是把身份与 OAuth2 能力拆给 Ory，原因大致可以总结为：

- 浏览器登录、注册、找回密码、验证、设置这些标准身份流程由 Kratos 接管
- OAuth2 / OIDC 客户端与 token 生命周期由 Hydra 接管
- 浏览器访问受保护 API 时，不要求前端自己管理一套后端 session，而是让 Oathkeeper 对 Kratos session 做检查
- 后端只保留“本地业务用户模型”和 “Kratos identity / Hydra subject” 的映射关系

这使得项目能够把：

- 身份认证
- 浏览器会话
- OAuth2 授权
- 业务数据

拆成边界更清晰的几个层次。

## 3. 三个 Ory 组件分别承担什么职责

### 3.1 Kratos

Kratos 在当前项目里承担这些职责：

- 浏览器身份提供者
- 邮箱密码认证
- 浏览器 session
- recovery / verification / settings self-service flow
- identity 的 traits / verifiable addresses

项目在配置层面强制把浏览器身份提供者固定为 `kratos`。相关字段在：

- `config/config.go`
- `user_system.auth_provider`
- `user_system.kratos_public_url`
- `user_system.kratos_admin_url`
- `user_system.kratos_session_header`
- `user_system.kratos_session_cookie`

启动时会做强校验，见 `internal/bootstrap/run.go`：

- `user_system.kratos_public_url` 不能为空
- `user_system.kratos_admin_url` 不能为空

### 3.2 Oathkeeper

Oathkeeper 在当前项目里不是 OAuth2 server，也不是用户数据库，它的职责是：

- 作为浏览器受保护 API 的网关
- 用 Kratos `/sessions/whoami` 检查浏览器 session
- 把可信身份头转发给 backend
- 让 backend 在 `auth_proxy_enabled=true` 时信任这些 header

对 backend 而言，Oathkeeper 产出的 header 不是“可选优化”，而是部署契约的一部分。

尤其要注意：

- `auth_proxy_trusted_header`
- `auth_proxy_trusted_value`
- `auth_proxy_subject_header`
- `auth_proxy_session_header`

其中 `auth_proxy_session_header` 现在是强制项，原因是后台某些敏感操作不只需要知道“是谁”，还需要知道“是哪个浏览器会话完成了重认证”。

### 3.3 Hydra

Hydra 在当前项目里负责：

- OAuth2 / OIDC 客户端管理
- authorize / token / revoke / consent / login challenge
- 管理端 OAuth 客户端 CRUD
- access token introspection

后端不是另起一套 OAuth2 server，而是提供一层兼容 / 编排逻辑，把业务用户状态和 Hydra 的授权流程拼接起来。

相关入口：

- `internal/modules/oauth2/hydra_routes.go`
- `internal/modules/adminoauth/hydra_handlers.go`
- `utils/oauth2/middleware.go`
- `utils/oauth2/provider.go`

## 4. Backend 如何装配 Ory

启动流程在 `internal/bootstrap/run.go` 中完成。

关键步骤：

1. 加载配置并校验 Ory 必需项
2. 初始化 PostgreSQL、MongoDB、Redis
3. 创建 `SessionHandler`
4. 调用 `ConfigureIdentityProvider(...)` 注入 Kratos 配置
5. 调用 `ConfigureAuthProxy(...)` 注入 Oathkeeper/Auth Proxy 配置
6. 调用 `ConfigureAuthProxySessionHeader(...)` 设置代理会话头

这意味着：

- Kratos 模式
- Auth Proxy 模式
- 后端本地用户映射

并不是散落在各模块自己解析，而是集中由 `SessionHandler` 协调。

## 5. 本地用户与 Kratos identity 的关系

项目没有直接把全部业务都建立在 Kratos identity 上，而是保留了本地业务用户表 `users`，再通过 `users.kratos_identity_id` 做映射。

常见关系：

- 本地业务用户主键：`users.id`
- Kratos 身份主键：`users.kratos_identity_id`
- Hydra subject：优先使用 `kratos_identity_id`，兼容旧 `users.id`

这一层映射很关键，因为项目里大量业务表、权限、日志、管理员能力仍然围绕本地 `users.id` 展开。

因此当前架构不是“完全无本地用户表”，而是：

- 认证在 Ory
- 业务主体仍然在本地 PostgreSQL

## 6. 浏览器登录态在项目里的验证方式

### 6.1 统一入口：SessionHandler.VerifySessionToken

绝大多数用户受保护接口都使用：

- `apiHelper.SessionHandler.VerifySessionToken`

这个中间件会优先尝试 Auth Proxy，再回退到 Kratos token / cookie 验证。

行为大致分两种：

#### 模式 A：Auth Proxy 模式

如果 `auth_proxy_enabled=true`，后端会信任来自 Oathkeeper 的 header：

- `X-Auth-Proxy-Secret`
- `X-Kratos-Identity-Id`
- `X-User-Name`
- `X-User-Email`
- `X-User-Email-Verified`
- `X-User-Id`
- `X-Auth-Proxy-Session-Id`

其中：

- `X-Kratos-Identity-Id` 用于定位 Kratos identity
- `X-Auth-Proxy-Session-Id` 用于管理员敏感操作的“会话级重认证标记”

#### 模式 B：直接 Kratos 模式

如果不是 Auth Proxy 模式，`VerifySessionToken` 会尝试从以下来源解析 Kratos session：

- `Authorization: Bearer ...`
- `X-Session-Token`
- `Cookie: ory_kratos_session=...`

然后调用 Kratos whoami / session 相关逻辑，再映射回本地用户。

### 6.2 为什么 `auth_proxy_session_header` 很重要

项目中有一类管理员敏感操作，要求不是“这个用户身份曾经登录过”就够，而是要验证：

- 当前这个代理会话是否真正完成过最近一次重认证

因此不能只依赖：

- `user_id`
- `kratos_identity_id`

必须额外使用代理 session id。

这也是为什么：

- 启动时会强校验 `user_system.auth_proxy_session_header`
- Oathkeeper 需要把会话 `id` 注入 header

## 7. 浏览器登录、注册、找回密码、改密为什么大量返回 410

当前项目对“旧浏览器认证接口”的策略非常明确：

- 当项目处于 `Kratos` / managed browser auth 模式时，旧本地浏览器认证入口应被禁用
- 前端应该直接走 Kratos self-service flow

示例：

- `internal/modules/userauth/route.go`
- `internal/modules/userpasswordreset/resetpassword.go`
- `internal/modules/userprofile/account.go`
- `internal/modules/userauth/managed_identity.go`

禁用后的统一提示是：

`browser identity is managed by Ory Kratos; use Kratos self-service flows instead`

### 7.1 被禁用的典型入口

在 managed browser auth 模式下，以下入口会被禁用或返回 `410 Gone`：

- `/api/user/login`
- `/api/user/register`
- `/api/user/reset-password/send`
- `/api/user/reset-password`
- `/api/user/:toolbox_user_id/change-password`

### 7.2 为什么代码里还保留了 Kratos 版实现

虽然路由层会在 managed browser auth 模式下直接禁用旧入口，但代码里仍保留了：

- `handleLoginViaKratos`
- `handleRegisterViaKratos`
- `handleSendResetPasswordViaKratos`
- `handleResetPasswordViaKratos`
- `handleChangePasswordViaKratos`

这些实现的价值主要在于：

- 单元测试
- 受控兼容场景
- 模块内统一复用 SessionHandler 能力

但对当前标准浏览器接入路径来说，前端仍应直连 Kratos self-service flow。

## 8. Kratos 在项目里的具体使用方式

### 8.1 登录

项目里存在通过 `SessionHandler.LoginWithKratosPassword(...)` 走 Kratos 密码登录的能力。

核心行为：

- 接收邮箱 / 密码
- 调 Kratos 登录
- 取得 Kratos session token
- 再通过 session token 反查本地用户
- 返回本地业务用户数据

### 8.2 注册

项目里存在通过 `SessionHandler.RegisterWithKratosPassword(...)` 走 Kratos 注册的能力。

核心行为：

- 把邮箱 / 密码 / traits 交给 Kratos
- 注册成功后拿到 Kratos session token
- 通过 session token 解析本地 user
- 继续加载本地业务用户信息

也就是说，项目并不是注册完只留在 Kratos 里，而是要求注册后的 Kratos identity 能与本地业务用户关联起来。

### 8.3 找回密码

项目里保留了通过 `SessionHandler.StartKratosRecoveryByEmail(...)` 和 `ResetKratosPasswordByRecoveryCode(...)` 与 Kratos recovery flow 交互的能力。

但浏览器标准路径仍然应使用 Kratos 自己的 recovery self-service flow。

### 8.4 改密码

项目里保留了：

- 通过 Kratos 校验旧密码
- 通过 Kratos 更新新密码
- 成功后撤销 Kratos sessions

的实现。

但在 managed browser auth 模式下，浏览器侧标准用法仍应是 Kratos settings flow。

### 8.5 邮箱验证状态

项目使用 Kratos identity 上的：

- `verifiable_addresses[].verified`
- `verifiable_addresses[].status`

来表示验证状态。

本地历史 `users.email_verified` 只在迁移期和同步期有意义，不应再作为长期浏览器身份真相来源。

## 9. Oathkeeper 在项目里的具体作用

从后端视角看，Oathkeeper 主要解决两个问题：

### 9.1 受保护浏览器 API 网关

浏览器访问 `/api/user/*`、`/api/admin/*` 等受保护接口时，不需要后端自己直接暴露“浏览器 cookie session 校验网关”能力，而是由 Oathkeeper 先做：

- 检查 Kratos session
- 把可信用户信息写入 header
- 再把请求转发给 backend

### 9.2 会话级重认证支撑

管理端某些操作不是只校验“当前是不是管理员”，还要校验“当前浏览器会话是否做过最近重认证”。

这依赖：

- Oathkeeper 注入 `X-Auth-Proxy-Session-Id`
- 后端将其放入 `Locals("authProxySessionID")`
- 管理员重认证逻辑根据这个值生成和校验 marker

## 10. Hydra 在项目里的具体作用

### 10.1 OAuth2 浏览器兼容入口

项目提供了一组后端兼容入口，背后实际对接 Hydra：

- `/api/oauth2/authorize`
- `/api/oauth2/token`
- `/api/oauth2/revoke`
- `/api/oauth2/login`
- `/api/oauth2/login/accept`
- `/api/oauth2/login/reject`
- `/api/oauth2/consent`
- `/api/oauth2/consent/accept`
- `/api/oauth2/consent/reject`
- `/api/oauth2/authorize/consent`（legacy frontend compatibility）

这些入口的核心实现位于：

- `internal/modules/oauth2/hydra_routes.go`

### 10.2 登录同意与用户 subject

Hydra 需要 subject。项目里的策略是：

- 优先用 `kratos_identity_id`
- 兼容旧 `users.id`

这由以下 helper 实现：

- `HydraSubjectsForUser`
- `PreferredHydraSubject`
- `CurrentHydraSubjects`
- `CurrentHydraSubject`
- `CurrentHydraSubjectMatches`

意义在于：

- 新 OAuth2 数据尽量围绕 Kratos identity 稳定下来
- 老数据、过渡期客户端仍有兼容空间

### 10.3 Token Introspection

后端对 OAuth2 bearer token 的校验不是本地解 token，而是通过 Hydra admin introspection：

- 调 Hydra introspection endpoint
- 取 `sub`
- 先尝试按 `users.kratos_identity_id` 找本地用户
- 找不到时再 fallback 到 `users.id`

实现位于：

- `utils/oauth2/middleware.go`

这让 API 的 bearer token 身份与浏览器身份迁移可以在同一项目内逐步收敛。

### 10.4 管理端 OAuth Client 管理

管理员对 OAuth client 的查询、创建、更新、启停等能力，当前已经改为 Hydra-backed：

- 客户端列表
- 创建 client
- 更新 client
- 激活 / 禁用
- revoke / consent session 相关统计与操作

实现主要在：

- `internal/modules/adminoauth/hydra_handlers.go`
- `internal/modules/adminusers/user_oauth_handlers.go`

## 11. 当前配置层面对 Ory 的约束

### 11.1 Kratos 相关

核心配置项：

- `user_system.auth_provider`
- `user_system.kratos_public_url`
- `user_system.kratos_admin_url`
- `user_system.kratos_request_timeout_seconds`
- `user_system.kratos_session_header`
- `user_system.kratos_session_cookie`
- `user_system.kratos_auto_link_by_email`
- `user_system.kratos_auto_provision_user`

环境变量覆盖项：

- `KRATOS_PUBLIC_URL`
- `KRATOS_PUBLIC_BASE_URL`
- `KRATOS_ADMIN_URL`
- `KRATOS_ADMIN_BASE_URL`
- `KRATOS_REQUEST_TIMEOUT_SECONDS`
- `KRATOS_SESSION_HEADER`
- `KRATOS_SESSION_COOKIE`
- `KRATOS_AUTO_LINK_BY_EMAIL`
- `KRATOS_AUTO_PROVISION_USER`

### 11.2 Auth Proxy / Oathkeeper 相关

核心配置项：

- `user_system.auth_proxy_enabled`
- `user_system.auth_proxy_trusted_header`
- `user_system.auth_proxy_trusted_value`
- `user_system.auth_proxy_subject_header`
- `user_system.auth_proxy_name_header`
- `user_system.auth_proxy_email_header`
- `user_system.auth_proxy_email_verified_header`
- `user_system.auth_proxy_user_id_header`
- `user_system.auth_proxy_session_header`

其中 `auth_proxy_session_header` 现在是运行必需项，不再是可选项。

### 11.3 Hydra 相关

核心配置项：

- `oauth2.provider`
- `oauth2.hydra_public_url`
- `oauth2.hydra_browser_url`
- `oauth2.hydra_admin_url`
- `oauth2.hydra_client_id`
- `oauth2.hydra_client_secret`
- `oauth2.hydra_request_timeout_seconds`

环境变量覆盖项：

- `HYDRA_PUBLIC_URL`
- `HYDRA_PUBLIC_BASE_URL`
- `HYDRA_BROWSER_URL`
- `HYDRA_ADMIN_URL`
- `HYDRA_ADMIN_BASE_URL`
- `HYDRA_CLIENT_ID`
- `HYDRA_CLIENT_SECRET`
- `HYDRA_REQUEST_TIMEOUT_SECONDS`

## 12. 当前架构下的常见坑

### 12.1 忘记配置 `auth_proxy_session_header`

后果：

- backend 启动直接失败

原因：

- 管理员敏感操作需要会话级重认证 marker

### 12.2 Oathkeeper 没把 session id 注入 header

后果：

- backend 能启动
- 普通接口可能正常
- 管理端重认证相关逻辑会异常

### 12.3 只保留了 Kratos identity，没有同步本地用户映射

后果：

- Kratos 登录成功，但 backend 无法解析本地业务用户
- 表现为 identity unmapped / invalid user session

### 12.4 改动了 Hydra subject 规则但没兼容旧数据

后果：

- OAuth2 授权列表或 revoke 行为出现漏数据
- token introspection 解析不到本地用户

### 12.5 把 Kratos / Oathkeeper / Hydra 逻辑写散

后果：

- 同一种身份解析逻辑在多个模块里重复实现
- 后续改 header、改 subject、改 session 策略时容易漏

正确做法是尽量收敛在：

- `utils/api/session_handler.go`
- `utils/oauth2/...`
- `internal/modules/oauth2/...`

## 13. 对后续开发的建议

如果你要继续在本项目里开发 Ory 相关能力，建议遵守以下原则：

1. 浏览器身份逻辑优先考虑 Kratos self-service flow，而不是在 backend 里再造一套页面式认证。
2. 浏览器受保护 API 默认考虑 Oathkeeper/Auth Proxy，而不是让前端直接长期依赖后端本地 session。
3. OAuth2 能力优先通过 Hydra 编排，不要再新增一套本地 OAuth server。
4. 本地用户表仍然是业务真相来源，不能把所有业务都直接挂在 Kratos identity JSON 上。
5. 涉及会话、header、whoami、subject 的修改优先收敛到 `SessionHandler` 和 `utils/oauth2`。
6. 改动 Ory 相关行为时，优先补充：
   - `session_handler` 测试
   - Hydra middleware 测试
   - 对应业务模块 managed identity / route 测试

## 14. 一句话总结

这个项目对 Ory 的使用方式不是“把后端完全变成无状态代理”，而是：

- 用 `Kratos` 承接浏览器身份与认证流程
- 用 `Oathkeeper` 承接浏览器 API 入口与可信身份转发
- 用 `Hydra` 承接 OAuth2 / OIDC
- 用本地 `PostgreSQL users` 承接业务用户主体
- 用 `users.kratos_identity_id` 把 Ory 世界和业务世界桥接起来

这就是当前 Haruki Toolbox Backend 对 Ory 套件的核心集成方式。
