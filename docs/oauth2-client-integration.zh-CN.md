# Haruki Toolbox OAuth2 / OpenID Connect 客户端对接说明

本文档只说明一件事：

> **OAuth2 客户端如何接入 Haruki Toolbox，以及如何把 Toolbox 作为 OIDC Provider 完成登录。**

不展开旧版本历史，不展开过多内部实现，只讲客户端和前端实际该怎么对接。

如果你的客户端属于以下类型：

- Telegram Bot
- 机器人后端
- Web 后端
- 任何需要安全保存 `client_secret` 的服务端客户端

请额外阅读：

- [`docs/oauth2-confidential-client-integration.zh-CN.md`](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/docs/oauth2-confidential-client-integration.zh-CN.md)

如果你的客户端需要在授权用户数据更新后收到通知，请阅读：

- [`docs/oauth2-webhook-integration.zh-CN.md`](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/docs/oauth2-webhook-integration.zh-CN.md)

---

## 1. 实际接入地址

当前线上接入地址如下：

- 前端：`https://haruki.seiunx.com`
- 后端 / Oathkeeper：`https://toolbox-api-direct.haruki.seiunx.com`

当前 Hydra 的浏览器跳转配置是：

- `URLS_LOGIN = https://haruki.seiunx.com/oauth2/login`
- `URLS_CONSENT = https://haruki.seiunx.com/oauth2/consent`
- `URLS_LOGOUT = https://haruki.seiunx.com/logout`

这意味着：

- 浏览器发起 OAuth2 授权时，真正展示给用户的登录页和授权页应该是前端页面
- 后端提供的是 OAuth2 编排 API
- 前端页面负责消费这些 API 并完成跳转

### 1.1 Toolbox 作为 OIDC Provider

Toolbox 的 OIDC issuer 由 Hydra 的 `HYDRA_PUBLIC_BASE_URL` 决定。当前线上 issuer 是：

```text
https://toolbox-api-direct.haruki.seiunx.com
```

标准 OIDC 元数据与端点：

- Discovery：`https://toolbox-api-direct.haruki.seiunx.com/.well-known/openid-configuration`
- Authorization：`https://toolbox-api-direct.haruki.seiunx.com/oauth2/auth`
- Token：`https://toolbox-api-direct.haruki.seiunx.com/oauth2/token`
- JWKS：`https://toolbox-api-direct.haruki.seiunx.com/oauth2/jwks.json`
- UserInfo：`https://toolbox-api-direct.haruki.seiunx.com/userinfo`

OIDC 客户端应从 Discovery 文档读取端点，不要在 SDK 内硬编码。现有
`/api/oauth2/authorize` 与 `/api/oauth2/token` 仍作为兼容入口保留。

发起登录时至少请求 `openid`，常见组合是：

```text
scope=openid profile email
```

授权成功后 token 响应会包含 `id_token`。客户端必须使用 Discovery 中的 JWKS 校验签名，
并校验 `iss`、`aud`、`exp` 与自己生成的 `nonce`；账户主键应使用标准 `sub`，不要使用可能
变化的邮箱。`uid` 是兼容既有 Toolbox API 的本地用户 ID 扩展 claim。

标准 claims 按用户实际授权的 scope 最小化发放：

- `openid`：签发 ID Token 与标准 `sub`
- `profile`：增加 `name`
- `email`：增加 `email` 与 `email_verified`

只有管理员为 client 登记的 scope 才能被请求；`profile`、`email` 不能脱离 `openid` 单独登记。

当前先开放 OIDC 登录、ID Token 与 UserInfo。RP-Initiated Logout / Single Logout 尚未完成
Hydra `logout_challenge`、Kratos 会话注销与前端跳转的端到端编排，客户端暂时不要依赖
Discovery 中可能出现的 `end_session_endpoint`；应用退出时应清理自己的本地会话，必要时调用
token revoke 接口。

---

## 2. 最新版本推荐的接入方式

### 推荐模式

最新版本推荐客户端使用：

- `Authorization Code`
- `PKCE (S256)`

适用对象：

- Web 前端
- SPA
- 公共客户端（public client）

