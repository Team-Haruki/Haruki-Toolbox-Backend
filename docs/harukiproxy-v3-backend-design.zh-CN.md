# 历史方案说明

本文保留前一阶段设计背景；其中固定密钥 v3 鉴权和外层加密已被 OAuth2 方案替代，不作为当前客户端协议。当前契约见 [客户端对接](harukiproxy-v3-client-integration.zh-CN.md) 与 [读写授权方案](upload-oauth2-write-grants-design.zh-CN.md)。

# HarukiProxy v3 后端对接与上传分析设计

- 状态：历史设计。UA 解析、渠道版本策略、上传日志字段、入口聚合与管理统计已按本文实现并沿用；固定密钥鉴权与外层 AES-GCM 从未正式发布（仅见于预发布 v9.0.0-rc4/rc5），已由 OAuth2 v3 取代（见文首说明）。
- 日期：2026-10-05。
- 后端基线：`d3895e4`。
- 客户端核对来源：HarukiProxy 的 `src/upload_protocol.rs`。已核对 UA 生成、错误响应解析和请求重试入口；尚未进行 APK 与实际后端联调。
- 现行协议：[HarukiProxy 上传](harukiproxy-upload.zh-CN.md)（已按 OAuth2 v3 更新）。

## 1. 目标与范围

后端支持 HarukiProxy 完整发行版本、预发布渠道和平台 UA，同时建立各种上传方式共用的客户端元数据与统计口径。工具箱既能横向比较上传方式，也能独立分析 HarukiProxy 的版本、渠道、平台和故障分布。

本期后端交付范围：

1. v3 UA 解析、渠道独立的最低版本策略及强制平台校验。
2. 与已改造客户端一致的错误码、请求 ID 和重试语义。
3. 上传处理记录扩展、入口拒绝聚合、管理员查询与分析 API。
4. v2 下架、历史数据兼容、数据库部署及客户端联调说明。

本期不增加安装 ID、设备指纹、客户端主动遥测接口或上传幂等协议；不调整 AES-GCM 请求体，不改变现有账号鉴权和 Ory 身份体系。工具箱页面属于后续前端对接，本期提供所需后端数据。

## 2. 实施前的实现与差距（2026-10-05 基线）

| 位置 | 当前行为 | 需要调整 |
| --- | --- | --- |
| `internal/modules/upload/harukiproxy.go` | 支持简单 `-preview` 后缀；UA 必须完全匹配旧正则 | 支持完整 SemVer、平台与可选系统字段 |
| 同上 | v3 独立认证、加密密钥；最低版本与旧入口共用 | v3 独立版本策略 |
| 同上 | 认证失败为 400，错误主要是文本 | v3 认证失败改为 401，并返回结构化错误 |
| `internal/modules/upload/handler.go` | 进入公共处理流程后记录审计 | 衔接入口观测，补齐解密等早期失败并防止重复记录 |
| `ent/toolbox/schema/uploadlog.go` | 保存上传方式、账号、类型、成功状态和错误文本 | 增加客户端信息、失败阶段、时间及关联字段 |
| `internal/modules/adminstats/` | 已有上传日志查询与方式分类统计 | 增加过滤、分组及迁移指标 |

当前业务日志由有界后台任务写入，写入失败只记录日志；它不是保证不丢失的事件流。新增统计必须保留写入失败监测，不能将数据库中记录数无条件视为所有收到的请求数。

## 3. 迁移与兼容期限

截止时间唯一且固定：**2026-11-01 00:00:00 Asia/Shanghai，即 2026-10-31 16:00:00 UTC**。

| 场景 | 2026 年 10 月 | 截止时刻及之后 |
| --- | --- | --- |
| v2 旧入口 | 保留现有认证和请求体，响应携带 Sunset | 全部返回 410 / `protocol_retired` |
| v3 + 新 UA | 接受并记录元数据 | 接受并记录元数据 |
| v3 + 无平台旧 UA | 返回 400 / `invalid_client_metadata` | 返回 400 / `invalid_client_metadata` |
| v3 + 畸形新 UA | 拒绝，不回退旧解析器 | 拒绝 |

v3 仍在内部测试，按 2026-10-06 确认的新要求直接淘汰旧 UA，不提供平台校验开关。v2 的旧格式仅由旧入口接受。

截止判断逐请求执行，不依赖定时任务；旧入口在认证之前执行下架判断。v3 不回退旧认证或加密密钥。两个路径别名应用相同规则。

