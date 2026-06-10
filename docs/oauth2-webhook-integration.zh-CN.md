# Haruki Toolbox OAuth2 Webhook 接入说明

OAuth2 Webhook 用于通知已经通过 OAuth2 授权读取游戏数据的第三方客户端：用户绑定账号的数据已经上传更新。它和旧 public API Webhook 分离，不需要用户开启 `allowPublicApi`。

前端集中对接说明见 [`docs/frontend-integration-notes.zh-CN.md`](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/docs/frontend-integration-notes.zh-CN.md)。

## 触发条件

当一次上传成功后，服务端会异步检查：

- 上传账号存在已验证的游戏账号绑定
- 绑定 owner 未被封禁
- OAuth2 client 配置了启用状态的 webhook endpoint
- 该 owner 对该 OAuth2 client 存在有效 Hydra consent session
- consent 的 grant scope 包含 `game-data:read`

满足条件时，服务端会向该 client 的 webhook endpoint 发起回调。Hydra 查询失败或回调失败不会影响上传响应，只会记录日志。

## 回调请求

回调请求与 public API Webhook 保持一致：

- 方法：`POST`
- Body：空
- 默认请求头：

```http
User-Agent: Haruki-Toolbox-Backend/<version>
```

如果 endpoint 配置了 bearer，回调还会包含：

```http
Authorization: Bearer <bearer>
```

## Callback URL 占位符

callback URL 支持以下占位符：

- `{user_id}`：游戏用户 ID
- `{server}`：区服
- `{data_type}`：数据类型

示例：

```text
https://example.com/oauth-webhook/{server}/{data_type}/{user_id}
```

当用户 `123456789` 在 `jp` 上传 `suite` 数据时，实际请求地址为：

```text
https://example.com/oauth-webhook/jp/suite/123456789
```

## 管理方式

OAuth2 Webhook endpoint 由管理员在 OAuth2 client 下维护：

- `GET /admin/oauth-clients/:client_id/webhooks`
- `POST /admin/oauth-clients/:client_id/webhooks`
- `PUT /admin/oauth-clients/:client_id/webhooks/:webhook_id`
- `DELETE /admin/oauth-clients/:client_id/webhooks/:webhook_id`

创建或更新 endpoint 时会校验 callback URL，拒绝 localhost、内网 IP、回环地址、带用户名密码的 URL 和非 HTTP/HTTPS URL。

## 与 public API Webhook 的区别

- public API Webhook 依赖客户端 token 自行订阅具体游戏账号，并且只在 `allowPublicApi` 开启时触发。
- OAuth2 Webhook 依赖 OAuth2 client 配置和用户对 client 的 Hydra consent，不要求 `allowPublicApi`。
- OAuth2 Webhook 不改变 OAuth2 game-data API 的响应格式，也不改变 public/private API 的权限语义。