### 不推荐的理解方式

不要再把最新版本理解成：

- 后端自己渲染登录页
- 后端自己渲染授权页
- 浏览器直接打开 `/api/oauth2/login` 就应该看到网页

最新版本不是这种模式。

---

## 3. 接入时必须先理解的核心点

### 3.1 `/api/oauth2/authorize` 是浏览器入口

客户端发起授权时，浏览器应该打开：

- `https://toolbox-api-direct.haruki.seiunx.com/api/oauth2/authorize?...`

### 3.2 `/api/oauth2/login` 和 `/api/oauth2/consent` 不是页面

这两个接口是 **JSON API**：

- `GET /api/oauth2/login?login_challenge=...`
- `GET /api/oauth2/consent?consent_challenge=...`

它们是给前端页面调用的，不是给用户直接看的网页。

如果浏览器最终停在 JSON 页面，通常说明：

- Hydra 的 login / consent URL 还指到了后端 API
- 或者前端没有正确承接 `/oauth2/login` 与 `/oauth2/consent` 页面

### 3.3 前端必须提供两个页面

最新版本要正常工作，前端至少需要这两个页面：

- `https://haruki.seiunx.com/oauth2/login`
- `https://haruki.seiunx.com/oauth2/consent`

这两个页面是 OAuth2 浏览器流的关键组成部分。

---

## 4. 最新版本完整授权流程

下面是**推荐且正确**的最新版本接入流程。

### 第 1 步：客户端生成 PKCE 参数

客户端本地生成：

- `state`
- `code_verifier`
- `code_challenge`

其中：

- `code_challenge_method=S256`

### 第 2 步：浏览器跳转到授权入口

浏览器打开：

```text
https://toolbox-api-direct.haruki.seiunx.com/api/oauth2/authorize
  ?response_type=code
  &client_id=<你的 client_id>
  &redirect_uri=<你注册的 redirect_uri>
  &scope=<空格分隔或编码后的 scope>
  &state=<你的 state>
  &code_challenge=<你的 code_challenge>
  &code_challenge_method=S256
```

例如：

```text
https://toolbox-api-direct.haruki.seiunx.com/api/oauth2/authorize?response_type=code&client_id=uni-viewer-public&redirect_uri=https%3A%2F%2Fviewer.unipjsk.com%2Foauth2%2Fcallback%2Fcode&scope=game-data%3Aread&state=<state>&code_challenge=<challenge>&code_challenge_method=S256
```

### 第 3 步：后端把请求转给 Hydra

后端会先把请求重定向到 Hydra 浏览器授权地址。

这一步是自动完成的，客户端不需要额外处理。

### 第 4 步：Hydra 把浏览器带到前端登录页

如果当前浏览器还没有可复用的登录结果，Hydra 会把浏览器重定向到：

- `https://haruki.seiunx.com/oauth2/login?login_challenge=...`

注意：

- 这里应该是前端页面
- 不是后端 JSON API

### 第 5 步：前端登录页读取 `login_challenge`

前端 `/oauth2/login` 页面需要：

1. 从 URL 读取 `login_challenge`
2. 调用：

```http
GET https://toolbox-api-direct.haruki.seiunx.com/api/oauth2/login?login_challenge=...
```

这个接口会返回 login request 的 JSON。

前端应该重点关心这些字段：

- `challenge`
- `skip`
- `subject`
- `client`
- `requested_scope`

### 第 6 步：如果用户还没登录，则先完成 Kratos 登录

如果当前前端用户还没有 Haruki 的浏览器登录态，就应该先让用户完成正常登录。

这里的登录是：

- Haruki 当前前端登录流程
- 本质上走 Kratos 管理的浏览器身份体系

也就是说：

- 先让用户登录 Haruki
- 登录完成后再回到 `/oauth2/login?login_challenge=...`

### 第 7 步：前端调用接受 login challenge 接口

当用户已经登录后，前端调用：

```http
POST https://toolbox-api-direct.haruki.seiunx.com/api/oauth2/login/accept
```

请求体示例：