“v2”是本设计对无版本旧入口的统计命名；协议版本由服务端路由确定，不能从 UA 主版本推断。历史记录没有可靠证据时保持协议未知，不批量推测回填。

## 4. 上传请求协议

### 4.1 路径和头（鉴权头已废弃：现行为 `Authorization: Bearer <access_token>`，见客户端对接文档）

```http
POST /harukiproxy/v3/{server}/{user_id}/{data_type}/upload
Content-Type: application/octet-stream
X-Haruki-Toolbox-Secret: <v3 认证密钥>
User-Agent: HarukiProxy/v3.0.0-preview.1+gabcdef1 (platform=Windows; os_version=10.0.26100; os_arch=arm64; app_arch=x64)
```

别名为 `/api/harukiproxy/v3/{server}/{user_id}/{data_type}/upload`。

- `server`：`jp/en/tw/kr/cn`，实际数据类型权限仍由业务策略决定。
- `user_id`：十进制游戏账号 ID，客户端按字符串处理，服务端验证范围及载荷身份。
- `data_type`：`suite/mysekai/mysekai_birthday_party`。
- `/v3/` 表示协议，`v3.0.0` 表示应用发行版本，两者独立。

### 4.2 UA 字段

```text
HarukiProxy/<version> (platform=<platform>; os_version=<version>; os_arch=<arch>; app_arch=<arch>; os_build=<build>)
```

| 字段 | 必填 | 格式与语义 |
| --- | --- | --- |
| 产品名 | 是 | 固定 `HarukiProxy`，大小写固定 |
| version | 是 | 小写 `v` 前缀 + 严格 SemVer 2.0.0；去前缀后的版本最长 128 字节 |
| platform | 新格式是 | Windows/macOS/Android/iOS/Linux/Unknown |
| os_version | 否 | 操作系统实际版本，最多 64 字节 |
| os_arch | 否 | x64/arm64/x86/arm/unknown |
| app_arch | 否 | 进程架构，同上 |
| os_build | 否 | 系统构建号，已有版本包含相同值时省略 |

平台属于运行代理的主机，不代表游戏设备。Windows ARM 运行 x64 进程时分别记录 `os_arch=arm64`、`app_arch=x64`。

解析约束：

- 总长度不超过 512 字节，仅允许可打印 ASCII；拒绝控制字符。
- 版本通过独立 SemVer 校验器验证，不能仅依赖宽松版本库接受结果。
- 元数据键使用小写字母和下划线，长度 1–32；值使用 `[A-Za-z0-9._+-]`，长度 1–64。
- 允许字段顺序变化；拒绝重复键、空字段、嵌套括号、尾随内容及歧义语法。
- 新格式必须包含 `platform`；合法但未知平台/架构规范化为 `unknown`，不扩充无限统计标签。
- 未识别扩展键在语法验证后忽略，不自动存入 JSON。
- 可选字段缺失允许；提供了非法字段值则返回 `invalid_client_metadata`。
- 不持久保存原始 UA，避免长期保留任意客户端自报内容；保存白名单规范化字段即可。
- 不收集主机名、账号名、序列号、MAC、IP 指纹或完整硬件清单。

v3 始终要求结构化 UA；单独的 `HarukiProxy/v3.0.0-preview.1+gabcdef1` 即使版本合法也会被拒绝。v2 保留既有 UA 规则直至下架。

### 4.3 版本和渠道

支持 `v3.0.0`、`v3.0.0-preview`、`v3.0.0-preview.1`、`v3.0.0-dev.12`、`v3.0.0-beta.2`、`v3.0.0-rc.1` 以及 `+gabcdef1` 构建后缀。

- 无预发布后缀：渠道为 `stable`。
- 有预发布后缀：首段为渠道；官方使用 `dev/preview/beta/rc`。
- 其他合法渠道只有显式配置允许后才接入；渠道名限制为小写字母开头、后续小写字母/数字/连字符，最长 32 字节。
- 最低版本只在所属渠道内比较。不能将 SemVer 字典顺序当作 dev → preview → beta → rc 的发布顺序。
- 构建信息保存用于排查，不参与版本优先级比较。

### 4.4 请求体不变（已废弃：OAuth2 v3 直接接收原始游戏载荷，不再做外层解密）

