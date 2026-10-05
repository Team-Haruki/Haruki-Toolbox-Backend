# HarukiProxy 上传

客户端开发者请先阅读 [HarukiProxy v3 对接指南](harukiproxy-v3-client-integration.zh-CN.md)；本文保留后端配置、部署与管理统计说明。

v3 上传入口（POST）：

- `/harukiproxy/v3/:server/:user_id/:data_type/upload`
- `/api/harukiproxy/v3/:server/:user_id/:data_type/upload`

v3 **只接受包含 platform 的结构化 UA**，无旧格式兼容开关。`X-Haruki-Toolbox-Secret` 必须使用 v3 认证密钥。旧配置 `haruki_proxy.user_agent` / `version` 仅控制 v2。

```http
Content-Type: application/octet-stream
User-Agent: HarukiProxy/v3.0.0-preview.1+gabcdef1 (platform=Android; os_version=15; os_arch=arm64; app_arch=arm64)
```

版本为小写 `v` 前缀加严格 SemVer，支持 `-preview`、`-preview.1`、`-dev.12`、`-beta.2`、`-rc.1` 和 `+build`。产品名固定为 HarukiProxy。

- `platform` 必填：Windows/macOS/Android/iOS/Linux/Unknown；指运行代理的设备。
- `os_version`、`os_build`、`os_arch`、`app_arch` 可选。架构为 x64/arm64/x86/arm/unknown。
- 字段值仅用 ASCII 字母、数字及 `._+-`，每项最多 64 字节；版本最多 128 字节，UA 总长最多 512 字节。
- 字段顺序可变；重复键、控制字符、嵌套括号或缺失平台拒绝。合法未知扩展键忽略，未知平台/架构归为 unknown。
- 服务端只保存白名单字段，UA 不作为身份凭据。

新增 `haruki_proxy.v3_client_policy`（重启生效）：

```yaml
v3_client_policy:
  allowed_channels: [stable, preview, beta, rc, dev]
  minimum_versions:
    stable: "3.0.0"
    preview: "3.0.0-preview"
    beta: "3.0.0-beta"
    rc: "3.0.0-rc"
    dev: "3.0.0-dev"
```

未配置时使用以上默认值；显式空列表、未知键、缺失最低版本或渠道不匹配会导致启动失败。各渠道独立比较最低版本，构建元数据不参与比较。`require_platform` 不再是可用配置。

v3 使用独立配置，不回退到旧密钥：

| YAML 配置（`haruki_proxy` 下） | 环境变量 |
| --- | --- |
| `v3_secret` | `HARUKI_PROXY_V3_SECRET` |
| `v3_unpack_key` | `HARUKI_PROXY_V3_UNPACK_KEY` |

环境变量优先；配置变更需要重启后端。管理员运行时配置中的旧版
`harukiProxySecret` / `harukiProxyUnpackKey` 不影响 v3。两项 v3 密钥均需配置。
密钥应放在部署端配置中，不提交到仓库。

请求体协议保持 AES-GCM：解包密钥去除首尾空白后做 SHA-256，得到 AES-256
密钥；请求体依次为 12 字节 nonce、密文及 16 字节认证标签。AAD 保持
`:server|:user_id|:data_type`，各部分使用 URL 参数原始文本，v3 不改变 AAD。

旧入口 `/harukiproxy/:server/:user_id/:data_type/upload` 及对应 `/api/` 别名，
在北京时间 **2026-11-01 00:00:00（UTC+8）** 起返回 HTTP 410 Gone，
包括截止时刻本身。停用判断在每次请求时执行，不依赖定时任务或服务器时区。
截止前继续使用旧 `secret` / `unpack_key`；旧入口响应携带 `Sunset` 头。
v3 不受该截止时间影响。现有 Oathkeeper HarukiProxy 通配规则已覆盖 v3。

## 响应与客户端开关

所有响应统一使用工具箱既有 `updatedData` 外层，不提供 `data` 别名：

```json
{"status":400,"message":"Client version is below minimum required","updatedData":{"error_code":"client_version_unsupported","request_id":"0cfb45ef-c262-4d74-bc57-45f999ec2709","retryable":false,"client_policy":{"channel":"preview","minimum_version":"3.0.0-preview.2"}}}
```

成功响应 status=200，updatedData 包含 request_id 和 retryable=false，无 error_code。`X-Request-ID` 与 updatedData.request_id 一致，由服务端生成，不复用客户端头。

