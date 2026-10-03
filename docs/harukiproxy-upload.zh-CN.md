# HarukiProxy 上传

v3 上传入口（POST）：

- `/harukiproxy/v3/:server/:user_id/:data_type/upload`
- `/api/harukiproxy/v3/:server/:user_id/:data_type/upload`

`User-Agent` 格式及最低客户端版本仍由 `haruki_proxy.user_agent` 和
`haruki_proxy.version` 控制。`X-Haruki-Toolbox-Secret` 必须使用 v3 认证密钥。

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

# 生日材料监听

按材料名配置监听时，支持 `diamond`（12）、`yuugiri` / `yugiri`（5）、
`clover`（20）、`battery`（17，电池）、`amethyst` / `quartz`（11，紫水晶，
主数据名称为闪耀石英），以及对应 `mysekai_material_<ID>` 名称。
显式 `material_ids` 仍按 ID 过滤；事件继续返回 `matched_material_ids`，
并保留命中位置、同一采集物的相关掉落与采集物信息。