```text
key  = SHA-256(UTF-8(trim(v3_unpack_key)))
AAD  = UTF-8(server + "|" + user_id + "|" + data_type)
body = nonce[12 bytes] || ciphertext || authentication_tag[16 bytes]
```

使用 AES-256-GCM；AAD 保持 URL 参数原始文本，不包含路径前缀或 UA。每次新加密必须生成安全随机 nonce。网络重发已生成的相同密文不等同于用同一 nonce 加密另一份明文。

UA 是自报诊断信息，认证密钥、载荷属主校验及账号权限仍是独立安全边界。认证密钥保持常量时间比较。

## 5. 服务端版本策略

建议新增 YAML 配置，密钥仍使用现有环境变量入口：

```yaml
haruki_proxy:
  v3_client_policy:
    allowed_channels: [stable, preview, beta, rc, dev]
    minimum_versions:
      stable: "3.0.0"
      preview: "3.0.0-preview"
      beta: "3.0.0-beta"
      rc: "3.0.0-rc"
      dev: "3.0.0-dev"
```

规则：

1. 未配置策略时采用上面的完整默认值；显式空渠道列表视为配置错误，避免和“未配置”混淆。
2. 自定义策略必须为每个允许渠道提供最低版本；拒绝重复渠道、未知配置键、缺失项及额外的最低版本项。
3. 最低版本可带小写 `v`，规范化后严格校验；不得带构建信息，且预发布渠道必须与配置键一致。
4. 启动时验证策略，错误则启动失败，不把配置错误报告成客户端版本错误。
5. 平台字段始终必填；配置中不存在 `require_platform`，提供该键会作为未知配置拒绝。
6. 配置通过 bootstrap 装配为只读策略，重启生效，不读取模块全局可变配置。
7. 现有 `haruki_proxy.version` 和管理员旧运行时版本设置只控制 v2；文档明确其不再影响 v3。

默认最低版本 3.0.0 会拒绝发行版本低于 3.0.0 的客户端，即使它使用 v3 路径。部署前确认现有活跃客户端版本；如需过渡接纳，通过对应渠道显式设置较低最低版本，不能隐式回退旧策略。

## 6. 响应、请求 ID 与重试

按 2026-10-06 确认的新要求，统一使用工具箱 `{status, message, updatedData}` 响应，不为尚未发布的客户端提供 data 外层兼容。客户端必须同步改为读取 updatedData。示例：

```json
{
  "status": 400,
  "message": "Client version is unsupported",
  "updatedData": {
    "error_code": "client_version_unsupported",
    "request_id": "0cfb45ef-c262-4d74-bc57-45f999ec2709",
    "retryable": false,
    "client_policy": {
      "channel": "preview",
      "minimum_version": "3.0.0-preview.2"
    }
  }
}
```

请求 ID 在 HarukiProxy 入口中间件生成 UUID，不信任客户端传入值；使用值复制，不能让后台任务持有 Fiber 请求生命周期内的引用。响应头 `X-Request-ID` 与 `updatedData.request_id` 一致，成功响应也携带 ID。

| HTTP | error_code | retryable | 行为 |
| --- | --- | --- | --- |
| 400 | invalid_client_metadata | false | 修正 UA，不重复发送同一错误请求 |
| 400 | client_version_unsupported | false | 客户端暂停并提示升级 |
| 400 | client_channel_disabled | false | 客户端暂停并提示渠道不可用 |
| 401 | invalid_client_credentials | false | 客户端暂停，认证失败不附带策略（已废弃：现行 v3 为 401 `invalid_token`、403 `insufficient_scope`；`invalid_client_credentials` 只剩 v2，且为 400） |
| 403 | upload_not_allowed | false | 统一权限拒绝，不泄漏是否存在账号 |
| 400 | payload_decryption_failed | false | 修正密钥、AAD 或请求体（现行 v3 不解密，只剩 v2 会返回） |
| 400 | invalid_upload_payload | false | 参数/载荷非法或身份不匹配，不回显内部数据 |
| 410 | protocol_retired | false | 停止旧入口请求 |
| 429 | rate_limited | true | 携带已知等待时间的 Retry-After |
| 500 | internal_error | 按提交状态 | 内部故障，仅明确安全时建议重试 |
| 503 | temporarily_unavailable | 按提交状态 | 依赖不可用或过载 |

仅版本拒绝和渠道拒绝返回白名单 `client_policy`。认证先于 UA/版本检查，认证失败不回显最低版本、系统配置或账号信息。

