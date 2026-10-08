# 后端架构与渐进重构约定

本文描述 Haruki Toolbox Backend 的目标结构与迁移约束。它是后续代码评审的架构基线，不要求为了目录整齐一次性搬动现有代码。

## 1. 总体形态

项目采用按业务域组织的模块化单体：同一业务用例的用户端、管理员端、后台任务和外部适配逻辑共享一个领域边界，HTTP 身份不是拆分业务模块的依据。

```text
main.go                         进程入口，仅加载配置并驱动应用生命周期
api/                            总路由装配与 HTTP 契约清单
internal/bootstrap/             composition root、资源获取、生命周期与配置校验
internal/modules/<domain>/       业务域；handler、use case、消费方接口
internal/platform/               跨业务域复用的平台能力
utils/                           数据库、HTTP、邮件等基础设施与外部系统适配器
ent/                             Ent schema 与代码生成入口
utils/database/postgresql/       Toolbox Ent 生成产物（迁移期保持位置不变）
utils/database/neopg/            Bot Ent 生成产物（迁移期保持位置不变）
```

小模块可以保持少量平铺文件；只有当职责已经明确并且文件数量足够多时，才拆成 `transport`、`service`、`store` 或 `adapter` 子目录。目录层级不应先于实际依赖边界出现。

### 当前共享包布局

| 目录 | 职责 |
| --- | --- |
| `internal/platform/api` | HTTP 公共响应、会话验证、身份集成、审计及游戏数据访问；`data`、`ios` 为子包 |
| `internal/platform/upload` | 游戏上传预处理、Suite 复原编排、同步与通知 |
| `internal/platform/runtimeconfig` | Redis 中可由管理员修改的运行期设置（如 `oauth2DeviceFlowEnabled`） |
| `internal/platform/mailnotify` | 有界的通知邮件派发 |
| `internal/platform/{authheader,filtering,identity,pagination,timeutil}` | bearer 头解析、CSV 过滤值、邮箱规范化、分页与时间范围工具 |
| `internal/platform/oauth2` | Hydra 客户端（含不跟随重定向的 `DoWithoutRedirect`）、token introspection（bearer 中间件与内部 API 共用 `IntrospectAccessToken`）、scope 定义与鉴权中间件 |
| `utils/game/sekai`、`utils/game/sekaiapi` | 游戏客户端加解密、协议处理及 SekaiAPI 适配 |
| `utils/game/nuverserestore` | 统一 AVSC 加载、Suite/MYSEKAI 复原、compact 列式展开及离线对照工具 |
| `utils/codec/{jsoncodec,jsonvalue,msgpackcodec}` | 通用 JSON/MessagePack 编解码与 JSON 值操作 |
| `utils/orderedmap` | 通用有序容器 |
| `utils/database` | 数据库连接、游戏数据存储、Redis 及 Ent 生成代码 |
| `utils/{http,smtp,cloudflare,logger,background,perfstats,perfdebug,redact}` | 网络、邮件、外部适配、日志、日志脱敏与运行期基础设施 |

### OAuth2 设备授权的代码落点

RFC 8628 设备授权集中在 `internal/modules/oauth2`，不另建模块；行为、Redis 键与状态机以 [Ory 套件使用说明](ory-suite-usage.zh-CN.md) §10.5 为准，配置与启动校验以 §10.6.1 为准。

| 位置 | 职责 |
| --- | --- |
| `hydra_device_authorization.go` | `POST /api/oauth2/device/auth`，代理 Hydra 并签发包装设备码 `hdc_…` |
| `hydra_token_endpoint.go` | `POST /api/oauth2/token` 兼容层：设备授权许可走本地状态机，其余请求经 `handleHydraPublicProxy` 逐字节转发 |
| `hydra_device_browser.go`、`hydra_device_verification.go` | `/device` 页面调用的 lookup / approve / deny，以及 approve 内的服务端代驱链 |
| `hydra_device_store.go`、`hydra_device_decision_store.go`、`hydra_device_rate_limit.go` | Redis 流程状态机、Lua 脚本与限流计数 |
| `hydra_device_codes.go`、`hydra_device_policy.go`、`hydra_device_challenge.go` | 码的生成、密封与规范化；设备授权的 scope 策略；通用 login / consent 拒绝设备模式的 challenge |
| `hydra_device_config.go` | 不可变的 `DeviceFlowConfig` 与运行时闸门 `Active(ctx)` |
| `hydra_device_reaper.go` | 回收器 `StartDeviceFlowReaper`：撤销已批准却从未兑换的授权（见 §3） |
| `internal_introspect.go` | 供自有服务使用的内部令牌校验接口（不对外），只在配置了内部 token 时注册，不经 Oathkeeper |
| `internal/modules/useroauth`、`adminoauth`、`adminusers` | 按设备列出与撤销授权；管理端 client 的 `grantTypes` / `devicePolicy`；管理员只读镜像 |
| `utils/database/redis/keys.go` | `BuildOAuth2Device*Key`：`haruki:oauth2-device:` 与 `haruki:rate-limit:oauth2-device:` 两个前缀，码只以 HMAC 出现在键名里 |
| `utils/redact` | 访问日志里 `user_code`、`device_code`、`flowHandle` 与令牌字面量的脱敏 |

