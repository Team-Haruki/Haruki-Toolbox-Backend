# Haruki Toolbox Backend 中 Ory 套件的使用说明

本文档说明当前项目如何使用 Ory Kratos、Ory Hydra 与 Ory Oathkeeper，以及这些组件在代码中的职责边界、配置入口、请求路径、数据映射关系与注意事项。

## 1. 总体架构

当前项目把 Ory 套件拆成三层职责：

- `Kratos`：负责浏览器身份、自助认证流程、邮箱验证、找回密码、设置流、浏览器 session
- `Oathkeeper`：负责浏览器受保护 API 的反向代理与会话鉴权，把可信 header 注入给后端
- `Hydra`：负责 OAuth2 / OIDC 客户端、授权码流程、设备授权的底层机制、token / revoke / consent / login challenge
- `Backend`：负责本地业务数据、用户映射、管理员能力、OAuth2 兼容入口（含设备授权的端点、令牌端点兼容层与服务端代驱链，§10.5）、Kratos/Hydra 与业务数据库之间的桥接

代码入口：

- 启动装配：`internal/bootstrap/run.go`
- 路由总装配：`api/route.go`
- Kratos / Auth Proxy 会话处理：`internal/platform/api/session_handler.go`（`VerifySessionToken` 在 `session_verify.go`，Auth Proxy 解析在 `session_auth_proxy.go`）
- Hydra 路由兼容层：`internal/modules/oauth2/hydra_routes.go`
- Hydra token introspection：`internal/platform/oauth2/middleware.go`

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

- `config/types.go`（字段定义；默认值在 `config/defaults.go`，环境变量覆盖在 `config/env.go`）
- `user_system.auth_provider`
- `user_system.kratos_public_url`
- `user_system.kratos_admin_url`
- `user_system.kratos_session_header`
- `user_system.kratos_session_cookie`

启动时会做强校验，见 `internal/bootstrap/validate.go` 的 `validateUserSystemConfig`（由 `internal/bootstrap/run.go` 的 `Build` 调用）：

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
- 设备授权（RFC 8628）的底层机制：签发设备码与用户码、保存设备码行、兑换令牌。Hydra 的设备端点只由后端调用

后端不是另起一套 OAuth2 server，而是提供一层兼容 / 编排逻辑，把业务用户状态和 Hydra 的授权流程拼接起来。

设备授权是这种编排最重的一处：Hydra v25.4.0 的设备流程不限流、不保证单次批准、不把拒绝和过期告诉设备、也不清理过期的设备码行，所以 Hydra 自己的 `/oauth2/device/auth`、`/oauth2/device/verify`、`/oauth2/fallbacks/device` 不经 Oathkeeper 对外（公网 404）。设备调用后端的 `POST /api/oauth2/device/auth` 并轮询令牌端点兼容层 `POST /api/oauth2/token`；用户在前端 `/device` 页输入用户码，后端在 approve 请求内代替浏览器走完 Hydra 的 verify → login → consent 链。两份发现文档（`/.well-known/openid-configuration` 与 `/.well-known/oauth-authorization-server`）的 `device_authorization_endpoint` 和 `token_endpoint` 都由 Hydra 环境变量覆盖为后端地址（§11.3）。过期设备码行由 compose 中的 `hydra-device-janitor` 每小时分批删除。设备流程的端点见 §10.1，架构见 §10.5，配置与开关见 §10.6。

相关入口：

- `internal/modules/oauth2/hydra_routes.go`
- `internal/modules/adminoauth/hydra_client_handlers.go`、`hydra_revoke_handlers.go`
- `internal/platform/oauth2/middleware.go`
- `internal/platform/oauth2/provider.go`

## 4. Backend 如何装配 Ory

启动流程在 `internal/bootstrap/run.go` 中完成。

关键步骤：

1. 加载配置并校验 Ory 必需项
2. 初始化 PostgreSQL（业务库与游戏数据库）、Redis
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

对于已关联的 identity，SessionHandler 在同一请求内复用映射查询返回的 ID、名称、邮箱和 identity ID，供资料同步比较使用，避免重复读取。新建、按邮箱关联和自定义 resolver 分支仍重新读取资料。这不缓存会话、角色或权限；每次请求继续执行原有的身份和对象范围校验。

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
- `internal/modules/userpasswordreset/routes.go`、`send_handler.go`、`apply_handler.go`
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

当前 Toolbox 前端可通过以下登录用户接口读取自己已验证绑定账号的数据：

- `GET /api/user/:toolbox_user_id/game-account/:server/:game_user_id/:data_type`
- `:data_type` 允许 `suite`、`mysekai`、`profile`
- `suite` 和 `mysekai` 复用 public API / OAuth2 game-data 的数据读取逻辑，并支持 `key` 查询参数
- `suite` 和 `mysekai` 支持 `known_upload_time=<上次完整响应中的 upload_time>` 条件读取；数据未变化时返回 `304 Not Modified`、空响应体和 `X-Upload-Time` 响应头
- 使用 `key` 过滤时必须同时请求 `upload_time` 才可能返回 304；suite 的公开字段允许列表不包含 `upload_time` 时也会回退到完整响应
- 完整响应的时间戳始终以响应体 `upload_time` 为准；时间戳精度为 unix 秒，同一秒内多次上传无法区分
- `profile` 通过 Haruki Sekai API 读取绑定账号 profile，并直接透传 JSON 响应体
- 该接口会校验浏览器登录态、`:toolbox_user_id` 是否为当前用户、账号绑定是否属于当前用户且已验证；`suite` / `mysekai` 也允许通过有效的游戏账号数据授权读取
- `suite` 数据会保留 `userGamedata.userId` number 字段，并额外返回 `userGamedata.userIdString` 字符串镜像；前端应优先使用字符串字段避免 64 位整数精度丢失
- 当数据响应暴露顶层 `_id` 时，会同时返回 `_idString`

旧的 `GET /api/user/:toolbox_user_id/game-data/:server/:data_type/:user_id` 未正式接入前端，已由上述 `game-account` 入口替代。

设备授权（RFC 8628）相关路径在 Oathkeeper 上的归属（`external/oathkeeper/access-rules.yml`，架构测试 `internal/architecture/oathkeeper_oauth2_device_rules_test.go` 守护，每条路径加方法恰好命中一条规则）：

| 路径 | 方法 | 规则 | 认证 |
| --- | --- | --- | --- |
| `/api/oauth2/device/auth`、`/api/oauth2/token` | POST | `haruki-public-oauth-proxy` | noop（匿名；客户端认证由 Hydra 做） |
| `/api/oauth2/device/lookup`、`/approve`、`/deny` | POST | `haruki-protected-oauth-consent` | cookie_session + header mutator |
| `/api/user/:toolbox_user_id/oauth2/authorizations[/:client_id[/consents/:consent_request_id]]` | GET / DELETE | `haruki-protected-user-get` / `haruki-protected-user`（未新增规则） | cookie_session + header mutator |
| `/oauth2/device/auth`、`/oauth2/device/verify`、`/oauth2/fallbacks/device`（Hydra） | 任意 | **无**（404） | — |
| `/oauth2/token`（Hydra 直连，给写死地址的授权码客户端） | POST | `hydra-public-oauth`（不变） | noop |

- `TestDeviceAuthRoutesToPublicProxyOnly`：`/api/oauth2/device/` 下只有 `device/auth` 可匿名访问，且只到 backend
- `TestDeviceDecisionRoutesRequireCookieSession`：lookup / approve / deny 任何方法都不能匿名命中
- `TestHydraDeviceEndpointsAreNotRouted`：Hydra 的设备路径任何方法都不命中规则；发现文档与 Hydra `/oauth2/token` 仍然路由
- `TestPerDeviceRevokeRouteCovered`：按设备撤销由现有 `haruki-protected-user` 覆盖；路由清单（`api/testdata/routes.golden`）里每条 `/api/user/:toolbox_user_id/oauth2/authorizations…` 路由都必须恰好命中一条 cookie_session 规则

**内部路由不走 Oathkeeper。** 后端另有少量只供自有服务调用的内部路由，它们没有任何 Oathkeeper 规则、不属于对外接口，公网请求得到 404；不要为它们加规则，架构测试 `TestInternalAPINotRoutedByOathkeeper` 会拒绝这类规则。它们的说明不在本仓库。

兼容期内项目仍保留组卡推荐输入数据接口：

- `GET /api/user/:toolbox_user_id/game-account/:server/:game_user_id/recommend-data`
- 查询参数 `mode=suite|mysekai`，默认 `suite`
- `mode=mysekai` 会在 suite 基础数据上合并 MySekai 推荐所需字段，方便前端 wasm 直接作为 `user_data` 使用

游戏账号数据授权（把账号数据的读或写授权给其他 Toolbox 用户）的读写规则见 [OAuth2 / OIDC 接入](oauth2-integration.zh-CN.md) §7.4。

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
- `/api/oauth2/token`（令牌端点兼容层，见下文）
- `/api/oauth2/revoke`
- `/api/oauth2/device/auth`（RFC 8628 设备授权端点，匿名；见 §10.5）
- `/api/oauth2/device/lookup`、`/api/oauth2/device/approve`、`/api/oauth2/device/deny`（设备授权的浏览器决定端点，须登录；见下文与 §10.5）
- `/api/oauth2/login`
- `/api/oauth2/login/accept`
- `/api/oauth2/login/reject`
- `/api/oauth2/consent`
- `/api/oauth2/consent/accept`
- `/api/oauth2/consent/reject`
- `/api/oauth2/authorize/consent`（legacy frontend compatibility）

这些入口的核心实现位于：

- `internal/modules/oauth2/hydra_routes.go`

`/api/oauth2/token` 是令牌端点兼容层（`hydra_token_endpoint.go`）：

- 不是表单、或 `grant_type` 不是 `urn:ietf:params:oauth:grant-type:device_code` 的请求，与 `/api/oauth2/revoke` 一样经 `handleHydraPublicProxy` 逐字节转发（原始 body、查询串、`Authorization` / `Content-Type` / `Accept`），响应原样返回。`TestTokenShimNonDeviceGrantVerbatim` 守护这一点
- 设备授权许可的请求带的是后端签发的包装设备码 `hdc_…`：兼容层用它找到流程、在本地执行 `slow_down`（早于 `interval` 的轮询不调用 Hydra），其余轮询解封出 Hydra 的 `ory_dc_…` 后转发 `POST /oauth2/token`，由 Hydra 认证客户端，再按流程状态改写 Hydra 的回答（`access_denied`、`expired_token`，或对已拒绝 / 已停用的流程不交出令牌并按 `consent_request_id` 撤销）。客户端认证之前只会本地回答参数错误、`slow_down`、未知码（`invalid_grant`）、功能关闭（`expired_token`）和 503
- 设备分支的每个响应（包括透传的 Hydra 响应）都带 `Cache-Control: no-store` 与 `Pragma: no-cache`
- Hydra 自己的 `/oauth2/token` 仍由 Oathkeeper `hydra-public-oauth` 直通，供写死 Hydra 地址的授权码客户端使用；它不认识 `hdc_…`，只会返回 `invalid_grant`

`/api/oauth2/device/lookup`、`approve`、`deny` 是 `/device` 页面调用的浏览器端点（`hydra_device_browser.go`、`hydra_device_verification.go`），与 consent 一样经会话守卫：

- 开头依次检查功能开关（关闭 403 `feature_disabled`）、`Content-Type: application/json`（否则 415）、`Origin` 在 `oauth2.device_flow.allowed_origins` 中（否则 403）、请求体 ≤ 1 KiB；错误一律放在 `updatedData.code`，`message` 是固定英文短句，从不转发 Hydra 的文本，处理函数自己从不返回 401
- lookup 用 Redis Lua 认领用户码并返回审核卡与流程句柄；未知、过期、已被他人认领的码统一 400 `invalid_code` 并计入失败预算，格式错误的 `malformed_code` 不计
- approve 先做认领者、会话、句柄与尝试次数的 CAS，再由后端在同一请求内用不跟随重定向、不保持连接的 `HydraConfig.DoWithoutRedirect` 和每次批准新建的内存 Cookie 容器走完 Hydra 的 verify → device accept → login accept → consent accept → 最后一跳（只校验不访问）；每一跳都校验流程标记 `haruki_dfl` 与 `client_id`，login `skip` 直接失败，login / consent 都以 `remember=false` 接受。consent accept 的请求体由 `buildHydraConsentAcceptBody` 构造，浏览器同意页共用它
- deny 只写 Redis，不调用 Hydra 的 reject；流程已记录 `consent_request_id` 时随后按它撤销

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

通用 login / consent 端点另有三条 Hydra 自己不做的校验（`internal/modules/oauth2/hydra_device_challenge.go`、`hydra_consent_routes.go`）：