错误分类必须来自类型化结果和处理阶段，禁止匹配错误消息字符串。数据库失败不能统一伪装成非法载荷。确认未提交的临时错误可返回 `retryable=true`；提交结果不明或已持久化后出错时返回 false，避免后端主动建议不安全重试。

游戏数据已持久化但缓存清理或通知失败，沿用成功上传语义，另记后处理故障。不能因统计写入失败将成功上传改成失败。

客户端已对 429、500、503 支持有限重试，并尊重显式 `retryable=false`。本期没有幂等性承诺：响应丢失后的网络重试仍可能重复处理和通知，每次请求都是独立尝试。请求 ID 仅作关联，不是幂等键。

本规范保证到达应用路由的响应格式；边缘代理提前产生的 413、429、502 等响应可能没有结构化 updatedData。保留客户端已有 HTTP 状态降级处理，联调时验证真实代理链路。

## 7. 处理流程与观测边界（其中「常量时间认证」「v3 解密」两步已由 OAuth2 令牌校验取代）

```text
生成请求 ID 和开始时间
  → 旧路径截止判断
  → 服务端配置可用性检查
  → 常量时间认证
  → UA 解析与版本策略
  → 参数校验
  → v3 解密
  → 公共上传流程：账号策略、解码、载荷身份校验、预处理、持久化
  → 确定最终结果，记录一次业务尝试
  → 缓存清理与后续分发
```

分为两种记录：

- **入口聚合**：覆盖所有到达 HarukiProxy 入口的请求及拒绝；不关联客户端宣称的账号，不保存任意 UA 标签。
- **业务记录**：认证、UA、版本和路由参数通过后进入统计范围；解密失败也记为业务失败，但账号身份未验证时不能写入可信账号归属。

引入显式 attempt 对象，包含不可变客户端信息、开始时间、request ID 及最终结果；一个地方负责 finalize。公共处理流程把类型化 outcome 返回给入口，避免入口与旧审计函数各写一条。

解密或载荷身份验证前的路径 ID 如需排查，单独存入 `claimed_game_user_id`，不作为可信 `game_user_id`。账号级统计和用户活动只使用验证后的身份；没有可核对载荷身份且没有已认证属主证明的上传，即使持久化成功也保守标记为未验证（仍计入超级管理员的尝试和成功数），属主必须由认证身份/数据库绑定解析。未验证或归属不明的失败详情仅供 super_admin 查看，普通管理员不能利用这些记录绕过目标用户层级限制。

其他方式逐步接入同一 attempt 模型；iOS 等异步流程在实际任务结束后 finalize，HTTP 接收成功不等于上传成功。后台传递值拷贝，不保存 Fiber Ctx。

## 8. 数据模型

扩展 Toolbox 的 `UploadLog`；不建立另一套 HarukiProxy 专用上传日志。

| 新字段 | 类型/约束 | 说明 |
| --- | --- | --- |
| client_name | nullable string(64) | 客户端产品名 |
| client_version | nullable string(128) | 不含前导 v，保留预发布和构建信息 |
| client_channel | nullable string(32) | 从版本派生 |
| client_metadata_format | nullable string(16) | legacy / structured |
| protocol_version | nullable string(16) | HarukiProxy 为 2 / 3，其他方式按自身协议 |
| platform | nullable string(16) | 规范化小写平台；unknown / not_applicable 含义不同 |
| os_version / os_build | nullable string(64) | 系统版本及构建 |
| os_arch / app_arch | nullable string(16) | 规范化架构 |
| failure_stage | nullable string(32) | decrypt / decode / account_policy / identity / preprocess / persist 等固定阶段 |
| error_code | nullable string(64) | 机器可读失败类别 |
| request_id | nullable string(36) | 服务端 UUID，关联 HTTP 尝试 |
| processing_duration_ms | nullable int64 | 到最终业务结果的后端耗时，非客户端端到端耗时 |
| request_bytes | nullable int64 | 实际收到的 body 字节数，不信 Content-Length |
| identity_verified | nullable bool | true/false；null 为历史未标注 |
| claimed_game_user_id | nullable string(30) | 参数声称的 ID，仅受限排查使用 |

同步调整可信身份字段的可空语义：未验证的 `game_user_id`、未知 `toolbox_user_id` 不填写伪造值或占位 0。迁移时保留已有历史值，不将历史行自动标为 verified。新增 `received_at` 可空时间字段，当前新记录使用接收时间作为统计时间，历史行回退 `upload_time`；原有 upload_time 语义不静默重写。