配置分两处：启动配置 `oauth2.device_flow.*`、`oauth2.internal_api.*` 与作为键名 HMAC 密钥的 `user_system.session_sign_token`（`config/types.go`，校验在 `internal/bootstrap/validate.go`），以及运行期设置 `oauth2DeviceFlowEnabled`（`internal/platform/runtimeconfig`，字段缺失视为关闭）。过期的 Hydra 设备码行由 compose 中的 `hydra-device-janitor` 清理，它是独立容器，不在后端进程里。

`utils` 根暂保留共享类型和枚举。包迁移直接更新调用方，不保留旧路径转发包；修改 Go 导入路径不改变 HTTP、配置、存储或加密协议。测试数据随包迁移，仓库 `data/` 中的发布 schema 路径保持不变。

### Bot 安全告警的代码落点

`internal/modules/botsecurity` 是一个完整业务域：Haruki Cloud 推送的 bot 安全告警（阈值越线）由内部接口接收并去重写入 Toolbox 库的 `bot_security_alerts`（不对外、不经 Oathkeeper，只在配置了 `bot_security.ingest_token_sha256` 时注册）；管理端在 `/api/admin/bot-security` 下列出、处理与汇总告警，告警所属 bot 的主人 QQ 按页批量从 Bot 库查询，Bot 库不可用时只置空、不让列表失败。

## 2. 依赖方向

允许的核心依赖方向如下：

```text
main -> bootstrap -> api -> modules -> platform
                         \---------------> utils
              \--------------------------> modules
              \--------------------------> platform
              \--------------------------> utils
```

- `internal/bootstrap` 可以 import 业务模块，用于构造模块配置、启动模块的长期任务（如 `StartDeviceFlowReaper`）及注入依赖；反向禁止。
- `main.go` 不创建数据库、外部客户端或业务 handler。
- `api/` 只组合路由，不实现用例。
- 业务模块可以依赖 `internal/platform` 和 `utils`，但不应通过反向 import 暴露自身能力。
- `internal/platform` 与 `utils` 都不得 import `internal/modules`。
- `internal/platform` 可以依赖 `utils`；`utils` 不得反向依赖 `internal/platform`。原有例外已随平台服务迁移清除，架构测试禁止重新引入。
- 跨域调用依赖由消费方定义的窄接口，通过 `internal/bootstrap` 注入；不得新增可变包级回调或服务定位器字段。
- 业务代码不直接依赖另一个模块的 HTTP handler、Fiber 路由或响应类型。

仓库中的架构测试（`internal/architecture`）守卫：`utils/**`、`internal/platform/**` 不 import `internal/modules/**`；`utils/**` 不 import `internal/platform/**`；`api`、`internal/modules`、`internal/platform`、`utils` 不新增全局 `config.Cfg` 读取（遗留读取按白名单只减不增）；`HarukiToolboxRouterHelpers` 与 `HarukiToolboxDBManager` 不新增字段。

## 3. Composition root 与生命周期

`internal/bootstrap.Build` 是唯一的进程级装配入口：

1. 校验不可变启动配置。
2. 获取PostgreSQL（业务库与游戏数据库）、Redis、日志、HTTP 与第三方客户端。
3. 构造业务服务并显式注入模块。
4. 注册路由和后台任务。
5. 返回拥有全部资源的 `Application`。