- **拒绝设备授权模式的 challenge**：Hydra 的 login / consent 请求不带设备授权标记，只能靠 `request_url` 识别（路径以 `/oauth2/device/verify` 结尾）。查询、接受、拒绝（含旧版 `authorize/consent` 的两个分支）一律返回 403（`updatedData.code` 为 `device_flow_challenge`），不向 Hydra 发出 accept / reject。设备授权的 challenge 只由设备授权专用端点处理，即使泄漏也不能从通用端点接受或拒绝。为此 login accept / reject 会先 GET 一次 login request。consent 端点先校验请求主体：不属于当前用户的请求仍返回原有的主体不符错误，不暴露它是不是设备授权流程。
- **不转发浏览器提交的 `acr`**：login accept 只发送 `subject`、`remember`、`remember_for`，用户不能自报 id_token 里的认证强度。
- **停用的 client 不能完成 consent**：Hydra 不认识 `metadata.haruki.active`。consent accept 在主体校验之后、发出 accept 之前查 client，已停用或已删除都返回 403（`updatedData.code` 为 `client_disabled`，不区分两者），查询失败返回 503（措辞与 bearer 中间件相同）；OAuth2 webhook 扇出（`WebhookAuthorizer`）同样剔除停用的 client。已签发的 token 仍由 bearer 中间件按 client 状态拦截。

### 10.2.1 RP-Initiated Logout（2026-08-26 打通）

登出与登录同构，但**三个编排端点是匿名的**：

```text
GET  /api/oauth2/logout          查询 logout request
POST /api/oauth2/logout/accept   接受，返回 redirect_to
POST /api/oauth2/logout/reject   拒绝，Hydra 返回 204 无 body
```

匿名是刻意的。到达登出挑战的用户正在退出，Kratos 会话可能已经失效 —— 要求登录会让登出恰好
在最需要的时刻失败。challenge 本身就是凭证：Hydra 签发、一次性、指名要结束的会话。这与
consent 端点要求登录的取舍不同，因为 consent 是在授予权限，登出是在收回。

完整链条：

```text
RP 带 id_token_hint 请求 /oauth2/sessions/logout
  → Oathkeeper 放行（hydra-public-oauth 规则）
  → Hydra 生成 logout_challenge，跳转 URLS_LOGOUT
  → 前端 /logout 页面消费上面三个端点
  → accept 时前端同时注销 Kratos 会话
  → 浏览器跳回 RP 的 post_logout_redirect_uri
```

**前端在 accept 时一并注销 Kratos 会话**，这是必需的：只结束 Hydra 会话而保留 Kratos 的话，
用户下次授权会因 `skip=true` 被静默重新登录，等于没退出。

一个实现细节：`reject` 不能复用 `sendHydraAdminJSON`。Hydra 对它返回 204 空 body，而那个
helper 会尝试把响应解析成 `redirect_to`，空 body 会解析失败 —— 用户取消登出反而收到 500。

### 10.3 Token Introspection

后端对 OAuth2 bearer token 的校验不是本地解 token，而是通过 Hydra admin introspection：

- 调 Hydra introspection endpoint
- 取 `sub`
- 先尝试按 `users.kratos_identity_id` 找本地用户
- 找不到时再 fallback 到 `users.id`

实现位于：

- `internal/platform/oauth2/middleware.go`

这让 API 的 bearer token 身份与浏览器身份迁移可以在同一项目内逐步收敛。

内省的判定规则（非 active、不是 `access_token`、已过期、找不到本地用户或用户被封禁、客户端已停用或已删除都视为无效）集中在 `internal/platform/oauth2/introspect.go` 的 `IntrospectAccessToken`，bearer 中间件与后端供自有服务使用的内部令牌校验接口共用它。内部接口不是对外接口，不写进本仓库的文档。

### 10.4 管理端 OAuth Client 管理

管理员对 OAuth client 的查询、创建、更新、启停等能力，当前已经改为 Hydra-backed：

- 客户端列表
- 创建 client
- 更新 client
- 激活 / 禁用
- revoke / consent session 相关统计与操作

编辑、启停、恢复和轮换 secret 都通过 `PATCH /admin/clients/{id}`（JSON Patch，请求体是操作数组）完成，不用整体 `PUT`：

- 只改管理端负责的字段：`client_name`、`scope`、`redirect_uris`、`post_logout_redirect_uris`、`token_endpoint_auth_method`、`client_secret`、`metadata.haruki.active`，以及载荷里带了才改的 `grant_types` / `response_types` 与 `metadata.haruki.device` 的三个键（写到已存在的最深父节点）。token 寿命、`skip_consent` 和其他 metadata 保持原样
- 只用 `add` / `replace`，不用 `test`（Hydra 对 `test` 返回 500，即使值匹配）
- 公开客户端切换为保密客户端时，新 secret 和 `token_endpoint_auth_method` 写在同一个 patch 里。只改认证方式不写 secret，Hydra 也会接受，但客户端之后无法认证
- 公开客户端（`token_endpoint_auth_method=none`）拒绝轮换 secret
- `PUT /admin/clients/{id}/lifespans` 会整体替换所有寿命字段，后端不调用它

停用、撤销全部授权和删除客户端时，授权按 subject 撤销：

- Hydra v25.4.0 的授权会话撤销接口（`DELETE /admin/oauth2/auth/sessions/consent`）只接受三种参数组合：只带 `consent_request_id`；`subject`+`client`；`subject`+`all=true`。按客户端整体撤销（`client=X&all=true`，或只带 `client`）返回 400，所以撤销一个客户端的授权要对每个 subject 各调用一次 `subject`+`client`
- subject 由 `collectHydraClientAuthorizationRecords` 枚举：遍历本地用户，找出持有该客户端授权会话的用户，再撤销每个用户的全部 subject（`kratos_identity_id` 和 `users.id`），因为旧授权可能挂在回退 subject 下。有两类授权枚举不到：
  - 没有本地用户的 subject 的授权
  - 用户在该客户端下的流程全部不在 Hydra 的授权会话列表里。v25.4.0 的列表（`GET /admin/oauth2/auth/sessions/consent?subject=`）跳过 `consent_skip=TRUE` 的流程（因为记住了之前的同意而跳过同意页），也跳过 `remember_for > 0` 且已过期的流程（不论 `remember` 是否为 true）。这些流程签发的 refresh token 可能仍然有效
  - 这两类授权在客户端停用期间由 bearer 中间件的 active 检查拦截，但没有被撤销：重新启用客户端后又能使用，「撤销全部授权」也撤不到。被枚举到的用户不受影响，因为撤销接口按 `subject`+`client` 删除时不做这层过滤，隐藏的流程会一起删掉。单个用户可以用指定用户的撤销补救（请求体带 `targetUserId` 和 `"revokeTokens": false`，否则返回 501），它直接按该用户的 subject 撤销，不依赖枚举
- 查询串用 `url.Values` 构造，subject 中的 `+` 编码为 `%2B`。未编码的 `+` 会被 Hydra 解成空格，返回 204 但什么都没删
- 撤销授权会话会级联删除该会话签发的 access token 和 refresh token，这才是真正的撤销。`DELETE /admin/oauth2/tokens?client_id=` 只删 access token，refresh token 仍能换出新的 access token，只作补充清理
- 停用（`PUT /api/admin/oauth-clients/:client_id/active`，`active=false`）：先改 metadata，再逐 subject 撤销，最后删 access token。metadata 改完后一律返回 200，响应带 `revokedSubjects`（Hydra 接受撤销的 subject 数）、`failedSubjects`（撤销失败、且当前管理员有权管理的用户的 subject，以及管理员本人的 subject。这几个路由只有超级管理员能访问，所以目前不会过滤掉任何 subject；按角色层级过滤是纵深防御，防止将来放宽路由守卫后普通管理员看到超级管理员的 subject）和 `revocationComplete`（任何一步失败即为 false，包括看不到的 subject）。停用之后界面上只剩「启用」，没撤销完的授权用「撤销全部授权」补救
- 撤销全部授权（`POST /api/admin/oauth-clients/:client_id/revoke`，不指定用户）同样逐 subject 撤销。部分失败返回 200 和上述字段。枚举失败，或者枚举到了授权却没有一条确认撤销（每个持有者都至少有一个 subject 撤销失败）时，返回 500，也不再删 access token。判断依据是确认撤销的授权数，而不是 `revokedSubjects`：持有者另一个空的 subject 撤销时 Hydra 也返回 204，会被计入 `revokedSubjects`。返回 500 不保证什么都没变（持有者的一个 subject 可能已经撤销成功），但撤销是幂等的，可以直接重试
- 删除客户端时，Hydra 通过外键级联删除它的授权会话和 token，后端不再先调用撤销接口；`deletedAuthorizations` 仍按枚举到的授权数计

实现主要在：

- `internal/modules/oauth2/hydra_clients.go`
- `internal/modules/oauth2/hydra_consent.go`
- `internal/modules/adminoauth/hydra_client_handlers.go`
- `internal/modules/adminoauth/hydra_client_grant_revocation.go`
- `internal/modules/adminusers/user_oauth_handlers.go`

### 10.5 设备授权（RFC 8628）：架构

设备授权是 Toolbox OAuth 提供方的一项通用能力：任何由管理员登记、并按客户端开通设备授权许可的客户端，都能在没有浏览器的环境里以 Toolbox 用户的身份取得令牌，不绑定特定接入方，也不改变授权码客户端。第一个接入方是 Haruki-Client 的车牌收集：公开客户端 `haruki-client` 走设备授权拿到带 `station:room:write` 的令牌，以 `Authorization: Bearer` 向 Sekai Station 提交车牌；Station 在服务端向 Toolbox 校验令牌，接受任何有效且带该 scope 的令牌，不限客户端。Haruki-Client 的机器人功能使用 Haruki Cloud 的认证，与这里完全分开：设备授权的发码与展示不经机器人通道，设备标签与内省结果里没有 bot_id、QQ 号或任何 Cloud 身份，Toolbox 令牌也从不发给 Haruki Cloud。

接入方契约（轮询算法、错误表、展示与令牌保存要求、示例）只写在 [OAuth2 / OIDC 接入](oauth2-integration.zh-CN.md) §4A；本节写我们自己的实现，配置与开关见 §10.6。部署配置见 §11.3，Oathkeeper 规则见 §9.1。

总体做法是**后端中介 + 服务端代驱**：Hydra 的设备机制只由后端经内部地址调用。设备调用后端的 `POST /api/oauth2/device/auth`，拿到后端签发的包装设备码和前端短地址 `https://haruki.seiunx.com/device`；用户登录后在 `/device` 输入 8 位用户码，后端用 Redis Lua 认领并返回审核卡；用户点「允许」时，后端在同一个请求里用新建的内存 Cookie 容器经 `http://hydra:4444` 走完 verify → device accept → login accept → consent accept → 最后一跳；点「拒绝」只写 Redis。`/api/oauth2/token` 是令牌端点兼容层，非设备授权许可逐字节透传。回收器撤销「已批准却从未兑换」的授权，清理服务删除过期设备码行。

#### 10.5.1 Hydra 的缺口与后端的补偿

目标 Hydra 版本为 v25.4.0（`ORY_VERSION`），不打补丁；下表在 v26.2.0 上同样成立（源码对照）。每一项都由真实 Hydra 集成测试复核（§11.3「真实 Hydra 集成测试」），测试失败消息以「§10.5.1」开头。

