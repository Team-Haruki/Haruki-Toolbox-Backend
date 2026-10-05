# HarukiProxy v3 客户端与第三方开发者对接指南

更新日期：2026-10-06。

本文面向获授权接入 HarukiProxy v3 上传协议的客户端开发者，以当前仓库后端实现为准。后端代码已完成本地验证，生产部署和 APK 实机联调需由维护方确认；本文不表示线上环境已切换。

## 1. 接入范围与准备

本协议用于向 Haruki Toolbox 上传 Project Sekai 游戏数据。接入前向维护方取得：

| 项目 | 用途 |
| --- | --- |
| HTTPS 服务地址 `BASE_URL` | 由维护方提供测试或正式环境地址 |
| v3 认证密钥 `V3_SECRET` | 放入请求头 `X-Haruki-Toolbox-Secret` |
| v3 加密口令 `V3_UNPACK_KEY` | 派生 AES-256-GCM 密钥，不作为请求头发送 |
| 允许的客户端版本/渠道 | 以目标环境实际配置为准 |
| 可用于联调的游戏账号及原始上传数据 | 验证完整上传链路 |

两项密钥作用不同，均不能使用 v2 旧值替代。使用 HTTPS，不在日志、错误截图或抓包报告中公开密钥。

**这是受授权的 HarukiProxy 协议入口，不是凭任意第三方应用名称即可使用的公共上传 API。** 当前产品标识固定为 `HarukiProxy`。独立第三方应用若需要由用户授权代为上传，应使用 [OAuth2 / OIDC 对接文档](oauth2-integration.zh-CN.md) 中的代理上传接口，不自行套用 HarukiProxy 名称或密钥。

## 2. 必须落实的客户端约定

1. 使用 v3 路径，不回退 v2。
2. 始终发送带 `platform` 的新 UA；旧格式立即拒绝，没有过渡开关。
3. 统一读取响应 `updatedData`，不读取或兼容旧草案的 `data`。
4. 根据 **HTTP 状态 + error_code** 决定处理动作，不匹配 message 文本。
5. 请求 ID 优先取 `X-Request-ID`，回退 `updatedData.request_id`。
6. 仅对允许重试的故障做有限重试；显式 `retryable=false` 时不自动重试。
7. 自定义地址若指向 Toolbox v3，同样必须发送新 UA。
8. 禁止自动跟随重定向携带密钥至其他主机或降级到旧入口；遇到 3xx 应停止并检查配置。

## 3. 上传接口

```http
POST {BASE_URL}/harukiproxy/v3/{server}/{user_id}/{data_type}/upload
```

等价别名：

```http
POST {BASE_URL}/api/harukiproxy/v3/{server}/{user_id}/{data_type}/upload
```

两个路径的认证、UA、请求体及响应规则相同，选择一个即可。

| 参数 | 类型 | 允许值/要求 |
| --- | --- | --- |
| server | 字符串 | `jp`、`en`、`tw`、`kr`、`cn` |
| user_id | 十进制字符串 | 正整数，最大 `9223372036854775807`；建议无前导零，与载荷身份一致 |
| data_type | 字符串 | `suite`、`mysekai`、`mysekai_birthday_party` |

游戏账号 ID 必须按字符串传递和保存。JavaScript 等语言不要先转为普通 Number，避免超过安全整数范围后改变 ID。

不同区服、数据类型及账号仍受后端业务权限与载荷校验约束；参数合法不等于一定允许上传。

### 请求头

```http
Content-Type: application/octet-stream
X-Haruki-Toolbox-Secret: <维护方提供的 v3 认证密钥>
User-Agent: HarukiProxy/v3.0.0-preview.1+gabcdef1 (platform=Windows; os_version=10.0.26100; os_arch=arm64; app_arch=x64)
```

无需浏览器 Cookie、Kratos 会话或 OAuth2 bearer；本入口自行验证专用密钥。不要把认证密钥放入 URL 查询参数。

## 4. User-Agent 规范

### 标准格式

```text
HarukiProxy/<version> (platform=<platform>; os_version=<version>; os_arch=<arch>; app_arch=<arch>; os_build=<build>)
```

最小合法例子：

```http
User-Agent: HarukiProxy/v3.0.0 (platform=Android)
```

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| 产品名 | 是 | 固定 `HarukiProxy`，大小写敏感 |
| version | 是 | 小写 `v` 前缀 + 完整 SemVer；不含前缀的版本最多 128 字节 |
| platform | 是 | `Windows`、`macOS`、`Android`、`iOS`、`Linux`、`Unknown` |
| os_version | 否 | 系统实际版本，最多 64 字节 |
| os_arch | 否 | `x64`、`arm64`、`x86`、`arm`、`unknown` |
| app_arch | 否 | 当前客户端进程架构，取值同上 |
| os_build | 否 | 系统构建号，最多 64 字节；与 os_version 重复时省略 |

