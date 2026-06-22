# 爱发电赞助接入说明

后端通过两种方式从爱发电同步赞助者数据，二者互补：

- **Webhook（被动接收）**：爱发电在产生订单时主动回调本服务配置的 URL。
- **API（主动拉取）**：后台用 token 生成 sign 签名，定时调用爱发电开放接口拉取历史赞助者。

## 1. 配置项

配置位于 `haruki-toolbox-configs.yaml` 的 `afdian` 段，所有字段均可用环境变量覆盖：

| YAML 字段 | 环境变量 | 默认值 | 说明 |
|---|---|---|---|
| `user_id` | `AFDIAN_USER_ID` | 空 | 爱发电开发者 user_id |
| `api_token` | `AFDIAN_API_TOKEN` / `AFDIAN_API_KEY` | 空 | 爱发电开放 API token，切勿泄露 |
| `api_base_url` | `AFDIAN_API_BASE_URL` | `https://afdian.com/api/open` | 开放 API 基础地址 |
| `request_timeout_seconds` | `AFDIAN_REQUEST_TIMEOUT_SECONDS` | `10` | 调用爱发电 API 的超时时间（秒） |
| `webhook_secret` | `AFDIAN_WEBHOOK_SECRET` | 空 | webhook 回调 URL 的密钥路径段，见下文 |
| `sync_enabled` | `AFDIAN_SYNC_ENABLED` | `true` | 是否启用后台定时拉取 |
| `sync_interval_seconds` | `AFDIAN_SYNC_INTERVAL_SECONDS` | `300` | 定时拉取间隔（秒），最小 60 |

`user_id` 或 `api_token` 任一为空时，定时同步与 webhook 的 API 反查都会自动停用。

## 2. Webhook 接入与安全

爱发电的 webhook **不带任何签名**，请求体可被任意伪造，因此后端做了两道校验：

### 2.1 URL 密钥（第一道）

爱发电后台「Webhook URL」配置为带密钥的路径：

```
https://<域名>/api/sponsor/afdian/callback/<webhook_secret>
```

- 当 `webhook_secret` 配置非空时，缺少或不匹配密钥的请求会被拒绝（仍按约定返回 `{"ec":200}` 以避免爱发电重试），并记录一条 warn 日志。
- `webhook_secret` 留空时，跳过该校验，仅依赖第二道 API 反查。

### 2.2 API 反查（第二道）

收到 webhook 后，后端只取请求体里的 `out_trade_no`，用 sign 调用爱发电 `query-order` 接口反查订单是否真实存在，并**以 API 返回的订单数据为准**入库，webhook 请求体的其余内容一律不信任：

- 订单在爱发电侧不存在 → 视为伪造，跳过入库并记录 warn 日志。
- 反查请求失败（网络/接口错误）→ 跳过入库并记录 warn 日志。
- 未配置 API 凭据（`user_id`/`api_token` 为空）→ 无法反查，降级为信任 webhook 请求体入库，并记录 warn 日志提示。

> 生产环境建议同时配置 `webhook_secret` 与 API 凭据，两道校验同时生效。

### 2.3 响应格式

后端始终返回 `{"ec":200}`（HTTP 200），仅在数据库写入失败时返回 `{"ec":500}`，与爱发电的约定一致。

## 3. 定时同步

`sync_enabled` 为 `true` 且 API 凭据完整时，后端在启动后立即执行一次拉取，之后每 `sync_interval_seconds` 秒拉取一次，调用 `query-sponsor` 接口分页导入赞助者。`sync_enabled` 为 `false` 时仅依赖 webhook。

## 4. 相关端点

| 端点 | 方法 | 鉴权 | 说明 |
|---|---|---|---|
| `/api/misc/sponsors`、`/api/sponsor/afdian` | GET | 公开 | 赞助者展示列表 |
| `/api/sponsor/afdian/callback[/:secret]` | POST | 公开（密钥 + API 反查） | 爱发电 webhook 回调 |
| `/api/admin/sponsors` | GET | 管理员 | 后台赞助者列表 |
| `/api/admin/sponsors/:sponsor_id` | PUT | 管理员 | 编辑赞助者档案 |
| `/api/admin/sponsors/sync/afdian` | POST | 超级管理员 | 手动触发一次 API 同步 |

Oathkeeper 公开规则见 `external/oathkeeper/access-rules.yml` 中的 `haruki-public-afdian-sponsor-*`。
