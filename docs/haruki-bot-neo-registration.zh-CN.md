# Haruki Bot Neo 注册 API 对接文档

本文档描述 Haruki Bot Neo 注册流程的前端对接方式。

---

## 前置条件

- 所有 `/send-mail` 和 `/register` 接口**需要用户登录**（通过 Kratos session 或 Auth Proxy 认证）。
- 请求头需携带有效的 `Authorization`（Bearer token）或 `X-Session-Token` 或 session cookie。
- `/status` 接口无需登录，可用于前端判断是否展示注册入口。

---

## 统一响应格式

所有接口返回统一信封结构：

```json
{
  "status": 200,
  "message": "描述信息",
  "updatedData": { ... }
}
```

- `status` — HTTP 状态码
- `message` — 描述信息
- `updatedData` — 响应数据（可选，部分接口为 `null`）

---

## 1. 查询注册状态

判断后端是否开启了注册功能，前端可据此决定是否显示注册入口。

### 请求

```
GET /api/haruki-bot-neo/status
```

无需认证，无需请求体。

### 响应

**200 OK**

```json
{
  "status": 200,
  "message": "ok",
  "updatedData": {
    "enabled": true
  }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `enabled` | `boolean` | `true` 表示注册开放，`false` 表示注册关闭 |

---

## 2. 发送验证码

向指定 QQ 邮箱（`{qq_number}@qq.com`）发送 6 位数字验证码，验证码有效期 10 分钟。

### 请求

```
POST /api/haruki-bot-neo/send-mail
Content-Type: application/json
```

```json
{
  "qq_number": 123456789
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `qq_number` | `number` (int64) | 是 | QQ 号码，必须大于 0 |

### 响应

**200 OK** — 验证码已发送

```json
{
  "status": 200,
  "message": "verification code sent"
}
```

**400 Bad Request** — 参数缺失或无效

```json
{
  "status": 400,
  "message": "missing qq_number"
}
```

**401 Unauthorized** — 未登录

```json
{
  "status": 401,
  "message": "missing token"
}
```

**403 Forbidden** — 注册功能已关闭

```json
{
  "status": 403,
  "message": "registration is currently disabled"
}
```

**429 Too Many Requests** — 触发速率限制

```json
{
  "status": 429,
  "message": "too many verification emails sent to this QQ (retry after 3540s)"
}
```

响应头包含 `Retry-After`（秒），表示剩余冷却时间。

### 速率限制

| 维度 | 限制 | 窗口 |
|------|------|------|
| 每 IP | 20 次 | 60 分钟 |
| 每 QQ 号 | 5 次 | 60 分钟 |

---

## 3. 注册 / 凭据重置

使用 QQ 号和收到的验证码完成注册。若该 QQ 号已注册，则更新凭据并返回新的 `credential` JWT。注册或重置成功后返回 `bot_id` 和签名后的 `credential` JWT。

### 请求

```
POST /api/haruki-bot-neo/register
Content-Type: application/json
```

```json
{
  "qq_number": 123456789,
  "verification_code": "123456"
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `qq_number` | `number` (int64) | 是 | 与发送验证码时相同的 QQ 号码 |
| `verification_code` | `string` | 是 | 收到的 6 位数字验证码 |

### 响应

**201 Created** — 新注册成功

```json
{
  "status": 201,
  "message": "registration successful",
  "updatedData": {
    "bot_id": "10042042",
    "credential": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."
  }
}
```

**200 OK** — 已注册 QQ 号凭据重置成功

```json
{
  "status": 200,
  "message": "credential reset successful",
  "updatedData": {
    "bot_id": "10042042",
    "credential": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."
  }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `bot_id` | `string` | 8 位数字 Bot ID（已注册时返回原有 Bot ID） |
| `credential` | `string` | HS256 签名的 JWT，包含明文 credential，用于后续 Haruki-Cloud 认证 |

**400 Bad Request** — 验证码错误、过期或参数缺失

```json
// 验证码错误
{ "status": 400, "message": "verification code is invalid" }

// 验证码过期
{ "status": 400, "message": "verification code not found or expired" }

// 尝试次数过多（验证码已作废，需重新发送）
{ "status": 400, "message": "too many verification attempts, please request a new code" }

// 参数缺失
{ "status": 400, "message": "missing qq_number" }
{ "status": 400, "message": "missing verification_code" }
```

**401 Unauthorized** — 未登录

**403 Forbidden** — 注册功能已关闭

**429 Too Many Requests** — 触发速率限制

### 速率限制

| 维度 | 限制 | 窗口 |
|------|------|------|
| 每 QQ 号 | 5 次 | 10 分钟 |

### Credential JWT 格式

返回的 `credential` 是一个 HS256 JWT，Payload 内容：

```json
{
  "bot_id": "10042042",
  "credential": "base64url-encoded-32-bytes"
}
```

此 JWT 由后端配置项 `haruki_bot.credential_sign_token` 签名，与 Haruki-Cloud 共享同一密钥。客户端应安全保存此 JWT，后续用于 Haruki-Cloud 的 Bot 认证。

---

## 前端对接流程

```
┌─────────────┐                          ┌────────────────┐
│   前端/客户端  │                          │  Haruki Toolbox │
│             │                          │    Backend      │
└──────┬──────┘                          └───────┬────────┘
       │                                         │
       │  1. GET /api/haruki-bot-neo/status       │
       │────────────────────────────────────────>│
       │         { enabled: true }               │
       │<────────────────────────────────────────│
       │                                         │
       │  2. POST /api/haruki-bot-neo/send-mail  │
       │     { qq_number: 123456789 }            │
       │────────────────────────────────────────>│
       │         200 OK                          │──── 发送邮件到
       │<────────────────────────────────────────│     123456789@qq.com
       │                                         │
       │  3. 用户从 QQ 邮箱获取验证码               │
       │                                         │
       │  4. POST /api/haruki-bot-neo/register   │
       │     { qq_number: 123456789,             │
       │       verification_code: "123456" }     │
       │────────────────────────────────────────>│
       │         201 Created / 200 OK            │
       │         { bot_id, credential(JWT) }     │
       │<────────────────────────────────────────│
       │                                         │
       │  5. 保存 bot_id 和 credential JWT         │
       │     用于后续 Haruki-Cloud Bot 认证         │
       └─────────────────────────────────────────┘
```

### 前端注意事项

1. **先检查 `/status`**：如果 `enabled` 为 `false`，隐藏注册入口或显示「注册暂未开放」提示。
2. **发送验证码后**：提示用户检查 QQ 邮箱，验证码 10 分钟内有效。
3. **处理 429 响应**：读取 `Retry-After` 头展示倒计时，禁用发送按钮。
4. **区分 201 和 200 响应**：`201 Created` 表示新注册，`200 OK` 表示已注册 QQ 号的凭据重置。两者均返回 `bot_id` 和新 `credential`。
5. **保存注册结果**：`bot_id` 和 `credential` JWT 是一次性返回的，credential 明文不会再次出现，务必提醒用户妥善保存。凭据重置后旧凭据立即失效。
6. **验证码输入错误 5 次后**：验证码自动作废，需重新发送。

---

## 错误消息汇总

| 消息 | 含义 |
|------|------|
| `missing qq_number` | 请求体缺少 `qq_number` 字段 |
| `missing verification_code` | 请求体缺少 `verification_code` 字段 |
| `invalid request body` | 请求体不是合法的 JSON |
| `registration is currently disabled` | 后端关闭了注册功能 |
| `verification code sent` | 验证码发送成功 |
| `verification code is invalid` | 验证码不正确 |
| `verification code not found or expired` | 验证码不存在或已过期 |
| `too many verification attempts, please request a new code` | 验证码输错超过 5 次，已作废 |
| `registration successful` | 新注册成功 |
| `credential reset successful` | 已注册 QQ 号凭据重置成功 |
| `too many requests from this IP` | IP 维度触发速率限制 |
| `too many verification emails sent to this QQ` | QQ 号维度触发发送速率限制 |
| `too many registration attempts` | QQ 号维度触发注册速率限制 |
| `registration service unavailable` | 后端服务异常（数据库 / Redis 不可用等） |
| `failed to send verification email` | 邮件发送失败 |
