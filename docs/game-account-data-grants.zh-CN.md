# 游戏账号数据授权说明

游戏账号数据授权允许用户把自己已经验证绑定的账号数据授权给另一个 Toolbox 用户读取。支持 `suite` / `mysekai`（已上传并存储的数据）和 `profile`（实时向游戏 API 查询），不影响 public API 或 private token API。

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
- `data_type`: `suite` / `mysekai` / `profile`
- `grantee_user_id`: 被授权的 Toolbox 用户 ID

### 撤销授权

```http
DELETE /api/user/:toolbox_user_id/game-account-grants/:server/:game_user_id/:data_type/:grantee_user_id
```

## 可访问账号聚合接口

选择器不应该自己去合并「我的绑定」和「收到的授权」，也不应该复刻后端的有效性判断。以下接口返回当前用户**现在就能读到数据**的全部游戏账号，本人绑定与有效授权已经合并、去重、预过滤。

```http
GET /api/user/:toolbox_user_id/accessible-game-accounts
```

鉴权与绑定接口一致（登录、未封禁、`:toolbox_user_id` 必须是当前登录用户）。

```json
{
  "generatedAt": "2026-08-25T12:00:00Z",
  "total": 2,
  "accounts": [
    {
      "server": "jp",
      "gameUserId": "123456789",
      "ownership": "own",
      "verified": true,
      "isDefault": true,
      "capabilities": { "suite": {}, "mysekai": {}, "profile": {}, "recommend": {} },
      "owner": null
    },
    {
      "server": "jp",
      "gameUserId": "987654321",
      "ownership": "granted",
      "verified": true,
      "isDefault": false,
      "capabilities": {
        "suite": { "expiresAt": "2026-09-30T00:00:00Z" },
        "mysekai": { "expiresAt": "2026-09-01T00:00:00Z" },
        "recommend": { "expiresAt": "2026-09-30T00:00:00Z" }
      },
      "owner": { "userId": "b7e2...", "name": "某某", "avatarPath": "/avatars/xx.png" }
    }
  ]
}
```

语义约定：

- **`capabilities` 是唯一的功能门控依据。** key 在不在里面，决定该账号能不能用于对应功能；value 携带该能力的到期时间，本人账号无到期（空对象）。前端不要按 `ownership` 硬编码功能可用性；以后新增可授权类型，选择器零改动。
- `recommend` 是**派生能力**，不是新的授权类型，表示 `recommend-data` 可用。它由 `recommend-data` 基础模式所读的数据类型推导（当前为 `suite`），到期时间取所依赖能力中最早的。烤森组卡模式额外需要 `mysekai`，前端同时检查这两个 key 即可——这正是读取路径本身的判断。
- `profile` 可授权，但**每种类型各自一行**：拿到 `suite` 授权不代表拿到 `profile`。它与另外两种的区别是实时向游戏 API 查询，而不是读已存储的数据 —— 因此被授权方的请求会打到 owner 账号的上游接口。
- 未验证的本人绑定仍会返回（`verified: false`），但 `capabilities` 为空对象：它确实读不了数据，前端应展示为「存在但不可用」而不是隐藏。
- 授权条目已按读取时的同一组谓词预过滤：绑定存在且 verified、绑定当前所有者仍是授权发起者、双方均未封禁、授权未过期。列表与读取结果因此保持一致，但仍存在「列出之后、读取之前授权失效」的窗口，前端遇到 `403/404` 时应重新拉取本接口而不是弹全局错误。
- 同一账号的多条授权（不同 `dataType`）合并为一个条目；自己拥有的账号即使同时被授权，也只按 `own` 返回一条。
- 排序：`own` 在前（默认账号优先），`granted` 按最近授权时间倒序。

## 功能与能力对照

前端页面到 `capabilities` key 的映射。后端不持有这张表 —— 它是前端功能与授权类型之间的约定，改动前端功能时需要同步维护。

| 前端功能 | 需要的 capability |
| --- | --- |
| events / training / cards / music | `suite` |
| 烤森相关 | `mysekai` |
| 组卡（deck recommend） | `recommend` |
| 烤森组卡 | `recommend` **且** `mysekai` |
| player-profile | `profile` |

功能门控只看 `capabilities`，不要按 `ownership` 硬编码 —— 这样以后新增可授权类型，选择器零改动。

## 数据读取影响

以下入口支持 owner 或有效授权用户读取：

- `GET /api/user/:toolbox_user_id/game-account/:server/:game_user_id/:data_type`
- `GET /api/user/:toolbox_user_id/game-account/:server/:game_user_id/recommend-data`
- `GET /api/oauth2/game-data/:server/:data_type/:user_id`

`recommend-data` 的授权口径与通用数据入口一致：默认（组卡）模式需要 `suite`，`mode=mysekai` 需要 `suite` 且 `mysekai`。绑定不存在或未验证返回 `404`，存在但无权限返回 `403`。

浏览器 owned-account 入口中，**只有 `:data_type` 这一条**（不含 `recommend-data`）的 `suite` / `mysekai` 读取可附带 `known_upload_time=<上次完整响应中的 upload_time>`。数据未变化时返回 `304 Not Modified`；若使用 `key` 过滤，必须同时包含 `upload_time`。该优化在所有权或授权校验完成后运行，不改变授权范围及错误语义。

OAuth2 入口仍要求 token scope 包含 `game-data:read`。授权不会改变 public API、private token API、Redis 数据缓存 key 或 Mongo 数据形状。

## 授权是只读的

**授权只赋予读取权限，不赋予写入权限。**

`POST /api/oauth2/game-data/:server/:data_type/:user_id`（代理上传，需 `game-data:write`）只对用户**自己拥有**的绑定生效。被授权方持有再合法的 token，对通过授权拿到的账号发起上传也会得到 `403`。

| 访问来源 | 读取 | 上传 |
|---|---|---|
| 自己拥有的绑定 | ✅ | ✅ |
| 通过授权获得 | ✅ | ❌ `403` |

理由：授权的语义是"让你看我的数据"，而不是"让你改我的数据"。如果两者不分，被授权方就能覆盖 granter 的存档，而 granter 在共享数据时并没有同意这件事。
