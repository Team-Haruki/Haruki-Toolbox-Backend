# Haruki Toolbox Backend 性能优化调研

日期：2026-09-08 · 面向项目维护者 · 源码：当前 Go 1.27.1 工作区 · 生产：`user-events-fix-223e0dd28cea`

## 结论与优先级

**先减少重复工作，再调整资源参数。** 最值得先实施的是：固定数据库 SQL 字段顺序、复用同一次请求中的 compact 展开结果、复用 gzip 编码器，以及缩减后台同步的重复解码和大对象驻留。缓存版本与后台任务容量控制是下一阶段的结构性工作。

初始调研只做只读生产采样和隔离实验；随后经用户授权实施第一批优化，见文末实施记录。**按用户要求，引继上传耗时属于预期行为，完全排除出优化候选和收益计算。** 上传完成后的通用缓存/后台同步资源使用仍在范围内，它同样服务其他上传来源。

| 建议顺序 | 优化点 | 证据 | 收益边界 | 改造风险 |
| --- | --- | --- | --- | --- |
| 1 | 固定 UPSERT SQL 列顺序 | 真实隔离 PostgreSQL 对照 | 1000 次合成小写入中位耗时下降约 36%；不等于全站提速 36% | 低至中：列和参数必须同步重排 |
| 2 | compact / mysekai 父对象在请求内只展开一次 | 代码 + 合成基准 | compact 冷读样本分配约减半，CPU 耗时下降约 44% | 中：保留缺字段与损坏数据语义 |
| 3 | gzip writer / buffer 有界复用 | 合成原型对照 | 样本分配约降 92%，CPU 耗时约降 15% | 中：并发独占、池容量及错误清理 |
| 4 | 后台同步按需处理、有界排队、减少重复解码 | 代码 + 归一化成本实验 | 减少峰值内存及无效 CPU；尚无生产收益百分比 | 中至高：对象生命周期、投递可靠性 |
| 5 | 合并 Auth Proxy 请求内重复用户查询 | 代码直接确认 | 常规身份解析/资料同步路径从 2 次 SELECT 降到 1 次 | 中：不得缓存或省略授权判断 |
| 6 | 历史合并仅读取本次上传涉及的列 | 代码直接确认 | 局部历史更新少读、少解码，缩短锁持有时间 | 中：保留行锁和首次并发写保护 |
| 7 | 独立数据 revision，逐步替代全库 SCAN 失效 | 代码 + 生产 Redis 统计 | 扩展性和一致性改进；当前低负载成本不高 | 高：需完整缓存并发/故障设计 |
| 8 | 管理端查询前移过滤、数据库聚合 | 代码直接确认 | 用户/审计日志增长时避免全量抓取 | 中；当前使用频率低 |

可以把 1、2、3 作为第一批独立小改动；4、7 应单独设计并验证，不宜与小优化混在一次部署里。

## 生产基线：目前没有整体资源饱和证据

2026-09-08 07:43:51–07:49:27（北京时间）的两次只读采样：后端 healthy，重启 0；首样内存约 542 MiB，PostgreSQL 约 2.19 GiB，Redis 约 1.14 GiB。首样 CPU 都很低，只能说明**这个早晨采样窗口低负载**，不能说明全天峰值充足。三个容器均未设置独立 Docker memory/CPU 限额；Docker 显示的 15.25 GiB 是可见宿主机内存，不是 backend 专用额度。

- 游戏库 18 个连接、Toolbox 库 4 个连接在首样均 idle，没有连接池饱和证据。
- suite：约 11,403 行，含索引/TOAST 总占用 4.94 GB；累计 HOT 更新 26,030 / 26,087（99.78%）。mysekai：约 4,208 行、396 MB，HOT 32,919 / 32,920。行数和 dead tuple 是统计估计，不能直接视为精确膨胀率。
- Redis：8,609 个 key，内存使用约 1.28 GB、maxmemory 3 GiB，evicted_keys=0。336 秒差分窗口内约 9.04 commands/s，SCAN 216 次、服务端 CPU 合计 131 ms。当前这段窗口中 SCAN 并不构成 CPU 饱和。
- 数据库副本 streaming。没有观察到 deadlock 或 temp spill，但计数属于累计统计。
- PostgreSQL `shared_preload_libraries` 为空，游戏库仅有 plpgsql，未启用 pg_stat_statements；`track_io_timing=off`。因此统计里的 IO 时间为 0，**不能解释为没有 IO 成本**。
- 后端运行环境包含 DEBUG 日志级别，但没有取得 CPU/heap/block profile，无法据此断言日志或 GC 是首要瓶颈。

原始证据为本次只读聚合采样，不含账号、凭据或完整请求内容。连接池等待和 GC 需要增量计数；`EmptyAcquireCount` 启动 Ping 本来就可能非零，现有门禁已正确使用预热后的差值。[pool.go](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/utils/database/gamedata/pool.go:67)、[gate_bench_test.go](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/utils/database/gamedata/gate_bench_test.go:49)

## 真实请求分布：优先看路由及数据类型

访问日志统计窗口约为 2026-09-07 07:50 至 09-08 07:50，以下仅列成功请求。不同采样脚本执行相差几分钟，分组以最终按路由/类型统计为准；窗口跨过 9 月 7 日的修复部署，不能用它作为部署前后 A/B 结果。

| 路由或类型 | 成功请求数 | p50 | p95 | p99 |
| --- | ---: | ---: | ---: | ---: |
| `/api/public/...` 游戏数据 | 3,740 | 2.59 ms | 4.27 ms | 10.11 ms |
| `/api/private/game-data/...` | 3,818 | 22.94 ms | 233.05 ms | 272.99 ms |
| 本人/被授权账号 suite 数据 | 2,260 | 6.22 ms | 17.69 ms | 46.32 ms |
| 本人/被授权账号 profile | 4,482 | 262.13 ms | 523.29 ms | 733.02 ms |
| `/api/user/me` | 10,371 | 3.81 ms | 5.45 ms | 7.91 ms |
| `/harukiproxy/.../upload` | 961 | 146.72 ms | 1,205.73 ms | 1,537.97 ms |
| `/ios/script/.../upload` | 3,598 | 7.25 ms | 12.63 ms | 38.57 ms |