`Application.Serve` 只负责运行并响应取消；`Application.Close` 幂等完成关闭。生命周期区分两类后台工作：爱发电同步、OAuth2 设备授权回收器（`oauth2.device_flow.enabled=true` 时启动，不受运行时总开关影响）与性能采样属于长期 scheduler，收到关闭信号后先取消并等待；upload audit、上传 fanout、birthday/webhook 通知及 iOS 异步组包属于请求派生的有限任务，必须先让 Fiber 停止接收请求并完成 handler drain，再封口任务组并有界等待。只有 HTTP shutdown 与 upload task drain 都成功后，才按资源获取的逆序释放连接；任一阶段超时都保留 PostgreSQL、Redis 等仍可能被使用的资源。`internal/platform/mailnotify` 当前仍使用自身的有界派发器，不属于本轮 upload task group，后续应单独迁移。不要在业务模块中自行管理进程信号或长期资源的关闭顺序。

## 4. 配置所有权

配置分为两个明确平面：

- **启动配置**：YAML 与环境变量加载得到的不可变配置，只在 composition root 读取，并以按能力裁剪后的值注入消费者。
- **运行期设置**：管理员可修改、存储在 Redis 的设置，由 `internal/platform/runtimeconfig.Service` 统一拥有。

新代码不得直接读取全局 `config.Cfg`。迁移旧代码时先保持现有默认值、环境变量别名和覆盖顺序，再把所需字段收敛成 typed config 或窄接口。运行期设置必须保持现有 Redis key 和 JSON 字段兼容。

## 5. 业务模块内部边界

一个成熟业务域通常包含以下职责，但不强制为每项创建目录：

- **transport**：解析 Fiber 请求、鉴权结果和分页参数，映射既有状态码与 JSON。
- **service/use case**：事务、状态转移和跨实体规则；不依赖 Fiber。
- **ports**：该用例实际需要的存储、通知或外部服务窄接口，定义在消费方。
- **adapters/store**：将 Ent、PostgreSQL 游戏数据存储、Redis 或第三方客户端适配到 ports。

用户端和管理员端可以保留不同 transport，但共同的状态机、校验、事务与通知规则应进入同一个领域服务。鉴权与审计仍由各自 transport 负责。

## 6. 数据访问与生成代码

- Toolbox 与 Bot 数据库保持独立 DSN、独立 Ent schema 和独立生成目标。
- 当前迁移阶段不移动 Ent 生成目录，也不手工修改生成文件。
- 不为每张表预先创建通用 repository；只为具体用例抽取最小接口。
- `owner_user_id` 等名字相似的字段不构成跨数据库外键，禁止据此合库。
- schema 或数据语义变化独立于结构重构，使用 expand、backfill、switch、contract 的数据库演进流程。

### 游戏数据存储约束

Suite / MYSEKAI 由独立 PostgreSQL pool 读写；`game_data.url`（或 `GAME_DATA_URL`）为必填，`game_data.read_source` 仅接受 `postgres`。MongoDB 已退役，残留 BSON 工具及 `mongodb.private_api_*` 配置命名仅用于兼容，不建立 Mongo 连接。

游戏字段以 `utils/database/gamedata/catalog` 为准。`userInherit`、`userPlatformInheritIos`、`userPlatformInheritAndroid`、`userPlatforms`、`userRegistration` 包含继承凭据或个人信息，必须维持拒绝存储规则；不得因删除旧的字段置空配置而重新纳入。API 的投影与授权另行生效。

游戏数据的 revision 与缓存一致性改造尚未落地；在此之前，条件读取以 unix 秒精度的 `upload_time` 为准，同一秒内的多次上传无法区分。

#### 私有数据接口的服务端视图（`profile`）

私有 token 接口 `GET /api/private/game-data/:server/suite/:user_id` 接受 `profile=<名称>`，返回服务端定义的视图：完整私有文档减去该视图的拒绝列表。目前只有 `cloud`，拒绝 `userCostume3dStatuses` 与 `userCostume3dShopItems`（Haruki Cloud 不读这两项，它们占 JP 压缩响应约 47%、CN/TW 约 23%）。