| HTTP | error_code | 处理 |
| --- | --- | --- |
| 400 | invalid_client_metadata | 修正 UA，不重试 |
| 400 | client_version_unsupported / client_channel_disabled | 客户端暂停并提示升级/渠道不可用 |
| 401 | invalid_client_credentials | 客户端暂停；不附带版本策略 |
| 403 | upload_not_allowed | 统一权限拒绝 |
| 400 | payload_decryption_failed / invalid_upload_payload | 不重试相同内容 |
| 410 | protocol_retired | 停止旧入口请求 |
| 500 | internal_error | 尊重 retryable；提交结果不明时 false |
| 503 | temporarily_unavailable | 未开始写入的临时依赖故障可重试 |

现有边缘限流若返回 429，客户端遵循 Retry-After；本次没有增加新的 HarukiProxy 业务限流器。代理提前生成的错误可能没有 updatedData。

客户端新 UA 是编译期开关 `HARUKI_PROXY_V3_METADATA=1`，必须使用已启用该开关的构建。未启用的 APK 将被新 v3 后端拒绝。自定义端点分支若仍只发送旧 UA，即使地址填官方 v3，也会被拒绝。

先部署后端并完成真实联调，再分别发布新 UA 和重试开关。没有幂等承诺：网络重发可能重复处理/通知，request_id 不可作为去重键。

### 客户端必须同步的修改

`src/upload_protocol.rs` 的 `response_policy` 把 `let data = &parsed["data"];` 改为 `let data = &parsed["updatedData"];`，同步更新响应 fixture 和测试：error_code、request_id、retryable、client_policy 全部从 updatedData 获取。不需要保留 data 回退兼容。

启用 `HARUKI_PROXY_V3_METADATA=1`；自定义端点如果指向 Toolbox v3，也必须发送包含 platform 的新 UA。HTTP 状态与机器码组合、X-Request-ID 优先级以及有限重试策略不变。

## 上传日志、管理分析与部署

上传日志采集版本、渠道、协议、平台、系统/进程架构、请求 ID、字节数、后端处理耗时及失败阶段。账号身份未验证的记录只保存 claimed ID，不归属为该账号活动。没有可核对身份信息的成功上传仍计入尝试/成功数，但不进入已验证活跃账号数。

管理员接口：

- `GET /api/admin/statistics/upload-logs`：原列表新增元数据字段和过滤。
- `GET /api/admin/statistics/upload-analytics`：返回 summary、groups、byFailureStage、migration；超级管理员另有 ingress。
- `from/to` 范围最多 31 天，半开区间 `[from,to)`；支持 upload_method、server、data_type、success，以及 protocol_version、client_name、client_version、client_channel、platform、os_version、os_arch、app_arch、oauth_client_id 过滤。
- `group_by` 最多两个白名单维度，默认 upload_method；可选 interval=hour/day，按 UTC 分桶。结果超过 5000 组返回 400，要求收窄范围。
- summary 提供尝试数、成功率、已验证活跃账号去重数、处理耗时 P50/P95 及样本量、元数据覆盖率。无分母时比例为 null。
- migration 提供成功 v2/v3/未知协议数量、v3 占比、窗口内仅 v2 成功账号数，沿用当前业务过滤条件。观察整体迁移时不要限定单一协议或客户端版本。
- 普通管理员只见已验证且属主可管理的记录，超级管理员可排查历史/未知归属记录及 claimedGameUserId。
- ingress 为 35 天保留的全局 HarukiProxy 日级入口计数；按相交的完整 UTC 日返回，不按业务过滤缩小。available 表示当前可读取，complete=false 表示非无损统计，不能将缺失数据当作完整 0。

数据库需要先扩展 schema。自动迁移使用生成后的 Ent schema；关闭自动迁移时先执行 [正式 DDL](harukiproxy-v3-schema.sql)，其中 CONCURRENTLY 索引必须在事务外执行。启动会验证字段存在与身份字段可空。历史元数据保持 null，不推测补齐。

详细实现、权限口径及验收见 [后端设计](harukiproxy-v3-backend-design.zh-CN.md)。部署不等于 APK 实机联调已完成。

# 生日材料监听

按材料名配置监听时，支持 `diamond`（12）、`yuugiri` / `yugiri`（5）、
`clover`（20）、`battery`（17，电池）、`amethyst` / `quartz`（11，紫水晶，
主数据名称为闪耀石英），以及对应 `mysekai_material_<ID>` 名称。
显式 `material_ids` 仍按 ID 过滤；事件继续返回 `matched_material_ids`，
并保留命中位置、同一采集物的相关掉落与采集物信息。