另增加 `oauth_client_id`（nullable string(255)），仅从已验证 token 中取得。

首期白名单信息全部落独立列，不引入无约束 metadata JSON；后续确有字段需求再做 schema 扩展。

已新增 `(upload_method, protocol_version, received_at)`、`(upload_method, client_channel, received_at)`、`(upload_method, platform, received_at)` 及 received_at 单列索引；request_id 使用非唯一索引，因为其他上传方式未来可能一次 HTTP 对应多条数据。新增时间列的索引与查询迁移需结合真实查询计划确定，避免同时创建大量重叠索引。

历史行新增字段保持 null，展示“未采集”；不要把历史 null 平台混同为客户端明确报出的 unknown。

### 8.1 各上传方式适配

| upload_method | 客户端来源 |
| --- | --- |
| haruki_proxy | 本文结构化 UA；过渡旧格式只记录可解析版本 |
| manual | 原生浏览器 UA 尽力解析，缺失不拒绝上传；不要推测不存在的工具箱客户端版本 |
| ios_proxy / ios_script | 明确工具名/版本的已知适配器，不根据上传方式硬编码宿主平台 |
| oauth2 | 使用已验证 token 的 client_id 识别集成；客户端自报系统信息仅作诊断 |
| inherit | 后端执行，平台为 not_applicable，不把后端服务器系统计入用户平台 |

现有 upload_method 枚举保持不变；平台、渠道、协议不扩充为新的上传方式。

## 9. 入口聚合与统计可靠性

入口计数采用 Redis 原子 `IncrementWithTTL`，禁止 Get/Set 读改写。按 UTC 日期、服务端协议、有限结果类别计数，例如：

```text
upload:ingress:2026-10-05:haruki_proxy:3:invalid_client_credentials
upload:ingress:2026-10-05:haruki_proxy:3:accepted
```

结果标签来自固定白名单，未知内部错误归为 internal_error；不使用 UA、版本、请求 ID、账号 ID 或任意 path 作为计数标签。建议保留 35 天，入口总数由各互斥结果求和，避免两个计数更新不同步。

每个请求只计一个最终入口结果；`accepted` 表示进入业务处理，不表示业务成功。进程崩溃或 Redis 不可用可能造成缺口，因此这是运行观测数据，响应携带可用性/保留窗口说明。缺失计数不能在已知存储故障时表示为确定的 0。

业务审计写入失败、后台任务拒收、入口计数失败均接入现有日志/监测；避免每条异常打印敏感信息或形成日志洪泛。Redis 写入设置短超时，统计故障不阻塞上传主流程。若以后需要无损统计，再单独设计持久事件队列，本期不宣称无损。

## 10. 管理员分析 API

复用管理员路由组及 RequireAdmin，增加相对路径：

```http
GET /statistics/upload-analytics
```

扩展现有 `/statistics/upload-logs` 支持相同元数据过滤；响应继续使用现有管理接口的 camelCase 字段风格。

过滤参数：`from`、`to`、`upload_method`、`protocol_version`、`client_version`、`client_channel`、`platform`、`os_version`、`os_arch`、`app_arch`、`server`、`data_type`、`success`。限制范围最多 31 天，并限制 CSV 数量、字段长度及结果分组数量；超限返回 400，不能截断后仍声称完整分布。

`group_by` 允许白名单维度及最多两个组合，默认 upload_method；时间序列使用 `interval=hour|day`；时间范围为 `[from,to)`，UTC 分桶。使用参数化 SQL 与数据库聚合，不将全部日志载入内存后分组。分组最多 5000 条，超出返回 400，要求收窄过滤；本版不对聚合分组分页。

| 指标 | 定义 |
| --- | --- |
| attemptCount | 业务处理尝试数，重试独立计数 |
| successCount / failureCount | 持久化成功/业务处理失败 |
| successRate | successCount / attemptCount，分母为 0 时 null |
| activeGameAccounts | 选定窗口内成功且身份可信的 `(server, game_user_id)` 去重数 |
| latencyP50Ms / latencyP95Ms | 已采集后端处理耗时的分位数，附样本量 |
| byFailureStage | 固定失败阶段计数 |
| metadataCoverage | 元数据已采集行数 / 当前窗口业务行数 |