`platform` 表示运行 HarukiProxy 的设备。例如 Windows 代理 Android 手机流量，平台仍是 Windows。Windows ARM 上运行 x64 客户端时分别发送 `os_arch=arm64`、`app_arch=x64`。

客户端应将 `amd64/x86_64` 规范化为 `x64`，将 `aarch64/arm64-v8a` 规范化为 `arm64`。服务端不会自动识别所有操作系统架构别名，未知值会计入 unknown。

### 编码与扩展规则

- UA 总长度最多 512 字节，只允许可打印 ASCII。
- 产品版本与左括号之间使用一个 ASCII 空格；示例中的 ` (` 是固定分隔符。
- 元数据键使用小写字母及下划线，长度 1–32；值只能包含 `[A-Za-z0-9._+-]`，长度 1–64。
- 键和值之间使用 `=`，键值两侧不要额外加空格。字段以 `; ` 分隔，字段顺序可调整。
- 不支持字符串引号、JSON、嵌套括号或转义。没有值的可选字段直接省略，不发送空字符串。
- 拒绝重复键、尾随分号、括号后多余内容、控制字符和缺失 platform。
- 合法未知扩展键被忽略；合法但未知的平台/架构值规范化为 unknown。请使用表中准确大小写以免丢失统计分类。
- 系统信息读取失败时省略可选字段；不要让可选采集失败阻断上传。
- 不发送主机名、系统用户名、序列号、MAC 地址或其他设备唯一标识。

### 示例

```text
HarukiProxy/v3.0.0 (platform=Windows; os_version=10.0.26100; os_arch=x64; app_arch=x64)
HarukiProxy/v3.0.0-dev.12 (platform=macOS; os_version=15.0; os_arch=arm64; app_arch=arm64)
HarukiProxy/v3.0.0-beta.2 (platform=Android; os_version=15; os_arch=arm64; app_arch=arm64)
HarukiProxy/v3.0.0-preview.1 (platform=iOS; app_arch=arm64)
```

以下形式会被拒绝：

```text
HarukiProxy/v3.0.0
HarukiProxy/3.0.0 (platform=Android)
HarukiProxy/v3.0.0 (os_arch=arm64)
HarukiProxy/v3.0.0 (platform=Android; platform=iOS)
HarukiProxy/v3.0.0 (platform=Android;)
```

## 5. 版本与发行渠道

可用形式包括：

```text
v3.0.0
v3.0.0-preview
v3.0.0-preview.1
v3.0.0-dev.12
v3.0.0-beta.2
v3.0.0-rc.1
v3.0.0-preview.1+gabcdef1
```

无预发布后缀时渠道为 `stable`；否则取预发布后缀的第一段，例如 `preview.1` 属于 preview。不要把正式版写成 `v3.0.0-stable`，该形式被拒绝。

其他规则：

- 主、次、修订版本及纯数字预发布段不能有多余前导零，例如 `v03.0.0`、`v3.0.0-beta.01` 非法。
- `+` 后构建信息不参与版本优先级比较；只改变构建后缀不能越过最低版本限制。
- 各渠道独立配置最低版本，不能将 dev/preview/beta/rc 的文字顺序当作发布时间顺序。
- 默认渠道为 stable/preview/beta/rc/dev，对应默认最低版本分别为 `3.0.0`、`3.0.0-preview`、`3.0.0-beta`、`3.0.0-rc`、`3.0.0-dev`。
- 运维可收紧渠道与最低版本。默认值不是线上永久承诺，以实际拒绝响应和维护方通知为准。
- 使用真实编译版本，不伪装版本绕过准入。

路径 `/v3/` 是上传协议版本，UA 中 `v3.0.0` 是客户端发行版本，二者不强制绑定。

## 6. 二进制请求体与加密

原始输入 `raw_game_payload` 是当前上传流程使用的原始游戏响应字节，外层 v3 解密后仍进入游戏协议解码流程。不要将其替换成自行序列化的 JSON、Base64 字符串、multipart 文件表单或工具箱响应对象。

外层协议：

```text
key  = SHA256(UTF8(trim(V3_UNPACK_KEY)))
AAD  = UTF8(server + "|" + user_id + "|" + data_type)
body = nonce[12 bytes] || ciphertext || authentication_tag[16 bytes]
```

- AES-256-GCM，密钥为 32 字节 SHA-256 摘要原始字节，不能使用摘要的十六进制文本。
- trim 指去除加密口令首尾空白；建议维护方分发不含外围空白的口令。
- nonce 必须来自安全随机数发生器；同一密钥下不能复用 nonce 加密不同明文。
- 某些库加密返回值已经包含末尾 tag，不要重复拼接。
- AAD 使用构造路径时的参数文本，不包括域名、`/api/`、`/v3/`、斜杠或 UA。
- tag 为 16 字节；body 至少 28 字节，具体上传体上限由目标部署及代理配置决定，向维护方确认。