| Hydra 的行为 | 后端的做法 |
| --- | --- |
| `POST /oauth2/device/auth` 只收表单；用 HTTP Basic 时表单里也必须有 `client_id`；响应多一个 `"Header":null`，带 `ory_dc_…`、不分组的用户码、`expires_in`（599）、`interval`（5） | 代理从 Basic 注入 `client_id`，重建响应去掉 `Header`，`ory_dc_` 不出服务端 |
| `verification_uri` 固定为 issuer + `/oauth2/device/verify`；`WEBFINGER_OIDC_DISCOVERY_*` 只改两份发现文档，不改 `verification_uri`、issuer 与 `authorization_endpoint` | 后端改写为前端短地址；两项发现文档覆盖都指向后端（§11.3） |
| 用户码按 LENGTH + CHARACTER_SET 生成，精确匹配、区分大小写（小写、带 `-` 或空格都 400）；输错不消耗 challenge；只做 accept 不会把码标为已用；schema 中熵预设与 `{length, character_set}` 是 `oneOf` | 服务端规范化后再提交；单次使用由后端保证；compose 只设 LENGTH + CHARACTER_SET（§12.9） |
| 从不返回 `slow_down`；从未批准的码过期后仍返回 `authorization_pending`，只有「过期后才批准」的码返回 `expired_token` | 兼容层本地实现 `slow_down`；`now ≥ exp` 时返回 `expired_token` |
| login / consent 的 reject 到不了设备：浏览器得到裸 JSON 400，设备继续 pending，同一用户码之后仍可被接受 | 拒绝只写 Redis `denied`，兼容层返回 `access_denied` |
| 批准不是单次的：同一用户码可以在多个 challenge 上 accept；Postgres 上不带 `openid` 时两边都显示成功，带 `openid` 时先完成者胜、败者 409；败者都留下孤儿授权会话 | Lua 认领 + 批准 CAS，都在任何 Hydra 调用之前 |
| 设备模式的 login / consent 请求没有设备字段和用户码；`request_url` = issuer 的 verify 地址 + 首个 verify 的查询串（`user_code` 的值被替换为 `****`），自定义参数一路保留 | 首个 verify 附流程标记 `haruki_dfl=<fid>`、绝不带 `user_code`；每一跳校验标记与 `client_id`；通用 login / consent 端点按 `request_url` 识别并拒绝设备模式（§10.2） |
| device accept（`PUT /admin/oauth2/auth/requests/device/accept`）是唯一的设备 admin 端点，没有 GET 和 reject；码错或过期 400，challenge 不存在 404，超过 `ttl.login_consent_request` 401 | 首次尝试 400 → `code_expired`，重试时 400 → `unconfirmed`；401 / 404 换新容器重来一次 |
| non-dev 下所有 Cookie 带 `Secure`，标准 cookiejar 经内部 http 会丢弃，下一跳 403 "No CSRF value…"；`ory_hydra_device_csrf` 不带客户端后缀，同一浏览器第二次 verify 会覆盖它 | 「名称 → 值」容器强制回放；每次批准新建容器 |
| 共享容器中 `remember=true` 登录后，下一流程 login `skip=true` 且带上一个用户的 subject；机密客户端即使新容器也会 consent `skip=true` | login `remember=false, remember_for=0`，`skip==true` 直接失败；忽略 consent skip |
| consent accept 完成、尚未访问 `consent_verifier` 时崩溃：授权会话列表为空，设备仍 pending，新链可成功；新容器重放旧 verifier 得 403 | 崩溃恢复与 `unconfirmed` 语义（§10.5.7） |
| `hdc_…` 直接交给 Hydra `/oauth2/token` 得到 400 `invalid_grant`；Hydra 先认证客户端再看设备码：secret 错误时即使码有效也 401 | 写死 Hydra 地址的轮询方明确失败；兼容层先转发 Hydra 认证再改写，并本地校验请求客户端 = 流程客户端 |
| 设备令牌与授权码令牌相同；内省没有授权类型字段，`ext` 即 consent accept 的 `session.access_token` | Bearer 中间件与资源接口不改；设备令牌靠 `ext.flow="device"` 区分 |
| `DELETE /admin/oauth2/auth/sessions/consent?consent_request_id=X`（单独使用）撤销该链 AT + RT（含刷新所得）；不存在的 ID 也返回 204；兑换前撤销会级联删除设备码行，之后轮询 `invalid_grant` | 按设备撤销前先确认 ID 属于调用者；回收器与拒绝用它干净地取消 |
| Hydra 不看 `metadata.haruki.active`，停用的客户端照样能发码并完成设备流程 | device/auth、lookup、approve、兼容层四处检查 active |
| 每次 device/auth 写一行（约 1.2 KB），单个匿名客户端可打到约 465 req/s；只有成功签发才删行，janitor 不清理；`expires_at` 无索引，按 UTC 写入 `timestamp without time zone` | 在 Hydra 之前限流（§10.5.10），另加清理服务（§11.3） |
| pairwise subject 的设备客户端没有 `redirect_uri` / `sector_identifier_uri` 时在 consent 一跳 400 | 后端从不设置 `subject_type` |

#### 10.5.2 参与方、不变量与端到端步骤

| 角色 | 地址 | 说明 |
| --- | --- | --- |
| API（后端 + Hydra issuer） | `https://toolbox-api-direct.haruki.seiunx.com` | 链路 边缘 WAF → Oathkeeper → `backend:16666` / `hydra:4444`（仓库 compose 的服务名）；OAuth 与设备流程一律不走 `toolbox-api-cdn` |
| 前端 SPA | `https://haruki.seiunx.com` | `/device`、`/device/done`；`/device` 的请求固定发往 direct 端点 |
| Hydra 内部地址 | public `http://hydra:4444`，admin `http://hydra:4445` | 只有后端访问 |
| 设备（如 Haruki-Client） | 运行者的主机 | 只调用 device/auth、token、revoke 与 `/api/oauth2/user/profile` |
| Sekai Station 后端 | 独立进程 | 只在服务端校验令牌，不经对外接口 |

不变量：浏览器**从不**访问 Hydra 主机；设备**只**拿到 `hdc_…`，`ory_dc_…` 密封存放在 Redis；代驱链的 Cookie 容器每次批准新建、只在内存、处理函数返回即丢弃，从不持久化或写日志；原始用户码、`hdc`、`ory_dc_`、流程句柄从不出现在 Redis 键名和日志中；Hydra 的 `/oauth2/device/auth`、`/oauth2/device/verify`、`/oauth2/fallbacks/device` 在 Oathkeeper 上返回 404（`TestHydraDeviceEndpointsAreNotRouted`）。

API = `https://toolbox-api-direct.haruki.seiunx.com`，FE = `https://haruki.seiunx.com`。日志与代码注释里的 H1–H15 指下表：

| 步骤 | 调用方 → 被调方 | 方法与路径 | 作用 / 预期 |
| --- | --- | --- | --- |
| H1 | 设备 → 后端 | `POST API/api/oauth2/device/auth`，表单 `client_id`、`scope`、`device_label?` | 发起流程（§10.5.3） |
| H2 | 后端 → Hydra admin | `GET /admin/clients/{client_id}`（device/auth 与兼容层进程内缓存 5 s；浏览器端点的刷新不缓存） | 存在、active、设备授权许可、允许名单、scope 策略、发码上限 |
| H3 | 后端 → Hydra public | `POST /oauth2/device/auth`，只带 `client_id` 与 `scope`，转发原 `Authorization` | Hydra 认证客户端，返回 `ory_dc_…` 与用户码 |
| H4 | 后端 → Redis → 设备 | `deviceFlowCreateScript`；200 | `{device_code: hdc_…, user_code: "BCDF-GHJK", verification_uri: FE/device, …}`，状态 `pending` |
| H5 | 设备 → 用户 | 只在本机展示用户码与验证地址，同时开始轮询 | — |
| H6 | 浏览器 → 前端 | `GET FE/device?user_code=…` | 未登录时页面内显示登录卡片；读取 `user_code` 后立即清掉查询串，不自动提交 |
| H7 | 浏览器 → 后端 | `POST API/api/oauth2/device/lookup` `{"userCode"}` | 规范化、限流、认领（`pending → claimed`，租约 300 s）、再做一次 H2，返回审核卡与 `flowHandle` |
| H8 | 浏览器 → 后端 | `POST API/api/oauth2/device/approve` `{flowHandle,userCode,label,acknowledged:true}` | 同步执行 H9a–H9k，整链 15 s |
| H9a | 后端 → Redis | `deviceFlowBeginApproveScript` | `claimed → approving`（租约 30 s、`anonce`、`att+1`） |
| H9b | 后端 → Hydra public | `GET /oauth2/device/verify?haruki_dfl={fid}`（无 Cookie，**绝不带** `user_code`） | 302 `FE/device?device_challenge=X`；`ory_hydra_device_csrf` 进容器 |
| H9c | 后端 → Hydra admin | `PUT …/requests/device/accept?device_challenge=X` `{"user_code":"BCDFGHJK"}` | `redirect_to` 含 `client_id`、`device_verifier`、`haruki_dfl`，**不含** `user_code` |
| H9d | 后端 → Hydra public | `GET` verify（H9c 的查询串），带容器 | 302 `FE/oauth2/login?login_challenge=L` |
| H9e | 后端 → Hydra admin | `GET …/requests/login?login_challenge=L` | `skip==false`、客户端、标记、scope、audience 为空 |
| H9f | 后端 → Hydra admin | `PUT …/login/accept` `{"subject":S,"remember":false,"remember_for":0}` | `redirect_to` 含 `login_verifier`、`client_id`、`haruki_dfl` |
| H9g | 后端 → Hydra public | `GET` 改写后的 `redirect_to`，带容器 | 302 `FE/oauth2/consent?consent_challenge=K` |
| H9h | 后端 → Hydra admin + Redis | `GET …/consent?consent_challenge=K`；`deviceFlowRecordConsentScript` | 在 consent accept **之前**写入 `crid`、`sub` 并加入未兑换集合 |
| H9i | 后端 → Hydra admin | `PUT …/consent/accept` | `redirect_to` 含 `consent_verifier`、`client_id`、`haruki_dfl` |
| H9j | 后端 → Hydra public | `GET` 改写后的 `redirect_to`，带容器 | 预期 302 `FE/device/done?client_id=C`，只校验、不访问 |
| H9k | 后端 → Redis / 审计 | `deviceFlowFinishApproveScript`（→ `approved`），审计 `user.oauth.device.approve`，丢弃容器 | 200 `{status:"approved",…}` |
| H10 | 浏览器 → 后端 | `POST API/api/oauth2/device/deny` `{flowHandle, reason}` | `deviceFlowDenyScript` → `denied`；不调用 Hydra reject；有 `crid` 时随后按它撤销 |
| H11 | 设备 → 后端 | `POST API/api/oauth2/token`，`grant_type=…device_code&device_code=hdc_…` | `deviceFlowPollScript`；过早则本地 `slow_down`，否则 H12 |
| H12 | 后端 → Hydra public | `POST /oauth2/token`，`device_code=ory_dc_…`、`client_id`，转发原 `Authorization` | 按流程状态改写（§10.5.4）；`deviceFlowSettleScript` 落定 |
| H13 | 设备 → 后端 | `GET API/api/oauth2/user/profile`（`user:read`） | 设备显示「已授权为 <name>」 |
| H14 | 回收器 → Hydra admin | `DELETE /admin/oauth2/auth/sessions/consent?consent_request_id=…` | 未兑换集合中到 `exp + 60 s` 仍未签发的流程（§10.5.9） |
| H15 | 清理服务 → Postgres | 分批 `DELETE FROM hydra_oauth2_device_auth_codes …` | 每小时一次（§11.3） |

主要取舍（已定案）：

- **服务端代驱，而不是让浏览器跳到 Hydra**：只有驱动方能把 login / consent 和用户码对应起来；同时避开 device CSRF Cookie 被覆盖、API 主机上的裸 JSON 页、应用内 webview 的 Cookie 问题、verifier 重放与登录会话 skip。
- **包装设备码**：交给设备的是 `hdc_` + base64url(32 随机字节)，Hydra 的 `ory_dc_` 以 AES-256-GCM 密封在 Redis。Hydra 换不了 `hdc_`，所有轮询必经兼容层；拿到 Redis 转储也换不出令牌。
- **上线当天**就把发现文档的 `token_endpoint` 覆盖为后端，否则按发现文档配置的库会把 `hdc_` 发给 Hydra。非设备授权逐字节透传，授权码 RP 不受影响；Hydra 直连的 `/oauth2/token` 仍然路由。
- **统一 `invalid_code`**：未知、认领前已过期、被其他账号认领或处理的码一律 400 `invalid_code` 并计入失败预算，文案点明「已被其他账号使用」；猜码者拿不到额外信号。
- **拒绝只写 Redis**，不调用 Hydra reject（它什么都不保存）；最后一跳结果未知时 Hydra 可能已完成同意，所以有 `crid` 就按它撤销。
- **不按 IP 限流**：`c.IP()` 是 Oathkeeper 的地址，边缘 WAF 也不支持按路径规则；预算全部由后端的全局、按用户、按客户端计数承担。
- **允许的客户端**：公共与机密都可以，须管理员创建并按客户端开通设备授权许可；另有可选的客户端允许名单（默认留空 = 所有持有许可的客户端）。`station:room:write` 是普通 scope，不需要额外许可。
- **撤销全部仍按用户枚举**，不建设备授权登记表；发起人提示、写操作的升级认证都不在 v1。

#### 10.5.3 设备授权端点 `POST /api/oauth2/device/auth`

`handleHydraDeviceAuthorization`（`hydra_device_authorization.go`），Oathkeeper `haruki-public-oauth-proxy`（noop）。处理顺序固定：

1. 解析：`application/x-www-form-urlencoded`（`mime.ParseMediaType`，允许 `charset`）、body ≤ 4096 字节、`scope` / `device_label` / `client_id` 不得重复、不得出现 `audience`；客户端 = Basic 用户名（base64 解码后两部分各自 URL 反转义），否则表单 `client_id`；两者都在时须相等。其余字段（含 `haruki_*`）丢弃。都不访问 Redis 或 Hydra。
2. 功能闸门 `cfg.Active(ctx)`：明确关闭 ⇒ 400 `unauthorized_client`；运行时配置读取出错（且无 1 s 内缓存值）⇒ 503。
3. H2，结果按 `client_id` 进程内缓存 5 s（存在与 404 都缓存）。404 ⇒ 计数 `auth-attempt:unknown-client`（只告警）并 401 `invalid_client`；Hydra admin 不可达 ⇒ 503。
4. `auth-attempt:client:{hx}` 计数，**只告警不拒绝**：机密客户端的 secret 要到 H3 才由 Hydra 校验，按客户端拒绝会让带错误 secret 的匿名洪泛挡住真正的客户端。
5. 允许名单、active、设备授权许可（任一不满足 ⇒ `unauthorized_client`，不区分原因）→ scope 策略（§10.5.11，⇒ `invalid_scope`）→ `deviceRateReserveScript` 预占按客户端类型分开的全局池 `auth-issued:global:{public|confidential}` 与 `auth-issued:client:{hx}`（⇒ 429 `temporarily_unavailable` + `Retry-After`）。
6. H3：只发 `client_id`、`scope`，转发原 `Authorization`。网络错误 ⇒ 释放预占、503；Hydra 非 200 ⇒ **释放两项预占**，原样透传状态码与 RFC JSON（含 `WWW-Authenticate`）。
7. 自检：`normalizeDeviceUserCode(user_code)` 须成功且等于原值，否则 500 并记 `charset_mismatch`；`expires_in` 不得超过 `user_code_ttl` 5 s 以上，否则 500 并记 `ttl_mismatch`（Hydra 与后端配置漂移时失败关闭）。
8. 生成 `fid`、`hdc`、密封值，`deviceFlowCreateScript`（`UC_COLLISION` ⇒ 500；Redis 失败 ⇒ 503，已产生的 Hydra 行交给清理服务）→ 200，`interval = max(Hydra interval, min_poll_interval_seconds)`。

