# 前端对接补充说明

本文档只记录**还没接完**的后端契约，以及前端需要长期遵守的约束。已经接完的功能不留说明，接口形状以 `api/testdata/routes.golden` 和对应 handler 为准。

## 游戏账号选择器数据源

**状态：后端已上线，前端待接入。**

站内所有游戏账号选择器统一从下面这个聚合接口取数，不要再用 `get-settings` 的 `gameAccountBindings` 拼授权账号：

```http
GET /api/user/:toolbox_user_id/accessible-game-accounts
```

它返回当前用户能读到数据的全部账号（本人绑定 + 有效授权），已按读取时的同一组谓词预过滤。字段与语义见 [`docs/game-account-data-grants.zh-CN.md`](game-account-data-grants.zh-CN.md) 的「可访问账号聚合接口」。

前端约束：

- 功能门控只看 `capabilities`，不要按 `ownership` 硬编码。
- `ownership: "granted"` 的条目带 `owner.name` / `owner.avatarPath`，用于在选择器里区分「他人授权的账号」。
- 授权增删、绑定增删之后需要失效重拉；读取遇到 `403/404` 时也应重拉一次再提示「授权已失效」。
- `get-settings` 的 `gameAccountBindings` 继续服务账号管理页（隐私设置等），两者互不干扰。

### 功能与能力对照

这张表是前端页面到 `capabilities` key 的映射，后端不持有它，改动前端功能时需要同步维护：

| 前端功能 | 需要的 capability |
| --- | --- |
| events / training / cards / music | `suite` |
| 烤森相关 | `mysekai` |
| 组卡（deck recommend） | `recommend` |
| 烤森组卡 | `recommend` **且** `mysekai` |
| player-profile | `profile` |

## 数据读取变化

以下入口允许 owner 或有效授权用户读取 `suite/mysekai`：

- `GET /api/user/:toolbox_user_id/game-account/:server/:game_user_id/:data_type`
- `GET /api/user/:toolbox_user_id/game-account/:server/:game_user_id/recommend-data`
- `GET /api/oauth2/game-data/:server/:data_type/:user_id`

注意：

- `profile` 仍只允许 owner 自己读取，不支持授权。
- `recommend-data` 默认模式需要 `suite`，`mode=mysekai` 需要 `suite` 且 `mysekai`；绑定不存在或未验证返回 `404`，存在但无权限返回 `403`。
- public API 和 private token API 不支持此授权模型。
- OAuth2 读取仍要求 token scope 包含 `game-data:read`。
