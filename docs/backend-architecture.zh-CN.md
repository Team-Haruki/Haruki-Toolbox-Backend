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

## 2. 依赖方向

允许的核心依赖方向如下：

```text
main -> bootstrap -> api -> modules -> platform
                         \---------------> utils
              \--------------------------> platform
              \--------------------------> utils
```

- `main.go` 不创建数据库、外部客户端或业务 handler。
- `api/` 只组合路由，不实现用例。
- 业务模块可以依赖 `internal/platform` 和 `utils`，但不应通过反向 import 暴露自身能力。
- `internal/platform` 与 `utils` 都不得 import `internal/modules`。
- `internal/platform` 与 `utils` 是并列的复用层；迁移期少量 `utils` 仍依赖
  `internal/platform` 的身份、鉴权头和 runtime-config 能力。架构测试把这些
  既有边精确锁定为只减不增，新增基础能力应放在不造成反向依赖的位置。
- 跨域调用依赖由消费方定义的窄接口，通过 `internal/bootstrap` 注入；不得新增可变包级回调或服务定位器字段。
- 业务代码不直接依赖另一个模块的 HTTP handler、Fiber 路由或响应类型。

仓库中的架构测试会守卫 `utils/**`、`internal/platform/**` 不反向依赖 `internal/modules/**`。

## 3. Composition root 与生命周期

`internal/bootstrap.Build` 是唯一的进程级装配入口：

1. 校验不可变启动配置。
2. 获取数据库、Redis、MongoDB、日志、HTTP 与第三方客户端。
3. 构造业务服务并显式注入模块。
4. 注册路由和后台任务。
5. 返回拥有全部资源的 `Application`。

`Application.Serve` 只负责运行并响应取消；`Application.Close` 幂等完成关闭。生命周期区分两类后台工作：爱发电同步与性能采样属于长期 scheduler，收到关闭信号后先取消并等待；upload audit、上传 fanout、birthday/webhook 通知及 iOS 异步组包属于请求派生的有限任务，必须先让 Fiber 停止接收请求并完成 handler drain，再封口任务组并有界等待。只有 HTTP shutdown 与 upload task drain 都成功后，才按资源获取的逆序释放连接；任一阶段超时都保留 PostgreSQL、Redis、MongoDB 等仍可能被使用的资源。`internal/platform/mailnotify` 当前仍使用自身的有界派发器，不属于本轮 upload task group，后续应单独迁移。不要在业务模块中自行管理进程信号或长期资源的关闭顺序。

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
- **adapters/store**：将 Ent、MongoDB、Redis 或第三方客户端适配到 ports。

用户端和管理员端可以保留不同 transport，但共同的状态机、校验、事务与通知规则应进入同一个领域服务。鉴权与审计仍由各自 transport 负责。

## 6. 数据访问与生成代码

- Toolbox 与 Bot 数据库保持独立 DSN、独立 Ent schema 和独立生成目标。
- 当前迁移阶段不移动 Ent 生成目录，也不手工修改生成文件。
- 不为每张表预先创建通用 repository；只为具体用例抽取最小接口。
- `owner_user_id` 等名字相似的字段不构成跨数据库外键，禁止据此合库。
- schema 或数据语义变化独立于结构重构，使用 expand、backfill、switch、contract 的数据库演进流程。

## 7. 契约与安全边界

结构重构默认不改变：

- HTTP 方法、路径、鉴权类型、状态码与 JSON 结构；
- Oathkeeper、Kratos 与 Hydra 的 header、subject 和会话语义；
- Redis key、TTL 与原子计数语义；
- MongoDB collection、文档字段和 int64 精度；
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
