# Admin 用户管理接口补充说明

本文档记录当前 admin 用户管理中容易遗漏的集成管理接口，供后台页面对接使用。

前端集中对接说明见 [`docs/frontend-integration-notes.zh-CN.md`](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/docs/frontend-integration-notes.zh-CN.md)。

## 主社交平台管理

主社交平台信息用于标记用户绑定的主要 QQ / QQ Bot / Discord / Telegram 身份。后台用户详情页或 integrations 区域可以直接使用以下接口。

### 查询

```http
GET /admin/users/:target_user_id/social-platform
```

响应中的 `exists` 表示该用户是否已有主社交平台信息。

### 创建或更新

```http
PUT /admin/users/:target_user_id/social-platform
Content-Type: application/json

{
  "platform": "qq",
  "userId": "123456789",
  "verified": true
}
```

字段说明：

- `platform`: `qq` / `qq_bot` / `discord` / `telegram`
- `userId`: 平台用户 ID
- `verified`: 可省略，默认 `true`

如果同一个 `platform + userId` 已绑定给其它用户，会返回 conflict。

### 清除

```http
DELETE /admin/users/:target_user_id/social-platform
```

该接口只清除主社交平台，不影响授权社交平台列表。