版本和平台分别提供上传次数占比与账号覆盖数。一个账号可能属于多个平台/版本组，组人数不能相加；跨日活跃账号不能累加每日 distinct。

HarukiProxy 迁移视图增加：

- 成功 v3 上传数 / 成功且协议已知的 HarukiProxy 上传数，同时显示协议未知数。
- 同一时间窗口内有 v2 成功、无 v3 成功的已验证游戏账号数。
- 下架后旧入口 protocol_retired 聚合计数，独立于 v3 业务失败率。

迁移指标遵循请求的业务过滤条件；需要观察整体迁移时，不应额外限定单一协议或版本。

入口聚合无账号归属，只向 super_admin 提供；普通管理员响应不附带全局入口总数。业务查询必须先 scope 再聚合：目标用户读取遵循角色层级限制，不能在筛选前计算全局摘要泄漏受限用户信息。未知属主明细和 claimed ID 仅 super_admin 可见，普通管理员仅统计其可管理的已确认目标范围。

## 11. 代码分层与实施文件

| 层级 | 拟修改内容 |
| --- | --- |
| config/ | 配置解码、默认策略表达，区分 omitted 与 empty |
| internal/bootstrap/ | 启动校验、策略装配、数据库兼容性检查 |
| api/route.go | 仅传递依赖和注册路由 |
| internal/modules/upload/ | UA parser、策略判定、响应构造、入口观测与 attempt 收口 |
| internal/platform/upload/ | 共享 ClientMetadata / UploadAttempt / Outcome 类型，不依赖 Fiber |
| ent/toolbox/schema/ | 扩展 UploadLog schema |
| internal/modules/admincore/ | 日志 DTO 与可见字段策略 |
| internal/modules/adminstats/ | 过滤、范围鉴权、聚合查询及指标定义 |
| docs/harukiproxy-upload.zh-CN.md | 更新最终生效对接协议 |

不在 utils 中反向引用平台层；不复制 SessionHandler 的 Ory/可信 header 解析。Ent 产物使用 `go generate ./ent/toolbox` 生成，不手改。新增统计路由须同步路由 golden，并核对 Oathkeeper 现有管理员规则覆盖。

## 12. 数据库与上线顺序

1. 先完成向后兼容的 schema 扩展与生成代码，验证 nullable 身份字段对旧查询和用户活动的影响。
2. 对自动迁移开启/关闭两种部署进行验证。关闭自动迁移时提供可审阅的正式 DDL 和启动期 schema 检查；不通过运行时错误碰运气。
3. 生产索引创建方式按表大小确定；大表考虑独立并发建索引，不能由普通事务迁移误执行。
4. 数据库准备完成后发布后端，v3 立即要求新 UA；v2 继续按既有截止时间下架。
5. 核验真实 Oathkeeper/反向代理允许新 UA、`Authorization` 头和响应 X-Request-ID 透传。
6. 发布启用新 UA 的客户端构建，验证实际上传、错误响应与统计记录。
7. 确认错误分类和重试风险后再启用客户端重试；新 UA 和重试分别灰度。
8. 11 月截止后核验旧入口 410、v3 旧格式拒绝，继续保留历史日志。

回滚只回退兼容的应用版本，保留新增列及历史数据；不能通过回滚绕过已约定的 v2 下架时间。schema 的身份可空改动需要专门测试旧版本读取兼容性，不能直接假设任意旧二进制可回滚。

## 13. 客户端实际对接注意事项

已核对客户端实现：

- 当前客户端仍读取 `data.error_code` / `data.retryable`，需要修改为 `updatedData.error_code` / `updatedData.retryable`，策略同样从 updatedData.client_policy 读取；不解析 message。
- 优先响应 X-Request-ID；客户端当前回退 data.request_id，需要同步改为 updatedData.request_id。
- 按 HTTP 状态和机器码组合判断暂停/重试，后端状态码不能任意替换。
- 当前新 UA 通过编译期 `HARUKI_PROXY_V3_METADATA=1` 启用，因此后端上线不会自动让既有 APK 切换 UA；需要对应构建发布。
- 当前自定义 upload_endpoint 分支始终生成无平台 UA。若用户把官方 v3 URL 填为自定义端点，新后端会立即拒绝；客户端需对这种使用方式作明确处理或引导使用内置端点。后端不为自定义端点身份开豁免。
- 系统信息采集不完整时可省略字段；当前 Windows/iOS 可能没有 os_version，不应因此拒绝。

