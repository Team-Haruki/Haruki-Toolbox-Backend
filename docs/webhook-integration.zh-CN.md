# Webhook 用户接入说明

本文档只面向实际接入 Haruki Toolbox Webhook 的使用方，说明你在已经拿到有效 token 后，如何维护订阅关系，以及收到回调时会发生什么。

## 1. 接入前提

开始接入前，你需要从服务端管理员处拿到：

- 当前 endpoint 的有效 webhook token
- 请求 header 名称：`X-Haruki-Suite-Webhook-Token`
- 已配置的 callback URL

本文档不介绍后台创建、修改、删除 endpoint 的管理操作，只说明 token 拿到之后的接入方式。

## 2. token 的用途

Webhook token 用于访问以下受保护接口：

- `GET /api/webhook/subscribers`
- `PUT /api/webhook/:server/:data_type/:user_id`
- `DELETE /api/webhook/:server/:data_type/:user_id`

请求时需要带上 header：

```http
X-Haruki-Suite-Webhook-Token: <token>
```

## 3. 查询当前订阅者

`GET /api/webhook/subscribers`

请求示例：

```http
GET /api/webhook/subscribers HTTP/1.1
Host: toolbox-api-direct.haruki.seiunx.com
X-Haruki-Suite-Webhook-Token: <token>
```

响应示例：

```json
[
  {
    "uid": "123456789",
    "server": "jp",
    "type": "suite"
  }
]
```

字段说明：

- `uid`: 游戏用户 ID
- `server`: 区服
- `type`: 数据类型

## 4. 订阅某个用户的数据上传事件

`PUT /api/webhook/:server/:data_type/:user_id`

示例：

```http
PUT /api/webhook/jp/suite/123456789 HTTP/1.1
Host: toolbox-api-direct.haruki.seiunx.com
X-Haruki-Suite-Webhook-Token: <token>
```

成功响应：

```json
{
  "status": 200,
  "message": "Registered webhook push user successfully.",
  "updatedData": null
}
```

参数说明：

- `server`: `jp` / `en` / `tw` / `kr` / `cn`
- `data_type`: 如 `suite`、`mysekai`
- `user_id`: 目标游戏用户 ID

## 5. 取消订阅

`DELETE /api/webhook/:server/:data_type/:user_id`

示例：

```http
DELETE /api/webhook/jp/suite/123456789 HTTP/1.1
Host: toolbox-api-direct.haruki.seiunx.com
X-Haruki-Suite-Webhook-Token: <token>
```

成功响应：

```json
{
  "status": 200,
  "message": "Unregistered webhook push user successfully.",
  "updatedData": null
}
```

## 6. 什么时候会触发回调

当用户上传对应数据，并且满足以下条件时，服务端会发起回调：

1. 上传成功
2. 该数据上传允许公开触发 webhook
3. webhook 全局开关为开启
4. 当前 endpoint 开关为开启
5. 该 endpoint 已订阅该 `server + data_type + user_id`

如果全局开关关闭，所有 endpoint 都不会收到回调。  
如果某个 endpoint 关闭，只有这个 endpoint 不会收到回调。

## 7. 回调请求格式

服务端会对 `callbackUrl` 发起：

- 方法：`POST`
- Body：空

默认请求头会包含：

```http
User-Agent: Haruki-Toolbox-Backend/<version>
```

如果 endpoint 配置了 `bearer`，还会附带：

```http
Authorization: Bearer <bearer>
```

## 8. callbackUrl 占位符

回调地址支持以下占位符：

- `{user_id}`
- `{server}`
- `{data_type}`

示例：

```text
https://example.com/apiwebhook/{server}/{data_type}/{user_id}
```

当用户 `123456789` 在 `jp` 区服上传 `suite` 数据时，实际请求地址会变成：

```text
https://example.com/apiwebhook/jp/suite/123456789
```

## 9. callbackUrl 限制

服务端会校验 callback URL，以下情况会被拒绝或跳过：

- 非 `http` / `https`
- 带用户名密码
- `localhost`
- 内网 IP
- 回 DNS 后落到内网/回环地址

因此 callback URL 必须是公网可访问地址。

## 10. 接入建议

建议接入方这样做：

1. 从管理员获取当前有效 token 后，再开始调用订阅接口
2. 用该 token 调用订阅接口建立订阅
3. 服务端回调时，以回调是否收到为准，不要依赖同步确认
4. 如果管理员更新了 `credential`，及时替换成新的 token
5. 如果需要轮换授权，联系管理员更新 endpoint 并下发新 token

## 11. 常见问题

### 11.1 已订阅但没有收到回调

优先检查：

1. 全局 webhook 开关是否开启
2. endpoint 开关是否开启
3. 上传是否成功
4. callback URL 是否为公网有效地址
5. 接口是否订阅到了正确的 `server / data_type / user_id`

### 11.2 什么时候需要更新 token

通常只有以下情况需要更新：

- 管理员修改了 endpoint 的 `credential`
- 管理员重新创建了新的 endpoint
- 服务端切换了 webhook JWT secret

### 11.3 bearer 和 token 有什么区别

- `token`: 用于调用 Haruki 的 `/api/webhook/...` 管理订阅接口
- `bearer`: Haruki 在回调你的 `callbackUrl` 时，附带给你的 Authorization 凭证