```json
{
  "loginChallenge": "<login_challenge>",
  "remember": true,
  "rememberFor": 3600
}
```

返回结果中会包含：

- `redirect_to`

前端收到后应执行：

- `window.location = redirect_to`

### 第 8 步：浏览器进入前端 consent 页面

完成 login accept 后，Hydra 会继续把浏览器导向：

- `https://haruki.seiunx.com/oauth2/consent?consent_challenge=...`

同样，这里应该是前端页面。

### 第 9 步：前端 consent 页面读取 challenge 并查询详情

前端 `/oauth2/consent` 页面需要：

1. 从 URL 读取 `consent_challenge`
2. 调用：

```http
GET https://toolbox-api-direct.haruki.seiunx.com/api/oauth2/consent?consent_challenge=...
```

后端会返回 consent request JSON。

前端应展示给用户的信息通常包括：

- client 名称
- 请求的 scopes
- 请求的 audience（如果有）

### 第 10 步：用户确认授权

用户点击“同意”后，前端调用：

```http
POST https://toolbox-api-direct.haruki.seiunx.com/api/oauth2/consent/accept
```

请求体示例：

```json
{
  "consentChallenge": "<consent_challenge>",
  "grantScope": ["game-data:read"],
  "grantAccessTokenAudience": [],
  "remember": true,
  "rememberFor": 3600
}
```

注意：

- `grantScope` 必须是本次请求中允许的 scope 子集
- 最简单的做法通常就是把后端返回的 `requested_scope` 原样回传

返回结果会包含：

- `redirect_to`

前端收到后应执行：

- `window.location = redirect_to`

### 第 11 步：浏览器回到客户端 `redirect_uri`

最终浏览器会跳回你注册的：

- `redirect_uri`

并在 query 中带上：

- `code`
- `state`

客户端这时应该：

1. 校验 `state`
2. 读取 `code`
3. 在后端或前端准备换 token

---

## 5. 换取 token

授权码拿到后，客户端应请求：

- `POST https://toolbox-api-direct.haruki.seiunx.com/api/oauth2/token`

这个接口由 backend 代理到 Hydra token endpoint。

### public client（推荐 PKCE）

```bash
curl -X POST 'https://toolbox-api-direct.haruki.seiunx.com/api/oauth2/token' \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -d 'grant_type=authorization_code' \
  -d 'client_id=<client_id>' \
  -d 'code=<authorization_code>' \
  -d 'redirect_uri=<redirect_uri>' \
  -d 'code_verifier=<code_verifier>'
```

### confidential client

```bash
curl -X POST 'https://toolbox-api-direct.haruki.seiunx.com/api/oauth2/token' \
  -u '<client_id>:<client_secret>' \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -d 'grant_type=authorization_code' \
  -d 'code=<authorization_code>' \
  -d 'redirect_uri=<redirect_uri>'
```

### refresh token

```bash
curl -X POST 'https://toolbox-api-direct.haruki.seiunx.com/api/oauth2/token' \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -d 'grant_type=refresh_token' \
  -d 'client_id=<client_id>' \
  -d 'refresh_token=<refresh_token>'
```

对于 confidential client，通常仍应带 client basic auth。

### refresh token 为什么有时不会返回

当前基于 Hydra 的行为里，如果你希望拿到 `refresh_token`，授权请求和最终同意授权的 `grantScope` 通常需要包含：

- `offline_access`

也就是说，如果你请求的 scope 只有：

- `game-data:read`
- `bindings:read`

而没有：

- `offline_access`

那么即使 client 本身允许 `refresh_token` grant，也可能不会返回 `refresh_token`。

---

## 6. 撤销 token

撤销入口：

- `POST https://toolbox-api-direct.haruki.seiunx.com/api/oauth2/revoke`

示例：

```bash
curl -X POST 'https://toolbox-api-direct.haruki.seiunx.com/api/oauth2/revoke' \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -d 'token=<token>' \
  -d 'token_type_hint=refresh_token'
```

---

## 7. Bearer Token 可访问的资源接口

### 7.1 用户资料

接口：

- `GET /api/oauth2/user/profile`

需要 scope：

- `user:read`