APK 的正式签名和回归状态来自客户端完成说明，本设计未重新验证签名或实际设备行为。必须完成联调后才能宣布可启用开关。

## 14. 验收要求

### 单元及接口测试

- 完整 SemVer、裸渠道后缀、数字预发布序号、构建信息；拒绝前导零和不完整版本。
- 构建信息不影响最低版本；preview 与 beta 独立策略；配置错误启动失败。
- 四个主要平台、Linux、Unknown，Windows ARM/x64 混合架构，可选字段缺失。
- UA 长度边界、重复键、控制字符、嵌套括号、未知扩展键、非法新 UA 不回退。
- 截止前一纳秒、截止时刻、截止后一纳秒，两条路径别名行为一致。
- 认证失败不泄漏策略；错误密钥、AAD、载荷和权限返回正确状态/错误码。
- 成功/失败/旧入口拒绝都有服务端生成的请求 ID，客户端请求 ID 不能覆盖。
- 每次业务尝试仅记录一次；早期解密失败、异步任务终态、日志写入失败均覆盖。
- 未验证 claimed ID 不进入账号活动或活跃账号统计。
- 同账号跨平台/跨日去重正确；普通管理员不能通过摘要或日志绕过角色层级。
- Redis 入口计数并发原子性、TTL、有限标签及故障降级。
- 历史 null 字段查询、schema 迁移、管理员过滤和路由 golden。

### 验证命令

```sh
go generate ./ent/toolbox
go test ./config ./internal/modules/upload ./internal/platform/upload ./internal/modules/admincore ./internal/modules/adminstats ./internal/bootstrap ./api
go build ./...
go vet ./...
```

最终按仓库 CI 执行格式、staticcheck 与 race 测试；如触及 Ory/Auth Proxy 行为，必须运行 `go test ./...`。新增测试应验证协议和统计不变量，不仅镜像实现细节。

### 实际联调

以关闭和开启新 UA 的客户端分别验证旧格式拒绝和新格式接受；至少一台 Android 设备完成加密上传成功路径。核对数据库元数据、请求 ID 关联、后台可见性、错误提示和有限重试。版本/渠道拒绝、401、429、500/503 使用受控环境注入，不能靠生产故障验证。

## 15. 实施补充

- 手动迁移 DDL：[harukiproxy-v3-schema.sql](harukiproxy-v3-schema.sql)，先在 Toolbox 库执行，再部署；索引部分必须在事务外执行。
- 实际统计入口为 `/api/admin/statistics/upload-analytics`，采用工具箱现有 `updatedData` 响应外层；HarukiProxy 上传响应也使用 `updatedData`。
- 入口聚合返回 `available` 和恒为 false 的 `complete`，明确其尽力计数语义；按与请求时间相交的完整 UTC 日返回，不随业务元数据过滤。
- 普通管理员仅见已验证且属主仍存在、可管理的记录；历史未标注和未知归属记录由超级管理员查询。
- 本期不增加 iOS 工具信息推断；没有明确可验证适配器时元数据保持空值。

### 已完成的本地验证

- Ent 代码已重新生成；文档链接及 git diff --check 通过。
- 干净源码副本中 `go mod tidy -diff`、`go build ./...`、`go vet ./...`、`go tool staticcheck ./...`、`go test -race -count=1 ./...` 全部通过。
- 工作目录原有、被 Git 忽略的 `cmd/suite-rec-backfill` 引用了已移除 Mongo 包，直接执行全仓命令会失败；验证副本排除了该临时脚本，未改动原文件。副本中的 Go 源码与本次工作目录一致。
- 隔离 PostgreSQL 17 已执行正式 DDL并确认历史元数据未被回填；真实 SQL 集成测试覆盖分位数、跨平台/跨日去重、迁移统计和管理员过滤，测试容器已清理。
- 尚未部署到生产，未完成 APK 实机上传联调。客户端必须先同步 updatedData 和强制新 UA 约定。

## 16. 参考

- [Semantic Versioning 2.0.0](https://semver.org/)：预发布和构建元数据规则。
- [RFC 9110 User-Agent](https://www.rfc-editor.org/rfc/rfc9110.html#name-user-agent)：产品标识、注释及避免过量细节。
- [现行 HarukiProxy 上传协议](harukiproxy-upload.zh-CN.md)。