面向设备的两个端点（device/auth 与兼容层的设备分支）用 RFC 6749 / 8628 错误体 `{"error","error_description"}`（`slow_down` 另带 `interval`），每个响应（含透传的 Hydra 响应）都带 `Cache-Control: no-store` 与 `Pragma: no-cache`。后端自产错误只用 `invalid_request`、`invalid_client`、`unauthorized_client`、`invalid_scope`、`invalid_grant`、`slow_down`、`access_denied`、`expired_token`、`temporarily_unavailable`、`server_error`；限流用 429 `temporarily_unavailable`，不用 `slow_down`。对接入方公开的错误表见 oauth2-integration §4A.2。

#### 10.5.4 令牌端点兼容层 `POST /api/oauth2/token`

`handleHydraTokenEndpoint`（`hydra_token_endpoint.go`）。不是表单、或 `grant_type` 不是设备授权许可的请求经 `handleHydraPublicProxy` 逐字节转发（§10.1）。设备分支：请求客户端 = Basic 用户名，否则表单 `client_id`；顺序为参数检查 → `hdc_` 格式与 `dc` 索引 → 功能闸门 → `deviceFlowPollScript`（传入请求客户端）→ 已批准类（`approving` / `approved` / `unconfirmed`）**预取**客户端启用状态（只在 Hydra 认证通过后使用）→ 解封 → H12 → `deviceFlowSettleScript` → 按下表回答。

| 条件 | 响应 | 状态变化 |
| --- | --- | --- |
| 缺客户端、缺 `device_code`、参数重复、表单 `client_id` ≠ Basic 用户名、Basic 格式错误 | 400 `invalid_request` | — |
| `device_code` 不是 `hdc_` + 32 字节；查不到 `dc` 索引；请求客户端 ≠ 流程 `cid` | 400 `invalid_grant` | — |
| 启动开关或运行时总开关为关 | 400 `expired_token` | — |
| 运行时配置读取出错；Redis 不可用；已批准类预取客户端失败；Hydra 不可达 | 503 `temporarily_unavailable` | — |
| 早到（`now − lpoll < ivl·1000 − 1000 ms`） | 400 `slow_down`，附 `interval` | `ivl=min(ivl+5, 60)`，`sdn+1`；`pending` / `claimed` 时 `sdn>30` ⇒ `failed` |
| Hydra 401 或其他非 200 / 非 400 响应 | 透传 | — |
| Hydra 200，已批准类，客户端启用 | 透传令牌，审计 `user.oauth.device.token_issued` | → `issued`，移出集合，flow 与 `dc` 300 s 后过期 |
| Hydra 200，已批准类，客户端已停用 | 不交出令牌，按 `crid` 撤销（级联作废刚签发的 AT / RT）；400 `access_denied` | → `denied`；撤销成功才移出集合 |
| Hydra 200，`st∈{pending,claimed,denied,failed,expired}` | 不交出令牌，按 `crid` 撤销；`denied` ⇒ `access_denied`，其余 ⇒ `expired_token`；记 `token_after_terminal` | 不变；撤销成功才移出集合 |
| Hydra 200，flow 已不存在 | 不交出令牌，按轮询时读到的 `crid` 撤销；`expired_token` | — |
| `authorization_pending` 且 `denied` / `failed` | `access_denied` / `expired_token` | — |
| `authorization_pending` 且（`expired` 或 `now ≥ exp`） | `expired_token` | 非终态 → `expired`（有 `crid` 的保留在集合） |
| `authorization_pending`，其他 | 透传 | — |
| Hydra `expired_token` | `expired_token` | `pending` / `claimed` → `expired`；已批准类 → `expired` 但**不移出集合**，由回收器撤销 |
| `invalid_grant` 且 `denied`；或已批准类且客户端已停用 | `access_denied` | 后者 → `denied`，移出集合 |
| `invalid_grant` 且 `st=expired`（含回收器撤销之后） | `expired_token` | — |
| `invalid_grant` 且已批准类（客户端启用） | 透传 | → `expired`，移出集合（令牌可能已被一次结算失败的轮询领走，或授权已被撤销） |
| 其他 | 透传 | — |

- 早到判定只看 `lpoll`：首次轮询一定转发；每次轮询（转发或 `slow_down`）都把 `lpoll` 更新为 now。
- 客户端认证之前，兼容层只会本地回答：参数错误、`slow_down`、未知码或客户端不符的 `invalid_grant`、功能关闭的 `expired_token`、503。已停用、已拒绝、已过期都在 Hydra 认证之后才透露。
- 「不交出令牌」一律用 `RevokeHydraConsentSessionByID(crid)`；`crid` 为空在设计上不会发生，万一发生照样不交出并记 error 日志。
- 结算失败（Hydra 已 200、Redis 写出错）短退避重试 2 次，仍失败时：已批准类且客户端启用就照常交出令牌并记 `settle_failed`（流程仍在未兑换集合里，回收器会在 `exp + 60 s` 撤销这些令牌，设备需要重新绑定，这是接受的残余代价）；否则不交出令牌并按 `crid` 撤销。Hydra 不是 200 时结算失败一律 503。
- 签发审计用 `WriteSystemLog`（actor 匿名，target = 认领者），不用 `WriteUserAuditLog`（会把 actor 记成目标用户本人）。

#### 10.5.5 浏览器端点：lookup / approve / deny

`hydra_device_browser.go`，Oathkeeper `haruki-protected-oauth-consent`（cookie_session）；`/device` 页面消费它们。共同规则见 §10.1；另外：

- **lookup** 返回的审核卡：`flowHandle`（`dfh_…`，只在 JSON 体中传递，前端只放内存）、`userCode`（`XXXX-XXXX`）、`client{clientId, clientName, clientType, firstParty, initiatorVerified}`、`scopes[{scope, risk}]`、`deviceLabel`、`requestedAt` / `expiresAt`（UTC，RFC 3339）、`account{userId, name}`、`writeWarning`。风险分级：`openid`、`profile` → `identity`；`offline_access` → `offline`；`user:read`、`bindings:read`、`game-data:read` → `read`；`game-data:write`、`station:room:write` 以及表里没有的 scope → `write`（任一 `write` 时 `writeWarning=true`）。`firstParty` = 设备策略 `firstParty` ∧ 机密客户端（公共客户端的 `client_id` 任何人都能冒用，永远不报「官方」）；`initiatorVerified` = 机密客户端。
- **lookup 顺序**：`lookup:user` 计数（429）→ 规范化（失败 ⇒ `malformed_code`，不计预算）→ 一次预占 `lookup-fail:user`、`lookup-fail:user-day`、`lookup-fail:global`（任一达上限 ⇒ 429 且不消耗）→ **一次** `deviceFlowClaimScript`（脚本内读 `uc` 得 fid 再读写流程，「不存在」与「被他人认领」的响应时间无法区分）→ `CLAIMED_NEW` / `CLAIMED_RENEWED` 释放预占、H2 刷新（客户端不可用 ⇒ 流程 `failed`，403 `client_unavailable`）、审计 `user.oauth.device.claim`；`MISSING` / `EXPIRED` / `TAKEN` 保留预占，400 `invalid_code`（`TAKEN` 另记 `lookup_conflict`）；`HANDLED_OWN` 409 `already_handled`；`IN_PROGRESS_OWN` 409 `flow_conflict`；`EXPIRED_OWN` 410 `code_expired`。
- **approve 顺序**：`acknowledged === true`（否则 `ack_required`）→ `decision:user-day` 计数 → `fh` 索引（缺失 ⇒ `flow_conflict`）→ 规范化 `userCode` 并与流程的 `uch` 常数时间比较（不等 ⇒ `flow_conflict`）→ 清洗 `label` → H2 刷新 → `deviceFlowBeginApproveScript`（`HANDLE_MISMATCH` / `IN_PROGRESS` ⇒ `flow_conflict`，`SESSION_CHANGED` ⇒ `session_changed`，`NOT_CLAIMER` / `HANDLED` ⇒ `already_handled`，`TOO_LATE` / `EXPIRED` ⇒ `code_expired`，`MAX_ATTEMPTS` ⇒ 502 `approval_failed`、`retryable:false`）→ 代驱链（§10.5.7）→ 200 `{status:"approved", clientName, consentRequestId, accountName}`，或 202 `{status:"unconfirmed"}`（最后一跳结果未知；Hydra 实际已完成时设备仍会拿到令牌）。
- **标签**：用户填写的非空 `label`（`label_source=user`）> 设备自报 `device_label`（`device`）> 默认 `"{clientName} · {YYYY-MM-DD}"`（Asia/Shanghai 日期，`default`）；非空但清洗后为空 ⇒ `invalid_request`。最终标签写进 consent `context.haruki.label` 与 `session.access_token.device_label`，批准后不能改。
- **deny**：`reason` 为 `user_denied`（缺省）或 `not_initiated_by_me`（另记 `phishing_signal`），其他值 `invalid_request`；`decision:user-day` 计数 → `fh` 索引 → `deviceFlowDenyScript`（成功返回 `crid`）→ `crid` 非空则按它撤销（期望 204）→ 审计 `user.oauth.device.deny`。允许拒绝的状态是 `claimed` 与批准租约已过期的 `approving`：后者正是「后端在最后一跳后崩溃」或「最后一跳结果未知后用户改点拒绝」，Hydra 可能已完成同意，所以写入 `denied` 后立即撤销，Hydra 级联删除设备码行，之后轮询得到 `invalid_grant` 并被翻译为 `access_denied`。撤销失败仍返回 200，由兼容层和回收器兜底。

| `updatedData.code` | HTTP | 条件 | 计入 lookup 失败预算 |
| --- | --- | --- | --- |
| （无） | 401 / 404 | 401：Oathkeeper cookie_session 失败（处理函数自己从不返回 401）；404：网关或后端没有该路由 | — |
| `feature_disabled` | 403 | 启动开关或运行时总开关为关 | — |
| `unsupported_media_type` | 415 | Content-Type 不是 `application/json` | — |
| `origin_rejected` | 403 | 缺 `Origin` 或不在 `allowed_origins` | — |
| `invalid_request` | 400 | JSON 错、缺字段、label 只有控制字符、未知 deny reason、body 超过 1 KiB | 否 |
| `malformed_code` | 400 | 规范化失败 | 否 |
| `invalid_code` | 400 | 未知、认领前已过期、已被其他账号认领或处理 | **是** |
| `rate_limited` | 429（`Retry-After` 与 `updatedData.retryAfter`） | 任一浏览器侧限制 | — |
| `code_expired` | 410 | 本人的流程已过期、批准时剩余不足 30 s、首次 device accept 收到 Hydra 400 | 否 |
| `already_handled` | 409 | 本人的流程已是 `approved` / `unconfirmed` / `issued` / `denied` / `failed` | 否 |
| `flow_conflict` | 409 | 流程句柄不匹配、批准已在进行、`userCode` 与流程不符 | 否 |
| `session_changed` | 409 | 认领发生在另一个 Kratos 会话 | 否 |
| `ack_required` | 400 | `acknowledged !== true` | 否 |
| `client_unavailable` | 403 | 客户端已删除 / 停用 / 被移除设备授权许可 / 不在允许名单 / 不再符合 scope 策略 | 否 |
| `approval_failed` | 502（`retryable`） | Hydra 链路确定失败；可重试时流程回到 `claimed` | 否 |
| `temporarily_unavailable` | 503 | Redis 或 Hydra 不可达；运行时配置读取出错 | 否 |

Oathkeeper CORS 的 `exposed_headers` 只有 `Content-Type`，页面读不到 `Retry-After`，所以秒数同时放在 `updatedData.retryAfter`。

#### 10.5.6 按设备列出与撤销授权

- `GET /api/user/:toolbox_user_id/oauth2/authorizations` 与管理员只读镜像 `GET /api/admin/users/:target_user_id/oauth-authorizations` 的每一项带 `flowType`（`device` / `browser`）与 `deviceLabel`（浏览器授权为空串）。`flowType="device"` 当且仅当 consent `context.haruki.flow=="device"`，或 `request_url` 的 path 以 `/oauth2/device/verify` 结尾。
- `DELETE /api/user/:toolbox_user_id/oauth2/authorizations/:client_id/consents/:consent_request_id`（`internal/modules/useroauth/oauthmanage.go`）：先列出调用者自己全部 subject 的授权会话，`(consent_request_id, client_id)` 没有匹配就 404 `authorization_not_found` 且**不调用 Hydra**（Hydra 对不存在的 ID 也返回 204，预检是唯一的越权防线）；否则只带 `consent_request_id` 撤销，200 `{revoked:true}`，Hydra 出错 502 `revoke_failed`；审计 `user.oauth.authorization.revoke_consent`。按应用撤销 `DELETE …/authorizations/:client_id` 不变（subject + client），同时删掉该客户端的浏览器与设备授权。