### 7.2 用户绑定游戏账号

接口：

- `GET /api/oauth2/user/bindings`

需要 scope：

- `bindings:read`

### 7.3 游戏数据读取

接口：

- `GET /api/oauth2/game-data/:server/:data_type/:user_id`

需要 scope：

- `game-data:read`

当前该接口还会校验：

- 该 token 对应的用户是否拥有这个绑定
- 绑定是否已经验证通过

响应兼容说明：

- `suite` 数据中的 `userGamedata.userId` 保持原有 number 字段
- 同时返回 `userGamedata.userIdString` 作为字符串镜像，JS / TS 客户端应优先读取该字段以避免 64 位整数精度丢失
- 当响应暴露顶层 `_id` 时，会同时返回 `_idString`

条件拉取（避免重复拉取未变化的数据）：

- 请求可附带 `?known_upload_time=<unix 秒>`，值取自上一次完整响应数据中的 `upload_time` 字段；如果你使用 `key` 过滤字段，必须把 `upload_time` 一并列入，否则该参数会被忽略（完整响应里也拿不到新的时间戳）
- 若数据自那以后没有更新，直接返回 `304 Not Modified`（空响应体，附带 `X-Upload-Time` 响应头），客户端应继续使用本地已有数据；原本的"先探测 `upload_time` 再拉全量"两次请求可以合并为一次
- 若数据已更新（或服务端无法确认未变化），返回完整数据，行为与不带该参数时完全一致；此时应从响应数据的 `upload_time` 字段刷新本地记录的值——完整响应不附带 `X-Upload-Time` 头，时间戳一律以响应体为准
- 参数值非法时按未携带处理（类比 HTTP 对不可解析 `If-Modified-Since` 的处理方式），不会报错
- 该参数不改变鉴权与字段可见性：suite 数据面上，若服务端配置的公开字段允许列表不含 `upload_time`，该参数会被忽略；`key` 过滤无效时不会返回 304，而是照常返回原有错误
- 时间戳精度为 unix 秒，同一秒内的多次上传无法通过时间戳区分（该限制与"先探测 `upload_time` 再拉全量"的方式一致）；此外服务端对时间戳有至多约 60 秒的短记忆，数据刚更新后的极短窗口内可能仍返回 304（旧探测方式对应的缓存窗口约为 5 分钟，比现在更宽）。对一致性极敏感的场景请直接拉取完整数据
- public API 与 private token API 的同形数据读取接口同样支持该参数

---

### 7.4 游戏数据上传（代理上传）

接口：

- `POST /api/oauth2/game-data/:server/:data_type/:user_id`

需要 scope：

- `game-data:write`

请求体是**原始游戏负载**——也就是你从游戏抓到的、未解密的原文，与 `manual` / proxy / 脚本上传接口收的是同一种东西。`Content-Type` 不限，服务端读原始 body。

成功返回与手动上传一致；负载被拒时返回对应的错误码与说明。

#### 为什么必须是原始负载

服务端解密之后，会要求**游戏自己写在负载里的** game user id 与 URL 里的 `:user_id` 一致，不一致直接拒绝。

这是这个接口的防伪基础：拿到某个账号的 token，并不等于可以往那个账号里写任意内容——你还得拿得出游戏为该账号生成的真实负载。如果接口收已经解码好的 JSON，那个 id 就变成了客户端自己填的字段，这条防线就没了。

因此**不会**提供"提交已解码 JSON"的变体。

#### 授权比读取更严

读取允许通过 **grant**（其他用户把自己的数据共享给你）访问；上传**不允许**：

| | 自己拥有的绑定 | 通过 grant 获得的访问 |
|---|---|---|
| `GET`（读取） | ✅ | ✅ |
| `POST`（上传） | ✅ | ❌ `403` |

别人授权你**查看**他的数据，不等于授权你**覆盖**他的数据。

#### 其余校验

这个接口走的是与其他上传方式完全相同的处理链路，因此同样会：