本人 mysekai 只有 3 个成功样本，未据此做尾延迟判断。管理员接口成功样本也很少，管理端优化按规模风险排在后面。

**解释边界：** profile 会调用游戏公开资料服务，不能把这部分慢请求归因于 PostgreSQL；不同上传入口的响应语义和样本体积也不一样，不能直接把上述差异当作实现优劣。[game_account_data.go](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/internal/modules/usergamebindings/game_account_data.go:115)

这里测的是 Fiber logger 记录的处理耗时，不是用户端完整下载时间。压缩中间件注册在 logger 外层，部分回程压缩成本及网络传输未被完整覆盖；日志的 bytesSent 实际是记录时的 `Response().Body()` 长度，缓存 gzip 与未压缩响应口径可能不同。因此没有把约 17 GB 的私有读取累计 body 长度称为实际网络流量。[fiber.go](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/internal/bootstrap/fiber.go:35)

私有读取返回大包的占比较高，是值得建立“请求字段数、返回字节、缓存命中、是否 compact、压缩协商”的分层观测对象；目前还不能区分其 p95 中数据库、解压、渲染各占多少。

## 1. 固定 SQL 字段顺序：最适合先做

`encoded.order` 的注释称顺序稳定，但 `encode` 遍历 Go map，并按遇到顺序追加列；合并历史列时又遍历另一个 map。`upsertStatement` 直接依此生成 SQL，同一字段集合因此反复生成不同语句。[writer.go:137](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/utils/database/gamedata/writer.go:137)、[writer.go:252](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/utils/database/gamedata/writer.go:252)、[writer.go:486](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/utils/database/gamedata/writer.go:486)

