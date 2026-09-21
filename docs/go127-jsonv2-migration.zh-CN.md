# Go 1.27、JSON v2 与有序 MessagePack 迁移

更新日期：2026-09-07。Go 1.27 / JSON v2 已切换生产；userEvents 合并修复已于 9 月 7 日北京时间 12:38:57 上线。

## 工具链与泛型方法

工具链、Docker 构建镜像和开发说明统一到 Go 1.27.1。CI 与 Release 从 go.mod 读取版本。

[Go 1.27 发布说明](https://go.dev/doc/go1.27)介绍了泛型方法、结构体字面量中的嵌套字段选择器和扩展的泛型函数类型推断。泛型方法不能实现接口方法。本轮将原有十五个泛型函数全部归属到结构体：

- `api.Responses` 的 `ResponseBuilder`：NewResponse、UpdatedDataResponse、ResponseWithStruct、SuccessResponse。调用改为 `api.Responses.SuccessResponse(...)`，类型仍可推断。
- `gamemerge.recordCollector.collect`：为三种游戏记录主键生成稳定顺序的合并结果。
- 两套 Ent 每套五个泛型函数：`mutationHookRunner.withHooks`，以及 `queryInterceptorRunner` 的 withInterceptors、scanWithInterceptors、querierAll、querierCount：通过 `ent/codegen.Modernize` 生成钩子迁移，禁止直接维护生成文件。

自有有序容器新增 `GetAs[T]` 泛型方法，按动态类型读取值，不进行有损数值转换。

## JSON v2 覆盖范围

项目自有及生成源码已移除旧 encoding/json 和 Sonic 导入，删除 Sonic 与上游 orderedmap 依赖。通用对象编解码使用 `encoding/json/v2`，原始值与流式 token 使用 `encoding/json/jsontext`。第三方库内部源码不做 vendor 修改；Go 1.27 的旧标准库 API 本身由 v2 实现支撑。参见 [官方迁移指南](https://go.dev/doc/jsonv2-migration) 与 [JSON v2 API](https://pkg.go.dev/encoding/json/v2@go1.27.1)。

迁移包含 Fiber、Resty 的隐式 JSON 编解码、通用 Redis 缓存、运行时配置、OAuth2/Kratos、pgx JSON/JSONB、Ent 生成代码、游戏 JSON 与 MessagePack→JSON 流式输出。原始游戏 JSON 响应仍直接发送已有字节，避免将 []byte 编码成 base64 字符串。

### 明确的行为选择

| 场景 | 本轮行为 |
| --- | --- |
| 重复 JSON 字段、非法 UTF-8 | 使用 v2 默认严格校验，返回错误 |
| 结构体字段名 | 按 JSON 标签精确匹配；未知字段一般继续忽略，catalog 显式拒绝 |
| 顶层尾随 JSON 值 | Unmarshal/UnmarshalRead 拒绝多个值 |
| HTTP 与通用缓存的 nil map/slice | jsoncodec 显式保留 null；空集合保持空集合 |
| 运行时 nil allowlist | 仍写 null；webhookEnabled 的 nil 省略、false 保留 |
| 零数值、布尔值、nil 指针的省略 | 显式 omitzero 标签；空集合继续使用 omitempty |
| 动态游戏/赞助 JSON 的数字 | jsonvalue.Numbers 保留原文，避免 any/float64 损坏大整数 |
| 原始 JSON 片段 | jsontext.Value；有序解析保留数字原文及字段顺序 |
| 确定性报告/测试比较 | 显式 Deterministic(true)，不依赖默认 map 排序 |
| MessagePack 的非有限浮点、binary/ext 转 JSON | 延续原有 null 策略 |
| 派生 userIdString | 从真实 userId 生成，覆盖输入提供值，避免重复输出 |

自有递归 JSON 解析限制嵌套深度 256。MessagePack 保留上传预校验、解码深度限制和剩余字节约束。Redis WATCH 重试、原子写入及 Lua 比较流程保留，并以旧原生 Sonic 字符串样本验证转义兼容性。

## 自有 orderedmap

已阅读 [iancoleman/orderedmap v0.3.0 源码](https://github.com/iancoleman/orderedmap/blob/v0.3.0/orderedmap.go)。原实现分别维护键切片和值哈希表；本实现按 MessagePack 小型记录较多、按顺序遍历较多的使用方式重新设计：

- 连续 entry 切片，小于等于 8 个字段时直接查找，超过阈值建立索引。
- 解码器利用已验证的 map 长度预分配，推测预分配最多 4096 个条目，避免重复键或无效内容导致过大预分配。
- 重复键更新值并保留首次位置；删除后重插移到末尾。Keys 返回独立快照，All 无需键快照和二次哈希。
- JSON v2 的 MarshalJSONTo 直接写入外层流，继承其编码选项。
- msgpackcodec 的 Marshal、Unmarshal、Reader/Writer 入口支持容器及嵌套对象；失败解码不修改目标。
- 容器可变，不支持并发写；使用后不应复制并独立修改共享内容。

### 实测性能

以下保留 Go 1.27 迁移阶段的测量记录；后续 codec 合并、旧包退役和小对象存储优化见 [MessagePack codec 与 OrderedMap](msgpack-codec.zh-CN.md)。下方命令已更新到当前包名。

Apple M4，darwin/arm64，Go 1.27.1；相同解码输入，三轮 ns/op 的中位数。旧版为替换前解码器配合 iancoleman/orderedmap；新版为本轮最终实现。

| 场景 | 旧 ns/op | 新 ns/op | 耗时下降 | 旧/新 B/op | 旧/新 allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: |
| 8 字段小对象 | 330.6 | 195.9 | 40.7% | 752 / 416 | 15 / 10 |
| 128 字段嵌套对象 | 14853 | 7374 | 50.4% | 44632 / 20984 | 566 / 422 |
| 4096 元素数组 | 40735 | 37063 | 9.0% | 96762 / 96425 | 3851 / 3848 |

这是合成载荷的本机结果，不代表所有生产负载。复现：

```sh
go test ./utils/msgpackcodec -run '^$' -bench=BenchmarkDecodeOrdered -benchmem -count=3
```

## 验证与维护

- 完整 build、vet、staticcheck、全仓 race 测试通过。Linux amd64/arm64 交叉编译与本地 Docker 镜像构建通过。
- AST 审计覆盖 827 个 Go 文件：15 个原有泛型函数全部改为方法，新增 GetAs 方法；旧 JSON/Sonic/orderedmap 导入与依赖均无遗留。
- 临时 PostgreSQL 18 上执行真实写入、合并和 v2-only Number 经 pgx JSONB 编码的集成测试。
- 新增 Fiber 绑定、Resty、Redis、精确数字、深度、非法 UTF-8、重复键、容器变更及 MessagePack 往返测试。
- 有序解码往返 fuzz 运行 10 秒，完成 58159 次执行，无失败。
- 两套 Ent 重新生成后的 205 个 Go 文件哈希一致。

```sh
go generate ./ent/toolbox ./ent/bot
go test -race -count=1 ./...
go test ./utils/msgpackcodec -run '^$' -fuzz=FuzzOrderedRoundTrip -fuzztime=10s -parallel=2
```

全仓验证在 /tmp 的项目源码副本中执行；本地被忽略的 backfill 工具仍引用已退役 Mongo 包，不属于本轮源码改造，保持原样。Canary 与生产部署记录见下节。

## 2026-09-05 Canary 部署与烟测

Canary 于北京时间 **09:37:35** 在 CN02-HGH01 启动，入口 `http://100.80.207.86:16667`，仅绑定 Tailscale IP。生产主实例继续使用 16666，容器 ID、启动时间及镜像均未改变；未切换 Oathkeeper 或公共入口流量。

| 项目 | 值 |
| --- | --- |
| Canary 镜像 | `haruki-toolbox-backend:go127-canary-525959ad` |
| 镜像 ID | `sha256:a5652cca80355f37e5c833ef0b3decb689ad9170a7cefe8562579ddfda16d78b` |
| 源码标识 | `worktree-525959ad1d2a`，工作区源码清单 SHA-256 的前缀，不是 Git 提交 |
| 二进制 SHA-256 | `1a345eb28c8ff16a0fa1081e4a7a46a7c4d1acde53e10f5eef0a6abf6b4b951c` |
| Compose 项目 | `haruki-toolbox-go127-canary` |
| 部署目录 | `/data/HarukiService/toolbox-go127-canary/` |
| 构建与测试证据 | `/data2/backups/toolbox-go127-canary-525959ad/` |

目标机使用经过核验的现有 amd64 生产运行时作为基础镜像，替换本次 Go 1.27.1 编译的二进制。部署归档保留源码包、逐文件哈希、Dockerfile、镜像构建日志及烟测摘要；运行中二进制哈希已复核一致。

Canary 使用独立 Redis、日志和头像目录，共享现有 PostgreSQL 数据。自动迁移、爱发电定时同步、Webhook 和 Bot 注册已关闭；游戏库连接池上限 8，容器限额 2 CPU / 2 GiB。此次线上测试调用只读接口和鉴权拒绝路径，未执行真实账号上传写入；写入与合并此前已通过隔离 PostgreSQL 集成测试。应用自身的启动过期授权清理沿用既有实现。

烟测结果：

- 185 组字段读取对照：公开 38、私有 18 组 HTTP 200，摘要全部一致；双方 404 的 129 组单独记录。
- 12 组热缓存复测一致。
- 8 组完整游戏快照全部一致，最大响应 9,920,060 字节。
- 赞助公开响应排除每次请求生成的 `updatedData.summary.generatedAt` 后一致，该动态时间戳是唯一初始差异。
- 未认证私有请求为 401，已退役登录入口为 410，生产与 canary 一致。
- 09:44 检查时 canary 和生产均 healthy；canary 重启 0 次，启动以来无 ERROR/FATAL、panic 或 Sonic 回退日志。

这份记录是首次部署烟测，canary 的持续观察时间从本次启动起计算。

日常检查与停止命令（仅作用于独立 canary 项目）：

```sh
/data/HarukiService/toolbox-go127-canary/canary.sh ps
/data/HarukiService/toolbox-go127-canary/canary.sh logs --tail=100 backend-canary
/data/HarukiService/toolbox-go127-canary/canary.sh down
```


## 2026-09-06 生产切换

Canary 从 9 月 5 日 09:37:35 连续运行至切换前约 30 小时，健康检查通过、重启 0、OOM 0、运行时错误 0。此期间主要接受健康探测和烟测请求，不代表承载了 30 小时生产流量；后台同步和通知仍在原生产实例执行。

切换前重新对照 185 组字段读取（40 组双方 200、145 组双方 404），无差异；12 组热缓存及 9 组完整响应全部一致。完整响应包含 8 组游戏快照及赞助响应，最大 5,786,066 字节，仅排除赞助响应的逐请求生成时间戳。私有接口无凭证为 401，退役登录为 410。

北京时间 **15:44:46**，通过生产 stack 的 `compose.sh up -d --no-deps --pull never backend` 重建 backend，镜像切换为 `haruki-toolbox-backend:go127-525959ad`。该标签与上述 canary 指向完全相同的镜像 ID；运行中二进制 SHA-256 已复核一致，没有重新构建。二进制版本元数据仍保留 `go127-jsonv2-canary` 标识，源码标识仍为工作区清单摘要而非 Git 提交。

仅修改 `.portainer-env.sh` 的 `BACKEND_IMAGE`，其余环境设置、Compose 文件及 wrapper 经比较保持一致。生产 Redis、Auth Proxy、Webhook、爱发电同步、自动迁移等沿用原配置；Oathkeeper、Ory、PostgreSQL、Redis 均未重建。入口仍为 Tailscale 16666。

切换后再次完成上述 185 + 12 + 9 项检查，均通过。15:46:13 检查时，生产 healthy、HTTP 健康检查 200，重启、OOM、ERROR/FATAL/panic、WARN 均为 0；游戏库中上传时间晚于启动时间的 suite 记录 5 条、mysekai 记录 2 条，日志显示后台订阅通知成功 1 次。这里统计的是近期记录数，不是累计上传请求数。PostgreSQL 副本 `toolbox_cn07_backup` 保持 streaming，采样 replay lag 约 27 毫秒。这是上线初期验证，不代表长期负载验收。

后续观察至 **15:48:43**，生产仍 healthy、零重启、无 OOM；启动后更新的 suite 记录为 13 条、mysekai 4 条，后台通知成功 3 次。15:47 出现 6 条游戏上游 403 ERROR 和 7 条相关 WARNING；核对当天旧版本日志，相同 suite follow-up、invitation、home refresh、MYSEKAI maintenance 接口在 **15:13:06–15:13:09** 已有同类 403。当前错误均属于这组既有上游拒绝，未发现其他错误，因而保留新版本。不能把本次结果表述为生产日志完全无错误。

生产备份及前后验收证据位于 `/data2/backups/toolbox-go127-production-20260906/`（仅 root 可访问）。保留原镜像 `haruki-toolbox-backend:u11-95d2a22`；必要时执行以下脚本恢复原环境文件并仅重建 backend：

```sh
/data2/backups/toolbox-go127-production-20260906/rollback.sh
```

回滚脚本恢复切换前完整环境文件；若后续另有配置变更，应先核对再使用。独立 canary 保留运行，未调整公网路由。


## 2026-09-07 userEvents 增量更新修复

收到同一期活动数据未刷新的反馈后，复现并修复两处问题：

1. `gamemerge.Events` 原先在积分相同且双方都带（或都不带）rank 时保留旧记录，导致排名、领奖时间等字段无法刷新。现在相同积分且记录完整程度相同时采用新快照；较低积分仍不能覆盖较高积分，同分但缺少 rank 的快照仍不能覆盖带 rank 的记录。其他历史活动继续保留。
2. PostgreSQL suite 历史合并原先只开启事务，没有锁住读到的旧记录。两个上传可读到同一旧版本，随后互相覆盖。现在事务内先确保账号行存在，再以 `SELECT ... FOR UPDATE` 读取并合并，覆盖已有账号与首次并发上传两种情况。占位行与最终写入属于同一事务，失败整体回滚。

回归测试先验证旧代码失败：同分排名/领奖更新保留旧值；并发上传在新账号、已有账号两种情况下都会丢失一组历史。修复后 `go test -race -count=1 ./utils/database/gamedata/... ./utils/handler` 通过，gamedata 测试连接本机临时 PostgreSQL 18，实际执行读写及并发阻塞验证。临时数据库已移除。另覆盖同活动积分增长及较低积分不回退；不依赖生产写入构造测试数据。

修复镜像 `haruki-toolbox-backend:user-events-fix-1acbc5628f57`，镜像 ID `sha256:52a8a173f1f46e46306cc3b17c2dfa053abba4997495b81ecf3152706ee7f408`，二进制 SHA-256 `e336feb0bdee5f92127bea4e8395e8aadc0b4cabe98ce10ddf0ad5d54ffd1b1b`。源码清单标识 `1acbc5628f57` 不是 Git 提交；与上次部署源码清单相比，仅本次四个合并/写入实现及测试文件、部署说明发生变化。

12:37:44 完成独立 canary 更新；切换前 185 组字段对照（51 组双方 200、134 组双方 404）、12 组缓存复测和 9 组完整响应一致，鉴权拒绝符合预期。12:38:57 将同一镜像切到生产，仅更新 backend，保持生产配置和其他服务不变。12:39:24 检查时 healthy，重启、OOM、ERROR、WARNING 均为 0，真实上传已继续写入。

备份、源码包及前后验证证据：`/data2/backups/toolbox-user-events-fix-1acbc5628f57/`。回滚脚本为该目录的 `rollback.sh`，恢复上一个 `go127-525959ad` 镜像及切换前环境文件；使用前需核对后续是否另有配置变更。

目前尚无反馈账号的具体区服、UID、活动 ID 和上传样本，因此这两处是已复现的缺陷，不应表述为已完成对反馈账号的端到端确认。修复不会重建过去已被丢弃的快照；账号下一次有效上传后按新规则更新，仍受防止较低积分覆盖的保护。

12:40:37 最终检查：生产仍 healthy、零重启/错误/警告，启动后 suite 5 条、mysekai 10 条记录已更新，后台通知成功 3 次；PostgreSQL 副本 streaming。生产切换后重复 185 + 12 + 9 项检查全部通过，运行中二进制哈希和仅镜像配置变更均已核验。


### 指定积分的 Dummy 对照验证

为进一步核对反馈，使用合成账号 `900178`、国服活动 178，构造 `100956916 → 144520517` 积分变化。所有写入只发生在本机临时 PostgreSQL 18；测试未使用真实账号写入生产。对照旧版来自 9 月 5 日实际部署的源码归档，修复版为当前实现，两者运行同一份 `utils/database/gamedata/user_events_dummy_integration_test.go`。

| 用例 | 修复前 | 修复后 |
| --- | --- | --- |
| 顺序上传旧积分、结算积分，再补传旧积分 | 保留 144520517，通过 | 保留 144520517，通过 |
| 相同积分，dummy 排名从 10 更新到 12 | 仍为 10，失败 | 更新为 12，通过 |
| 暂停旧积分上传于读后写前，让结算上传尝试完成，再恢复旧上传 | 回退至 100956916，失败 | 行锁串行化，最终 144520517，通过 |

顺序场景分别覆盖原生 Go map、保留精确数字的 JSON 解码，以及实际 SekaiCryptor 的 AES / MessagePack 打包解包。并发用例通过测试专用 pgx tracer 控制交错顺序，不修改业务代码，也不依赖随机调度。两版本均使用 `-race`，旧版两个功能断言失败，修复版全部通过。

这次验证证明旧版并发覆盖能够产生与反馈相同的旧积分结果，并证明当前修复可阻止该覆盖；顺序增长在旧版已经正确，因此不能仅凭存储值推定真实账号一定遭遇了并发。真实账号仍需要新上传或当时原始上传证据做端到端确认。

复现命令（DSN 必须指向可丢弃测试库，测试会创建和删除表）：

```sh
GAMEDATA_WRITE_TEST_PG='postgres://.../disposable_test_database' go test -race -v -count=1 ./utils/database/gamedata -run TestUserEventsDummy
```


### 12:55 重建部署与指定账号纠正

按操作人要求，将包含 dummy 回归测试的源码重新归档构建：`worktree-223e0dd28cea`。Canary 在 12:53:55 完成更新，185 组字段、12 组缓存、9 组完整响应对照全部通过；12:55:10 将相同镜像 `haruki-toolbox-backend:user-events-fix-223e0dd28cea` 部署生产。

- 镜像 ID：`sha256:b7f4566b5860760257928c0f9b6ef2da267ddbdf48c5d0069cb15694244b4c82`
- 二进制 SHA-256：`9ca248375d99f2a900a3620ba7e03f325e3fafa9f021798e540ecab7ce8f12c6`
- 备份与验收目录：`/data2/backups/toolbox-user-events-fix-223e0dd28cea/`
- 生产回滚：该目录 `rollback.sh`，恢复前一版 `user-events-fix-1acbc5628f57` 及当时环境文件。

游戏公开 profile 查询成功，但未提供活动 178 数据；未取得新的完整游戏上传。按操作人明确提供的结算值及更新授权，于 **12:55:56** 对指定国服账号进行人工纠正：仅把活动 178 的 `eventPoint` 从 `100956916` 改为 `144520517`，没有补造排名、领奖状态，也没有修改 `upload_time` 或其他活动。操作前保存原始记录，事务内行锁及原值校验防止覆盖并发上传；操作后逐项确认其他数据未变。用户身份与完整前后记录仅保存在上述 root 受限目录的 `account-event178-repair/` 中。

该目录 `rollback.sql` 仅在活动数组及上传时间仍匹配纠正后快照时恢复备份，后续若有新上传则不覆盖。此纠正是依据操作人提供的值进行的人工修复，不能当作已从游戏上游获取到结算数据的证据。


人工纠正后，生产 API 首次复核仍命中旧值，数据库与 canary 已是新值。按 `ClearCache` 的同账号 suite 范围清理时间戳和响应缓存，共删除 4 个键（含 2 个响应键）；生产 API、canary API 及生产热缓存复测均返回活动 178 `eventPoint=144520517`。若执行数据回滚，也必须清理该账号对应缓存。

12:58:43 最终检查：生产 healthy、零重启、无 OOM/ERROR；后台真实上传继续，副本 streaming。存在 1 条外部数据同步目标返回 554 的 WARNING，属于下游同步结果，不是数据库写入失败；本次接口回归 185 + 12 + 9 项全部通过。运行中二进制哈希与构建归档一致，生产配置仅变更镜像。
