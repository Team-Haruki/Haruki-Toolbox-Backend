# HarukiProxy v3 客户端对接：OAuth2 上传

适用：本分支实现的 OAuth2 v3 协议。上线前须完成数据库迁移及后端部署；不能据此认定生产已切换。固定密钥 v3 从未正式发布，不提供兼容降级。

## 1. 客户端必须调整的内容

1. 删除 X-Haruki-Toolbox-Secret 和工具箱外层 AES-GCM 封装，不再使用 v3_secret/v3_unpack_key。
2. 使用公开 OAuth2 客户端授权码 + PKCE S256，经系统浏览器登录工具箱并授权。APK/桌面程序不能内置 client_secret。
3. 上传时使用 Authorization: Bearer <access_token>；发送原始游戏载荷，保留游戏协议自身的内容与编码。
4. 保留严格版本/平台 UA、请求 ID、updatedData 错误解析、上传暂停及有限重试。
5. 操作者可以上传本人账号或别人授予其 write 权限的账号；不能用 read grant 上传。

完整 OAuth2 端点和换取/刷新 token 的参数见 [OAuth2 接入](oauth2-integration.zh-CN.md)。需先登记 client_id、准确的回调 URI 和所需 scope；回调校验 state，code_verifier 只用于当前授权流程。

没有系统浏览器的部署（例如无界面的服务器上运行代理）可以改用设备授权（同一文档 §4A）：程序在本机显示用户码，用户在自己的浏览器里批准，拿到的 token 与授权码流程相同，本文其余要求不变。前提是管理员为该公开客户端开通设备授权许可并设 `devicePolicy.allowWrite=true`（否则经设备授权申请 game-data:write 会得到 `invalid_scope`），且请求的 scope 必须含 user:read。

## 2. Scope 与目标账号

| scope | 用途 |
| --- | --- |
| game-data:write | 上传必需 |
| offline_access | 获取刷新能力，需用户实际同意 |
| bindings:read | 使用上传目标账号列表时必需 |

不需要 game-data:read 就可以上传。写权限不隐含读取已有数据的权限。

```http
GET /api/oauth2/game-data/upload-targets
Authorization: Bearer <access_token>
```

该接口要求 game-data:write 和 bindings:read。成功响应：

```json
{"status":200,"message":"ok","updatedData":[
  {"server":"jp","gameUserId":"123","dataType":"suite"},
  {"server":"jp","gameUserId":"456","dataType":"mysekai","expiresAt":"2026-11-01T00:00:00Z"}
]}
```

无 expiresAt 表示本人绑定；存在时为有效授权到期时间。列表不返回游戏数据、所有者私人信息或用户凭据。游戏 ID 必须按字符串处理。列表是提示，上传时会再次鉴权；账号封禁、区域策略等仍可能使上传被拒绝。

## 3. 上传请求

```http
POST /harukiproxy/v3/jp/456/mysekai/upload
Authorization: Bearer <access_token>
User-Agent: HarukiProxy/v3.0.0-preview.2+build.8 (platform=Android; os_version=15; os_arch=arm64; app_arch=arm64)
Content-Type: application/octet-stream

<原始游戏载荷字节>
```

别名：`/api/harukiproxy/v3/:server/:user_id/:data_type/upload`。

其他第三方应用使用通用入口：`POST /api/oauth2/game-data/:server/:data_type/:user_id`。注意它与 HarukiProxy 的路径参数顺序不同。通用入口不要求伪装 HarukiProxy UA；其现有响应契约与专用 v3 的结构化错误不同，不能混用解析假设。

有效 token 对应实际操作者 A。后端检查目标账号为 A 所有，或当前所有者授予 A 对该 server/game ID/data type 的有效 write 权限；并在持久化前重新检查。载荷账号必须与目标一致。客户端不得传 owner_user_id 来决定所属用户。

不自动回退 v2、固定密钥协议或其他入口；不向重定向目标转发 bearer token。强制使用 HTTPS。

## 4. UA 契约

格式：`HarukiProxy/v<SemVer> (platform=<platform>; <可选字段>)`。

- 必须带 platform；支持 Windows、macOS、Android、iOS、Linux，未知值归类 unknown。
- 完整 SemVer：3.0.0、3.0.0-preview、3.0.0-preview.2、3.0.0-dev.12、3.0.0-beta.1、3.0.0-rc.1，支持 +build 元信息。
- 无预发布后缀为 stable；预发布第一段是 channel。禁止数字标识前导零，禁止用 stable 作为预发布 channel。
- 可选 os_version、os_build、os_arch、app_arch；架构支持 x64/arm64/x86/arm，未知归类 unknown。
- UA 最大 512 字节、可打印 ASCII；版本最大 128 字节；字段名满足 `[a-z_]{1,32}`，值满足 `[A-Za-z0-9._+-]{1,64}`。重复键、空字段和非法格式拒绝。
- 合法未知字段被忽略。不发送设备唯一标识、用户名、序列号或 token。

版本策略按 channel 配置最低版本。UA 是客户端自报诊断信息，不是身份凭据；OAuth client_id 才是 token 所属客户端标识，也不证明客户端二进制或载荷真实性。

## 5. 响应与重试

```json
{"status":200,"message":"Upload successful","updatedData":{"request_id":"<服务端 UUID>","retryable":false}}
```

```json
{"status":403,"message":"failed to process upload","updatedData":{"error_code":"upload_not_allowed","request_id":"<服务端 UUID>","retryable":false}}
```

成功和失败均从 X-Request-ID 或 updatedData.request_id 提取请求 ID；服务端生成 ID，不信任客户端自带值。使用 HTTP 状态与 error_code，不依赖 message 文案。网关/网络错误不保证 JSON。

| HTTP / error_code | 客户端处理 |
| --- | --- |
| 401 invalid_token | 串行刷新一次并重试一次；失败暂停并重新授权 |
| 403 insufficient_scope | 重新请求用户授权，不自动重试 |
| 403 upload_not_allowed | 刷新目标列表并提示权限失效，不自动重试 |
| 400 invalid_client_metadata | 修正 UA |
| 400 client_channel_disabled / client_version_unsupported | 更新客户端或使用允许渠道；updatedData.client_policy 提供渠道及最低版本 |
| 400 invalid_upload_payload | 检查目标与载荷，不重试 |
| 503 temporarily_unavailable | 仅 retryable=true 时有限退避 |
| 500 internal_error | 可能已发生写入，不盲目重试 |
| 429（边缘/限流层） | 尊重 Retry-After，有限退避 |

刷新成功后使用新 access token，并保存返回的新 refresh token（若提供）。刷新失败或撤销授权后暂停上传。网络超时不是“未写入”的证明；本协议没有承诺幂等上传，禁止无限重试。refresh token 使用平台安全存储，禁止进入日志、URL 或共享配置。

## 6. 联调验收

- 本人上传、write grant 上传；read-only grant 与 scope 不足分别拒绝。
- suite/mysekai、区服和账号 ID 不串权；只写用户不能读取数据。
- 撤销/到期/封禁/绑定转移后拒绝；客户端禁用及 token 撤销生效。
- 原始载荷可进入游戏解析；旧外层封装不能当成新版数据发送。
- preview/dev/beta/rc 版本及各平台 UA；成功/失败请求 ID 与错误暂停行为。
- 401 刷新并发串行化、最多一次刷新重试、跨域重定向不泄露 token。

v2 截止：2026-11-01 00:00（UTC+8）。启用新版前先确认后端部署、迁移、OAuth 客户端登记和真实设备联调完成。
