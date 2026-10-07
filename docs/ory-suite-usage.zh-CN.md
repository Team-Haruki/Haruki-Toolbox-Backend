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
- Kratos / Auth Proxy 会话处理：`internal/platform/api/session_handler.go`
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

兼容期内项目仍保留组卡推荐输入数据接口：

- `GET /api/user/:toolbox_user_id/game-account/:server/:game_user_id/recommend-data`
- 查询参数 `mode=suite|mysekai`，默认 `suite`
- `mode=mysekai` 会在 suite 基础数据上合并 MySekai 推荐所需字段，方便前端 wasm 直接作为 `user_data` 使用

游戏账号数据授权接口见 [`docs/game-account-data-grants.zh-CN.md`](game-account-data-grants.zh-CN.md)。

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

- `internal/platform/api/session_handler.go`
- `internal/platform/oauth2/...`
- `internal/modules/oauth2/...`

### 12.6 开了 trust proxy 却没限定可信代理

后果：

- 所有以 IP 为键的限流、尝试计数、验证码次数上限全部失效
- 表面上一切正常，日志里的来源 IP 也「看起来对」

原因：

- `BACKEND_ENABLE_TRUST_PROXY=true` 而 `BACKEND_TRUSTED_PROXIES` 为空或填了网段时，
  `X-Forwarded-For` 变成客户端可控输入，攻击者每次请求换一个伪造 IP 即可绕过上限

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