#### 10.5.7 服务端代驱链

`hydra_device_verification.go`：

- **HTTP 客户端**：`HydraConfig.DoWithoutRedirect`（不跟随重定向、无 Jar）；public 侧响应体限 64 KiB。不发送 `Host` / `X-Forwarded-*`。
- **Cookie 容器** `deviceCookieJar`（name → value）：BeginApprove **之后**创建，每次批准一个；吸收每个 Hydra public 响应的 Cookie，忽略 `Secure` / `Domain` / `Path` / `SameSite`，过期即删；只在请求 Hydra 内部 verify 地址时合并成一个 `Cookie` 头发送。
- **唯一的 URL 改写**：源等于 issuer 源、path 等于 issuer path + `/oauth2/device/verify` 的绝对 URL，改写为内部 `PublicEndpoint("/oauth2/device/verify")` + 原查询串。指向前端的 `Location` 只解析、不请求，源须等于前端源：H9b `/device` + `device_challenge`；H9d `/oauth2/login` + `login_challenge`；H9g `/oauth2/consent` + `consent_challenge`；H9j `/device/done` + `client_id == cid`。其他任何情况（他源、他路径、缺参数、带 `prompt`、出现 `user_code`、该 302 却不是）都失败关闭，记 `chain_error stage=<H9x> reason=<枚举>`（不含 URL）。
- **流程标记**：H9c / H9f / H9i 的每个 `redirect_to` 与 H9e / H9h 的 `request_url` 都须 `haruki_dfl==fid` 且 `client_id==cid`。
- **时间预算**：每次尝试最多 4 次 public GET（另加 1 次 H9j 重试）和 6 次 admin 调用；整链 `approval_timeout_seconds`（15 s），小于 30 s 的批准租约；H9j 的重试与撤销各用独立短超时。

| 跳 | 必须满足 | 失败时 |
| --- | --- | --- |
| H2 刷新 | 客户端存在、启用、仍有设备授权许可、仍在允许名单、流程 scope 仍符合策略 | `failed`，403 `client_unavailable` |
| H9b | 只带 `haruki_dfl`；302 → 前端 `/device?device_challenge=…`；容器里有 `ory_hydra_device_csrf` | 网络 / 5xx：回退，502 可重试 |
| H9c | 200；`redirect_to` = issuer verify 地址，含 `device_verifier`、标记与 `client_id`，不含 `user_code` | 400：`att=1` ⇒ `failed`、410 `code_expired`；`att>1` ⇒ `unconfirmed`、202。401 / 404 ⇒ 换新容器从 H9b 重来一次，仍失败回退、502 可重试；从不对外暴露 401 |
| H9d | 302 → 前端 `/oauth2/login?login_challenge=L` | 403（容器缺陷）⇒ 回退、502 可重试 + error 日志 |
| H9e | `skip == false`；客户端、`request_url`、标记一致；`requested_scope` 集合 == 流程 scope；audience 为空 | `skip==true` ⇒ `failed` + `login_skip_unexpected`（绝不 accept、不回显 subject）；其他不匹配 ⇒ `failed`、502 不可重试 |
| H9f | body 恰为 `{"subject":CurrentHydraSubject,"remember":false,"remember_for":0}`（无 `acr`、无 `context`）；`redirect_to` 含 `login_verifier`、标记与 `client_id`，无 `prompt` | 回退、502 可重试（不匹配 ⇒ `failed`） |
| H9g | 302 → 前端 `/oauth2/consent?consent_challenge=K` | 回退、502 可重试 |
| H9h | `subject == S`；客户端、`request_url`、scope、audience 同 H9e；忽略 `skip`；在 H9i **之前** RecordConsent | 不匹配 ⇒ `failed` |
| H9i | `redirect_to` 含 `consent_verifier`、标记与 `client_id` | 回退、502 可重试 |
| H9j | 302 → 前端 `/device/done?client_id=cid` ⇒ 成功 | **首次结果未知**（超时 / 传输错误）⇒ 同容器、短超时重试一次：302 即成功；任何非 302（实测 403 "The consent verifier has already been used"，说明首跳已在 Hydra 完成）或仍未知 ⇒ `unconfirmed`、202。**首次即明确非 302** ⇒ 先按 `crid` 撤销（Hydra 先写授权会话、再在另一事务里更新设备码，5xx 时可能已留下授权会话），204 后才回退为 `claimed` 并移出集合、502 可重试；撤销失败 ⇒ `unconfirmed`、202 |

H9c–H9i 任一处 admin 5xx 或网络错误 ⇒ 回退、502 可重试（H9j 之前放弃的挑战不会持久化；H9h 之后回退时一并移出集合）。3 次尝试后流程置 `failed`。

consent accept 的请求体由 `buildHydraConsentAcceptBody` 构造，浏览器同意页共用它（浏览器路径的 `session.access_token` 仍只有 `{"uid":…}` 且不带 `context`）。设备路径：

```json
{"grant_scope":["<流程 scope>"],"grant_access_token_audience":[],"remember":false,"remember_for":0,
 "session":{"access_token":{"uid":"<users.id>","flow":"device","device_flow_id":"<fid>","device_label":"<label>"},
            "id_token":{"…":"按授予的 scope 构造"}},
 "context":{"haruki":{"flow":"device","device_flow_id":"<fid>","label":"<label>","label_source":"user|device|default","approved_via":"device-bff/v1"}}}
```

`grant_scope` 里永远没有 `email`，id_token 不含 email 声明。崩溃恢复：后端在链中途崩溃时流程停在 `approving`（过了 H9h 则已在未兑换集合中），批准租约过后认领者可以用新容器走新链（Hydra 不持久化半途的同意，旧 verifier 无法重放）；崩溃在最后一跳之后时，重试的 device accept 收到 Hydra 400，按 `att>1` 进入 `unconfirmed`；认领者改点拒绝则按 `crid` 撤销；都不做时设备照常兑换，或回收器在 `exp + 60 s` 撤销。

#### 10.5.8 标识符、Redis 键与状态机

| 名称 | 格式（`crypto/rand`） | 谁能看到 | 用途 |
| --- | --- | --- | --- |
| `fid` | 32 位小写 hex | 服务端；Hydra `request_url`（`haruki_dfl`）；consent context；内省 `ext.device_flow_id`；审计 `deviceFlowID` | 流程主键 + 流程标记 |
| `hdc` | `hdc_` + base64url-nopad(32 字节) | 仅设备 | 轮询用的秘密；密封 `ory_dc_` 的密钥材料 |
| `flowHandle` | `dfh_` + base64url-nopad(32 字节) | 认领者浏览器（只在 JSON 体中） | approve / deny 的认领凭证 |
| `anonce` | 32 位 hex | 仅服务端 | 保护某一次批准尝试的 Record / Finish / Revert |

密封：`key = HKDF-SHA256(raw32, salt=fid, info="haruki/oauth2-device/dc-wrap/v1")`；AES-256-GCM，12 字节随机 nonce，AAD = `"v1|" + fid + "|" + cid`。Redis 里只有密文 `wdc`，没有 `hdc` 解不开。

`hx(d, v)` = hex(HMAC-SHA256(`user_system.session_sign_token`, d + "\x00" + v))，**不 trim、不转小写**（`hdc` 区分大小写）。设备流程启用时启动校验要求该密钥非空且 ≥ 16 字符，不接受无密钥的 SHA-256 退化（否则拿到 Redis 转储即可离线穷举 2.56×10^10 的码空间）。更换它会让进行中的设备流程（以及邮箱验证码、重置链接、QQ 验证码）失效。

| Key | 类型 | 值 | TTL |
| --- | --- | --- | --- |
| `haruki:oauth2-device:flow:{fid}` | HASH | 字段见下 | `expires_in + record_grace_seconds`（1800 s）；`issued` 后 300 s |
| `haruki:oauth2-device:dc:{hx("dc",hdc)}` | STRING | fid | 同 flow |
| `haruki:oauth2-device:uc:{hx("uc",规范化用户码)}` | STRING（SET NX） | fid | `expires_in` |
| `haruki:oauth2-device:fh:{hx("fh",flowHandle)}` | STRING | fid | `min(300 s, exp−now)`，续租时重置 |
| `haruki:oauth2-device:unredeemed` | ZSET | 成员 fid，score = `exp`（ms） | 无（签发、撤销成功或回收时移除） |
| `haruki:oauth2-device:crid:{fid}` | STRING | crid | flow 剩余 TTL + 7 天；回收器每次撤销失败续到 7 天；fid 离开未兑换集合时同时删除 |
| `haruki:rate-limit:oauth2-device:…` | 计数器 | §10.5.10 的 10 个计数器；客户端维度后缀 `{hx("cid",cid)}`，用户维度 `{hx("uid",users.id)}` | 带 `-day` 的 86400 s，其余 600 s |

流程 HASH 字段（名称固定）：`v`（结构版本 `"1"`）、`cid`、`ctype`（`public|confidential`）、`scope`（去重排序后空格连接）、`dlb`（清洗后的 device_label）、`uch`（`hx("uc", 用户码)`）、`wdc`（密封的 `ory_dc_`）、`crt` / `exp`（ms）、`ivl` / `lpoll` / `sdn`、`st`、`cby` / `csh` / `cuntil` / `hnd`（认领者 `users.id`、会话哈希、认领租约、`hx("fh", flowHandle)`）、`att` / `auntil` / `anonce`、`sub` / `crid`（H9h 写入）、`lbl` / `lsrc` / `dres` / `ist`。

状态：`pending` · `claimed` · `approving` · `approved` · `unconfirmed` · `issued` · `denied` · `failed` · `expired`；后四个为终态。每个脚本开头先做惰性过期（`pending` / `claimed` 且 `now ≥ exp` ⇒ `expired`）。

| 从 | 事件 | 到 | 守卫 |
| --- | --- | --- | --- |
| — | device/auth 成功 | `pending` | — |
| `pending` | 用户 U lookup | `claimed`（`cuntil=min(now+300 s, exp)`） | `now < exp` |
| `claimed` | 认领者再次 lookup（续租、签发新句柄）；他人 lookup（接管） | `claimed` | 接管须 `cuntil < now` |
| `claimed`（或 `auntil < now` 的 `approving`） | BeginApprove | `approving`（`auntil=now+30 s`、`anonce`、`att+1`） | 认领者、句柄、会话匹配 ∧ `exp−now ≥ 30 s` ∧ `att < 3` |
| `approving` | RecordConsent | `approving`（写 `crid`、`sub`，加入集合） | `anonce` |
| `approving` | 代驱链成功 | `approved` | `anonce` |
| `approving` | H9j 之前确定的可重试失败；或 H9j 首次即明确非 302 且撤销 204 | `claimed`（`att ≥ 3` ⇒ `failed`） | `anonce` |
| `approving` | 校验不匹配 / login skip / 客户端不可用 / `att=1` 时 device accept 400 | `failed` | `anonce` |
| `approving` | H9j 结果未知且重试仍非 302 或仍未知；H9j 明确非 302 但撤销失败；`att>1` 时 device accept 400 | `unconfirmed` | `anonce` |
| `claimed`（或租约过期的 `approving`） | 认领者拒绝 | `denied`；有 `crid` 时随后撤销，成功则移出集合 | 句柄、会话匹配 |
| `pending` / `claimed` | `now ≥ exp`；`slow_down` 超过 30 次；H2 刷新发现客户端不可用 | `expired` / `failed` / `failed` | — |
| 已批准类 | 兼容层看到的 Hydra 结果 | 见 §10.5.4 | — |
| 集合中任一非 `issued` 状态 | 回收器在 `exp + 60 s` 处理 | 按 `crid` 撤销；非终态 → `expired`，`denied` / `failed` 不变 | 抢到 ZREM ∧ `crid` 非空 |

**未兑换集合**的成员资格与状态分开管理：RecordConsent 时加入；只在五种情况移出——签发成功、Hydra 对已批准流程返回 `invalid_grant`、按 `crid` 撤销成功、H9j 之前的回退（Hydra 侧没有完成的同意）、回收器认领。带 `crid` 的 `denied` / `failed` / `expired` 在撤销成功前一直留在集合里。

流程定位：lookup 用 `uc` 索引，approve / deny 用 `fh` 索引，令牌请求用 `dc` 索引。

Lua 脚本（`hydra_device_store.go`、`hydra_device_decision_store.go`，`now` 以 ARGV 毫秒传入）：`deviceFlowCreateScript`（`OK` / `UC_COLLISION`）、`deviceFlowClaimScript`（`CLAIMED_NEW`、`CLAIMED_RENEWED`、`TAKEN`、`EXPIRED`、`EXPIRED_OWN`、`HANDLED_OWN`、`IN_PROGRESS_OWN`、`MISSING`）、`deviceFlowBeginApproveScript`、`deviceFlowRecordConsentScript`、`deviceFlowFinishApproveScript`（`approved` / `unconfirmed` / `revert` / `failed`；`ALREADY_ISSUED` 按成功处理）、`deviceFlowDenyScript`、`deviceFlowPollScript`、`deviceFlowSettleScript`、`deviceFlowReapScript` / `deviceFlowMarkExpiredScript`、`deviceFlowRequeueScript` / `deviceFlowRemoveUnredeemedScript`、客户端停用与失败标记脚本、限流的预占 / 释放脚本。Claim 在脚本内 `GET uc` 得 fid 再拼出 flow key，该 key 没有在 KEYS 中声明，只适用于单节点 Redis（当前部署与测试都是单节点），将来改集群必须改写。