- 校验该 token 对应的用户拥有这个绑定，且绑定已验证通过
- 校验账号所有者未被封禁
- 应用该账号的上传策略（例如公开 API 可见性、cn 服 mysekai 限制）
- 写入审计日志，上传方式记为 `oauth2`，可与账号所有者本人的上传区分开

另外，**token 所属 client 被停用后，该 token 立刻失去上传能力**，无需等待其自然过期。

---

## 8. 当前可申请的 scope

当前内置 scope 如下：

- `openid`
- `profile`
- `email`
- `offline_access`
- `user:read`
- `bindings:read`
- `game-data:read`
- `game-data:write`

全部 scope 均已对外可用且被本文覆盖。

`game-data:write` 是其中唯一的**写**权限，申请时请注意：

- 它允许你代表用户上传游戏数据（§7.4），只对用户**自己拥有**的绑定生效
- 它不隐含 `game-data:read`；两者需要分别申请
- 同意页会向用户展示"Upload game data on your behalf"，用户可以只授予读、不授予写

---

## 9. 管理员创建 OAuth Client 时需要提供什么

管理员在后台创建 client 时，需要提供：

- `clientId`
- `name`
- `clientType`
- `redirectUris`
- `scopes`

其中：

- `clientType` 只能是 `public` 或 `confidential`
- `redirectUris` 必须是合法 URI
- `redirectUris` 不能包含 fragment
- `scopes` 必须来自系统允许的 scope 集

### public client 示例

```json
{
  "clientId": "uni-viewer-public",
  "name": "Uni PJSK Viewer",
  "clientType": "public",
  "redirectUris": [
    "https://viewer.unipjsk.com/oauth2/callback/code"
  ],
  "scopes": [
    "openid",
    "profile",
    "email",
    "offline_access",
    "game-data:read"
  ]
}
```

### confidential client 说明

如果创建的是 `confidential`：

- 服务端会生成一次性返回的 `clientSecret`
- 客户端接入方必须自行安全保存

---

## 10. 最新版本接入时最容易踩的坑

### 坑 1：把 `/api/oauth2/login` 当成网页

错误理解：

- 浏览器打开后应该看到登录页

实际情况：

- 它返回的是 JSON

### 坑 2：把 Hydra 的 `URLS_LOGIN` / `URLS_CONSENT` 配成 backend API

错误配置：

- `URLS_LOGIN = https://toolbox-api-direct.haruki.seiunx.com/api/oauth2/login`
- `URLS_CONSENT = https://toolbox-api-direct.haruki.seiunx.com/api/oauth2/consent`

结果：

- 浏览器会直接停在 JSON 页面

### 坑 3：前端没有 `/oauth2/login` 和 `/oauth2/consent` 页面

结果：

- Hydra 跳过去后 404
- 或流程无法继续

### 坑 4：public client 没做 PKCE

结果：

- token 交换失败

### 坑 5：`redirect_uri` 不是完全匹配

结果：

- 授权失败或换 token 失败

---

## 11. 最终推荐接入模板

如果你是一个新的 OAuth2 客户端，推荐你按这个模板接入：

1. 申请一个 `public` client
2. 使用 `Authorization Code + PKCE`
3. 浏览器发起授权时访问：
   - `https://toolbox-api-direct.haruki.seiunx.com/api/oauth2/authorize?...`
4. 前端提供并接管：
   - `https://haruki.seiunx.com/oauth2/login`
   - `https://haruki.seiunx.com/oauth2/consent`
5. 前端页面调用 backend 的 login / consent JSON API
6. 用 `/api/oauth2/token` 换 token
7. 用 bearer token 调：
   - `/api/oauth2/user/profile`
   - `/api/oauth2/user/bindings`
   - `/api/oauth2/game-data/...`

---

## 12. 一句话结论

最新版本的 OAuth2 客户端接入，不再是“后端直接渲染授权页”的模式，而是：

> **客户端通过 `https://toolbox-api-direct.haruki.seiunx.com` 发起 OAuth2，前端 `https://haruki.seiunx.com` 负责承接 `/oauth2/login` 和 `/oauth2/consent` 页面，后端提供 challenge 编排 API，Hydra 最终签发 code / token。**

只要按这个思路接，最新版本就能正常工作。
