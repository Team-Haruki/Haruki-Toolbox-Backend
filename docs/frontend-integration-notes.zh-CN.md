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

查询响应中的 `exists` 表示该用户是否已有主社交平台信息。`DELETE` 只清除主社交平台，不影响授权社交平台列表。

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

## 游戏账号选择器数据源

站内所有游戏账号选择器统一从下面这个聚合接口取数，不要再用 `get-settings` 的 `gameAccountBindings` 拼授权账号：

```http
GET /api/user/:toolbox_user_id/accessible-game-accounts
```

它返回当前用户能读到数据的全部账号（本人绑定 + 有效授权），已按读取时的同一组谓词预过滤。字段与语义见 [`docs/game-account-data-grants.zh-CN.md`](game-account-data-grants.zh-CN.md) 的「可访问账号聚合接口」。

前端约束：

- 功能门控只看 `capabilities`，不要按 `ownership` 硬编码。events/training/cards/music 需要 `suite`，烤森相关需要 `mysekai`，组卡需要 `recommend`（烤森组卡再额外检查 `mysekai`），player-profile 需要 `profile`。
- `ownership: "granted"` 的条目带 `owner.name` / `owner.avatarPath`，用于在选择器里区分「他人授权的账号」。
- 授权增删、绑定增删之后需要失效重拉；读取遇到 `403/404` 时也应重拉一次再提示「授权已失效」。
- `get-settings` 的 `gameAccountBindings` 继续服务账号管理页（隐私设置等），两者互不干扰。

## 数据读取变化

以下入口现在允许 owner 或有效授权用户读取 `suite/mysekai`：

- `GET /api/user/:toolbox_user_id/game-account/:server/:game_user_id/:data_type`
- `GET /api/user/:toolbox_user_id/game-account/:server/:game_user_id/recommend-data`
- `GET /api/oauth2/game-data/:server/:data_type/:user_id`

注意：

- `profile` 仍只允许 owner 自己读取，不支持授权。
- `recommend-data` 默认模式需要 `suite`，`mode=mysekai` 需要 `suite` 且 `mysekai`；绑定不存在或未验证返回 `404`（此前未验证绑定返回 `400`）。
- public API 和 private token API 不支持此授权模型。
- OAuth2 读取仍要求 token scope 包含 `game-data:read`。