示例：

```text
路径：/harukiproxy/v3/jp/123456789012345678/suite/upload
AAD：jp|123456789012345678|suite
```

语言无关伪代码：

```text
server = "jp"
user_id = "123456789012345678"
data_type = "suite"

key = sha256(utf8(trim(v3_unpack_key)))
nonce = secure_random_bytes(12)
aad = utf8(server + "|" + user_id + "|" + data_type)
ciphertext, tag = aes_256_gcm_encrypt(key, nonce, raw_game_payload, aad)
body = concat(nonce, ciphertext, tag)

POST(base_url + "/harukiproxy/v3/" + server + "/" + user_id + "/" + data_type + "/upload",
     headers = {"Content-Type": "application/octet-stream",
                "X-Haruki-Toolbox-Secret": v3_secret,
                "User-Agent": generated_user_agent},
     body = body,
     follow_redirects = false)
```

## 7. 响应格式

### 成功：HTTP 200

```http
X-Request-ID: 0cfb45ef-c262-4d74-bc57-45f999ec2709
Content-Type: application/json
```

```json
{
  "status": 200,
  "message": "Upload successful",
  "updatedData": {
    "request_id": "0cfb45ef-c262-4d74-bc57-45f999ec2709",
    "retryable": false
  }
}
```

### 版本不受支持：HTTP 400

```json
{
  "status": 400,
  "message": "Client version is below minimum required",
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

| 字段 | 类型 | 约定 |
| --- | --- | --- |
| status | 整数 | 应用生成响应与 HTTP 状态一致；客户端以 HTTP 状态为准 |
| message | 字符串 | 可读说明，不作为程序判断依据 |
| updatedData.request_id | 字符串 | 服务端生成的关联 ID |
| updatedData.error_code | 字符串，可缺省 | 失败类别；成功时省略 |
| updatedData.retryable | 布尔 | 是否建议自动重试；显式 false 必须尊重 |
| updatedData.client_policy | 对象，可缺省 | 仅版本或渠道拒绝时提供 |
| client_policy.channel | 字符串 | 被拒绝的渠道；非正常超长值可能归为 unknown |
| client_policy.minimum_version | 字符串，可缺省 | 该渠道的最低版本，不含前导 v；渠道禁用时通常无此字段 |

不提供 `data` 别名。响应头 X-Request-ID 优先于正文 ID；客户端自己发送的 X-Request-ID 不会被当作服务端关联 ID。未知响应字段可以忽略。

认证失败不附带版本策略。边缘代理可能提前返回 HTML、空正文或没有 updatedData 的错误，JSON 解码失败不能使客户端崩溃。

## 8. 错误码与处理动作

以下为 v3 应用响应；旧入口退休另列于表中。

| HTTP | error_code | 当前 retryable | 客户端动作 |
| --- | --- | --- | --- |
| 400 | invalid_client_metadata | false | 修正 UA；不重试同一请求 |
| 400 | client_version_unsupported | false | 暂停自动上传，提示升级 |
| 400 | client_channel_disabled | false | 暂停自动上传，提示渠道不可用 |
| 401 | invalid_client_credentials | false | 暂停自动上传，检查 v3 认证配置 |
| 403 | upload_not_allowed | false | 提示该上传受权限限制；不解析内部账号状态 |
| 400 | payload_decryption_failed | false | 检查加密口令、AAD、nonce/tag 拼接 |
| 400 | invalid_upload_payload | false | 检查路径参数和原始游戏数据 |
| 500 | internal_error | false | 当前实现不建议自动重试；可能配置异常或写入结果不明 |
| 503 | temporarily_unavailable | true | 当前对应写入前依赖失败，可有限重试 |
| 410 | protocol_retired | false | 停止旧协议请求，更新客户端 |

服务端没有为账号不存在和无权限提供可用于枚举的独立错误，客户端也不应从 message 推断差别。

429 通常来自边缘限流，本次后端没有新增 v3 业务限流器。处理 429 时遵循 Retry-After（秒数或 HTTP 日期），没有已知机器码也保留合理的限流退避行为。未知 4xx、3xx 不自动重试；无结构化信息的 500/503 和网络故障可采用下述有限策略，不将所有 5xx 都视为必然可安全重试。

## 9. 重试、暂停与重定向

建议客户端策略（不是服务端保证）：

- 最多 3 次重试，即一次上传最多 4 次 HTTP 尝试。
- 建议退避约 2 秒、5 秒、15 秒，并加入随机抖动。
- Retry-After 优先；若等待时间超过客户端允许保留任务的时间，结束本次重试并提示稍后再试，不提前发送。
- 仅在处理允许重试的状态/错误后重试；`retryable=true` 也不能覆盖版本拒绝或认证失败的暂停逻辑。
- `retryable=false` 优先禁止自动重试，尤其是写入结果不明的 500。
- 暂停状态的恢复应由升级、配置修正或用户明确恢复触发，避免后台反复发送已知失败请求。
- 不跟随 3xx 自动切换地址；尤其不能将密钥转发到不同主机，也不能转回无版本旧路径。

**没有上传幂等性承诺。** 网络超时或响应丢失时，服务端可能已写入；再次发送可能重复处理和触发通知。request_id 仅供排查，不是幂等键，客户端不能通过复用该头要求去重。

## 10. 现有 HarukiProxy 客户端改造清单

现有 `src/upload_protocol.rs` 的响应解析改为：

```rust
let parsed: serde_json::Value =
    serde_json::from_slice(body).unwrap_or(serde_json::Value::Null);
