# 游戏账号数据授权说明

游戏账号数据授权允许用户把自己已经验证绑定的账号数据授权给另一个 Toolbox 用户读取。第一版只支持已上传并存储的 `suite` / `mysekai` 数据，不支持 `profile` 实时查询，也不影响 public API 或 private token API。

前端集中对接说明见 [`docs/frontend-integration-notes.zh-CN.md`](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/docs/frontend-integration-notes.zh-CN.md)。

## 权限规则

- 授权创建者必须拥有对应 `server + game_user_id` 的 verified binding
- 被授权用户必须存在且未被封禁
- 不允许授权给自己
- `expiresAt` 必填，必须是未来时间
- 过期授权不会生效；服务启动时会清理已过期授权

## 用户端接口

所有接口都需要登录、当前用户未封禁、邮箱已验证，并且 `:toolbox_user_id` 必须是当前登录用户。

### 列出我创建的授权

```http
GET /api/user/:toolbox_user_id/game-account-grants
```

### 列出别人授权给我的数据

```http
GET /api/user/:toolbox_user_id/game-account-grants/received
```

### 创建或更新授权

```http
PUT /api/user/:toolbox_user_id/game-account-grants/:server/:game_user_id/:data_type/:grantee_user_id
Content-Type: application/json

{
  "expiresAt": "2026-07-01T00:00:00Z"
}
```

字段说明：

- `server`: `jp` / `en` / `tw` / `kr` / `cn`
- `game_user_id`: 游戏账号 ID
- `data_type`: `suite` / `mysekai`
- `grantee_user_id`: 被授权的 Toolbox 用户 ID

### 撤销授权

```http
DELETE /api/user/:toolbox_user_id/game-account-grants/:server/:game_user_id/:data_type/:grantee_user_id
```

## 数据读取影响

以下入口支持 owner 或有效授权用户读取：

- `GET /api/user/:toolbox_user_id/game-account/:server/:game_user_id/:data_type`
- `GET /api/oauth2/game-data/:server/:data_type/:user_id`

OAuth2 入口仍要求 token scope 包含 `game-data:read`。授权不会改变 public API、private token API、Redis 数据缓存 key 或 Mongo 数据形状。
