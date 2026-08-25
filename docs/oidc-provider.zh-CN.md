# 用 Haruki 账号登录你的站点（OIDC Provider 接入）

本文档面向**外部服务商** —— 你有自己的网站或应用，想让用户用 Haruki 账号登录，不需要读取游戏数据。

如果你要的是读取用户的游戏数据、绑定信息或代理上传，那不是这篇，见
[`oauth2-integration.zh-CN.md`](oauth2-integration.zh-CN.md)。

Haruki Toolbox 是一个标准的 OpenID Connect Provider，底层是 Ory Hydra。你可以用任何成熟的
OIDC 客户端库接入，不需要为 Haruki 写特殊代码 —— **除了本文最后列出的两处偏差**。

---

## 1. 你需要的全部信息

**Issuer：**

```text
https://toolbox-api-direct.haruki.seiunx.com
```

绝大多数库只需要这一个值，其余端点从 Discovery 自动获取：

```text
https://toolbox-api-direct.haruki.seiunx.com/.well-known/openid-configuration
```

端点清单（2026-08-26 实测）：

| 用途 | 端点 | 状态 |
| --- | --- | --- |
| Discovery | `/.well-known/openid-configuration` | ✅ 200 |
| Authorization | `/oauth2/auth` | ✅ 302 |
| Token | `/oauth2/token` | ✅ |
| JWKS | `/.well-known/jwks.json` | ✅ 2 把 RS256 密钥 |
| UserInfo | `/userinfo` | ✅ |
| Revocation | `/oauth2/revoke` | ✅ |
| End Session | `/oauth2/sessions/logout` | ⚠️ 见 §5 |

支持 `authorization_code` + PKCE（`S256` 与 `plain`），签名算法 RS256。

---

## 2. 申请 client

联系管理员创建，提供：

| 字段 | 说明 |
| --- | --- |
| `clientId` | 你指定的标识，例如 `example-site` |
| `name` | 展示给用户的应用名，**会出现在授权页上** |
| `clientType` | `public`（SPA / 移动端）或 `confidential`（有后端） |
| `redirectUris` | 完整回调地址，**必须精确匹配，不能带 fragment** |
| `scopes` | 登录场景通常是 `openid profile email`；要长期访问再加 `offline_access` |

`confidential` 会返回**仅显示一次**的 `client_secret`。

`profile` 和 `email` **不能脱离 `openid` 单独登记**。

---

## 3. 接入

标准 OIDC 授权码流程，你的库会处理。发起授权时至少请求 `openid`：

```text
scope=openid profile email
```

用户会被带到 Haruki 的登录页与授权页完成登录，然后跳回你的 `redirect_uri`。

> 你**不需要**实现 `/oauth2/login` 或 `/oauth2/consent` 页面 —— 那是 Haruki 自己前端的职责。
> 主文档里提到的"前端必须提供两个页面"说的是 Haruki 的前端，不是你的。

### ID Token 校验

拿到 token 后必须校验 `id_token`：

- 用 Discovery 里的 `jwks_uri` 取公钥验签（RS256）
- 校验 `iss` 等于上面的 issuer
- 校验 `aud` 包含你的 `client_id`
- 校验 `exp` 未过期
- 校验 `nonce` 与你发起授权时生成的一致

### 用 `sub` 做主键

```text
sub    ← 稳定标识，用这个
email  ← 用户可以改，不要用作主键
uid    ← Haruki 本地用户 ID 的扩展 claim，仅为兼容既有 Toolbox API 保留
```

### claims 的实际发放

按用户**实际授权**的 scope 最小化发放：

| scope | 得到的 claims |
| --- | --- |
| `openid` | `sub` |
| `profile` | 增加 `name` |
| `email` | 增加 `email`、`email_verified` |

用户可以在授权页上只勾选一部分，所以**你的代码必须容忍 `name` 或 `email` 缺失**。

---

## 4. 偏差一：不要信 Discovery 的 `scopes_supported` 和 `claims_supported`

Discovery 目前公布的是：

