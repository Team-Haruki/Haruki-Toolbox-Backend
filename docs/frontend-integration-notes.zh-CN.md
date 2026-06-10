# 前端对接补充说明

本文档记录近期后端新增或已存在但前端容易遗漏的对接点。

## Admin OAuth2 Client Webhook

位置建议：OAuth2 client 详情页的 integrations / webhook 区域。

接口：

- `GET /admin/oauth-clients/:client_id/webhooks`
- `POST /admin/oauth-clients/:client_id/webhooks`
- `PUT /admin/oauth-clients/:client_id/webhooks/:webhook_id`
- `DELETE /admin/oauth-clients/:client_id/webhooks/:webhook_id`

创建 payload：

```json
{
  "callbackUrl": "https://example.com/oauth-webhook/{server}/{data_type}/{user_id}",
  "bearer": "optional-callback-bearer",
  "enabled": true
}
```

更新 payload 支持局部字段：

```json
{
  "callbackUrl": "https://example.com/new-webhook/{server}/{data_type}/{user_id}",
  "enabled": false,
  "clearBearer": true
}
```

列表响应里 `bearerSet` 只表示是否配置了 bearer，不会返回 bearer 明文。callback URL 支持 `{server}`、`{data_type}`、`{user_id}` 占位符。

## Admin 主社交平台

位置建议：admin 用户详情页的 integrations 区域，和游戏账号绑定、授权社交平台放在一起。

接口：

- `GET /admin/users/:target_user_id/social-platform`
- `PUT /admin/users/:target_user_id/social-platform`
- `DELETE /admin/users/:target_user_id/social-platform`

保存 payload：

```json
{
  "platform": "qq",
  "userId": "123456789",
  "verified": true
}
```

`platform` 可选值：`qq`、`qq_bot`、`discord`、`telegram`。`verified` 可省略，默认按已验证保存。若接口返回 conflict，表示同一平台账号已绑定到其它 Toolbox 用户。

## 用户账号数据授权

位置建议：用户自己的游戏账号详情页，针对每个已验证绑定账号提供“数据授权”管理入口。

接口：

- `GET /api/user/:toolbox_user_id/game-account-grants`
- `GET /api/user/:toolbox_user_id/game-account-grants/received`
- `PUT /api/user/:toolbox_user_id/game-account-grants/:server/:game_user_id/:data_type/:grantee_user_id`
- `DELETE /api/user/:toolbox_user_id/game-account-grants/:server/:game_user_id/:data_type/:grantee_user_id`

创建或更新 payload：

```json
{
  "expiresAt": "2026-07-01T00:00:00Z"
}
```

前端约束：

- 只允许 `data_type=suite|mysekai`
- 不提供永久授权，`expiresAt` 必须是未来时间
- 不允许授权给自己
- 当前登录用户必须邮箱已验证
- 被授权用户必须是未封禁 Toolbox 用户

## 数据读取变化

以下入口现在允许 owner 或有效授权用户读取 `suite/mysekai`：

- `GET /api/user/:toolbox_user_id/game-account/:server/:game_user_id/:data_type`
- `GET /api/oauth2/game-data/:server/:data_type/:user_id`

注意：

- `profile` 仍只允许 owner 自己读取，不支持授权。
- public API 和 private token API 不支持此授权模型。
- OAuth2 读取仍要求 token scope 包含 `game-data:read`。