Redis 不可用时一律失败关闭（503）。仓库 compose 未设 `maxmemory-policy`（默认 `noeviction`）；方案不依赖淘汰策略：热键都有 TTL，未兑换集合由回收器清空，内存打满表现为写入报错（503），不会静默丢键。

#### 10.5.9 回收器与 crid 键

`StartDeviceFlowReaper`（`hydra_device_reaper.go`），启动开关 `enabled=true` 时启动，由 `application.stopWorkers` 等待退出，**不受运行时总开关影响**。每 `reaper_interval_seconds`（60 s）一轮：

1. `ZRANGEBYSCORE unredeemed -inf (now − reaper_grace)` 取最多 100 个 fid。
2. 对每个 fid 执行 `deviceFlowReapScript`：先 ZREM 抢占（多实例只处理一次），仅当抢到、`st≠issued` 且 `crid` 已知时返回 `REVOKE`（flow 已过期时取 crid 键）。
3. `RevokeHydraConsentSessionByID(crid)`（期望 204）；失败则以 score = now 重新加入、crid 键续到 7 天，下一个宽限期后再试。
4. 成功后 `deviceFlowMarkExpiredScript`：删 crid 键，非终态置 `expired`，`denied` / `failed` 不变；记 `reaped`。

为什么要宽限期之后的回收：Hydra 在 `exp` 后拒绝兑换迟到的批准，但**不删**授权会话，清理服务也只删设备码行；没有回收器，「批准了却从没兑换」的流程会一直留在用户的「已授权应用」和 webhook 推送范围里。代价是「交出了令牌但结算失败」时会撤销已交出的令牌（§10.5.4）。

为什么要单独的 crid 键：flow HASH 只活 `expires_in + record_grace_seconds`。如果撤销一直失败超过这段时间（Hydra admin 长时间不可用），回收器只靠 flow 就找不到要撤销的授权。crid 键比 flow 多活 7 天，并在每次撤销失败时续期，所以 7 天只限制「回收器完全停摆」的时长；fid 离开未兑换集合的每条路径都同时删除它。选它而不把 crid 编进 ZSET 成员，是为了让成员保持 fid，已有的 ZREM 调用方与多实例抢占都不用改。键名直接用 fid（服务端随机、非秘密），值里的 crid 也不是凭据（撤销要 Hydra admin）。

#### 10.5.10 用户码、限流与暴力破解预算

`normalizeDeviceUserCode`（`hydra_device_codes.go`）：全角 U+FF01–U+FF5E 映射为 ASCII，U+3000 映射为空格；ASCII 转大写；删除空白以及 `-`、`_`、`.`、U+2010–U+2015、U+2212、U+30FC；长度恰为 `user_code_length` 且每个字符都在字母表中才接受，否则 `malformed_code`（不计预算）。字母表 `BCDFGHJKLMNPQRSTVWXZ`（20 个辅音：纯字母不用切键盘，无元音不拼词，无 0/O/1/I），8 位（约 34.58 bit），TTL 10 min，显示为 `XXXX-XXXX`。`sanitizeDeviceLabel`：删除 Unicode Cc 与 Cf（含零宽与双向覆盖字符），连续空白合并为一个空格，截断到 64 rune，trim。

限流是固定窗口（INCR + 首次 PEXPIRE），上限只在 YAML `oauth2.device_flow.limits`（§10.6.1）。键前缀 `haruki:rate-limit:oauth2-device:`：

| Key | 窗口 | 默认上限 | 类型 | 超限 |
| --- | --- | --- | --- | --- |
| `auth-attempt:unknown-client` | 10 min | 6000 | 只告警 | 记 `auth_unknown_client_warn` |
| `auth-attempt:client:{hx}` | 10 min | 10 × 该客户端 `maxCodesPer10m` | 只告警 | 记 `auth_attempt_client_warn` |
| `auth-issued:global:public` / `…:confidential` | 10 min | 各 600（之和 ≤ `auth_issued_global_per_10m` 1200；各到 300 时记 `auth_issued_global_warn pool=…`） | 预占，Hydra 非 200 释放 | 429 `temporarily_unavailable` |
| `auth-issued:client:{hx}` | 10 min | 客户端 `maxCodesPer10m`（默认 60） | 预占，Hydra 非 200 释放 | 同上 |
| `lookup:user:{hx}` | 10 min | 30 | 计数 | 429 `rate_limited` |
| `lookup-fail:user:{hx}` | 10 min | 5 | 预占，认领成功释放 | 429 `rate_limited` |
| `lookup-fail:user-day:{hx}` | 24 h | 20 | 预占，认领成功释放 | 429 `rate_limited` |
| `lookup-fail:global` | 10 min | **150 硬上限**（50 时记 `lookup_fail_global_warn`） | 预占，认领成功释放；读索引之前检查 | 429 `rate_limited` |
| `decision:user-day:{hx}` | 24 h | 20 | 计数（approve + deny） | 429 `rate_limited` |
| 流程 `att` | 流程生命周期 | 3 | Lua | `failed`，502 `approval_failed` |
| 流程 `ivl` / `sdn` | 每次轮询 | 初始 5 s，早到 +5 s（保持），上限 60 s，容差 1 s；`pending` / `claimed` 时早到超过 30 次 ⇒ `failed` | Lua | 400 `slow_down` |

`Retry-After` 取超限 key 的剩余 TTL（至少 1 s）；每个告警只在计数恰好等于阈值时记一次。公共客户端的 `client_id` 任何人都能冒用，公共池与机密池分开后，冒用最多耗尽公共池，不影响机密客户端。边缘不做按路径或按 IP 的限流，设备侧两个 POST 也不需要边缘豁免；CDN 对 `/api/oauth2/*` 设「不缓存」只作兜底。

暴力破解预算（期望命中须 ≤ 1 次 / 年，启动校验）：

| 量 | 取值 |
| --- | --- |
| 码空间 S / 每年 10 分钟窗口数 W | 20^8 = 2.56×10^10 / 52,560 |
| 同时存活的码 N_live | ≤ (⌈TTL / 600 s⌉ + 1) × 1200 = 2400 |
| 每年失败查询 G | ≤ 150 × 52,560 = 7.884×10^6（成功不耗全局预算，格式错误不算猜测） |
| 最坏期望 | G × N_live / S ≈ **0.74 次 / 年**；实际 N_live ≈ 20 时约 6.2×10^-3 次 / 年 |
| 攻击成本 | 每账号每天最多 20 次失败 ⇒ 打满 G 每天至少约 1,080 个新账号；触发全局熔断需每 10 min 30 个账号。Kratos 注册不限一次性邮箱（只要求验证邮箱），所以熔断的 DoS 成本很低，告警必须有人值守 |
| 命中后果 | 攻击者只能用**自己的账号**批准陌生人的设备（设备会显示「已授权为 <攻击者>」），拿不到受害者账号 |
| DoS 杠杆（接受） | 全局熔断只阻断 lookup ≤ 10 min；device/auth、轮询和已认领流程的批准不受影响 |
| 设备码 | `hdc` 256 位、`ory_dc_` 不出服务端，不可暴力破解 |
| Hydra 行数 / 轮询上界 | 稳态 ≤ 约 16k 行（约 19 MB）；转发到 Hydra 的轮询 ≤ N_live / 4 s = 600 次 / 秒，实际 < 5 次 / 秒 |

启动校验公式：`52560 × lookup_fail_global_per_10m × (⌈user_code_ttl / 600 s⌉ + 1) × auth_issued_global_per_10m / len(charset)^length > 1.0` 即拒绝启动。默认 ≈ 0.74；默认限额下 TTL 超过 10 min 时系数至少为 3（≈ 1.11），会拒绝启动，延长 TTL 必须同时调低限额。

#### 10.5.11 scope 策略、审计与日志

- **scope 策略**（`hydra_device_policy.go`，device/auth 与每次 H2 刷新时执行）：`scope` 非空且都已为该客户端登记，并 ⊆ {`openid`, `profile`, `offline_access`, `user:read`, `bindings:read`, `game-data:read`, `station:room:write`} ∪（公共客户端且 `allow_write` 时加 `game-data:write`）；**必须含** `user:read`；含 `email` ⇒ `invalid_scope`；带 `audience` ⇒ `invalid_request`。`station:room:write` 公共与机密客户端都可以申请，不需要 `allow_write`。管理端客户端校验见 oauth2-integration §10。
- **审计**：浏览器端点用 `WriteUserAuditLog`，签发用 `WriteSystemLog`；device/auth 量大，不写审计行，只写结构化日志。元数据始终含 `clientID`、`deviceFlowID`，从不含任何码。

| action | 时机 | 额外元数据 |
| --- | --- | --- |
| `user.oauth.device.claim` | lookup 认领成功 | `scopes` |
| `user.oauth.device.approve` | approve 结束（成功或失败） | `result`、`reason`、`scopes`、`label`、`consentRequestId`、`attempt` |
| `user.oauth.device.deny` | deny 成功 | `reason` |
| `user.oauth.device.token_issued` | 兼容层交出令牌（actor 匿名，target = 认领者） | — |
| `user.oauth.authorization.revoke_consent` | 按设备撤销 | `consentRequestId` |

- **结构化日志**统一为 `oauth2_device event=<e> fid=… cid=… [stage=… reason=… status=… hydra_error=…]`，`<e>` ∈ {`authorize`, `poll`, `lookup_fail`, `lookup_conflict`, `lookup_fail_global_warn`, `claim`, `approve`, `deny`, `phishing_signal`, `chain_error`, `login_skip_unexpected`, `unconfirmed`, `poll_slow_down`, `token_issued`, `token_after_terminal`, `settle_failed`, `reaped`, `charset_mismatch`, `ttl_mismatch`, `auth_issued_global_warn`, `auth_unknown_client_warn`, `auth_attempt_client_warn`}。
- **永不记录**：Cookie 容器内容、`Set-Cookie`、`Location` / `redirect_to`、challenge、verifier、`user_code`、`hdc`、`ory_dc_`、`flowHandle`、Hydra device/auth 的响应体。访问日志按 `utils/redact` 脱敏 `user_code` / `device_code` 查询参数、`userCode` / `deviceCode` / `flowHandle` JSON 字段，以及 `ory_at_` / `ory_rt_` / `ory_ac_` / `ory_dc_` / `hdc_` / `dfh_` 字面量。

#### 10.5.12 安全要点

| 威胁 | 主要措施 | 残余风险 |
| --- | --- | --- |
| 用户码暴力破解（RFC 8628 §5.1） | 20^8 码空间、10 min TTL；先登录；统一 `invalid_code`；三档失败预算在读索引前检查；首次 lookup 即认领；启动预算校验 | 最坏约 0.74 次 / 年，只造成账号混淆 |
| 设备码暴力破解（RFC 8628 §5.2） | `hdc_` 256 位；`ory_dc_` 密封不出服务端；Hydra 直连换不了 `hdc_` | 无 |
| 仿冒地址与标签（RFC 8628 §5.3） | 固定第一方短地址；只有管理员开通的客户端能发起；审核卡信息来自管理端登记；标签清洗并标为「应用自述」按纯文本渲染 | 用户不核对域名仍可能在仿冒站泄露密码（所有 OAuth 流程共有） |
| 远程钓鱼与非可视化传码（RFC 8628 §5.4、§5.7） | 接入方契约要求只在本机展示代码、回显账号名、标签不含个人标识；页面不自动提交；审核卡展示客户端、风险着色、时间、固定警告与代码核对，必须勾选确认（后端校验 `acknowledged`）；「不是我发起的」记 `phishing_signal`；`email` 永不授予；`remember=false`；按设备撤销；每账号每天 20 次决定 | 服务端无法强制接入方遵守；`station:room:write` 对所有登记了它的客户端开放，公共客户端 `haruki-client` 的 `client_id` 任何人都能冒用：攻击者可诱骗用户授权，从而以受害者的身份向 Station 提交车牌。影响限于提交车牌（审核卡与同意页红色提示），受害者可以按设备或按应用撤销，Station 可以按 `user_id` 封禁 |
| 会话窥视、抢先批准（RFC 8628 §5.5） | 首次 lookup 认领（用户、Kratos 会话哈希、句柄，租约 300 s）；只能由认领者在同一会话批准；设备必须回显「已授权为 <name>」 | 抢先者成功时受害者需要重新开始，账号不受影响 |
| 非机密客户端被冒用与匿名洪泛（RFC 8628 §5.6） | 公共客户端默认只读、永不显示「官方」；每客户端配额；公共池与机密池分开；未知客户端只计告警；只有签发预占能拒绝，错误 secret 在 H3 后立即释放 | 被冒用的公共客户端（或公共池）最多被阻断 10 min |
| 浏览器端点 CSRF、点击劫持、重复提交；按设备撤销越权 | 只收 JSON（415）；Origin 允许名单（403）；句柄只在 JSON 中；approve 须重新提交匹配的 `userCode`；`/device` 在 iframe 内不渲染审核卡；BeginApprove CAS + `anonce`；按设备撤销先匹配本人授权，否则 404 且不调用 Hydra | 没有响应头级的 `frame-ancestors` |
| 代驱链的 SSRF / 开放跳转与凭据泄漏 | 不跟随重定向；只改写一种 URL；前端 Location 只解析不请求；响应体上限；整链超时；容器、challenge、verifier 不返回浏览器、不记日志 | 无 |
| Redis 被读取或篡改；码进入日志或 URL | 键名只有 HMAC，启动时拒绝空密钥；`ory_dc_` 密封；日志脱敏；页面读取后立即清除查询串；前端统计去掉敏感参数 | 密钥本身泄漏；有写权限者可让流程失败；前端托管方的访问日志可能记录首次请求的 `?user_code=` |
| Hydra 行为随版本变化 | 代驱链依赖未文档化的 Cookie / 重定向机制，由真实 Hydra 集成测试在每次升级前验证（§11.3） | — |