```json
"scopes_supported": ["offline_access", "offline", "openid"],
"claims_supported": ["sub"]
```

这是 Hydra 的默认公告行为，**不是实际能力**。真实可用的 scope 由管理员为你的 client 登记（§2），claims 见 §3。

**影响：** 有些库会按 `scopes_supported` 做能力协商，发现 `profile` / `email` 不在列表里就拒绝请求或静默丢弃。如果你的库有这类校验，请关掉它，或把 scope 硬编码进配置。

---

## 5. 偏差二：必须显式关闭 RP-Initiated Logout

Discovery 公布了 `end_session_endpoint`：

```text
https://toolbox-api-direct.haruki.seiunx.com/oauth2/sessions/logout
```

端点本身可达（2026-08-26 补齐了路由），但**登出流程尚未端到端打通**：

- 不带 `id_token_hint` 请求 → 302 到 Hydra 内置兜底页，而该兜底页当前返回 404
- 带 `post_logout_redirect_uri` 但不带 `id_token_hint` → Hydra 明确报错 `invalid_request`
- 带 `id_token_hint` 的标准流程会跳到 Haruki 前端的 `/logout` 页面，但该页面处理
  `logout_challenge` 的编排尚未完成

**你该怎么做：**

在库的配置里**显式禁用 RP-Initiated Logout / Single Logout**，不要让它按 Discovery 自动发现。用户登出时：

1. 清理你自己站点的会话
2. 需要的话调用 `/oauth2/revoke` 撤销 token

```bash
curl -X POST 'https://toolbox-api-direct.haruki.seiunx.com/oauth2/revoke' \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -d 'token=<token>' \
  -d 'token_type_hint=refresh_token'
```

> 这条打通后本文档会更新。在那之前，**按 Discovery 自动配置登出的库会在用户点登出时报错**。

---

## 6. 常见库的配置要点

以下只列需要偏离默认的地方，其余按库的标准用法即可。

**Node.js `openid-client`**

```js
const issuer = await Issuer.discover('https://toolbox-api-direct.haruki.seiunx.com');
const client = new issuer.Client({
  client_id: 'example-site',
  client_secret: '...',                  // public client 省略
  redirect_uris: ['https://example.com/callback'],
  response_types: ['code'],
});
// 不要调用 client.endSessionUrl() —— 见 §5
```

**Spring Security**

```yaml
spring:
  security:
    oauth2:
      client:
        provider:
          haruki:
            issuer-uri: https://toolbox-api-direct.haruki.seiunx.com
        registration:
          haruki:
            client-id: example-site
            scope: openid,profile,email      # 显式写死,不要依赖 scopes_supported
```

不要注册 `OidcClientInitiatedLogoutSuccessHandler`，改用普通的 `logout()`。

**`mod_auth_openidc`**

```apache
OIDCProviderMetadataURL https://toolbox-api-direct.haruki.seiunx.com/.well-known/openid-configuration
OIDCScope "openid profile email"
# 不要设置 OIDCProviderEndSessionEndpoint
```

---

## 7. 排错

| 现象 | 原因 |
| --- | --- |
| 授权跳转后 404 | `redirect_uri` 与注册值不完全一致（含尾斜杠、协议、端口） |
| 换 token 报 `invalid_client` | `client_id` / `client_secret` 不匹配，或 public client 误用了 secret |
| 换 token 报 `invalid_grant` | `code` 已使用或已过期，或 `redirect_uri` 与授权时不一致 |
| 公开客户端换 token 失败 | 没做 PKCE —— 公开客户端必须带 `code_verifier` |
| 拿不到 `refresh_token` | scope 里没有 `offline_access` |
| 拿不到 `name` / `email` | 对应 scope 未登记，或用户在授权页上没有勾选 |
| 登出报错 | 见 §5，需要关闭 RP-Initiated Logout |

---

## 8. 一句话

> 用 issuer `https://toolbox-api-direct.haruki.seiunx.com` 接标准 OIDC 授权码流程，
> 用 `sub` 做用户主键，**把 scope 写死在配置里、把 RP-Initiated Logout 关掉**。