- 视图按完整文档渲染，不是 key 投影：保留 `extra` 中的未编目键（新游戏版本的键先落在这里），行中不存在的键省略而不是返回 `null`，`userGamedata` 照常只返回七个允许字段；被拒绝键在 `extra` 中的 compact 别名同样不返回。读库时不选择被拒绝的列。
- 鉴权与完整文档完全相同：先由已验证绑定解析数据属主，授权查询带 `UserIDEQ(owner)`。`profile` 只在查询参数层面校验，不改变任何鉴权分支。
- 缓存：每个文档版本一份规范条目，surface 段为 `private-profile-<名称>-<拒绝列表摘要>`（不含 `:`；与完整文档一样经 `SetGameDataBodyCache` 写入该文档的缓存索引 `game_data:idx:…`），TTL 与完整文档相同（7 天，新版本窗口内 5 分钟）。上传清理完整文档缓存时一并清理；拒绝列表变化时摘要变化，读者直接换到新条目。任意 `?key=` 组合仍是 6 小时上限。
- `known_upload_time` 条件读取（304 + `X-Upload-Time`）与 zstd 直通（`Content-Encoding: zstd`）和完整文档一致。
- 未知名称、与 `key` 同时使用、或用于 `suite` 以外的数据类型，返回 400。
- 新增视图只改 `internal/modules/userprivateapi/profile.go` 的注册表；拒绝列表只放消费方确定不读的键。

#### suite 大列的变更门

suite 上传的 upsert 对每列执行 `COALESCE(EXCLUDED.c, t.c)`，值未变也会重新校验、压缩、写 TOAST 和 WAL。`writer.go` 的 `changeGatedSuiteKeys` 列出的列（目前是 `user_costume3d_statuses_j`、`user_costume3d_shop_items_j`，约占 JP 行存储的 62%）在写入事务内先以 `SELECT sha256(convert_to(col::text, 'UTF8')) ... FOR UPDATE` 读取已存值摘要，与本次最终编码字节（复原、拒绝键丢弃、别名与目录解析之后）的摘要相同时传 NULL，由 COALESCE 保留原 TOAST 指针。

- 不存摘要列，摘要每次从已存值现算，因此手工修复、`WriteMigrate` 重建或目录变化都不会留下过期摘要。
- 门内列用排序键编码（`json.Deterministic`）：上传解码为 Go map，默认编码的键顺序每次不同，不排序摘要永远不相等。其它列编码不变。
- 历史合并键（`userEvents` 等）与全部 mysekai 列不能加入：前者要与已存值合并，后者的扁平子列由 `EXCLUDED` 硬赋值，传 NULL 会清空。
- 行锁保证比较与 upsert 之间没有其它写入；行不存在或列为 NULL 时照常写入。加入新列前先确认它在两次上传之间通常不变，否则只多付一次摘要读取。

性能工作优先减少重复解析、展开、查询和复制；引继等待及代理上游耗时不计入后处理优化收益。对照测试应保持投影、压缩协商、缓存状态和响应体积一致，分别记录延迟分位与分配量，不能将微基准结果直接当作生产 API 收益。

## 7. 契约与安全边界

结构重构默认不改变：

- HTTP 方法、路径、鉴权类型、状态码与 JSON 结构；
- Oathkeeper、Kratos 与 Hydra 的 header、subject 和会话语义；
- Redis key、TTL 与原子计数语义；
- PostgreSQL 游戏数据列、响应字段和 int64 精度；
- Webhook 的 dial-time DNS 校验、IP pin、重定向与 proxy 限制；
- 上传深度、字段名、剩余长度等不可信输入限制；
- 管理员角色层级、对象级 scope 和公开接口的防枚举行为。

管理员角色层级必须在数据库查询阶段生效，而不是在构造响应时事后隐藏：
普通 `admin` 的列表、分页总数、聚合、导出与详情都不得包含
`super_admin` 作为 actor、owner、creator 或 target 的用户级对象；写操作同样先调用
`admincore.EnsureAdminCanManageTargetUser`。新增管理员全局视图时，应同时覆盖正常列表与
alternate route（统计、审计、风险、OAuth、工单等），并为普通管理员不可见超级管理员数据补回归测试。

`api/testdata/routes.golden` 固定完整路由清单。任何预期的 API 变化都应单独评审，并同步 Oathkeeper 规则和对接文档。

## 8. 渐进迁移顺序

每个业务域按以下顺序独立迁移：

1. 固化该域的路由、响应和安全契约。
2. 提取不依赖 Fiber 的校验、状态转移和事务用例。
3. 在消费方定义存储和外部服务的窄接口。
4. 从 composition root 注入实现，使 handler 只做 transport 映射。
5. 删除旧委托和对 `HarukiToolboxRouterHelpers`、全局配置的依赖。
6. 运行触达包测试、跨模块测试和全量 race 测试。

迁移期间 `HarukiToolboxRouterHelpers` 与聚合 `DBManager` 仅作为兼容层；不得向其中继续添加新的业务能力。一次提交不要同时混入目录移动、Ent schema、数据迁移和外部行为变化。