### 10.6 设备授权：配置与开关

本节是后端的配置参考。生产上线、监控告警与回滚手册属于运维文档，不在本仓库。部署配置（compose 环境变量、清理服务）见 §11.3；Oathkeeper 规则见 §9.1。

#### 10.6.1 后端配置与启动校验

`config.OAuth2DeviceFlowConfig`，YAML `oauth2.device_flow`（`haruki-toolbox-configs.example.yaml` 有逐键注释的示例块，默认值在 `config/defaults.go`）：

| 分组 | 键（默认值；env） |
| --- | --- |
| 开关与地址 | `enabled`（`false`；`OAUTH2_DEVICE_FLOW_ENABLED`）、`client_allowlist`（`[]` = 所有持有设备授权许可的客户端；`OAUTH2_DEVICE_FLOW_CLIENT_ALLOWLIST`，CSV）、`verification_url`（空 ⇒ `{user_system.frontend_url}/device`；`OAUTH2_DEVICE_FLOW_VERIFICATION_URL`）、`hydra_issuer_url`（空 ⇒ `oauth2.hydra_browser_url`；`OAUTH2_DEVICE_FLOW_HYDRA_ISSUER_URL`）、`allowed_origins`（空 ⇒ `[origin(user_system.frontend_url)]`；`OAUTH2_DEVICE_FLOW_ALLOWED_ORIGINS`，CSV） |
| 用户码 | `user_code_charset`（`BCDFGHJKLMNPQRSTVWXZ`）、`user_code_length`（`8`）、`user_code_ttl`（`"10m"`，Go duration）；env `OAUTH2_DEVICE_FLOW_USER_CODE_*`，与 Hydra 同源（§12.9） |
| 时序（无 env） | `min_poll_interval_seconds: 5`、`claim_ttl_seconds: 300`、`approve_lease_seconds: 30`、`approval_timeout_seconds: 15`、`min_remaining_seconds_to_approve: 30`、`max_approve_attempts: 3`、`record_grace_seconds: 1800`、`reaper_interval_seconds: 60`、`reaper_grace_seconds: 60` |
| `limits`（无 env） | `auth_attempt_unknown_client_warn_per_10m: 6000`、`auth_attempt_client_warn_multiplier: 10`、`auth_issued_global_per_10m: 1200`、`auth_issued_public_per_10m: 600`、`auth_issued_confidential_per_10m: 600`、`auth_issued_client_default_per_10m: 60`、`lookup_user_per_10m: 30`、`lookup_fail_user_per_10m: 5`、`lookup_fail_user_per_day: 20`、`lookup_fail_global_per_10m: 150`、`decision_user_per_day: 20`、`max_slow_down: 30`、`max_interval_seconds: 60` |

compose 总会设置 7 个 backend `OAUTH2_DEVICE_FLOW_*` 变量（空串也算设置），会覆盖 YAML 的同名键（§11.3）。启动校验 `validateOAuth2DeviceFlowConfig`（`internal/bootstrap/validate.go`，只在 `enabled=true` 时执行）：

1. Hydra public / admin / browser URL 都已配置，Redis 已配置；issuer URL 与验证 URL 是绝对 `https` 地址（`http` 只允许 localhost / 127.0.0.1）；`allowed_origins` 每项都是纯源；`client_allowlist` 不含空值。
2. 字母表只含 `A-Z0-9`、不重复、至少 8 个字符（Hydra schema 的下限）；`user_code_length` 在 6–12。
3. 所有 limits 与时序值 > 0；`auth_issued_public_per_10m + auth_issued_confidential_per_10m ≤ auth_issued_global_per_10m`；`min_poll_interval_seconds ≤ max_interval_seconds`。
4. `approval_timeout_seconds + 10 < approve_lease_seconds`，且 `min_remaining_seconds_to_approve ≥ approval_timeout_seconds`。
5. `user_code_ttl` 可解析且在 1–30 min，并通过 §10.5.10 的预算公式。
6. `user_system.session_sign_token`（env `SESSION_SIGN_TOKEN`）trim 后至少 16 个字符。

**`session_sign_token` 是什么**：它**不是**会话签名密钥（`NewSessionHandler` 忽略该参数，登录会话由 Kratos 管理），现在唯一的用途是 Redis 键名里邮箱、QQ 等标识的 HMAC 化名密钥，设备流程的 `hx` 也用它。设置或更改它只会让当时进行中的邮箱验证码、重置链接、QQ 验证码与设备流程作废、限流计数归零，**不会让任何人掉登录**。所以在首次启用设备授权时生成写入，之后不再更改、不打印。

#### 10.6.2 开关层级与运行时总开关

| 层级 | 怎么改 | 需重启 | 关闭时的效果 |
| --- | --- | --- | --- |
| 运行时总开关 `oauth2DeviceFlowEnabled` | `PUT /api/admin/config/runtime`（需二次认证） | 否，最多 1 s 后生效 | device/auth `unauthorized_client`；lookup / approve / deny 403 `feature_disabled`；兼容层对 `hdc_` 回答 `expired_token`；非设备授权许可与回收器不受影响 |
| 启动开关 `OAUTH2_DEVICE_FLOW_ENABLED` | 改 env 后重启 backend | 是 | 同上；另外回收器不启动 |
| 客户端允许名单 `OAUTH2_DEVICE_FLOW_CLIENT_ALLOWLIST` | 同上（CSV） | 是 | 名单外客户端 `unauthorized_client`；空 = 全部持有许可的客户端 |
| 单个客户端的设备授权许可 / 配额；停用客户端 | 管理端（JSON Patch；停用时逐 subject 撤销） | 否 | 只影响该客户端，客户端缓存最多 5 s 后生效；停用还拦截已批准流程的兑换并撤销已有授权会话（列表找不到的授权只被拦截、不被撤销，见 §10.4） |

生效 = 启动开关 ∧ 运行时总开关。运行时总开关是三态：明确开；明确关（`false` **或字段缺失**）；读取出错（503）。字段缺失视为关闭，使开关在状态丢失时失败关闭：Redis 运行时键丢失时种子快照来自启动配置（没有该字段）；Redis 以 `--save 60 1` 持久化，崩溃前 60 s 内的 `PUT false` 可能丢失；旧镜像修改运行时配置时会丢掉该字段。三种情况都回到「关」，不会悄悄重新打开在钓鱼事件中关掉的开关。`Active(ctx)` 把**成功**读取的结果缓存 1 s（出错不缓存），避免匿名洪泛拖住同一把锁后面的私有 API 鉴权。已签发的设备令牌在开关关闭后仍然有效，需要时按授权会话或按客户端撤销。

#### 10.6.3–10.6.5 上线、监控与回滚

这三部分是生产运维手册，维护在私有的运维文档中，不在本仓库。与代码相关的约束都已写在本文其他章节：Hydra 与 backend 的用户码变量必须同时生效（§12.9），发现文档必须指向后端（§12.10），客户端管理必须用 JSON Patch 与逐 subject 撤销（§12.7、§12.8）。

#### 10.6.6 升级 Ory 前

代驱链依赖 Hydra 未文档化的 Cookie 与重定向行为（§10.5.1）。修改 `ORY_VERSION` 前，在待部署的 commit 上对当前版本与目标版本各跑一遍真实 Hydra 集成测试（§11.3），全部通过才能升级；`chain_error` 在升级后出现时，先关闭运行时总开关（§10.6.2）再排查。

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

#### 11.1.1 Sign in with Apple

Kratos 的 Apple provider 配置位于 `external/kratos/kratos.yml`，claim mapper 位于
`external/kratos/oidc.apple.jsonnet`。Apple 登录完成后仍由 Kratos 创建会话，Backend 和
Oathkeeper 的会话验证方式不变。

在 Apple Developer 后台完成以下配置：

1. 创建并启用 **Sign in with Apple** 的 App ID。
2. 创建 **Services ID**；该 Identifier 是 `KRATOS_OIDC_APPLE_CLIENT_ID`，不是 Bundle ID。
3. 在 Services ID 的 Web Authentication 配置中登记：
   - Domain：`KRATOS_PUBLIC_BASE_URL` 的主机名，例如 `toolbox-auth.example.com`
   - Return URL：`https://<Kratos 公网域名>/self-service/methods/oidc/callback/apple`
4. 创建启用了 Sign in with Apple 的私钥，并记录 Team ID 和 Key ID。

部署环境需要设置：

- `KRATOS_OIDC_APPLE_CLIENT_ID`
- `KRATOS_OIDC_APPLE_TEAM_ID`
- `KRATOS_OIDC_APPLE_PRIVATE_KEY_ID`
- `KRATOS_OIDC_APPLE_PRIVATE_KEY`（完整 `.p8` PEM，包含首尾标记）

`.env` 支持用单引号保存多行 PEM，例如：

```dotenv
KRATOS_OIDC_APPLE_PRIVATE_KEY='-----BEGIN PRIVATE KEY-----
<private key content>
-----END PRIVATE KEY-----'
```

真实私钥不得提交到仓库。Apple 浏览器回调使用 `form_post`，因此 provider 的 `id` 必须保持
为 `apple`，不可改成自定义值。Mapper 只接受 Apple 标记为已验证的邮箱；Apple 的隐藏邮箱
（Private Relay）也可以正常使用。前端无需硬编码 provider 列表，应渲染 Kratos login /
registration flow 中 `method=oidc`、`provider=apple` 的 UI node。

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

部署层面还有两项与 Auth Proxy 直接相关：

- `BACKEND_ENABLE_TRUST_PROXY`（默认 `false`）
- `BACKEND_TRUSTED_PROXIES`

backend 位于 Oathkeeper 之后，`c.IP()` 取到的是 Oathkeeper 的地址而不是真实客户端。
只有在开启 trust proxy **并且**把 `BACKEND_TRUSTED_PROXIES` 精确设为边缘代理地址
（IPv4 写 `/32`、IPv6 写 `/128`）时，`X-Forwarded-For` 才会被采信。

**不要填 Docker、LAN 或 Tailscale 网段。** 那等于信任该网段内的每个容器和节点，
其中任何一个都能伪造 `X-Forwarded-For`。不确定边缘代理地址时就保持 `false`：
限流会退化成按 Oathkeeper 这一个地址计数，粗糙但不会被绕过。
开启后若 `BACKEND_TRUSTED_PROXIES` 为空、含空值或填了网段（不是 `/32`、`/128`），
或 `BACKEND_PROXY_HEADER` 为空，backend 会拒绝启动。

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

#### 设备授权（RFC 8628）的部署配置

设备授权只通过 compose 环境变量配置，`external/hydra/hydra.yml` 不写任何设备相关的键（只有一段说明注释）。`.env.example` 在 `HYDRA_PUBLIC_BASE_URL` 之后有 10 个键：

| 键 | 示例值 | 去向 |
| --- | --- | --- |
| `DEVICE_FLOW_USER_CODE_LENGTH` | `8` | Hydra `OAUTH2_DEVICE_AUTHORIZATION_USER_CODE_LENGTH` 与 backend `OAUTH2_DEVICE_FLOW_USER_CODE_LENGTH` |
| `DEVICE_FLOW_USER_CODE_CHARSET` | `BCDFGHJKLMNPQRSTVWXZ` | Hydra `OAUTH2_DEVICE_AUTHORIZATION_USER_CODE_CHARACTER_SET` 与 backend `OAUTH2_DEVICE_FLOW_USER_CODE_CHARSET` |
| `DEVICE_FLOW_USER_CODE_TTL` | `10m` | Hydra `TTL_DEVICE_USER_CODE` 与 backend `OAUTH2_DEVICE_FLOW_USER_CODE_TTL` |
| `DEVICE_FLOW_POLLING_INTERVAL` | `5s` | Hydra `OAUTH2_DEVICE_AUTHORIZATION_TOKEN_POLLING_INTERVAL`（后端下发 `max(interval, 5)`） |
| `OAUTH2_DEVICE_FLOW_ENABLED` | `false` | backend 启动开关；还须打开运行时开关 `oauth2DeviceFlowEnabled` |
| `OAUTH2_DEVICE_FLOW_CLIENT_ALLOWLIST` | 空 | backend；CSV，空 = 所有持有设备授权许可的客户端 |
| `OAUTH2_DEVICE_FLOW_ALLOWED_ORIGINS` | 空 | backend；CSV，空 = `origin(FRONTEND_URL)`。不要把 `oathkeeper.yml` 的占位 CORS 列表抄进来 |
| `HYDRA_DEVICE_JANITOR_GRACE` | `"1 hour"` | 清理服务：只删过期超过该时长的行 |
| `HYDRA_DEVICE_JANITOR_BATCH` | `5000` | 清理服务：每条 DELETE 最多删的行数 |
| `HYDRA_DEVICE_JANITOR_INTERVAL_SECONDS` | `3600` | 清理服务：两轮之间的间隔 |