let data = &parsed["updatedData"];

let code = data["error_code"].as_str().unwrap_or("");
// request_id、retryable、client_policy 也从同一 updatedData 对象读取。
```

同步更新测试 fixture，不保留 `parsed["data"]` 的回退分支。X-Request-ID 优先级、状态码与错误码组合及有限重试策略保持不变。

当前新 UA 使用编译期 `HARUKI_PROXY_V3_METADATA=1` 开关。仅部署后端不会改变已安装 APK 的 UA；需发布启用了该开关的构建。当前自定义端点分支如果仍生成无平台 UA，也必须在目标为 Toolbox v3 时调整，否则会立即得到 invalid_client_metadata。

## 11. 联调步骤与验收

1. 由维护方确认目标环境已部署本文协议并提供测试凭据；确认测试账号、区服和数据类型。
2. 检查编译后的实际 UA，至少包含正确版本及 platform。
3. 使用合法原始游戏数据构造 v3 body；先关闭自动重试，完成一次上传并取得 200 和 request_id。
4. 让维护方通过 request_id 确认持久化及客户端元数据记录。HTTP 200 表示上传处理成功，不证明所有后续通知已送达。
5. 在受控环境验证下表错误，再启用有限重试；不在生产制造依赖故障验证重试。

| 用例 | 预期 |
| --- | --- |
| 正式版及允许的 preview/dev/beta/rc | 按实际最低版本策略接入 |
| 没有平台的旧 UA | 400 / invalid_client_metadata |
| 错误认证密钥，同时 UA 非法 | 401 / invalid_client_credentials，先认证 |
| 正确认证、错误加密口令或 AAD | 400 / payload_decryption_failed |
| 原始载荷损坏 | 400 / invalid_upload_payload |
| 版本低于当前渠道下限 | 400 / client_version_unsupported，并暂停 |
| 渠道未允许 | 400 / client_channel_disabled，并暂停 |
| 500 + retryable=false | 不自动重试 |
| 503 + retryable=true | 最多约定次数重试 |
| 代理 429/非 JSON 错误 | 不崩溃，遵循限流/网络策略 |
| 3xx | 不自动降级或跨主机转发密钥 |
| 客户端自带 X-Request-ID | 后端仍生成自己的 ID |

向维护方反馈：时间（注明时区）、HTTP 状态、error_code、request_id、客户端版本及平台。不要附带密钥、完整游戏载荷或未脱敏账号数据。

## 12. v2 下架时间

旧入口：

```text
/harukiproxy/{server}/{user_id}/{data_type}/upload
/api/harukiproxy/{server}/{user_id}/{data_type}/upload
```

在 **2026-11-01 00:00:00（Asia/Shanghai，UTC+8）** 起强制返回 410 / protocol_retired，包括截止时刻本身。对应 UTC 为 2026-10-31 16:00:00。旧入口携带 Sunset 头；新开发只接入 v3。

v2 的 10 月过渡期不表示 v3 接受旧 UA。v3 从当前实现起始终要求 platform。

## 13. 服务端采集与其他接口

每次上传由服务端记录允许的客户端版本、渠道、协议、平台、系统/进程架构、请求 ID、请求字节数、处理耗时及成功/失败阶段。客户端不需要再调用统计上报接口，也不需要发送安装 ID 或设备指纹。

管理员分析 API 需要独立管理员会话，不能使用本协议上传密钥访问。第三方上传客户端只需实现本文的请求与响应；后端部署及统计实现参见 [维护文档](harukiproxy-upload.zh-CN.md)，普通第三方用户授权流程参见 [OAuth2 / OIDC 接入](oauth2-integration.zh-CN.md)。