pgx 按 SQL 文本缓存预编译语句，当前版本默认 statement cache 容量为每连接 512；不同语句文本可能持续淘汰旧条目。生产 DSN 仍应核对是否显式覆盖默认值，本次真实数据库实验使用依赖默认配置。[pgx v5.10.0 conn.go](https://github.com/jackc/pgx/blob/v5.10.0/conn.go)

在本机 PostgreSQL 18 中，同一账号、12 个固定字段、每轮 1000 次小 UPSERT，运行 6 组对照；后三组倒置顺序以检查预热偏差：

| 指标 | 当前顺序 | 规范顺序原型 |
| --- | ---: | ---: |
| 每 1000 次产生的 SQL 文本 | 944–961 种 | 1 种 |
| 查询结束时 prepared statement 数量 | 513 | 4 |
| 1000 次总耗时范围 | 4.56–5.73 s | 2.26–3.83 s |
| 六轮总耗时中位数 | 4.945 s | 3.158 s |
| 六轮 p50 的中位数 | 4.515 ms | 2.784 ms |

prepared 数量包含测试初始化和检查语句，并不表示规范版需要四种 UPSERT。总耗时中位数下降约 **36%**，每组方向一致；但倒序组中有两次规范版 p95 更高，说明本机尾延迟噪声仍大，**不能宣称生产 p95 必定改善 36%**。

实施建议：对 SQL 使用 catalog 固定顺序或排序后的列列表，参数从同一列表生成；读取投影也可规范化以共享模板，但响应字段顺序、缓存请求 key、别名胜负、mysekai 缺失子字段清理语义保持不变。验收以同一字段集合 unique SQL=1、读写契约全通过、实际 prepare/describe 次数减少为先，再比较生产灰度 p95。

## 2. 存在性检查正在重复做完整展开

`HasAny(keys)` 为判定 404 调用 `RawValue`；compact 字段会真正展开成行数组，随后 `SuiteBody` 再展开一次。mysekai 的 `updatedResources` 同理，存在性检查和渲染会各自重建整个父对象。[fetch_postgres.go](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/utils/api/data/fetch_postgres.go:44)、[store.go:204](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/utils/database/gamedata/store.go:204)、[store.go:304](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/utils/database/gamedata/store.go:304)

合成 `compactUserMusicResults` 样本包含 4096 行，比较当前 `HasAny + SuiteBody` 与只展开一次的成本下界，三轮中位数：

| 路径 | 时间 | 每次分配字节 | 分配次数 |
| --- | ---: | ---: | ---: |
| 当前双次展开 | 3.149 ms | 5,062,734 B | 106,669 |
| 单次展开成本下界 | 1.750 ms | 2,531,529 B | 53,335 |

多键请求的 HasAny 找到可用值后即停止，不是所有字段都必然展开两次。该单键路径时间减少约 44%，分配接近减半；**只作用于实际展开的请求，不能外推至稳定缓存命中**。实验跳过第二次工作以度量成本，不是建议删除存在性检查。

实施应采用请求内展开结果 memo，或一次渲染同时返回“是否有数据”。损坏 compact、空值、缺失字段、alias、未知 extra 子字段及大整数都需保持既有结果，不能以“列非 NULL”替代所有检查。

## 3. gzip 复用适合做小型优化，zstd 参数先不改

游戏响应已经做到 gzip 一次写缓存、命中直接发送，这是应保留的优化。但缓存未命中时 `CompressGameDataBody` 每次创建新的 gzip writer 和 buffer。[compressed_body.go](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/utils/api/data/compressed_body.go:26)

约 427 KB、5000 个合成记录的 JSON，三轮中位数：

| 路径 | 时间 | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| 当前新建 gzip writer | 0.638 ms | 1,002,337 | 18 |
| 复用 writer/buffer 原型 | 0.545 ms | 75,340 | 2 |

分配减少约 92%，时间减少约 15%。这是单线程复用原型，还需要测并发池、错误恢复和大 buffer 滞留；分配更少不等于常驻内存更少。池必须设置容量策略，编码器返回池前清理引用，避免保留偶发大包。

本次也比较了 zstd 默认并发与 concurrency=1：压缩比均约 0.1139，但三轮时间波动明显，单并发中位数还更慢（约 0.662 ms 对 0.530 ms）。**本轮不推荐直接改 zstd 并发参数。** 待有整体并发吞吐和 RSS 峰值实验后再定。

## 4. 后台同步：减少无效工作与控制驻留内存

上传主流程的 10 个并发槽位在提交后台任务后释放；`TaskGroup` 跟踪退出但没有任务数、队列或驻留字节上限。普通上传先复制整个 raw，再由后台判断有没有适用目标。目标失效或慢响应时，大包和衍生表示可能长时间保留。[handler.go](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/internal/modules/upload/handler.go:35)、[data_handler.go](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/utils/handler/data_handler.go:79)、[task_group.go](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/utils/background/task_group.go:43)

不同处理阶段还存在重复工作：主上传解密/解码一次；processed 同步再次解密；restored 同步再次解密和完整解码。两种同步格式都需要时，同一有效 map 上传至少有 3 次 AES 解密、2 次 MessagePack 实体化。当前已经按格式复用生成结果，不是每个目标都重新压缩。[uploader.go](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/utils/handler/uploader.go:50)

restored 同步又调用 `json.Marshal(NormalizeProviderResponse(restored))`，产生整棵归一化副本和完整 JSON 中间体。5000 条简单合成记录的成本拆分：深拷贝+Marshal 为 2.414 ms、2.924 MB/op；预先归一化后只 Marshal 为 1.480 ms、0.643 MB/op。这里是**成本拆分，不是可以直接删除归一化的实现证明**，ID 派生和 nil/非有限值兼容仍要保留。[uploader.go:105](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/utils/handler/uploader.go:105)、[provider_normalize.go](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/utils/api/data/provider_normalize.go:39)

建议分三步：

1. 先计算需要的目标和格式，无目标不复制、不解码；必要的接收方检查采用有限并行并统一总 deadline。
2. CPU 编码与网络投递分别限流，记录 queue depth、active bytes、oldest age。已持久化上传的后续任务要背压或可靠排队，不能队列满就静默丢通知。
3. 建立不可变/明确所有权的已验证载荷，在格式间共享一次解密；尝试 `MarshalWrite` 直接写 zstd，再考虑流式归一化消除副本。

不能直接把数据库处理后的 map 交给所有同步分支：restore/preprocess 会改对象和裁剪字段，第三方契约不同。也不能异步引用 Fiber 或解密池已归还的缓冲。此方向应使用慢下游 stub + 1/10 MB 包做受控压力测试，衡量 peak heap、goroutine、投递完整率，当前不报告未经测量的生产百分比。

## 5. Auth Proxy 请求内复用用户查询

常规已关联用户：身份解析先按 Kratos identity 查询本地 user ID，随后资料同步又按 ID 查询同一用户资料。资料不变时已避免 UPDATE，但仍有两次 SELECT。[session_auth_proxy.go](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/utils/api/session_auth_proxy.go:64)、[session_kratos_identity.go](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/utils/api/session_kratos_identity.go:115)、[session_profile_sync.go](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/utils/api/session_profile_sync.go:25)

把首次查询结果扩大为最小所需资料并在请求内传递，可以省一次 SQL 往返；新建/自动关联分支另测。不建议为此缓存身份授权、角色、封禁或 client active 状态。Oathkeeper 信任头、代理会话 ID、伪造客户端头拒绝、email 验证及 Hydra subject fallback 都保留。验收先看请求 SQL 计数，再看真实 RTT 下的 p95，不能假设省一次查询就让整个请求减半。

## 6. 保留合并锁，减少锁内无用列读取

任一历史字段上传时，当前都会读出并解码 userEvents、userWorldBlooms、userGachas 三列，然后只合并本次上传实际包含的键。大 gachas/blooms 配小 events 的局部更新会增加无用读取和锁持有时间。[writer.go](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/utils/database/gamedata/writer.go:333)

可将 `enc.mergedRaw` 键集传入查询，按稳定顺序选择必要列。**首次上传的 INSERT 占位、SELECT FOR UPDATE、事务原子性必须保留**，此前 userEvents 并发回退已经证明不能省锁。PostgreSQL 行锁随事务持有；优化目标是缩短锁内工作，不是移走读取或取消锁。[PostgreSQL 18 行锁](https://www.postgresql.org/docs/18/explicit-locking.html#LOCKING-ROWS)

收益只在局部历史上传出现，三列都上传时基本无效；需先记录历史键组合分布，再用“小 events + 大 blooms/gachas”的本地样本测返回字节、解码分配、持锁时间和同账号并发 p99。

## 7. 数据 revision 与缓存失效应一起设计

每次上传在释放上传槽位前同步执行 `ClearUploadedGameDataCaches`。它先删除 stamp，再 SCAN 整个 Redis keyspace，匹配并 UNLINK 该用户所有版本缓存。MATCH 不把全迭代复杂度变成“仅这个用户的键”，完整 SCAN 仍是 O(N)。[cache.go](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/utils/database/redis/cache.go:335)、[Redis SCAN 文档](https://redis.io/docs/latest/commands/scan/)

当前低负载样本中 SCAN CPU 很低，故排在局部热点之后；它的意义在扩展性和数据版本正确性。现有版本是秒级 `upload_time`，同秒上传可碰撞，人工纠正未改变时间也已经实际出现缓存继续返回旧值。

建议引入事务内递增的独立 revision，覆盖 suite、mysekai、birthday 局部写、人工维护等全部写入口。新请求使用新版本，旧 body 交给 TTL 回收，上传主要失效 stamp。需要考虑两个不可省略的条件：版本必须在权威写事务中变化，缓存写入仍须校验版本；Redis/PG 故障及旧 stamp fallback 不能把旧数据判成 304。

stamp memo 冷读目前也在 body singleflight 之前，可能出现同用户并发回源和两次顺序 Redis SET；可进一步按账号合并 stamp resolution，并 pipeline 不同 TTL 的回写。[stamp.go](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/utils/api/data/stamp.go:41)

这个方案会触及 HTTP 条件请求契约，不能直接删除 SCAN 或把所有 `upload_time` 写成当前时间作为替代。先做 1万/10万 key、本地并发上传+读取、同秒写和故障矩阵，再进行部署。

## 8. 管理端：前移过滤、避免拉全量再统计

两类扩展性问题值得修，但当前管理员访问量低，不优先于热点读写：

- 一个 OAuth client 授权第一页也会加载所有 users，逐用户查询 1–2 个 Hydra subjects 的 consent，再筛 client 和 actor，最后分页。已有 8 workers 只限制并发，不改变总请求数。先将等价 actor scope 前移；长期可维护只用于候选定位的 client→subject 索引，最终仍实时校验 Hydra 和对象权限。10,000 个双 subject 用户对应约 20,000 次查询只是结构推算，不是生产观测；也不能假设当前 Hydra 提供可替代的按 client 全量检索 API。[hydra_authorization_records.go](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/internal/modules/adminoauth/hydra_authorization_records.go:43)
- 失败审计统计把时间窗内所有 metadata 下载到 Go，只为提取 reason 分组；dashboard 还有 13 次串行 SQL，method/type 边际分组可由已有联合分组派生。建议数据库条件聚合和等价 reason 分组，保留 actor scope、时间窗、非字符串/null/空白归一化规则；SQL trim 与 Go Unicode TrimSpace 不完全等价。[system_log_handlers.go](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/internal/modules/adminsyslog/system_log_handlers.go:151)、[statistics_dashboard_builder.go](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/internal/modules/adminstats/statistics_dashboard_builder.go:22)

## 暂不建议优先投入的方向

- **扩连接池、扩机器或重做宽表。** 当前连接多数 idle，HOT 更新率很高。PostgreSQL 会尽量保留未改动的 TOAST 值，不能把每次 UPDATE 描述成重写全部大字段。[PostgreSQL 18 TOAST](https://www.postgresql.org/docs/18/storage-toast.html)
- **再次迁移 JSON 库或重复重写 OrderedMap。** 这些优化已经完成。主上传当前走 shamaton 的 map 解码，之前 OrderedMap 小基准的收益不能当成上传端到端收益。
- **直接改 GOGC/GOMEMLIMIT。** 它们是 CPU/内存权衡，需先有真实 heap、GC CPU、RSS 和总服务预算；这里没有这些峰值证据。[Go GC 指南](https://go.dev/doc/gc-guide)
- **直接启用 PGO 并宣称固定收益。** 没有代表性生产 CPU profile，局部 microbenchmark 不能代表整个服务。可以留作后续优化，但应混合实际热点请求采样后评估。[Go PGO 文档](https://go.dev/doc/pgo)
- **仅凭默认 HTTP idle pool 就调大所有连接数。** 默认每 host 保留的 idle 数不是并发上限；需先用 httptrace 测复用率和拨号成本。Webhook 专用 SSRF transport 不应与普通外部请求混用。

## 实施与验收建议

第一批做 SQL 稳定化、compact 请求内复用和 gzip 有界复用，分别交付、分别比较。第二批处理后台生命周期、Auth Proxy 请求内查重和局部历史列。第三批单独设计 revision/失效及管理端规模化查询。

每批至少同时记录：接口 p50/p95/p99、SQL/Redis 请求次数、B/op 和 CPU/op、并发峰值 heap、正确性断言。灰度时保证相同字段集合、相同压缩协商、同冷/热缓存状态和相近响应体积；不拿不同路径的天然耗时差异当收益。

观测建设应与第一批并行：按 route template + data type + cache state 打点，拆出 DB acquire/query、history merge/lock、compact render、gzip、Redis invalidation、后台 queue/delivery。现有 profiling 开关只采 runtime 和 database/sql pool，不能替代 CPU/heap/block profile，且没有覆盖所有 pgx 路径。[stats_sampler.go](/Users/seiun/GolandProjects/Haruki-Toolbox-Backend/internal/bootstrap/stats_sampler.go:26)、[Go Diagnostics](https://go.dev/doc/diagnostics)

pg_stat_statements 适合确认生产规划/执行成本，但当前没有加载，启用通常需要配置 shared_preload_libraries 并安排重启；本轮没有为调研修改生产。优先先加请求内轻量耗时和 pgx pool 增量指标，再安排数据库观测变更。[PostgreSQL pg_stat_statements](https://www.postgresql.org/docs/18/pgstatstatements.html)

## 方法、可复现性与剩余缺口

环境：Apple M4 / darwin-arm64，Go 1.27.1；隔离 PostgreSQL 18 Docker。生产是 linux-amd64，网络 RTT、CPU 和真实数据分布不同。本机 benchmark 运行在日常开发机器上；三轮 microbenchmark 取中位数，SQL 数据库对照六组并反转顺序。没有生产压力测试、真实账号 dummy 写入或配置调整。

基准样本：4096 条 compact music records；5000 条普通 JSON records；12 个固定字段的重复小写入。compact 的单次展开和预归一化仅用于拆分重复成本，不是完整兼容实现。gzip 原型不等于已验证的并发池。zstd 测量没有稳定收益，因此没有据此推荐参数。

临时实验与聚合证据保存在 `/tmp/haruki-performance-research/`，不纳入业务源码：

- `sql-templates.log`、`pg-sql-cache.log`、`pg-sql-cache-reversed.log`：SQL 文本、prepared 数量和数据库时间对照。
- `benchmarks.log`、`benchmark-summary.json`：compact、归一化、gzip、zstd 的逐轮测量。
- `production*.json`、`access-types.json`：脱敏生产聚合及差分。
- `source/utils/database/gamedata/performance_research_test.go`、`source/utils/api/data/performance_research_test.go`：实验 harness。

复现入口：在该临时源码副本运行 `go test ./utils/database/gamedata -run TestResearchSQLTemplates -v`，以及 `go test ./utils/database/gamedata ./utils/api/data -run '^$' -bench BenchmarkResearch -benchmem -benchtime=300ms -count=3`。真实数据库对照使用 `PERF_RESEARCH_PG` 指向**可删除表的隔离数据库**，运行 `TestResearchSQLPreparedCache`。临时 PostgreSQL 在本轮结束后移除。

尚缺峰值时段 CPU/heap/锁 profile、按请求的 cache hit/压缩协商分布、后台目标数量与驻留字节、生产实际 prepare 次数和 SQL IO 时间。它们限制了全站收益估计，但不影响上述已复现的重复工作结论。调研在关键机会已有代码/测量支持、反证已检查、剩余问题需要下一阶段观测时收敛，没有继续做低价值的广泛搜索。

官方资料访问日期均为 2026-09-08；项目行为以当前源码和部署观察为准，第三方资料用于核对缓存、存储和诊断机制，未将第三方基准百分比套用到本项目。


## 第一批实施记录（2026-09-08）

本批实现固定 SQL 列顺序、请求内展开结果复用、gzip 编码器和输出缓冲复用。源码快照标识 `91ad052ffe7d` 是工作区内容指纹，不是 Git commit。

### 实现与行为约束

- `utils/database/gamedata/writer.go`：先计算部分写入、父对象替换或全量替换拥有的字段集合，再固定最终 SQL 列顺序；参数同步排序，原始字段集合和清空范围不变。原有首次 INSERT 占位与 `SELECT FOR UPDATE` 仍保护历史合并。
- `utils/database/gamedata/store.go`：按规范列名缓存 compact 展开结果与错误，flattened parent 在同一 Row 内复用。Row 仅属于一次同步请求，返回字节不可修改。metadata 仍读取当前字段；正常 row-form 原字节直接返回。带 key 的 SQL 投影按列排序，同时维护扫描位置，响应仍使用请求的 key 顺序。
- `utils/api/data/compressed_body.go`：`sync.Pool` 独占复用 gzip writer/buffer，归还时 writer 脱离输出缓冲，单个输出缓冲容量超过 1 MiB 即释放。返回 string 复制内容，后续复用不覆盖旧响应。该阈值是单个缓冲的保留上限，sync.Pool 不是进程总内存的硬上限。

### 实际实现后的隔离验证

| 测量 | 旧实现 | 本批实现 | 结论 |
| --- | --- | --- | --- |
| 同组字段 1,000 次写入的 SQL 模板数，三轮 | 951 / 934 / 958 | 1 / 1 / 1 | 消除字段排列造成的模板碎片 |
| 同连接 prepared 总数，三轮 | 513 / 513 / 513 | 4 / 4 / 4 | 统计值包含建表与检查查询 |
| 4,096 行 compact，HasAny + Body | 5,062,605 B/op，106,669 allocs/op | 2,532,140 B/op，53,337 allocs/op | 分配约减少 50% |
| 4,096 行 mysekai 子项，HasAny + 父对象 Body | 330,391 B/op，19 allocs/op | 219,270 B/op，14 allocs/op | 分配约减少 33.6% |
| 普通 row-form 私有读取 | 400 B/op，2 allocs/op | 400 B/op，2 allocs/op | 分配保持不变 |
| 1 MiB JSON gzip | 1,289,045 B/op，17 allocs/op | 212,392 B/op，1 alloc/op | 分配约减少 83.5% |
| 8 MiB JSON gzip | 4,541,264 B/op，17 allocs/op | 3,770,569 B/op，3 allocs/op | 分配约减少 17%；超限输出缓冲仍释放 |

以上为 Apple M4 / Go 1.27.1 的合成样本中位数；Row 旧版 3 次、新版 5 次，gzip 各 3 次。SQL 测试对真实旧版和新版各执行 3 次，均关闭研究 harness 的手动排序控制，交替先后顺序。并行构建干扰 SQL 时延，gzip 也未显示稳定 CPU 收益，因此不将这些结果折算成生产接口提速百分比。

回归覆盖 SQL 三种清空范围、列与参数映射、compact 别名/错误/空值、超大整数、不同 Row 的隔离、父对象未知子项，以及 gzip 并发返回值稳定性。隔离 PostgreSQL 的写入与 race 测试还覆盖此前 userEvents 结算更新和并发旧上传保护。


### 发布与验收

- 完整验证：Go 1.27.1 `gofmt`、`go build ./...`、`go vet ./...`、`go tool staticcheck ./...`、`go test -race -count=1 ./...` 全部通过；69 个有测试的包通过，另有 50 个无测试包。全仓验证在归档源码副本执行，排除未纳入源码的旧本地调试工具。
- Canary：2026-09-08 08:24:06（北京时间）部署；先对照旧生产，再清理独立 canary Redis 后重复冷读/热缓存验证。两轮各 185 项字段接口对照均无差异，9 项完整响应一致，12 项热缓存一致；未认证私有接口 401、退役登录接口 410。最大完整响应约 6.45 MB。08:26:11 时 healthy，错误/警告/重启/OOM 均为 0。
- 生产：2026-09-08 08:26:25 切换，镜像 `haruki-toolbox-backend:perf-batch1-91ad052ffe7d`；镜像 ID `sha256:7a1ee4e113af323ff62a192b554083a4d541f81673da6c5dfd1f59614415ab99`。生产同样通过 185 项字段接口、9 项完整响应、12 项热缓存及 401/410 检查。
- 08:26:58 的生产样本：healthy，health HTTP 200，错误/警告/重启/OOM 均为 0；切换后时间戳范围内已有 suite 1 行、mysekai 2 行上传更新。这是发布后的短时验收，不是长期负载或生产 p95 的性能结论。
- 二进制 SHA-256：`13fb09becfbd3ca8f2479f25d97ae72d29bc4f9c8b51f96186b5583ef98468d0`。
- CN02 发布归档：`/data2/backups/toolbox-perf-batch1-91ad052ffe7d/`，含精确源码/manifest、二进制、验证日志、前后接口对照及旧配置。`rollback.sh` 可恢复本次切换前的 backend 配置并重建后端。旧镜像 `haruki-toolbox-backend:user-events-fix-223e0dd28cea` 保留。

本批完成优先级 1–3。后台任务容量/重复解码、Auth Proxy 重复读取、历史仅读必要列、独立 revision 和管理端聚合仍按调研清单作为后续批次处理；引继上传的预期耗时始终排除。


## YHM01 真实数据补充：单次展开与 JSON 往返（2026-09-08）

来源：[独立实验报告（本机研究产物）](/Users/seiun/.codex/visualizations/2026/09/07/01a07e40-e535-7d90-8160-9eb2b74d7318/serde-json-real/README.md)。本节核对了报告与实验入口源码，记录其测量结果，没有重新运行该实验。用户数据不复制进仓库。

这里的 **GoOptimized 是隔离原型的单次解析/展开算法优化**，与第一批已上线的 **Row 请求内结果复用** 是两个层次。该补充研究时，仓库 `ExpandCompactJSON` 仍使用复制标量和中间行结构，尚未合并单次展开原型；后续接入见下节第二批记录。不能将原型测量描述为现有生产版本的性能，也不能直接相乘两者的加速比。

完整 15,011,651 字节 JSON 的串行往返中位数为 Go 299.53 ms、GoOptimized 284.17 ms、Rust wrapper 240.81 ms。Rust 相对 GoOptimized 的速度比约 1.18，耗时减少约 15.3%；四并发吞吐比约 1.76。并发 ms/op 是总耗时除以完成次数，不代表单请求延迟或 p95。三方输出与转换文件逐字节一致。

此处 GoOptimized 的完整往返仅改为借用输入中的标量。Rust 解析树保留在 Rust 内部，未包含转成 Go map/struct 的成本。计时排除解密、MessagePack 转换和文件读取。项目上传同步已有 MessagePack → JSON 的直接转换路径，额外 JSON 往返不因此成为必要步骤；这些数值也不适用于标准 struct 的 Marshal/Unmarshal 性能结论。

五组真实表按字段集合分组后转成列式输入，纯 Go 单次展开原型相对原算法加速约 2.61–2.94 倍；Rust 相对该 Go 原型额外加速约 1.23–1.71 倍。这些是由行数据转置的派生输入，没有用补 null 改变缺失字段语义，也未构造真实枚举字典负载，不能称为整份 suite 的恢复收益。

Rust 三次独立进程峰值 RSS 为 174.36–499.86 MiB，样本波动不支持更省内存的结论。RSS 包含运行时、输入、解析树和输出；Go 的 allocs/op 不包含 Rust 分配。

**后续优先验证纯 Go 单次展开原型与现有 Row 复用的组合。** 验收须使用实际读取链路，并保留输入生命周期、整数精度、枚举、缺失字段、错误及并发行为契约。Rust 继续作为候选对照，待实际热点占比、跨语言数据交接和内存稳定性有证据后再决定是否接入。引继上传的预期耗时继续排除在优化目标外。


## 第二批实施：纯 Go 单次 compact 展开（2026-09-08）

本批只修改 `utils/database/gamedata/compactjson.go` 的生产实现，并新增四个测试文件。基线为已上线的第一批 `91ad052ffe7d`，本批源码快照为 `fbf7d6f143b5`。实现保留 Go 的严格 jsontext 解析，在 compact 展开调用内借用原输入中的标量，列名只编码一次，直接输出行 JSON，移除中间行对象及整列枚举映射副本。

普通 `parseOrdered` 继续复制标量；借用仅发生在同步展开调用期间，原输入保持存活且不可修改，展开输出独立拥有字节。嵌套深度 256、重复键拒绝、最短列截断、非数组列为空、非法枚举索引为 null、标量原文与整数精度等既有行为均保留。第一批 Row 复用继续工作。

### 正确性与测量边界

- 测试保留独立的旧实现作为差分参照，覆盖构造边界、2,000 组随机列式输入、256 深度边界、枚举类型/范围/非法字典及错误文本；30 秒 fuzz 完成 310,472 次执行，通过。
- 相关包 race 测试通过，另覆盖 decoder 多次补充读取缓冲、调用后修改原输入不影响展开输出、普通解析树继续独立拥有标量，以及并发请求与字典隔离。
- 使用用户提供的本地真实 JSON，通过既有 prepare 程序生成五组按字段集合划分的列式派生输入。正式 `Row.HasAny → SuiteBody` 和私有别名输出与预期行 JSON 逐字节一致，真实数据 race 检查通过。临时派生数据已删除，原本地 JSON 保持不变；本轮没有上传真实用户数据。
- 基准使用正式 Row 读取函数，每次创建新 Row，包含存在检查、展开结果复用和裸值响应；不包含 PostgreSQL/Redis、HTTP、gzip、解密或 MessagePack 转换。派生输入未构造真实枚举负载，枚举语义由差分测试覆盖。
- Apple M4、Go 1.27.1，`GOMAXPROCS=4`，每组 500 ms，三轮独立进程，前后版本交替顺序，以下为中位数。这里对照的是两个完整 Go 源码版本，不能把结果与 YHM01 单次原型数据跨机器相除，也不表示整站速度。

| 派生真实表 | 行数 | 第一批读取 ms | 第二批读取 ms | 串行速度比 | B/op 降幅 |
| --- | ---: | ---: | ---: | ---: | ---: |
| userCostume3dStatuses_schema1 | 26,513 | 39.95 | 11.44 | 3.49× | 47.9% |
| userCostume3dStatuses_schema2 | 96,003 | 93.10 | 24.62 | 3.78× | 47.8% |
| userCostume3dShopItems | 44,408 | 39.97 | 12.25 | 3.26× | 46.9% |
| userCharacterMissionV2Statuses | 15,528 | 43.34 | 13.05 | 3.32× | 45.7% |
| userMusicResults | 2,926 | 12.85 | 3.56 | 3.61× | 45.8% |

四并发样本吞吐比为 2.66–3.67×。并发 ns/op 是总体吞吐的倒数，不是单请求延迟。本批未引入 Rust，也未将引继上传的预期耗时作为优化目标。


### 第二批发布与验收

- 完整验证：Go 1.27.1 gofmt、build、vet、staticcheck 与全仓 `go test -race -count=1 ./...` 全部通过（69 个有测试包，50 个无测试包）。测试和 Linux amd64 二进制来自同一份归档源码，未纳入本地旧调试工具或用户数据。
- Canary：2026-09-08 08:56:20（北京时间）部署。清理独立 canary Redis 后，185 项接口对照无差异，9 项完整响应一致，12 项热缓存一致；未认证私有接口 401、退役登录 410。08:57:16 检查 healthy，错误/警告/重启/OOM 均为 0。
- 生产：2026-09-08 08:58:06 切换至 `haruki-toolbox-backend:perf-batch2-fbf7d6f143b5`。切换后的 185 项接口、9 项完整响应、12 项热缓存及安全响应检查均通过。
- 08:58:38 生产样本：healthy、health HTTP 200，错误/警告/重启/OOM 均为 0；切换后时间戳范围内 suite 已有 2 行更新，mysekai 尚无新行更新。该记录属于短时发布验收，不是生产延迟或长期负载结论。
- 镜像 ID：`sha256:3e505fecee489038309408675eb1a3b0376503f58675936a9be8fb436bb990b8`；二进制 SHA-256：`9e55e33b8980ca39d607292d621dda38adbb44b7d1cc8024a0f70e6423a0fe67`。
- CN02 归档：`/data2/backups/toolbox-perf-batch2-fbf7d6f143b5/`，已保存源码/manifest、二进制、差分 fuzz、真实表测试和基准、canary 验收、旧配置与 `rollback.sh`。回滚目标为第一批 `haruki-toolbox-backend:perf-batch1-91ad052ffe7d`，旧镜像保留。用户已明确批准将完整报告与独立补丁归档到上述 CN02 目录。生产接口对照文件保存在该主机既有 smoke 目录，本批 post-status.json 已在归档目录中。


## 第三批实施：历史按需读列与认证资料复用（2026-09-08）

本批基于第二批生产版本 `fbf7d6f143b5`，运行源码快照为 `f4fa51cf9021`（工作树内容指纹，不是 Git commit）。生产实现改动限于 `writer.go` 和四个 `utils/api/session_*.go` 文件。后台同步、独立 revision 与管理端聚合仍留作后续批次；引继上传的预期耗时不纳入优化目标。

- Suite 历史合并依据本次上传键，从 `userEvents`、`userWorldBlooms`、`userGachas` 中选择所需列，按固定顺序生成最多七种非空投影。显式 nil 仍算上传键。首次占位 INSERT、事务 `SELECT FOR UPDATE`、合并规则、遗漏列保留和 upload_time 更新均保留。
- 已关联 Kratos identity 的映射查询一次读取 ID、名称、邮箱、identity ID，直接交给本次请求的资料同步比较，资料查询由两次 SELECT 降为一次。新建、邮箱关联、自定义 resolver 或不同用户快照仍重新读取。没有跨请求缓存，也没有缓存角色、禁用状态或会话授权；Auth Proxy 和 whoami 两条路径均覆盖。

### 第三批验证与测量

隔离本地 PostgreSQL 的 race 测试通过：七种历史键组合、显式 nil、遗漏列不重写、结算更新、首次并发写入、并发旧上传保护。所有写测试仅使用本轮 disposable 数据库和 dummy 用户，没有向生产写测试数据。

认证测试通过：资料不变时一次 SELECT、零 UPDATE；资料变化时一次 SELECT、一次 UPDATE；下一请求能看到数据库新资料；自定义 resolver/关联/新建分支保留回退读取；未验证邮箱不能关联、错误信任密钥及伪造用户头被拒绝；不同用户快照不可复用；whoami 每次请求仍调用提供者。

本地 Apple M4、Go 1.27.1、Docker PostgreSQL 18，三个 1 秒样本的中位数如下。输入是纯合成数据：180 条活动记录，另两列各 10,000 条记录。两组读取同一行，均包括开始事务、带行锁读取、JSON 解析和提交事务；三列组模拟旧版本对活动局部更新的固定读取。

| 历史读取范围 | 中位 ms/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| 旧行为等价：三列历史 | 26.01 | 15,674,240 | 383,458 |
| 新行为：仅活动历史 | 1.99 | 125,759 | 3,436 |

此合成局部更新场景耗时约减少 92.3%，分配字节约减少 99.2%。该测量仅反映跳过无关大列的收益，不含后续合并、UPSERT、HTTP、Redis 或上游取数，不是整次上传或生产 p95 的收益。三列同时上传仍需读取三列，不应据此声称加速。

完整验证在同一份精确源码快照执行：gofmt、build、vet、staticcheck、全仓 `go test -race -count=1 ./...` 全部通过，69 个有测试包、50 个无测试包。Linux amd64 二进制 SHA-256：`ac2188bd38ed73ee42f624e526a30074ae7ca001dbb0168b7b26f6e8aaf72c37`。源码与完整日志暂存本机 `/tmp/haruki-perf-batch3/`；最终部署结果见后续记录。

### 第三批发布与验收

- Canary：2026-09-08 09:28:00（北京时间）更新为 `haruki-toolbox-backend:perf-batch3-f4fa51cf9021`。清理独立 canary Redis 后，185 项字段接口与旧生产无差异，9 项完整响应相同，12 项热缓存相同；私有未认证 401、退役登录 410。
- 认证补充对照：三个现有已关联用户，canary 与旧生产 `/api/user/me` 响应一致；伪造 `X-User-Id`、错误信任密钥、跨用户 settings 请求均返回 401。测试不发送名称/邮箱变更，并校验三个用户的数据库行版本未变化。此测试直接验证私网后端 Auth Proxy 边界，不代表重新走了一遍浏览器登录或 Oathkeeper 转发链路。
- 生产：09:29:11 切换到上述第三批镜像。再次通过 185 项字段、9 项完整响应、12 项热缓存及 401/410 检查；三个用户的认证补充检查也全部通过，用户行版本未变化。
- 09:30:01 生产样本：healthy、health HTTP 200、错误/警告/重启/OOM 均为零；切换后时间戳范围内已有 suite 1 行、mysekai 2 行上传更新。此为发布后的短时验收，不是长期性能或负载结论。
- 镜像 ID：`sha256:c9827a63c6c873d3662c7cda00d07302f8f50af791d1e63be1f90f7a6569bdbd`。
- CN02 `/data2/backups/toolbox-perf-batch3-f4fa51cf9021/` 保存运行二进制/哈希元数据、主机本地产生的验收汇总、旧配置与 `rollback.sh`。回滚目标为第二批 `haruki-toolbox-backend:perf-batch2-fbf7d6f143b5`，旧镜像保留。
- 用户随后明确批准将本批私有源码、补丁及测试报告上传至上述 CN02 目录。完整归档 `source-tests-report.zip` 与展开文件均已归档并通过 SHA-256 校验；目录权限 0700、归档文件权限 0600，原有回滚脚本保持 0700。本地保留相同副本。此前的自动审批归档阻塞已解除。


## 第四批实施：后台同步与阶段观测（2026-09-08）

本批基于第三批 `f4fa51cf9021`。范围是持久化完成后的通用后台分发和阶段观测；引继上游取数的预期耗时不作为优化目标。

### 实现与边界

- 同步配置在启动时按值注入，经路由依赖传到处理器，不新增业务代码的全局配置读取。先根据隐私设置和非空配置端点规划同步；没有适用端点时不复制 raw、不重新打包生日数据，普通后台任务不再捕获数据库预处理后的整棵 map。
- 四个配置接收方的存在检查并行执行，共享 30 秒 deadline；先排除不接收数据的目标，再计算所需格式。失败检查仍跳过该目标，既有检查凭据和投递头保持不变。
- 后台两种格式共享一份拥有独立内存的解密 MessagePack。恢复分支在任何递归解码前保留深度校验及既有 map/slice 值转换。没有复用已被预处理修改的 map，也没有异步引用已归还的解密缓冲。主上传解密仍独立进行；两种同步格式同时需要时，全链路三次解密变成两次，后台两次变成一次。
- restored 分支仍执行原有 NormalizeProviderResponse，再用 MarshalWrite 直接写 zstd，移除完整 JSON 中间缓冲。池化输出 buffer 超过 1 MiB 不保留，encoder 归还前解除输出引用。zstd 并发参数不变。格式失败仍按既有规则回退到已生成的 processed 或 raw。
- 后台父任务在复制 raw 之前取得容量：最多 4 个父任务，原始输入预算 64 MiB；超过预算的单个输入独占放行。FIFO 等待，不因容量满拒绝或静默丢弃；编码另设 2 个槽位，编码完成后释放再等待网络投递。任务结束、panic 清理和关机拒绝路径均释放许可。
- 预算描述的是已获准任务的原始输入字节，不是总 RSS 上限；等待请求已有的缓冲、生日 map、解码树和压缩器不包含在该数值里。拥塞时容量等待可延迟上传响应。沿用现有进程内任务排空机制，本批没有新增持久队列或崩溃后的可靠重投机制。

### 观测

新增固定九阶段累计直方图：上传解码、预处理、持久化、后台准入等待、同步编码槽等待、接收方检查、processed 编码、restored 编码、投递。每项记录次数、总纳秒、最大值和七个互斥桶（≤1/5/20/100/500 ms、≤2 s、>2 s）。计时包含失败调用；读取相邻快照的差值分析时段，不将累计最大值视为 p95。

采样器同时输出 active、active_input_bytes、waiting、oldest_wait_ms、峰值/完成任务数，以及 game-data pgx 池的连接数、累计 acquire 时间和空池/取消计数。没有账号、URL、载荷或凭据标签，不新增公开接口。现有 profiling 开关控制日志输出，本批部署已以 30 秒间隔开启；database/sql 与 runtime/GC 原有采样同时生效。这是首批阶段观测，尚未覆盖读取接口的缓存状态、完整 HTTP 延迟及 CPU/heap profile。

### 本地验证

- 相关包 race 回归通过。独立保留第三批编码函数作为参照，解压后 JSON 以保留整数精度的方式比较，覆盖大整数、nil、空数组、独立输出、并发池复用、编码错误清理、格式失败回退和 MessagePack 深度/截断拒绝。
- 四个接收方检查并发启动；拒绝目标不触发格式编码。实际本地慢 HTTP 服务测试中，六份 1 MiB 上传在四个父任务限制下全部送达，调用者随后复用/清空原始缓冲不影响投递；后台排空后活动数和字节数归零。
- 独立限额测试对 1/10 MiB 输入各执行 24 个慢任务，全部完成，活动父任务不超过四个；另外验证 FIFO、超大输入独占及重复释放保护。此为受控容量和正确性测试，不是生产峰值 RSS 的测量。
- Apple M4、Go 1.27.1、GOMAXPROCS=4，合成 5,000 行数据，两种格式串行生成，1 秒样本重复三次。第三批中位 16.62 ms/op、13,952,108 B/op；本批 14.54 ms/op、9,072,135 B/op，耗时约减少 12.5%、分配字节约减少 35.0%。不含主上传解密、网络、数据库或恢复表展开；使用无结构文件的恢复服务，保留归一化流程。池分配样本有波动，不能换算为生产收益。

### 第四批发布与验收

- 最终工作树源码指纹 `48e17e3795d8`，不是 Git commit。gofmt、build、vet、staticcheck、全仓 race 测试全部通过（70 个有测试包、50 个无测试包）；源码快照与 Linux amd64 构建一致。
- Canary：2026-09-08 09:58:27（北京时间）部署 `haruki-toolbox-backend:perf-batch4-48e17e3795d8`。与第三批生产对照的 185 项字段、9 项完整响应、12 项热缓存以及 401/410 检查全部通过；三个已关联用户的认证及跨用户拒绝检查通过，用户资料行未变化。09:59:19 healthy，错误/警告/重启/OOM 均为零。
- Canary 的九阶段、后台容量和 pgx 日志均正常输出。没有向共享生产库写入 dummy 上传，所以 canary 的上传/投递阶段计数为零；后台投递和慢接收方行为由前述本地集成测试验证。
- 生产：10:00:02 切换到第四批镜像，并开启 `BACKEND_PROFILING_ENABLED=true`、`BACKEND_PROFILING_INTERVAL_SECONDS=30`。切换前对 Compose 展开配置逐项比较，只有 backend 镜像与这两个采样变量改变。185/9/12 项接口对照和认证检查全部再次通过。
- 10:01:18 生产快照：healthy、health HTTP 200，无错误、警告、重启、OOM；切换后时间戳范围内 suite 有 2 行、mysekai 有 3 行更新。最近一次 30 秒采样累计完成 3 个后台父任务、5 次同步投递调用；active/waiting/input bytes 均归零，原始输入峰值 10,352,848 字节。该短时低负载记录不是生产 p95 或峰值容量结论。
- 镜像 ID `sha256:11ae297e6201108df68f60e0476abdde890d39bd8c4d9d1aa8986b28677d30d8`；二进制 SHA-256 `739f8ad6876c9443a9721e8521351c9c6fb997af4d99721f084f84f9153152fe`。
- CN02 运行归档 `/data2/backups/toolbox-perf-batch4-48e17e3795d8/`，保存运行二进制、主机验收汇总和旧配置。`rollback.sh` 同时恢复旧 env 与 Compose，再重建 backend；回滚目标第三批 `haruki-toolbox-backend:perf-batch3-f4fa51cf9021` 保留。
- 用户随后明确批准下载服务端验收文件。12 份认证、对照、状态、观测及发布报告已下载并逐文件核对 CN02 SHA-256，全部一致；本地文件权限 0600，并纳入完整源码/测试/报告归档。服务端原件仍保留，先前的导出审批阻塞已解除。