`hydra` 服务另有 `URLS_DEVICE_VERIFICATION=${FRONTEND_PUBLIC_URL}/device`、`URLS_DEVICE_SUCCESS=${FRONTEND_PUBLIC_URL}/device/done`（代驱链只解析这两个 Location，从不请求），以及发现文档覆盖 `WEBFINGER_OIDC_DISCOVERY_DEVICE_AUTHORIZATION_URL=${BACKEND_PUBLIC_BASE_URL}/api/oauth2/device/auth`、`WEBFINGER_OIDC_DISCOVERY_TOKEN_URL=${BACKEND_PUBLIC_BASE_URL}/api/oauth2/token`。后者同时改变两份发现文档；`private_key_jwt` 的 audience 新旧令牌地址都接受，issuer、`authorization_endpoint` 不变。`backend` 服务另有 `OAUTH2_DEVICE_FLOW_HYDRA_ISSUER_URL=${HYDRA_PUBLIC_BASE_URL}`（与 `HYDRA_BROWSER_URL` 今天取值相同但语义不同，所以单独传入）。

硬性约束（`internal/architecture/ory_oidc_contract_test.go` 与 `config/compose_device_flow_test.go` 守护）：

- 用户码只用 LENGTH + CHARACTER_SET，**永远不要**再设 Hydra 的 user_code 熵预设（`..._ENTROPY_PRESET`）：Hydra schema 中二者是 `oneOf`。字符集与长度只改 `DEVICE_FLOW_USER_CODE_*`，Hydra 与 backend 同时生效（§12.9）
- 这 7 个 backend `OAUTH2_DEVICE_FLOW_*` 变量 compose 总会设置（空串也算设置），会覆盖 backend YAML 里 `oauth2.device_flow` 的同名键；要改就改 env
- Hydra 不加 `--dev`、不配 `serve.public.tls`，issuer 保持 https，`hydra.yml` 保持 `serve.cookies.same_site_mode: Lax`；**不设置 `URLS_SELF_PUBLIC`**（device accept 的 `redirect_to` 由 PublicURL 构造；将来必须设置时 `OAUTH2_DEVICE_FLOW_HYDRA_ISSUER_URL` 也改成同一 origin）
- 重建 `hydra` 容器会让所有客户端的令牌签发与刷新短暂中断，`hydra-migrate` 随之重跑（空操作），安排在公告过的低峰窗口

清理服务 `hydra-device-janitor`（`postgres:18-alpine`，复用 `HYDRA_DB_USER`，后端不持有 Hydra DSN）：

- Hydra 只在成功签发时删除设备码行，从不清理过期行；该服务每 `HYDRA_DEVICE_JANITOR_INTERVAL_SECONDS` 秒一轮，每批 `LIMIT ${HYDRA_DEVICE_JANITOR_BATCH}` 删除 `expires_at < (now() AT TIME ZONE 'UTC') - :'grace'::interval` 的行，直到一批不足 batch；每轮输出一行 `hydra-device-janitor: deleted=<n> remaining=<表行数>`，失败时输出 `hydra-device-janitor: delete failed` 并等下一轮
- `expires_at` 是按 UTC 写入的 `timestamp without time zone`，所以拿 UTC 墙钟比较；该列无索引，靠 `LIMIT` 限住单条语句的锁和耗时；宽限期 ≥ 1 h 且只删已过期的行，不影响进行中的流程
- command 必须是 `entrypoint: ["/bin/sh", "-c"]` 加单元素列表（`- |` 字面块），不得改成折叠写法（`>`），否则 SQL 引号会丢；脚本里的 `$` 一律写成 `$$` 交给容器内 shell；heredoc 用带引号的 `<<'SQL'`，结束符在块标量去缩进后位于第 0 列
- 改动后用 `docker compose config --quiet` 校验，并 grep 渲染结果里原样的 `expires_at < (now() AT TIME ZONE 'UTC') - :'grace'::interval`
- 清理服务停掉时，手动用 psql 在 `hydra` 库执行同一段 SQL（参数写成字面量 `interval '1 hour'`、`LIMIT 5000`），重复到返回值小于 5000

仓库的 `docker-compose.yml`、`external/hydra/hydra.yml` 与 `external/oathkeeper/access-rules.yml` 是部署配置的参考来源。同步到实际部署时，Hydra 与 backend 的字符集 / 长度变量必须在同一次 `up -d hydra backend` 中生效。

#### 真实 Hydra 集成测试（升级门禁）

`internal/modules/oauth2/hydra_device_live_test.go`（build tag `hydra_live`，普通 `go test ./...` 不编译它）在真实的 non-dev Hydra + Postgres 18 上走完整个设备流程，并复核 §10.5.1 记录的 Hydra 行为（失败消息以「§10.5.1」开头）。后端在测试进程内按正式路由表启动（Redis 用 miniredis、用户库用 SQLite），只有 Hydra 与它的 Postgres 是真的。`external/hydra/it/docker-compose.device-it.yml` 与仓库 `docker-compose.yml` 同构（https issuer + 明文 http、仓库的 `hydra.yml`、同一组设备环境变量），差别只有：用户码 1 分钟过期（让过期类子测试一分钟内跑完）、没有清理服务（测试从 `docker-compose.yml` 提取清理 SQL 自己执行）。端口只绑 `127.0.0.1`（14444 / 14445 / 15432）。子测试 `GoXOAuth2Sample` 运行的就是 [OAuth2 / OIDC 接入](oauth2-integration.zh-CN.md) §4A.8 的 Go 示例，并注入一次 503 验证外层退避；普通 `go test` 里的 `TestIntegrationDocGoSampleMatchesLiveTest` 保证文档与测试代码一致。

本地运行（每个版本各一遍，用完 `down -v`）：

```bash
export COMPOSE_FILE=external/hydra/it/docker-compose.device-it.yml
for v in v25.4.0 v26.2.0; do
  HYDRA_IT_VERSION=$v docker compose up -d --wait
  HYDRA_IT_VERSION=$v go test -race -tags hydra_live -count=1 -run TestHydraDeviceFlowLive -v ./internal/modules/oauth2
  docker compose down -v
done
```

单遍约 80 秒（子测试并行，耗时取决于等待过期的两项）。`HYDRA_IT_VERSION` 设置时测试会核对 Hydra `/version`，避免测到旧容器；其余 `HYDRA_IT_*` 变量（地址、DSN、issuer、前端地址、用户码寿命）默认值与 IT compose 一致，只在改端口时需要设置。GitHub Actions 的 `Device flow live`（`.github/workflows/device-flow-live.yml`，只能手动触发、不是必需检查）对两个版本各跑一遍；上线前与修改 `ORY_VERSION` 前都要在待部署的 commit 上跑通。

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

- `internal/platform/api/session_handler.go`
- `internal/platform/oauth2/...`
- `internal/modules/oauth2/...`

### 12.6 开了 trust proxy 却没限定可信代理

后果：

- `BACKEND_ENABLE_TRUST_PROXY=true` 而 `BACKEND_TRUSTED_PROXIES` 为空、含空值或填了网段
  （不是 `/32`、`/128`）时，backend 启动校验直接拒绝启动
- 若填的是单个地址但不是真正的边缘代理（例如某个容器或节点的 IP），该地址就能伪造
  `X-Forwarded-For`，经它转发的请求上所有以 IP 为键的限流、尝试计数、验证码次数上限全部失效，
  表面上一切正常，日志里的来源 IP 也「看起来对」

原因：

- 启动校验（`internal/bootstrap/validate.go` 的 `validateBackendConfig`）只拦得住空值和网段，
  拦不住填错的单个地址

见 §11.2。

### 12.7 用整体 PUT 更新 Hydra client

后果：

- 每次编辑、启停或轮换 secret 之后，在 Hydra 侧单独配置的授权类型、token 寿命、
  `post_logout_redirect_uris` 和其他 metadata 都被清空，管理端看不出任何异常

原因：

- `PUT /admin/clients/{id}` 是整体替换，载荷里没带的字段都会丢失。生命周期操作要用
  JSON Patch，见 §10.4

### 12.8 按客户端整体撤销授权

后果：

- 停用接口在 metadata 已经改成停用之后返回 500，refresh token 仍然有效，重新启用后又能继续刷新
- 「撤销全部授权」失败；默认的「删除并撤销」在删除之前就失败，客户端没有被删掉

原因：

- Hydra 对 `client=X&all=true` 和只带 `client` 的撤销请求返回 400。要逐个 subject
  用 `subject`+`client` 撤销；`DELETE /admin/oauth2/tokens?client_id=` 只删 access token，
  不能代替撤销。见 §10.4

逐个 subject 撤销后仍有残余：

- subject 靠枚举本地用户的授权会话得到，有两类授权找不到：没有本地用户的 subject 的授权；
  用户在该客户端下的流程全部被 Hydra 的会话列表跳过（`consent_skip=TRUE`，或
  `remember_for > 0` 且已过期）。客户端停用期间这些 token 被 bearer 中间件拦截，重新启用后
  恢复有效，「撤销全部授权」也撤不到。见 §10.4

### 12.9 Hydra 与 backend 的用户码配置不一致

后果：

- `POST /api/oauth2/device/auth` 返回 500 `server_error`（日志 `charset_mismatch` 或 `ttl_mismatch`）：Hydra 生成的用户码不在 backend 的字母表或长度内，
  或 Hydra 的 `expires_in` 比 backend 的 `user_code_ttl` 长 5 s 以上时，设备拿不到用户码；授权码流程不受影响，所以容易被当成 Hydra 偶发故障
- 在 `hydra.yml` 或环境变量里再加 user_code 熵预设（`entropy_preset`）时，它与 compose 设置的 LENGTH + CHARACTER_SET
  同时存在，违反 Hydra schema 的 `oneOf`，Hydra 配置校验失败、无法启动

原因：

- 字符集、长度、TTL 只改了 Hydra 或只改了 backend 一侧；或单独 `up -d` 了其中一个服务。backend 的运行时自检（device/auth
  校验 Hydra 返回的用户码与 `expires_in`）在两侧不一致时失败关闭，这是故意的；启动校验只检查 backend 自己的配置，发现不了与 Hydra 的漂移
- 正确做法：只改 `.env` 里的 `DEVICE_FLOW_USER_CODE_*`，同时重建 `hydra` 与 `backend`；Hydra 只用 LENGTH + CHARACTER_SET。见 §11.3

### 12.10 把 Hydra 的设备端点接到 Oathkeeper 上，或发现文档没指向后端

后果：

- 给 `/oauth2/device/auth`、`/oauth2/device/verify` 或 `/oauth2/fallbacks/device` 加规则后，设备和浏览器可以绕开后端：
  没有限流、同一用户码可被多个账号批准、拒绝与过期传不回设备，用户码进入 Hydra 主机的 URL
- 漏设 `WEBFINGER_OIDC_DISCOVERY_DEVICE_AUTHORIZATION_URL` / `…_TOKEN_URL` 时，按发现文档配置的库会直接调用 Hydra：
  device/auth 得到 Oathkeeper 的 404，或者把后端签发的 `hdc_…` 交给 Hydra `/oauth2/token`，只能得到 `invalid_grant`

原因：

- 设备流程的单次使用、限流、拒绝回传与过期语义都在后端实现，Hydra 的设备机制只由后端经内部地址调用
- 正确做法：Hydra 的设备路径保持不路由（`TestHydraDeviceEndpointsAreNotRouted` 守护），两项发现文档覆盖都设为后端地址，
  上线后核对两份 `.well-known` 文档的 `device_authorization_endpoint` 与 `token_endpoint`。见 §3.3、§9.1、§10.5.1、§11.3

## 13. 对后续开发的建议

如果你要继续在本项目里开发 Ory 相关能力，建议遵守以下原则：

1. 浏览器身份逻辑优先考虑 Kratos self-service flow，而不是在 backend 里再造一套页面式认证。
2. 浏览器受保护 API 默认考虑 Oathkeeper/Auth Proxy，而不是让前端直接长期依赖后端本地 session。
3. OAuth2 能力优先通过 Hydra 编排，不要再新增一套本地 OAuth server。
4. 本地用户表仍然是业务真相来源，不能把所有业务都直接挂在 Kratos identity JSON 上。
5. 涉及会话、header、whoami、subject 的修改优先收敛到 `SessionHandler` 和 `internal/platform/oauth2`。
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
