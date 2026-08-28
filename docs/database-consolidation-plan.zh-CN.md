# 数据库整合方案：游戏数据从 MongoDB 迁往 PostgreSQL

本文记录把 suite / mysekai 游戏数据从 MongoDB 整合进 PostgreSQL 的动机、实测依据、目标结构、迁移 CLI 与灰度步骤。

> 状态：**代码已上生产，读源仍为 `mongo`，等待维护窗口执行翻转**（2026-08-29）。第 3 步已完成：写路径双写上线（§7.3.1），PG 与 Mongo 计数实时咬合，影子写零失败。**第 9 步已按 §7.1.1 修订**——剥离下沉到 Mongo 写入路径、PG 存全量，它不再是不可逆点。§1.1.1／§1.1.2 是 2026-08-29 新增的实测与前提更正，其中 §1.1.2 更正了「21 键已置空」这一前提（对 cn／tw／kr 从未生效）。代码侧 U0–U9 全部完成，`gofmt`／`go build`／`go vet`／`staticcheck`／`go test` 全清；剩余 U10–U12 是运维步骤，见 §7.6 runbook。§9.2 的开放问题已于 2026-08-24 全部拍板，结论并入 §9.1 与各章。所有数字均为实测；**§3.0 起已用生产全量普查数据**（2026-08-23），**§7.5 第二轮是生产 `suite` 全量副本上的离线装载**（10,928 篇，2026-08-24），测量条件见 §10。

## 1. 为什么要做

驱动这次改造的**不是存储成本**（见 §3.1，同等内容下 MongoDB 反而更省），而是下面三件事。

### 1.1 16 MB 单文档硬上限（决定性）

配置项 `sekai_client.suite_remove_keys` 目前会在写入前把 21 个顶层键置空（`utils/handler/suite_restore.go:144` `cleanSuite`）。维护者希望**停止丢弃这些数据**。

**生产全量普查表明：这在 MongoDB 上做不到。** `suite` 集合 10,928 篇文档的现状分布（21 键已置空）：

| 分位 | 现状 | 恢复 21 键后 | 占 16 MB |
|---|---:|---:|---:|
| p50 | 2.71 MB | **13.14 MB** | 82% |
| p90 | 4.21 MB | **14.64 MB** | 92% |
| p99 | 12.19 MB | **22.62 MB** | **141% 🔴** |
| max | 13.52 MB | **23.95 MB** | **150% 🔴** |

推算按「固定增量 +10.43 MB」——被移除的大头 `userCostume3dStatuses`（6.53 MB）与 `userCostume3dShopItems`（2.48 MB）是**游戏的服装／商品总目录**，对任何玩够久的账号体量基本相同，不随账号进度成比例。按「比例 3.17×」推算则 p99 会到 38.68 MB，更糟。**两种口径都在 p99 与 max 爆表。**

被剔除的 21 个键在单账号样本上合计 10.52 MB（占整档 69%），单键排名：

| 键 | BSON | 行数 | B/行 |
|---|---:|---:|---:|
| `userCostume3dStatuses` | 6.53 MB | 112,670 | 57.9 |
| `userCostume3dShopItems` | 2.48 MB | 40,896 | 60.6 |
| `userMissionStatuses` | 1.25 MB | 14,771 | 84.5 |

**而且现状本身已经危险**：max 13.52 MB 距上限只剩 2.5 MB。按 `obtainedAt` 直方图外推该账号年增 0.8–4 MB —— **即使什么都不做，最大的账号也会在 1–3 年内自然撞上限。**

**风险**：`utils/database/mongo/data_ops.go:76` 的 `UpdateOne` 完全没有识别超限写入错误，会冒泡成 500，并且该账号**此后每次上传都失败**。这一点与迁移方案无关，应立即修复（见 §7 第 0 步）。

#### 1.1.1 实测样本：一个已经站在上限之上的账号（2026-08-29）

上表是普查外推。这一条是**单份真实上传载荷的实测**——维护者提供的一个重度老玩家 jp 账号，
用生产的 `sekai_client` 密钥解包后逐项量取：

| | |
|---|---:|
| 载荷（密文） | 11.27 MB |
| 解包后顶层键 | 158 |
| 解包后 JSON | 14.32 MB |
| **解包后 BSON** | **16.16 MB** |
| 置空 20 键后 JSON | 4.62 MB |
| 置空 20 键后 BSON | **4.73 MB** |

**BSON 16.16 MB 已经超过 MongoDB 的 16 MiB 上限约 1%。** 也就是说：这个账号只要停止剥离，
**下一次上传就会失败**，而 §1.1 末尾那条「`UpdateOne` 不识别超限错误」意味着它会以 500
的形式反复出现、没有任何线索指向真正的原因。外推出来的 p99「141%」不再是推算——已经有
账号站在线上。

被剥离的 20 个键在这份样本上合计 **9.70 MB，占整档 67.8%**（与上面单账号样本的
10.52 MB／69% 相互印证），且高度集中在前三个：

| 键 | JSON | 元素数 |
|---|---:|---:|
| `userCostume3dStatuses` | 5.67 MB | 122,516 |
| `userCostume3dShopItems` | 2.12 MB | 44,408 |
| `userMissionStatuses` | 1.18 MB | 16,447 |
| *（其余 17 个合计）* | *0.73 MB* | |

**同一份数据写进 PostgreSQL 的实测落盘**（真实 `Store.Write` 路径，写进一次性库后读
`pg_total_relation_size`）：

| | PG 落盘 |
|---|---:|
| 置空 20 键（现状） | 0.45 MB |
| **完整数据** | **1.36 MB** |

14.32 MB 的 JSON 落盘 1.36 MB，**10.5×**。对照 MongoDB 需要 16.16 MB 却装不下 ——
**这一条本身就是整个迁移方案最有力的单点论据**：同一份数据，一个存不下，另一个只要
1/12 的空间。

#### 1.1.2 前提更正：`suite_remove_keys` 对 cn／tw／kr 从未生效（2026-08-29）

§1.1 开头「21 键已置空」这个前提**只对 jp／en 成立**。生产实测：

| 区 | 行数 | 已剥离 | 仍有完整数据 | `userCostume3dShopItems` 列占用 |
|---|---:|---:|---:|---:|
| cn | 5,822 | 1 | 5,821 | 640 MB |
| tw | 835 | 0 | 835 | 91 MB |
| kr | 4 | 0 | 4 | 438 kB |
| jp | 4,104 | 3,859 | 245 | 35 MB |
| en | 98 | 97 | 1 | 121 kB |

原因是 `cleanSuite`（`utils/handler/suite_restore.go:144`）**只按精确键名置空**：

```go
for _, key := range s.suiteRemoveKeys {
    if _, ok := suite[key]; ok { suite[key] = []any{} }
}
```

而这三个大键都有 compact 拼写（`catalog.CompactPairs`），**cn／tw／kr 客户端发的正是
compact 形态**，`cleanSuite` 认不出来。Mongo 抽样确认：cn／tw 的文档里
`userCostume3dShopItems` 缺失、`compactUserCostume3dShopItems` 存在；jp／en 则相反。
catalog 又把两种拼写解析到同一列，所以它在 `game_suite` 里显示为「有数据」。

三个后果：

1. **cn／tw／kr 没有撞上限，不是因为它们安全，而是因为 compact 形态本来就小得多。**
   不能拿它们当作「jp 也可以放开」的证据 —— §1.1.1 的样本证明 jp 放开就会爆。
2. **私有 API 一直在返回这些数据**（对 cn／tw／kr）。公开白名单不含这几个键，公开面
   没有暴露；`/api/private/*` 走的不是那个白名单。与维护者「这些数据已被丢弃」的预期不符。
3. 剥离逻辑应当同时匹配 `CompactPairs` 的 compact 拼写。**在 Mongo 侧剥离成为硬性防爆
   手段之后（见 §7.1 第 9 步的修订），这个洞就从「幸好没生效」变成隐患**：cn 客户端一旦
   改发行形态，或 tw 账号体量涨上来，就会静默撞上限。


### 1.2 端到端读取代价

MongoDB 的投影不是列式存储：服务端要把整个文档取回并解压，再由应用做 `BSON → Go 结构 → JSON` 两次形态转换。PostgreSQL 的列各自独立 detoast，且 `jsonb` / `bytea` 列取出来本身就是 JSON 文本，无需重新编码。

实测见 §3.2，最热路径（公开 API 无 `?key=`，取白名单键）相差 **14.5 倍**，分配次数相差 **6,250 倍**。

### 1.3 少一个数据存储

- 授权表 `game_account_bindings` 已在 PostgreSQL，公开读现在是「PG 查授权 → Mongo 取数据」的跨库两跳，合并后是单库 join，少一个 3s deadline。
- 单行 MVCC 让写入原子性免费。若继续留在 MongoDB 并按键拆文档，则需要自行实现「提交标记 + 代号过滤 + 按账号锁」的一致性协议。

## 2. 现状盘点

### 2.1 MongoDB 的实际用法

全仓 Mongo 调用点共 **9 处、3 个动词**：`FindOne` ×4、`UpdateOne` ×2、`Aggregate` ×1，外加 Ping/Disconnect。

- **没有** Find 游标、sort、分页、count、distinct、bulk write、事务、change stream
- **代码里没有创建任何索引**，唯一在用的是自动 `_id` 索引
- 唯一的聚合 `SearchPutMysekaiFixtureUser`（`utils/database/mongo/fixture_search.go:11`）**零调用者**，是预留未启用的功能

结论：**MongoDB 目前只是一个按 `_id` 存取的键值 blob 存储**，文档数据库的特性一个都没用上。

### 2.2 文档结构

两个 collection，`_id` 均为游戏 user id（int64），payload 平铺在文档根，服务端另加 `server` 与 `upload_time`。

**suite**：156 个顶层键（avsc 声明 209 个，写入前被 `cleanSuite` 置空 21 个）。体积分布极度倾斜：

| 区间 | 键数 | 合计 |
|---|---:|---:|
| > 100 KB | **12** | **13.0 MB（99%）** |
| 2 KB – 100 KB | 51 | 1.0 MB |
| < 2 KB | 93 | 33 KB |

**mysekai**：形状与 suite **完全不同**。实测一份真实文档（jp，2026-08-20，506 KB）：14 个顶层键，其中 `updatedResources` 一个键就占 **97.9%**。`updatedResources` 底下 35 个一级键，6 个占 96%：

| 键 | 字节 | 元素 |
|---|---:|---:|
| `userMysekaiCharacterTalks` | 155,827 | 3,371 |
| `userMysekaiHarvestMaps` | 131,630 | 4 |
| `userMysekaiFixtures` | 69,146 | 833 |
| `userMysekaiBlueprints` | 65,481 | 1,207 |
| `userMysekaiMusicRecords` | 43,312 | 768 |
| `userMysekaiSiteHousingLayouts` | 21,943 | 4 |

**mysekai 本身没有 16 MB 问题**（余量 32×），但它必须一起迁移才能下线 MongoDB。

### 2.3 写入语义

| 数据类型 | 语义 |
|---|---|
| `suite` | 先按 3 字段投影读旧值（`userEvents` / `userWorldBlooms` / `userGachas`），Go 侧做历史合并，再 `$set` 整份 |
| `mysekai` | `$set` 整份 |
| `mysekai_birthday_party` | 只 `$set` 三项：`server`、`upload_time`、以及**点路径** `updatedResources.userMysekaiHarvestMaps` |

`$set` 是**顶层字段级 merge**，不是整档替换 —— 旧上传里存在、新上传里没有的顶层键会保留。

三个历史合并键的冲突规则在 `data_ops.go` 的 `mergeUserEvents` / `mergeWorldBlooms` / `mergeUserGachas` 与对应的 `shouldReplaceX` 中。

### 2.4 对外契约

| 端点 | 认证 | key 过滤 | 响应 |
|---|---|---|---|
| `GET /public/:server/:type/:user_id`（+ `/api/public/...`） | 无（Oathkeeper noop） | suite：运行时白名单（默认 25 键）；**mysekai：无白名单** | 见下 |
| `GET /api/oauth2/game-data/...` | Hydra bearer + `ScopeGameDataRead` | 同上 | 同上 |
| `GET /api/private/game-data/...` | 静态 token + UA | **无过滤，任意顶层键，且允许点路径** | 无 key 时返回整档**含 `_id` 与 `server`** |
| `GET /api/user/:id/game-account/...` | Kratos cookie | 同公开 | 同公开，但不走 body 缓存 |

**响应形状随 key 个数变化**，迁移必须逐字节保持：

```
suite,  无 key    → { 白名单键的对象，按白名单顺序 }
suite,  ?key=A    → A 的裸值（数组 / 对象 / 数字，无外层）
suite,  ?key=A,B  → { A, B }
mysekai,无 key    → 整档去掉 _id 和 server
mysekai,?key=A    → { A }    ← 单键不解包，与 suite 不一致
```

其他必须保持的细节：

- 缺失的键返回**空数组**（`GetValueFromResult`）
- `userGamedata` 永远被接受为 `?key=`，且有 7 字段列级白名单（`utils/api/data/utils.go:11`）
- 部分键以 **compact 列式 + `__ENUM__` 字典**形态存储，`buildSuiteProjection` 同时投影明文键和 `compactFieldName(key)`，由 `GetValueFromResult` 展开
- **私有 API 的 `bsonDGet` 不展开 compact** —— 现存缺陷，见 §7 第 1 步

### 2.5 两个必须先澄清的现状缺陷

**私有 API 的点路径 key 今天返回 `null`，并不工作。** `buildKeyProjection`（`internal/modules/userprivateapi/private_data_handler.go:298`）确实放行点号，MongoDB 也确实返回嵌套结果，但 `bsonDGet`（`response.go:30`）是**平铺扫描**（`elem.Key == key`），拿 `updatedResources.userMysekaiHarvestMaps` 去比顶层键名必然匹配不上，返回 `nil` → HTTP 200 + `null`。

这一条对迁移的含义是：**不需要"保持点路径可用"，只需要保持"返回 null"**。若要修成真正可用，那是一次**对外行为变更**，必须单独排期并通知调用方。

**响应的对象键顺序今天已经不确定。** `NormalizeProviderResponse` 把 `bson.D` 转成 `map[string]any`（`utils/api/data/provider_normalize.go:54-83`），存储顺序在这一步就丢了；随后 `sonic.Marshal` 走的是 `ConfigDefault`（`SortMapKeys: false`，`internal/bootstrap/fiber.go:20`），所以最终顺序由 Go map 迭代决定 —— 而 Go map 迭代是随机的。

**实测**（sonic v1.15.1，同一篇 7 键元素连续 marshal 200 次）：

```
distinct serialisations over 200 marshals: 7
[{"defaultImage":"original","episodes":[],"userId":28808221489823746,"cardId":1,...}]
[{"userId":28808221489823746,"cardId":1,"level":2,...,"episodes":[]}]
[{"masterRank":3,"specialTrainingStatus":"done",...,"level":2}]
```

**同一篇文档、同一次进程，连续两次请求的键顺序就不同。** 因此没有任何客户端能依赖键顺序，迁移不必为此付出代价（这条推翻了"必须逐字节保序"的早期结论，但不影响 §4.1 的选型，理由见该节）。

这一条有一个**直接的收益**，见 §4.7.1「元素间键顺序不同不是参差」：它让 compact 化的适用面从约 10% 提高到接近全量。

## 3. 实测数据

§3.0 是**生产全量普查**（`Haruki-CN02-HGH01` 主库，2026-08-23）。§3.1 起是在本地用一份真实 suite 上传（userId 28808221489823746，rank 693，2026-04 快照）做的对照实验，MongoDB 7 开 zstd 块压缩，PostgreSQL 18（`postgres:18-alpine`）。

### 3.0 生产普查

| 集合 | 文档数 | 平均 | 逻辑总量 | 磁盘 | 压缩比 | 索引 |
|---|---:|---:|---:|---:|---:|---:|
| `collections.suite` | **10,928** | 2.89 MB | **30.81 GB** | **3.30 GB** | 9.4× | 1 MB |
| `collections.mysekai` | **4,124** | 383 KB | 1.32 GB | 0.20 GB | 7.4× | 4 MB |

体积分布（各抽样 200 篇）：

| | p50 | p90 | p99 | max | min |
|---|---:|---:|---:|---:|---:|
| suite | 2.71 MB | 4.21 MB | **12.19 MB** | **13.52 MB** | 0.73 MB |
| mysekai | 0.27 MB | 0.56 MB | 1.64 MB | 10.08 MB | — |

顶层键并集（抽样 400 篇），决定宽表列数：

| | 并集 | 单篇 p50 | 单篇 max | PG 1600 列上限 |
|---|---:|---:|---:|---|
| suite 顶层键 | **205** | 157 | 170 | ✅ 通过 |
| mysekai 顶层键 | **14** | 13 | 13 | ✅ |
| mysekai `updatedResources` 一级键 | **46** | 32 | 35 | ✅ |

> mysekai 的数字是 2026-08-23 清理数据污染**之后**的。清理前顶层键并集是 171、单篇 max 是 167，因为有 68 篇文档被 suite 数据／会话凭据／masterdata／错误响应污染（另见事故记录）。清理移除了 5,224 个字段实例、回收 189.9 MB，`updatedResources` 3,716 篇零损失。

**生产压缩比是 9.4×（suite）／7.4×（mysekai），不是本地单文档实验测到的 16–18×。** 单文档实验高估了 MongoDB 的压缩效果，§3.1 的对比要按这个折算。

### 3.1 存储占用（含索引）

| 方案 | 内容 | 磁盘 |
|---|---|---:|
| MongoDB（现状，21 键置空） | 今天实际存的 | **284 kB** |
| MongoDB，全部键 | 目标内容 | **936 kB** |
| PG 单 jsonb 列，21 键置空 | 同「今天」 | 496 kB |
| PG 单 jsonb 列，全部键 | 同「目标」 | 1416 kB |
| **PG 宽表混合**（12 大键 bytea+zstd + 144 json/jsonb） | 同「目标」 | **776 kB** |

**同等内容下 MongoDB + zstd 更省空间**（284 vs 496 kB，差 43%），因为 MongoDB 的 zstd 压到 16–18×，而 **PostgreSQL 的 TOAST 只支持 `pglz` 和 `lz4`，不支持 zstd**（实测：`ERROR: invalid compression method "zstd"`；而 `wal_compression` 是支持 zstd 的，说明 zstd 编译进去了只是没接 TOAST），pglz 只有 9.6×。

只有把 12 个大键改成 **bytea + 应用层 zstd** 之后 PG 才反超（776 vs 936 kB，好 17%）。**这一步值 1.82×**（1416 → 776 kB）；没有它，迁移在空间上是净亏的。

> ⚠️ **本节结论已被 §3.5 取代。** 本节是 n=1 的单文档实验，而且**没有把 compact 化算进去**。在 262 篇真实生产文档上重测的结论是：compact 化本身就贡献 30.8%，之后全 `json` 不压缩已经优于现状的 MongoDB（3.0 GB vs 3.30 GB），bytea + zstd 只多省 0.9 GB。**最终方案不做应用层压缩**，见 §3.5 与 §4.7.2。本节保留作为「单样本会怎样误导」的记录。
>
> 另：本节测量在 `jsonb` 列上进行。最终方案改用 `json`（理由见 §4.1），实测体积完全相同（776 kB）。

压缩算法对比（同一份 13.44 MB JSON）：

| 编码 | 大小 | 倍数 |
|---|---:|---:|
| PG jsonb + lz4 | 1808 kB | 7.61× |
| PG jsonb + pglz | 1440 kB | 9.56× |
| gzip -6 | 876 kB | 16.1× |
| **zstd -3** | **588 kB** | **24.0×** |
| zstd -19 | 433 kB | 32.6× |

> 注：`postgres:18-alpine` 的 `default_toast_compression` 实测为 `pglz`。**不要改成 lz4** —— 对这份数据 lz4 比 pglz 差 20%。

### 3.2 查询性能

端到端（Go 客户端 → 可发送的 JSON，热缓存，单连接）：

| 访问模式 | PG 宽表混合 | MongoDB（现有代码路径） | PG 快 |
|---|---:|---:|---:|
| 单中键 `userCards` | **259 µs** | 12,175 µs | **47×** |
| **19 白名单键（最热）** | **2,489 µs** | 36,040 µs | **14.5×** |
| 整档 | **11,079 µs** | 332,762 µs | **30×** |

分配次数：

| | PG | MongoDB |
|---|---:|---:|
| 19 白名单键 | **94 allocs / 6.2 MB** | 587,536 allocs / 26.5 MB |
| 整档 | **540 allocs / 63 MB** | 5,774,706 allocs / 238 MB |

**但传输层本身 MongoDB 并不慢。** 用 `bson.Raw` 最小解码对比：

| 访问模式 | PG | MongoDB `bson.Raw` |
|---|---:|---:|
| 单小键 | 75.5 µs | 133.9 µs |
| 19 白名单键 | 2,401 µs | **1,333 µs** |
| 整档 | 8,105 µs | **6,364 µs** |

**PG 的优势几乎全部来自「省掉 BSON → Go → JSON 的编解码往返」**，而不是存储层更快。这个优势 MongoDB 追不平：`bson.Raw` 能省掉解码，但 BSON ≠ JSON，仍需转换一次；唯一能追平的办法是在 MongoDB 里存 JSON 字符串，那等于放弃全部文档特性。

### 3.3 PG 内部布局对比

| 布局 | 单小键 `userProfile` | 单中键 `userCards` | 19 白名单键 | 整档 |
|---|---:|---:|---:|---:|
| 单 jsonb 列 | 6,455 µs | 7,478 µs | 70–121 ms | 50.2 ms |
| 按键拆行 | 58 µs | 2,534 µs | 6.0–6.3 ms | 48.3 ms |
| **宽表（列 = 键）** | **130 µs** | **2,583 µs** | **6.1–8.0 ms** | 55.0 ms |

**单 jsonb 列会精确复现 MongoDB 今天的读放大问题** —— PostgreSQL 的部分 detoast 只对 `text` / `bytea` + `STORAGE EXTERNAL` 生效，从大 jsonb 里取一个 key 必须全量解压。

宽表与按键拆行性能等价（±10%）。选宽表的理由见 §4.1。

### 3.4 关系表 vs 数组列 vs jsonb（针对最大的键）

`userCostume3dStatuses`，112,670 行：

| 布局 | 磁盘 | 读该键 | 跨用户查询 |
|---|---:|---:|---|
| jsonb 一行 | **540 kB** | 24.8 ms | ✗ |
| 数组列（复刻游戏 compact 形态） | 584 kB | **0.144 ms** | ✗ |
| 关系表 112,670 真行 | **9472 kB（17.5×）** | 6.2 ms | ✓ 0.043 ms |

**规范化成真行表比 jsonb 大 17.5 倍**（每行 23 字节 tuple header × 112,670 = 2.6 MB，加三列主键索引，且行小于 2 KB 永远不会被 TOAST 压缩）。它唯一买到的是跨用户查询能力，而全仓**零跨用户查询**。

因此本方案**不做关系化**。将来若要做家具搜索一类功能，只给需要的那一个键单独建行表或 GIN 索引。

### 3.5 大键是否压缩 —— 结论：不压

在 **262 篇真实生产文档**（分层抽样：jp 133 / cn 80 / tw 34 / en 12 / kr 3，含体积最大的 20 篇）上，落进本地 PostgreSQL 18 实测：

| 布局 | JSON 逻辑 | **PG 实测磁盘** | 每账号 | 外推 10,928 账号 |
|---|---:|---:|---:|---:|
| A 全 `json`，不做 compact 化 | 701.8 MB | — | — | — |
| **B compact 化 6 键，全 `json`，不用 zstd** | 485.6 MB | **71.7 MB** | 280 KB | **≈ 3.0 GB** |
| C B + 非白名单大键 zstd | 310.7 MB | 49.9 MB | 195 KB | ≈ 2.1 GB |
| D B + 全部大键 zstd | 200.4 MB | 44.9 MB | 175 KB | ≈ 1.9 GB |
| *(对照)* 现在的 MongoDB | — | — | — | **3.30 GB** |

两个关键事实：

**① compact 化本身就贡献 30.8%**（701.8 → 485.6 MB），而且它是原生 JSON、`psql` 里可读、不需要任何编解码层。

> 全量实测（2026-08-24）把这一条放大了：5 个键的行式值合计 **6,307.95 MiB → 1,459.39 MiB（23.1%，4.32×）**。抽样只有 30.8% 是因为样本里 cn／tw 占比偏高，那部分本来就已经是 compact 形态、没有可压缩空间。

**② 应用层 zstd 与 TOAST 的 pglz 收益重叠。** JSON 字节 B→C 是 1.56×，落到磁盘只剩 **1.44×** —— PG 本来就压了一道。

**决定：采用方案 B，不做 bytea + zstd。**

理由是 B 已经优于现状（3.0 GB vs MongoDB 的 3.30 GB），而 C 只多省 **0.9 GB**，代价却是一整层 zstd 编解码 + 帧头 + round-trip 测试，外加 23 个列在 `psql` 里不可读。为 0.9 GB 引入这些不划算。

##### 全量实测校正（2026-08-24）

上表的 3.0 GB 是 262 篇抽样外推。**全量 10,928 篇、按最终配置（含 `denied` 丢弃）实际落盘的数字是 3,113 MB**：

| | 总计 | heap | TOAST | 索引 |
|---|---:|---:|---:|---:|
| **方案 B 全量实测** | **3,113 MB** | 42 MB | 3,070 MB（98.6%） | 456 kB |
| *(对照)* 现在的 MongoDB | 3,297 MB | | | |

**比 MongoDB 小 5.6%** —— 结论方向不变，但**余量比抽样外推的说法小得多**（外推说小 9%）。抽样偏乐观的原因同上：样本里 cn／tw 占比高于全量。

而且这只是 `suite`。把 `mysekai` 算进来：

| 集合 | MongoDB | PostgreSQL | 差 |
|---|---:|---:|---:|
| `suite` | 3,297 MB | 3,113 MB | −5.6% |
| `mysekai` | 205 MB | 266 MB | **+30%** |
| **合计** | **3,502 MB** | **3,379 MB** | **−3.5%** |

**只小 3.5%，基本是持平。** 存储从来不是这次改造的理由（§1），不要拿它当卖点。

同一份数据在编码器修好之前（见 §4.7.1「元素间键顺序不同不是参差」）是 **3,518 MB**，反而比 MongoDB 大 6.7%。**那 401 MB 的差距完全来自那一个判定** —— 修不修它决定了这个方案是省空间还是费空间。

补充数据：compact 化之后仍 ≥100 KB 的键有 **23 个**（不是单样本时以为的 12 个），说明大键分布比预想分散、每个的 zstd 增量收益都不深。

这条不是不可逆的 —— §4.7.3 的「两列共存」机制让任何单个键都能后补 `_z` 列，不需要现在全上。

## 4. 目标结构

两张表，均以 `(user_id, server)` 为主键 —— 顺带修掉现存缺陷：写入 upsert 只按 `{_id}` 过滤而读取按 `{_id, server}` 过滤（`data_ops.go:76` vs `:338`／`:380`），同一 game user id 跨区服会互相覆盖。

⚠️ **这个修复在切换当时有两个可见后果，接受它们（2026-08-25 决定：旧有缺陷不做兼容层）：**

| 后果 | 说明 |
|---|---|
| suite 历史合并不再跨服 | `fetchOldData`（`data_ops.go:110`）同样只按 `{_id}` 取旧值，所以今天多区服账号的 `userEvents`／`userGachas`／`userWorldBlooms` 是**跨服混在一起**的。切换后各服独立，这些键在该账号**切换后第一次上传时**内容会变 |
| 第二区服的上传新建一行 | 今天是把同一行的 `server` 字段翻过去，于是旧区服立刻 404。切换后旧区服那行仍在，会**持续返回切换时的快照** |

第二条是行为**变好**（数据本来就不该被另一区服抹掉），但要注意它把一个「立刻消失」变成了「长期陈旧」——，如果某个账号的旧区服数据不该再对外可见，那是单独的数据清理，不是本方案的一部分。

### 4.1 列类型用 `json`，不用 `jsonb`

| | 体积 | 19 白名单键 | 全部 156 列 |
|---|---:|---:|---:|
| `jsonb` | 776 kB | 1.07–1.82 ms | 6.2–7.4 ms |
| **`json`** | **776 kB** | **0.62–1.01 ms** | **3.7–3.8 ms** |

**体积相同，`json` 快 1.4–1.9×**。原因是本方案的访问模式是「整键取出后直接拼进响应」，`jsonb` 必须从二进制形态重新渲染成文本，`json` 直接返回存储副本。

次要理由（实测确认）：

```
原文   {"zeta":1,"a":2,"mm":3,"a":9,"n":1.50,"big":1e2}
json   {"zeta":1,"a":2,"mm":3,"a":9,"n":1.50,"big":1e2}   ← 完全一致
jsonb  {"a": 9, "n": 1.50, "mm": 3, "big": 100, "zeta": 1} ← 重排、丢重复键、1e2→100
```

且 **`jsonb` 硬拒绝 ` `**（`ERROR:   cannot be converted to text`）。上传内容是用**公开的** Project Sekai 客户端密钥解密的，属攻击者可控 —— 一个带 NUL 的字符串会让写入直接失败。`json` 不会。

> 注意：**不要**用"保持响应键顺序"当理由。§2.5 已确认顺序在 `NormalizeProviderResponse` 那一步就丢了。选 `json` 的理由是速度、NUL 容忍和重复键保留。

### 4.2 不要用 Ent 建这两张表

Ent 在读取时会把 JSON 列 `json.Unmarshal` 成 Go 结构 —— **这正好重建了我们要消除的 `BSON → Go → JSON` 往返**，§3.2 测到的 14.5–47× 收益会被全部吃掉。

其他不适配之处：

- Ent 无法表达 `STORAGE EXTERNAL`
- 未启用任何 feature flag（`ent/toolbox/cmd/entc.go`），因此**没有 `OnConflict` upsert**
- 生成代码体积：按仓库现有 20 个 schema 回归，约 **134 行/字段 + 1460 行**基数；156 字段约 **22,000 行**
- 访问模式是**由 `?key=` 驱动的动态列清单**，与 Ent 的静态查询构建器天然不合

**结论：这两张表用 `database/sql` 手写数据访问，其余 20 张表继续用 Ent。** 仓库已有先例：`internal/bootstrap/schema_compat.go:48` 通过 `entClient.SQLDB()` 执行手写 DDL，并在 `AutoMigrate` 关闭时 fail-fast 校验（`:160`）。

### 4.3 驱动:为 game data 单开 pgx 连接池

现用的 `lib/pq`（`go.mod:17`）**只支持文本协议**，`bytea` 会以 `\x` 十六进制返回 —— 线上多传一倍字节，外加每次读的十六进制解码。整档读约多传 974 kB。

**为 game data 读写单开一个 `pgx/v5` 池**（二进制协议、可直接扫 `[]byte`），Ent 继续用 `lib/pq`。两者互不干扰。

⚠️ **必须设 `MinConns` 预热。** pgx 的池是**惰性建连**的：不预热时每个 worker 的第一次 `Acquire` 都会撞上空池。实测把 `EmptyAcquireCount` 顶成了**恰好等于 worker 数**（conc 4／8／16 分别得到 4／8／16），看起来像池打满，其实只是建池。把 `MinConns` 设成 `MaxConns` 并等 `IdleConns` 到位之后，同样的压测在 conc 8 与 conc 16（2× 峰值）下 30 秒、约 15–18 万次操作，`EmptyAcquireCount` **都是 0**（M6）。

这条同样是**监控口径**问题：如果线上直接用 `EmptyAcquireCount > 0` 告警而不排除预热，每次重启都会误报一次。

### 4.4 列命名

- **snake_case，不加引号**。全仓现有表均为 snake_case；带引号的 camelCase 会让每条手写 SQL 和每次运维 `psql` 永远背着双引号，而漏写引号会静默解析成另一个小写标识符而不是报错。实测 210 个 suite 键名 + 35 个 mysekai 键名转 snake_case **零冲突**，最长 55 字符（`user_mysekai_fixture_game_character_performance_bonuses`），在 `NAMEDATALEN-1 = 63` 之内。
- **snake_case 结果存进注册表，绝不在运行时重算**。`userCostume3dStatuses → user_costume3d_statuses` 依赖驼峰切分规则，规则一改会静默"重命名"线上列。生成器只**建议**名字，注册表**钉死**它。
- **每个游戏键列带存储后缀 `_j`（json）或 `_z`（zstd bytea）**。三个作用：元数据命名空间（`user_id` / `server` / `upload_time` / `extra`）与游戏键在结构上不可能撞名；运维 `\d game_suite` 一眼看出编码；存储类切换可以用「两列共存」表达，永远不需要 `RENAME` 或 `ALTER TYPE`（后者会以 `ACCESS EXCLUSIVE` 重写整表）。
- **mysekai 的 `updatedResources` 键加 `r_` 前缀**，把两个命名空间隔开。

### 4.5 DDL

```sql
CREATE TABLE IF NOT EXISTS game_suite (
    user_id      bigint   NOT NULL,               -- 原 _id：游戏 user id
    server       smallint NOT NULL,               -- jp=1 en=2 tw=3 kr=4 cn=5
    upload_time  bigint   NOT NULL DEFAULT 0,
    registry_rev integer  NOT NULL DEFAULT 0,     -- 写入该行时的注册表版本
    extra        json     NOT NULL DEFAULT '{}',  -- 未知顶层键

    user_gamedata_j              json,
    user_profile_j               json,
    -- ... 每个已知键一列
    user_costume3d_statuses_z    bytea,           -- 带帧头的 zstd
    -- ...

    CONSTRAINT game_suite_pkey PRIMARY KEY (user_id, server)
) WITH (fillfactor = 80,
        autovacuum_vacuum_scale_factor = 0.05,
        autovacuum_analyze_scale_factor = 0.02);

-- 每个 _z 列都要：
ALTER TABLE game_suite ALTER COLUMN user_costume3d_statuses_z SET STORAGE EXTERNAL;
```

`game_mysekai` 同构，但列来自 `updatedResources` 的一级键（带 `r_` 前缀），顶层那 13 个小键各自一列（合计仅 ~11 KB，不必塞进一个 envelope）。`mysekai` 与 `mysekai_birthday_party` 共用同一行。

### 4.6 已知键注册表

**不要用 `data/suite_user.avsc` 当唯一来源** —— 实测它**不完整**：`userBillingShopItems` 在 `suite_remove_keys` 里但不在 avsc 的 209 个字段中。而且它只对 `tw` 区生效。

注册表来源应为并集：`avsc ∪ suite_remove_keys ∪ public_api_allowed_keys ∪ 生产普查`，并**刻意超配**到约 210 列 —— 一个空列的成本是 NULL 位图里的 1 bit，而一个漏掉的键会静默落进 `extra` 失去独立投影能力。

注册表生成一个编译进二进制的 `columns_gen.go`（`map[string]columnSpec`）+ 一个 `CatalogChecksum`。**列名只能来自这张编译期的表，未知键的名字永远只作为 `extra` 里的 JSON 值出现，绝不进入标识符位置。**

### 4.7 编码策略

分两层：**先 compact 化，再对剩余大键选择性 zstd**。

#### 4.7.1 compact 白名单以 cn 服实际形态为准

生产实测（抽样 cn 120 篇）：**cn／tw 客户端发送 compact 形态，jp／en 发送行式**，比例约 64% : 36%。cn 服实际使用的 compact 键恰好 6 个，与 `data/suite_user.avsc` 声明的一致：

| 键 | cn compact 平均 | jp 行式平均 | 倍数 |
|---|---:|---:|---:|
| `userCharacterMissionV2Statuses` | 309.9 KB | 840.4 KB | **2.71×** |
| `userCostume3dStatuses` | 109.1 KB | 312.2 KB | **2.86×** |
| `userMusicAchievements` | 136.5 KB | 352.7 KB | **2.58×** |
| `userMusicResults` | 137.1 KB | 346.0 KB | **2.52×** |
| `userMissionStatuses` | 22.4 KB | 15.5 KB | — ⚠️ |
| `userCostume3dShopItems` | 900.8 KB | 130.4 KB | — ⚠️ |

> 后两行**不能当作编码效率**：`userCostume3dStatuses` 与 `userCostume3dShopItems` 都在 `suite_remove_keys` 里，jp 侧已被 `cleanSuite` 清空（漏网的少数拉高了平均），所以跨服比较在这两个键上失效。真实转换收益必须在**同一份数据**上做行式／compact 对照，由离线演练测出。

**规则：只对 cn 已经 compact 化的这 6 个键做转换，不自己发明 compact。**

理由是游戏官方已经替我们做过判断，而且实测吻合 —— `userCards` 官方没有 compact 化，实测 6/6 文档参差（同一数组里有的元素带 `episodes`、有的带 `specialTrainingStatus`）；`userMaterialExchanges` 更是 6 种元素形状。对参差数组做 compact 会让缺失字段在还原时变成显式 `null`（对外可见的响应变化），且 `RestoreColumns` 按最短列截断（`compactrestore.go` 的 `numEntries` 循环）会**静默丢数据**。

##### 最终名单（离线演练后定稿）

**jp／en 的行式数据只转 5 个键，`userCostume3dStatuses` 不转。**

| 键 | jp/en 转 compact | 依据 |
|---|---|---|
| `userCharacterMissionV2Statuses` | ✅ | 演练 round-trip 137/137 文档精确 |
| `userCostume3dShopItems` | ✅ | 14/14 精确 |
| `userMissionStatuses` | ✅ | 135/135 精确 |
| `userMusicAchievements` | ✅ | 136/136 精确 |
| `userMusicResults` | ✅ | 精确 |
| **`userCostume3dStatuses`** | ❌ **不转** | 见下 |
| `userCards` | ❌ | cn 未做；实测 241/254 文档参差（`episodes` vs `specialTrainingStatus` 字段集互斥）；且在公开白名单，是热路径 |
| `userMaterialExchanges` | ❌ | cn 未做；6 种元素形状 |
| 其余 | ❌ | cn 未做 |

**为什么 `userCostume3dStatuses` 例外**：它的 jp 行式数据是参差的 —— 已获得的服装带 `obtainedAt`，未获得的不带（实测某篇 92,556 行里 19,620 / 72,936 的分布）。演练的编码器在全部 14 篇有数据的文档上**正确拒绝**了它。

cn 官方确实 compact 了这个键，做法是**把 `obtainedAt` 补齐成等长列、缺失填 `null`**。跟进这个做法会让 jp 用户的响应里凭空出现 `"obtainedAt": null`（原本该字段不存在），**是对外可见的变化**。而这个键在 `suite_remove_keys` 里，停止置空后本来就是新增数据，没必要为它引入一个对外差异。cn／tw／kr 侧本来就是 compact 形态，原样保留，不受影响。

##### 两种形态并存的键：一列 + 别名就够（2026-08-24 定稿）

`userCostume3dStatuses` 不转，意味着 jp／en 存行式、cn／tw／kr 存 compact。本文早先写的是「它们是不同的键名，因此需要两个列」，**全量演练推翻了这个结论**：

| 方案 | 结论 |
|---|---|
| ~~两列：`user_costume3d_statuses_j` + `compact_user_costume3d_statuses_j`~~ | **不需要** |
| **一列 + `compactUserCostume3dStatuses` 别名** | ✅ 采用，全量装载已验证 |

一列够用，因为**存储值是自描述的**：JSON **数组**＝行式，JSON **对象**（带 `__ENUM__`）＝compact。读端按形状分派即可，这正是 `GetValueFromResult` 今天的行为。

两种形态并存的文档（全量实测 12 篇，横跨全部 6 个 compact 键）由别名冲突路径处理：**行形态胜出进列，compact 形态存进 `extra` 不丢**，与生产 `GetValueFromResult` 优先取明文键的优先级一致。

> 显式请求 `?key=compactUserCostume3dStatuses` 的路径不构成障碍 —— 外部调用方一般只取 `userCostume3dStatuses`（2026-08-24 确认）。别名仍然登记在注册表里，需要时按同一列作答。

全量装载后在表上直接核对，一列两形态确实同时存在：

| 列 | 对象（compact） | 数组（行式） |
|---|---:|---:|
| `user_costume3d_statuses_j` | **6,570**（cn／tw／kr） | **4,148**（jp／en） |
| `user_music_results_j` | 10,456 | 104 |

读端按首个非空白字符 `{` / `[` 分派即可，无需第二列，也无需额外的元数据列。

##### 元素间键顺序不同**不是**参差

演练第一次全量跑（2026-08-24）暴露了编码器的一个过严判定：它把「数组元素的键**顺序**不同」也当作参差拒绝，导致 compact 化几乎没有生效。

| 键 | 接受 | 因「键顺序不同」被拒 |
|---|---:|---:|
| `userCharacterMissionV2Statuses` | 382 | **3,766** |
| `userMusicAchievements` | 372 | **3,657** |
| `userMusicResults` | 388 | **3,656** |

**这个拒绝是错的。** 要区分两种"参差"：

| 情况 | 后果 | 处置 |
|---|---|---|
| 键**集合**不同（有的元素带 `obtainedAt`、有的不带） | 列式化必须补 `null` 填齐，还原时**缺失字段变成显式 `null`** —— 对外可见 | **仍然拒绝** |
| 键**顺序**不同、集合相同 | 只是同一批成员的排列。不发明任何值、不丢任何值，只改对象内成员的先后 | **规范化为首元素的顺序后转换** |

第二种在语义上完全无损，而且它改变的那个属性 —— 对象内的键顺序 —— **今天本来就不保留**（§2.5 实测：同一文档连续 marshal 200 次出现 7 种不同顺序）。为一个对外不可观测的属性放弃全部 compact 收益不划算。

判定为"排列"的充分条件（三者同时成立才算，缺一即按键集合不同拒绝）：

1. 元素键数与首元素相同
2. 每个键都是首元素键集合的成员
3. 元素内无重复键

⚠️ **round-trip 守卫必须跟着改口径**：规范化之后要比对的是**规范化后的行形态**，不是原始字节。实现上先按首元素顺序重排、序列化出 `canonRow`，再拿生产展开器的输出与 `canonRow` 逐字节比。并且在**没有发生重排**时断言 `canonRow == 原始字节` —— 否则说明序列化器本身不忠实，直接拒绝转换。这样守卫的强度和放宽之前完全一样，只多允许了排列这一件事。

##### 转换时必须复用已有字典

演练实测：游戏的 `__ENUM__` 是**客户端固定常量**，含数据中从未出现的标签，标签顺序写死。从行数据重建会得到更短、顺序不同的字典 —— 语义等价但字节不同。

| 转换策略 | 与原存储的一致性 |
|---|---|
| 从行数据推导字典 | `same-semantics-different-encoding` |
| **以存储字典为提示** | **`identical` / `same-modulo-key-order`** |

⚠️ **字典是 per-document 的。** 实测 `compactUserMissionStatuses.missionType` 有 2 种字典（17 vs 16 标签），同一整数索引在不同文档里含义不同。**绝不能抽取全局字典表**，那会静默改变数据语义。

⚠️ **列内整数宽度不一致。** 实测某 tw 文档的 `costume3dShopItemId` 列里 int32 与 int64 混用（2,673 / 34,072）。不能假设列内类型统一。

暂不新增自定义 compact 键。

#### 4.7.2 不做应用层压缩

**所有游戏键列一律 `json`，没有 `bytea`，没有应用层 zstd。** 依据见 §3.5：262 篇真实文档实测，加上 zstd 只多省 0.9 GB（外推全量），而 compact 化后的方案已经优于现状的 MongoDB。

这条决定消除了一整批复杂度：

- 不需要 zstd 编解码层、帧头、magic、版本号
- 不需要编解码的 round-trip 测试
- 不需要 `bytea` 列，因此 `STORAGE EXTERNAL` 和 pgx 二进制协议（§4.3）从**必需**降级为**可选优化**
- 所有列在 `psql` 里直接可读，运维和排障成本大幅下降
- 迁移 CLI（§5）少一个出错面

`_j` / `_z` 的列名后缀约定（§4.4）**保留** —— 它让将来任何单个键都能后补压缩而不需要 `RENAME` 或 `ALTER TYPE`。现在全部列都是 `_j`。

#### 4.7.3 禁止存储的键（denied）

以下键**不迁移、不建列、不进 `extra`，直接丢弃**：

| 键 | 内容 | 现状 |
|---|---|---|
| `userInherit` | 账号继承信息 | 在 `suite_remove_keys` 里，但 262 篇样本中仍有 **15 篇残留数据** |
| `userPlatformInheritIos` | 含 `userId` | **不在移除列表**，54 篇有数据 |
| `userPlatformInheritAndroid` | 含 `userId` | **不在移除列表**，55 篇有数据 |
| `userPlatforms` | `[]provider` | **不在移除列表**，90 篇有数据 |
| `userRegistration` | `registeredAt, userId, signature(JWT), platform, yearOfBirth, monthOfBirth, dayOfBirth, age, deviceModel, operatingSystem, billableLimitAgeType` | **不在移除列表**，**254/254 篇全部有完整数据** |

**保留**：`userChargedCurrency`（充值金额）及其余所有键。

**这套丢弃机制已在演练里实现并验证（2026-08-24）。** 它由三层构成，缺任何一层都会让这个控制悄悄失效：

| 层 | 位置 | 作用 |
|---|---|---|
| 扫描期 | `keyreg.Scanner.Observe` / `ObserveChild` | denied 键不生成列 —— 246 列而不是 251 |
| 校验期 | `keyreg.Registry.Validate` | **拒绝**任何把 denied 键放进列、别名或扁平子键的注册表。手改注册表也绕不过 |
| 装载期 | `pgwriter.write` | 在**值被编码之前**丢弃 —— 不进 Go buffer、不进列、不进 `extra`；逐键计数上报 |

⚠️ **没有任何体积或行数指标能验证这个控制**：5 个键合计仅 1,765 kB，v4（实现 denied）比 v3 只小 4 MB。因此它必须靠**直接断言**来守：注册表校验拒绝三种走私路径，装载后核对 250 列里 denied 列为 0、全表 `extra` 里 denied 键名出现 0 次。

全量实测丢弃 **27,300 次**：`userRegistration` 10,716 篇（含 `age`／`dayOfBirth`／`yearOfBirth`／`deviceModel`／`operatingSystem`／`platform` 与一个 HS256 JWT），其余四键各 4,146 篇。



安全性核查（迁移前已完成）：

- 五个键在 Go 代码里**零引用**（`grep` 全仓非测试代码），移除不打断任何内部功能
- 五个键**都不在生产的 `public_api_allowed_keys`**（35 条），公开 API 本来就取不到
- 唯一的对外影响：**私有 API 无 `?key=` 时返回整档**，这些字段会从响应中消失。私有 API **只有本团队自己的服务在用**（#4），没有外部调用方，因此不需要通知窗口 —— 只需与切换同批发布，并同步更新本团队消费方

##### 为什么不靠 `suite_remove_keys`

该机制已被实测证明有两个漏洞，都会让"移除"失效：

1. **不覆盖 `compact*` 变体** —— `compactUserCostume3dStatuses` 等从未被清理过（§4.7.1）
2. **payload 里没有该键时不清理** —— `cleanSuite` 是 `if _, ok := suite[key]; ok`，键缺席就跳过，而 `$set` 也不会覆盖，旧值永久残留。这正是 `userInherit` 明明在列表里却还有 15 篇残留的原因

迁移后改为 **PG 侧注册表的白名单机制**，`suite_remove_keys` 随旧库一起退役。

#### 4.7.4 键的三分类

注册表把每个顶层键分成三类，**denied 必须是显式类别，不能靠"不在白名单"来表达** —— 否则代码无法区分「游戏新增的键（该告警并进 `extra`）」和「我们明确不要的键（该静默丢弃）」。

| 类别 | 处理 | 说明 |
|---|---|---|
| `known` | 独立列 | 注册表里的已知键，列名已钉死（§4.4） |
| `denied` | **直接丢弃** | §4.7.3 的五个键。不写库、不进 `extra`、不告警 |
| 其余（未知） | **进 `extra` json 列** | 游戏新增的键。**要告警**，以便后续提升为独立列（§4.6） |

#### 4.7.5 通用规则

- 编码策略用**注册表里的固定清单**，不做运行时阈值（列类型是固定的，同一列不可能按行变编码）。
- 某个键将来确实需要压缩时，走「两列共存」：加 `_z` 新列 → 读端优先取非 NULL 的新列 → 写端写新列并把旧列置 NULL → 确认清空后删旧列。全程无锁表，不需要 `ALTER TYPE`（后者会以 `ACCESS EXCLUSIVE` 重写整表）。
- 届时 zstd 用 `SpeedDefault`；`github.com/klauspost/compress` 已是直接依赖且已在 `utils/handler/uploader.go:34` 池化。`bytea` 内容需带帧头（magic + 版本 + 原始长度），读端据此判断编码，不靠假设，且必须有 round-trip 测试。

### 4.8 `public_api_allowed_keys` 合并为统一的 `allowed_keys`

**决定（2026-08-24）**：把 `public_api_allowed_keys` 改名并合并成**单个 `allowed_keys`**，对**所有非 private API 统一生效** —— 三个服务面共用一份配置。private API 不受此列表约束（它是内部面，只有本团队服务在用，见 §2.4 与 §9.1 #4）。

**mysekai 端点不加白名单**（同日决定，理由与前提见下）。因此这次合并对读路径的实际改动是「一份配置、三个面统一」，不是「多挡一类数据」。

#### 现状：一份白名单，三个面各自重建

| 服务面 | suite | mysekai |
|---|---|---|
| `internal/modules/public/public.go:169` | 传 `allowedKeySet` + `allowedKeys` | **不传** |
| `internal/modules/oauth2/gamedata.go:159` | 同上 | **不传** |
| `internal/modules/usergamebindings/game_account_data.go:106` | 同上（`buildPublicAPIAllowedKeySet`） | **不传** |

三处都是同一个形状：先 `buildPublicAPIAllowedKeySet`，然后在 `if dataType == suite` 分支里用掉，`else` 分支整个忽略。

根因在签名上 —— `HandleMysekaiRequest`（`utils/api/data/fetch.go:140`）**没有白名单参数**，而且：

```go
func buildMysekaiProjection(keys []string) bson.M {
    if len(keys) == 0 {
        return bson.M{"_id": 0, "server": 0}   // ← 排除式投影 = 整篇文档
    }
    ...
}
func InvalidMysekaiRequestKey(requestKey string) (string, bool) {
    // 只拒绝空白与含 . $ 的键，没有任何白名单校验
}
```

对照 `HandleSuiteRequest`：无 `?key=` 时 `keys = allowedKeys`，是**包含式**的。

**这一处包含式／排除式的差别，就是 2026-08-23 事故里未认证请求能取到整篇 9 MB 文档的通道** —— 泄露的是被污染进 mysekai 文档的 suite 数据与会话凭据，而不是 mysekai 自身的数据。下一节说明为什么在污染源已清理、且 mysekai 自身数据面无敏感内容的前提下，决定**不**用白名单去堵这一处，以及这个决定依赖什么。

#### mysekai 不过白名单（2026-08-24 决定）

**决定：`allowed_keys` 只约束 suite，mysekai 端点不加键白名单。**

理由是实测下来没有值得挡的东西。担心的那几个 `updatedResources` 子键在生产里是空的：

| 子键 | 有该键的文档 | 非空 |
|---|---:|---:|
| `userBillingRefunds` | 2,958 | **0** |
| `userUnprocessedOrders` | 1 | **0** |
| `userPresents` | 1 | **0** |
| `userMysekaiGamedata` | 3,715 | **0** |
| `userInformations` | 1 | 1（游戏公告文案，公开信息） |

唯一带这几个键的那篇文档，14 个顶层键全是合法 mysekai 键，本身是干净的。**mysekai 侧不存在付款金额或 PII 暴露**，本文早先那条「整块放行等于把退款挂上公开端点」的警告是**过头了**，已按实测改正。

而且直接套用白名单反而会**误伤**：生产那 35 条里**一条 mysekai 顶层键都没有**。mysekai 的顶层数据键只有 11 个 ——

```
updatedResources
isEnabled                                          isRefreshed
isExpireMysekaiSiteHousingPresetExtendSlot         mysekaiPhenomenaSchedules
isMysekaiGateRefreshed                             policy
mysekaiHousingCompetitionPendingNotifyReviewCount  userMysekaiGateCharacterVisit
mysekaiHousingCompetitionUnreadBanInfoList         newArrivalMysekaiHousingCompetitionBackNumberList
```

白名单里那些看着像 mysekai 的条目（`userMysekaiGates`、`userMysekaiMaterials`、`userMysekaiCharacterTalks`、`userMysekaiCanvases`、`userMysekaiFixtureGameCharacterPerformanceBonuses`）**是 suite 的顶层键**，在 mysekai 这边是 `updatedResources` 的子键。直接套用会让公开 mysekai 端点从「什么都给」翻到「什么都不给」。

##### 这个决定依赖什么，以及什么时候要重新评估

这条建立在「上传已有校验、污染不会再发生」之上。**需要如实记录的是：现有校验并不覆盖键集合。**

| 现有防线 | 挡住什么 | 挡不住什么 |
|---|---|---|
| `validateNoMongoOperatorKeys`（`data_ops.go:20`） | 字段名里的 `.` 与 `$`（Mongo 操作符注入） | 键**集合** —— 名字干净的任意键照样写入 |
| `orderedmsgpack.ValidateMaxDepth` | 嵌套深度（Go 栈溢出不可 recover） | 同上 |
| `ExtractUploadTypeAndUserID`（`handler_parse.go:12`） | — | 它对**客户端传的** `X-Original-Url` 做 `strings.Contains` 匹配，路由本身由客户端影响 |

加上 mysekai 的写语义是整份 `$set`（§2.3），**结构上「任意顶层键落进 mysekai 文档」这条路依然存在**；没有白名单时，任何一次污染的爆炸半径就是整篇文档 —— 这正是 2026-08-23 事故的形状。

当前判断是**风险可接受**：污染源已清理，实测无敏感内容，且 mysekai 的数据面本身就是玩法状态。**但下面任一条成立时必须重新评估：**

- mysekai 文档里开始出现带实际内容的 `userBillingRefunds` / `userUnprocessedOrders` / `userPresents`
- 上传路由或写语义变更（尤其是 `$set` 改成别的形状）
- 再次出现跨类型污染

作为替代的最小防线，建议至少加一条**上传期的键集合告警**（不拒绝，只记录）：mysekai 上传里出现不在已知 56 键注册表内的顶层键时打日志。注册表在 §4.6 已经有了，成本几乎为零，而且它会在污染**写入时**就报警，比事后靠白名单在读端兜底早一步。

#### 改造清单

| 位置 | 改动 |
|---|---|
| `config/types.go:178` | `PublicAPIAllowedKeys` → `AllowedKeys`，yaml `public_api_allowed_keys` → `allowed_keys` |
| `config/env.go:166` | `PUBLIC_API_ALLOWED_KEYS` → `ALLOWED_KEYS` |
| `internal/platform/runtimeconfig/service.go` | 快照字段与 JSON `publicApiAllowedKeys` → `allowedKeys` |
| `internal/modules/admin/config.go` / `route.go` | 管理 API 的读写字段跟着改 |
| `utils/api/helper.go` | `GetPublicAPIAllowedKeys()` → `GetAllowedKeys()` |
| `utils/api/data/fetch.go` | 只跟着改名，**mysekai 侧不动** —— `HandleMysekaiRequest` 不加白名单参数，`buildMysekaiProjection` 保持排除式 |
| `public.go` / `gamedata.go` / `game_account_data.go` | 三处 `buildPublicAPIAllowedKeySet` 跟着改名；`else` 分支保持原样 |
| `utils/api/data/stamp.go` | 只跟着改名，条件请求的 allowlist 参与项仍只覆盖 suite |
| `haruki-toolbox-configs.example.yaml:187` | 键名与示例内容 |

**兼容性**：`public_api_allowed_keys` / `PUBLIC_API_ALLOWED_KEYS` / 管理 API 的 `publicApiAllowedKeys` 要保留一个废弃期别名，读到旧键时告警并映射到新键，**不要直接改名了事** —— 运行期配置存在 Redis 里（`internal/platform/runtimeconfig/redis_store.go`），部署瞬间新旧代码会并存。

> 缓存键不受影响：suite 响应体由白名单塑形，缓存键挂 `":a=" + data.PublicAllowlistDigest(...)`（`gamedata.go:97`／`:179`），这套机制原样保留；mysekai 不过白名单，响应体不由它塑形，因此不需要挂 digest。**如果将来给 mysekai 加了白名单，务必同时补上这个 digest** —— 否则改白名单不会让已缓存的未过滤响应体失效。

## 5. 迁移 CLI

新增 `cmd/gamedata-migrate/`，风格对齐 `cmd/suite-rec-backfill/main.go`：**默认 dry-run，必须显式 `-apply`**。

因为是**一次切换 + 只读窗口**（§7.1），CLI 比双写方案简单得多：不需要 `delta`、`repair`、`rollback-check`，也不需要防覆盖守卫。

### 5.1 子命令

| 子命令 | 作用 |
|---|---|
| `estimate` | 只走元数据投影（`{_id:1, server:1, upload_time:1}`），统计文档数与体积分布，**不搬数据** |
| `migrate` | 全量搬迁，幂等可重跑 |
| `verify` | 抽 N 个用户，两边各自重建 API 响应后比对 |

### 5.2 关键设计点

**幂等**：只读窗口内没有并发写，源数据不变，因此 `migrate` 直接 `INSERT ... ON CONFLICT (user_id, server) DO UPDATE SET <全部列>` 即可，重跑等价。不需要进度表、不需要水位、不需要内容校验和。

> CLI **切换完成后删除**（#9），随 §7.1 第 11 步一起下线。因此它需要的任何辅助表都用**手写 DDL**，不要进 Ent schema —— 免得留下一张迁移结束后没人维护的生成表。

> 双写方案里那条极易漏写的 `WHERE game_suite.upload_time <= EXCLUDED.upload_time` 守卫，在一次切换下**不需要** —— 没有并发写就不存在用旧代际覆盖新代际。

**编码策略**（§4.7）：

1. 按注册表把顶层键分成 `known` / `denied` / 未知三类
2. `denied` 的五个键（§4.7.3）**直接丢弃**
3. `known` 里属于 compact 白名单（cn 的 6 个）的行式值 → 转 compact
4. 未知键 → 收进 `extra`，并**逐键计数上报**（切换后据此决定要不要提升为独立列）
5. 全部列写 `json`，无压缩

⚠️ **转 compact 时必须复用文档已有的字典。** 离线演练实测：游戏的 `__ENUM__` 是**客户端固定常量**，包含数据里从未出现的标签，且标签顺序写死。从行数据重建字典会得到更短、顺序不同的字典 —— 语义等价但字节不同。以存储字典为提示时结果是 `identical` / `same-modulo-key-order`；纯推导则是 `same-semantics-different-encoding`。

⚠️ **字典是 per-document 的。** 实测 `compactUserMissionStatuses.missionType` 在样本里有 2 种不同字典（17 标签 vs 16 标签），同一个整数索引在不同文档里含义不同。**绝不能跨文档共享或抽取全局字典表**，否则静默改变数据语义。

⚠️ **不能假设列内类型一致。** 实测一篇 tw 文档的 `costume3dShopItemId` 列里 int32 和 int64 混用（2,673 / 34,072）。

⚠️ **单篇文档的数据缺陷必须隔离，不能中断整批。** 全量实测：生产 `suite` 里有 **1 篇 `_id` 是 `null`**（bson 0x0a）而不是 game user id 的文档。第一版 CLI 对它直接 `return err`，而 `Each` 会把错误冒泡出去 —— **一篇脏数据能让整个只读窗口的迁移在第 N 篇上停摆**，且此时已写入的行与未写入的行混在一起。

> `_id` 是 int32 而非 int64 **不是缺陷**，`toInt64` 两种都收；mysekai 全量 4,124 篇零隔离，印证了这一点。真正落进隔离的只有「根本不是整数」这一类。

正确做法是把「本篇文档没法装载」和「基础设施故障」分成两类错误：

| 类别 | 例子 | 处置 |
|---|---|---|
| 单篇数据缺陷 | `_id` 不是整数、没有 `_id`、`server` 不是字符串、未知区服 | **隔离计数**，继续跑，结束时按原因分组打印并给出样例 `_id` |
| 基础设施故障 | PG 连接断开、upsert 失败、BSON 解析失败 | 立即中止 |

结束时必须显式打印 `QUARANTINED n documents (NOT loaded)`。**隔离数不是零就不能放行切换** —— 要么先在源库修掉，要么明确接受这几篇不迁移。

**三种写语义**（runtime 侧，不是 migrate 侧）：

- migrate：源是整份文档 → **全量覆盖**
- runtime `suite`：Mongo 的 `$set` 是顶层字段级 merge，旧上传里有、新上传里没有的键要保留 → `col = COALESCE(EXCLUDED.col, table.col)`
- runtime `mysekai`：整份 `$set` → 全量覆盖
- runtime `mysekai_birthday_party`：只写 `server`、`upload_time`、`r_user_mysekai_harvest_maps_j` 三列

⚠️ 这四种语义必须分别实现，**不能一条规则套用**。

**内存**：`jsonb_populate_record` 在 156 列 × 20 MB 上实测**把 PostgreSQL 后端 OOM 打崩**（signal 9）。因此服务端不做任何整记录展开，用静态生成的参数绑定 INSERT（约 210 个占位符，prepare 一次）；Go 侧逐文档处理，用 `semaphore.Weighted` 按 `-max-inflight-bytes`（默认 256 MiB）限流。

**超限文档**：生产 max 已 13.52 MB。Mongo **读**没有 16 MB 限制（只有写有），所以能读出来，但要单独标记报告。

**verify**：不比对序列化字节（§2.5：键顺序本就不确定，比字节必然假阳性）。两边都**规范化**后比较：递归排序键、保留数组顺序、数字用 `json.Number` 字面量。

⚠️ **int64 精度**：game user id（如 28808221489823746）超过 2^53。任何经过 `float64` 的路径都会损坏它 —— `encoding/json` 不开 `UseNumber` 解进 `map[string]any` 就会。必须有针对该 id 的贯穿测试。

> **已实现**：`cmd/gamedata-migrate`（§7.6）。下面的 flag 清单是设计稿，实际以 `gamedata-migrate <子命令> -h` 为准；差异是实现期收敛的结果，不是遗漏。

### 5.3 flag 清单

```
-config              配置文件路径（默认 HARUKI_CONFIG_PATH）
-apply               真正写入；缺省为 dry-run
-data-type           suite | mysekai | all
-server              限定区服
-user                逗号分隔的 user id
-limit               限制处理条数
-rate                每秒用户数，默认 5（实测单线程只有 11–15 篇/秒，达不到阈值；压窗口要用 -concurrency）
-concurrency         并发 worker 数，默认 4
-max-inflight-bytes  内存闸，默认 256 MiB
-progress            每 N 条打印进度，默认 100
-report-extra        输出未知键的逐键计数
-verify-compact      每篇 compact 化的值都还原并逐字节回比，默认 true
-v                   打印每个处理的用户
```

> 一次切换在只读窗口内进行，源库无写入，因此不需要 `-mongo-read-pref` —— 直接读主库最简单，也避免复制延迟带来的漏迁。

`-verify-compact` 把每个 compact 化的值用**生产展开器**还原一遍再逐字节回比，是「转换无损」的全部证据。

**实测代价比预想小得多**：全量 10,928 篇，开守卫 16m26s、关守卫 11m51s，**守卫占 4m35s（28%）**，两次跑的 compact 成功数（12,701）与最终体积（3,117 MB）逐字节一致。

> 早先估计「守卫让装载慢 4 倍」是**错的** —— 那是拿开守卫跑的瞬时速率去比开守卫跑的平均速率，而这条管线的速率在集合前段本来就低。进程 CPU 占用两次都在 50% 左右，说明瓶颈是**单线程管线**（Mongo 读 → 编码 → PG upsert 串行），不是守卫。

因此**默认开着，不要为省这 4 分钟关它**。`-verify-compact=false` 只在一种情况下有意义：已经在**同一份数据**上跑过带守卫的 dry run，且窗口时间极度紧张。关闭时 CLI 必须打印 `WARNING: -verify-compact=false — compacted values are NOT round-trip checked in this run`。

真正该优化的是管线本身 —— 18,844 MiB 的参数字节量单线程串行发送才是主项。要缩窗口应该上并发（`-concurrency`），不是关守卫。

### 5.4 运行顺序

```bash
# 窗口前：摸底（不写任何东西）
gamedata-migrate estimate -data-type all

# 窗口前：试跑
gamedata-migrate migrate -data-type all -limit 100

# ===== 只读维护窗口开始（停止接受上传）=====

gamedata-migrate migrate -data-type all -apply -concurrency 4 -report-extra
gamedata-migrate verify  -data-type all -limit 500

# 比对全过后：翻转读源到 PG，停止 Mongo 写

# ===== 只读窗口结束（恢复上传）=====
```

按生产规模（suite 10,928 + mysekai 4,124），**全量实测单线程装载 ≈ 17.5 分钟**（§7.5）；加上 verify、响应比对与人工确认，窗口按 **45–60 分钟**预留。

`-rate` 在这里没有意义 —— 实测单线程只有 11–15 篇/秒，达不到限速阈值。要压窗口用 `-concurrency`。

## 6. 读写路径改造

### 6.1 读

新增一个 **`?key=` → 列投影**映射层：请求键查编译期注册表 → 命中则进 SELECT 列清单；未命中则从 `extra` 取。

**响应组装必须是字节拼接，不是 marshal** —— §3.2 测到的 14.5–47× 收益完全来自"不重新编码"，一旦中途 `Unmarshal`/`Marshal` 就全部还回去了。

必须逐条保持的行为：

| 行为 | 实现要点 |
|---|---|
| suite 无 key → 白名单键的对象 | 按白名单顺序拼接（顺序本身不是契约，但保持代价为零） |
| suite 单 key → **裸值** | 直接输出该列字节 |
| suite 多 key → 对象 | 拼接 |
| **mysekai 单 key 仍返回对象** | 与 suite 不一致，是既有行为，不要"顺手统一" |
| 缺失键 → `[]` | 列为 NULL 时输出 `[]` |
| **公开面 404 语义** | `buildSuiteProjection` 排除 `_id`，所以所有请求键都缺失时投影为空 → 404。PG 里行是存在的，朴素移植会返回 200 + 一堆 `[]`。必须显式：公开面在所有请求列均为 NULL 时 404 |
| 私有面无 key → 含 `_id` 与 `server` | 与公开面相反，必须保留 |
| 私有面点路径 → `null` | 保持现状（§2.5），**不要顺手修成可用** |
| `userGamedata` 7 字段列白名单 | 该列仅约 200 字节，Go 侧过滤即可 |
| compact 键展开 | 读端按**存储值的形状**分派：`{` 开头＝compact，走生产展开器；`[` 开头＝行式，原样输出。见 §4.7.1 |
| **私有面的 compact 展开** | **切换后自动修复，这不是可选项** —— 见下 |

⚠️ **私有面的 compact 展开缺陷会被切换顺手修掉，而且拦不住。**

`buildPrivateDataResponse`（`internal/modules/userprivateapi/response.go:15-27`）拿到整篇文档直接 `NormalizeProviderResponse`，**从不展开 compact**。所以今天一个 cn／tw 账号：

| 请求 | 今天（Mongo） |
|---|---|
| 私有面无 `?key=` | 响应里是原样的 `compactUserMusicResults`，**没有** `userMusicResults` |
| `?key=userMusicResults` | 平铺 `bsonDGet` 扫不到该键 → **`null`** |

新架构里 compact 值存在**行键的列**上，读端出列时就按形状展开了 —— **这个缺陷根本没法活过切换，除非刻意再把它弄坏。**

离线比对实测到的差异形态（`private.suite.nokey`，一篇 tw 文档）：

```
missing_on_pg    /compactUserCharacterMissionV2Statuses   ← Mongo 有，PG 没有
missing_on_mongo /userCharacterMissionV2Statuses          ← PG 有展开后的行式，Mongo 没有
```

**按 §9.1 #4 的决定（私有 API 只有本团队服务在用，无外部调用方），这是有意变更，随切换一起走。** 本文早先「私有面今天不展开，是缺陷，单独排期修」的写法与此矛盾，已作废 —— 修它是免费的，保持现状反而要额外写代码。

> 这与「私有面点路径返回 `null`」是**两回事**：后者的成因是 `bsonDGet` 平铺扫描（§2.5），与 compact 无关，**仍然保持现状**。

⚠️ **第四个服务面**：`internal/modules/usergamebindings/game_account_data.go:97/:106` 也调用 `HandleSuiteRequest` / `HandleMysekaiRequest` 并 `c.JSON(resp)`。改造清单里容易漏掉它。

⚠️ **重复请求键**：`?key=userCards,userCards` 今天被接受并产生重复 JSON 键。移植时要么保持，要么显式去重并记录为行为变更。

### 6.2 写

**分派表在 `init` 时构建一次**（`map[string]columnSetter`），不做每请求反射：已知键 → 对应列（大键先 zstd），未知键 → 攒进 `extra`。

**三个历史合并键**（`userEvents` / `userWorldBlooms` / `userGachas`）：在**一个 PG 事务内**做 read-modify-write，复用现有的 `mergeUserEvents` 等函数，语义不变。

⚠️ **合并输入的类型会变**：BSON 给的是 `int64`，从 JSON 列反序列化默认给 `float64`，超过 2^53 会静默损坏 `shouldReplaceX` 的判断。解码这三列必须配置 `UseInt64`，并用 JSON 来源的输入**重跑** `manager_test.go` 里现有的 15 个合并测试。

**生日会的部分写入**从点路径 `$set` 变成单列 UPDATE：

```sql
UPDATE game_mysekai SET r_user_mysekai_harvest_maps_j = $1, upload_time = $2
WHERE user_id = $3 AND server = $4;
```

**保留全部上传校验**：`validateNoMongoOperatorKeys`（改名为 `validateUploadFieldNames`）继续拒绝 `.` / `$`，并新增 NUL 与非 UTF-8 拒绝；`orderedmsgpack.ValidateMaxDepth` 不动。

⚠️ **16 MB 曾经是攻击者可控增长的唯一上界**。PG 单字段 1 GB、行宽几乎无限，而 Fiber 的 `BodyLimit` 是 100 MB。必须显式补上：单键大小上限、`extra` 成员数与字节数上限、整行大小上限。**这是移除 Mongo 时最容易漏掉的安全回归。**

### 6.3 缓存

⚠️ **`ConfirmGameDataCacheWrite`（`utils/api/data/stamp.go:113`）在 `DBManager.Mongo == nil` 时直接返回 false**，也就是说 Mongo 一摘掉，**所有响应缓存会静默失效**。必须与读切换在同一个提交里改造。

单行原子性让 Mongo 时代的撕裂读问题消失，写栅栏可以简化，但要注意现有栅栏是在 body 物化**之后**再读一次，能捕捉物化期间发生的新上传；改成复用行读结果会丢掉这个窗口。

## 7. 灰度与回滚

### 7.0 迁移前先做的三件事（与迁移方案无关）

这三项都在 2026-08-24 拍板（§9.1 #11／#12／#18），与目标结构无关，**应该现在就做，不必等切换**。第 ③ 项已完成。

**① `data_ops.go:76` 加超限写入错误处理。** `UpdateOne` 完全没有识别 16 MB 超限写入错误，会冒泡成 500，并且该账号**此后每次上传都失败**。生产 max 已 13.52 MB。

**② 把 `public_api_allowed_keys` 合并成统一的 `allowed_keys`（#11 + #12）。** 完整设计见 **§4.8**。要点：

- 一份配置，三个非 private 服务面统一使用；private API 不受约束。
- **mysekai 不加白名单**（实测无付款/PII 内容，且直接套用会让公开 mysekai 端点全部返回空 —— 生产那 35 条里一条 mysekai 顶层键都没有）。
- 顺带删掉误写的 `tw` `cn` `kr` 三条（#12）：它们是区服名不是数据键名。
- 建议同时加一条**上传期键集合告警**（不拒绝、只记日志）：mysekai 上传出现不在 56 键注册表内的顶层键时报警。它在污染**写入时**就报，比读端白名单早一步（§4.8）。

**③ ~~删除 `_id` 为 `null` 的那 1 篇脏文档（#18）~~ —— 已于 2026-08-24 完成。** 生产实测确认它 `_id: null`（bson 0x0a）、30 个键里没有 `userGamedata` 也没有任何玩家进度，装的是 `policy`／`version`／`appVersion`／`assetVersion`／`cdnVersion`／`dataVersion`／`suiteMasterSplitPath`／`configs`（`server: tw`，2025-09-24，属上传校验落地前的遗留污染）。删除前已单篇备份至 Toolbox 的 `/data/cleanup-2026-08-24/suite-null-id-doc.json`（chmod 600）。`deletedCount = 1`，suite 10,960 → 10,959，`suite` 与 `mysekai` 的非数字 `_id` 数量均已归零 —— §7.1 第 5 步的放行条件（隔离数为零）现在成立。

### 7.1 阶段（一次切换）

**方案是一次性切到新结构，不做双写。** 生产规模只有 10,928 篇 suite + 4,124 篇 mysekai（§3.0），只读维护窗口完全可行 —— 这比双写简单一个数量级：不需要差异检测、修复队列、双写语义、读源灰度开关。

**窗口预算按全量实测排**（§7.5，开发笔记本单线程，只看量级）：

| 项 | 实测 |
|---|---:|
| `suite` 全量装载（带 round-trip 守卫） | 16m26s |
| `mysekai` 全量装载（带守卫） | 56.7s |
| **装载合计** | **≈ 17.5 min** |

加上 verify 抽样、响应比对、翻转读源与人工确认，**窗口按 45–60 min 申请**，不要按早先"约 6 分钟"的估计排。要压缩窗口应该给 CLI 上并发（瓶颈是 18.8 GiB 参数字节单线程串行发送），**不是关掉守卫** —— 守卫只占 28%（§5.3）。

| 阶段 | 动作 | 放行条件 |
|---|---|---|
| 0 | §7.0（合并 `allowed_keys` ✅ 已实现；删 `_id: null` 脏文档 ✅ 已执行；超限写入错误处理 —— **按 2026-08-25 决定不做**，旧有缺陷不补强） | 第 5 步的放行条件已成立 |
| 1 | ~~修私有 API 的 compact 展开缺陷~~ **不再是独立步骤** | 新读路径出列时就展开，这个缺陷**会随第 3 步自动消失**（§6.1）。唯一可选项：要不要**先在 Mongo 侧单独修一次**，让这个对外变更与切换解耦、便于归因。不做也可以（#4：无外部调用方） |
| 2 | **离线演练**（§7.5） | ✅ **已完成（2026-08-24）**：compact round-trip 正反向、全量装载 ×4 轮、响应等价全量 269,032 次 mismatch 0、M1–M6 全过 |
| 3 | 建表 + 部署新读写路径（`read_source` 仍为 `mongo`） | ✅ **代码已就绪**（§7.6）。部署后行为不变，PG 表为空 |
| 4 | **只读维护窗口开始** | 停止接受上传 |
| 5 | `gamedata-migrate migrate -apply` 全量回填 | 隔离数为 0，且 `gamedata-migrate verify` 的 differing／missing 均为 0 |
| 6 | 翻转 `read_source=postgres` + **同步清空 game-data Redis 命名空间** + 停止 Mongo 写 | 抽查响应通过。⚠️ 不清缓存＝用一个库的 stamp 回答另一个库的 body |
| 7 | **只读窗口结束**，恢复上传 | 写入 PG 正常 |
| 8 | 观察 ≥ 7 天（一个 `GameDataCacheTTL`） | 零异常 |
| 9 | **改为：剥离只作用于 Mongo 写入路径**，PG 存全量（见 §7.1.1）。放开 `userProfileHonors` #6 与 `userMaterials` #13 是**再往后**的独立一步 | ← 见 §7.1.1。响应不变，因此不再是不可逆点 |
| 10 | **清理旧数据**：删除 MongoDB 的 suite / mysekai | 第 8 步观察期通过 |
| 11 | 下线 MongoDB 容器与依赖；**删除 `cmd/gamedata-migrate`**（#9） | — |

**旧数据的清理时机由维护者定为第 10 步** —— 即「确认新架构正常运行之后」，而不是切换当时。在第 6 步到第 10 步之间，MongoDB 保持**只读、不再写入**，作为随时可回退的完整快照。这段时间是整个方案的回滚窗口。

⚠️ **上面这两条警告已被 §7.1.1 取代。** 它们成立的前提是「清空 `suite_remove_keys`」这个动作对两个库同时生效；改成只在 Mongo 写入路径剥离之后，Mongo 永远不会被推过上限，第 9 步也就不再是不可逆点。保留原文于此以说明结论为何改变：

> ~~不可逆点是第 9 步（清空 `suite_remove_keys`），不是第 10/11 步。一旦停止置空，数据量恢复到会让 MongoDB p99 爆表的水平（§1.1），此时即使 Mongo 数据还在也回不去了。~~
> ~~反过来，第 9 步提前同样致命：只要 Mongo 还在被写入，清空 `suite_remove_keys` 会立刻把文档推到 16 MB 上限之上。~~

#### 7.1.1 修订：剥离改为只作用于 Mongo 写入路径（2026-08-29）

原设计里「停止丢弃这 20 个键」是一个**全局开关**，两个库同时生效，因此必须等到 Mongo 不再
被写入之后才能动 —— 这就是第 9 步之所以是单向门的全部原因。

§1.1.1 的实测把这个前提坐实了：一个真实的重度 jp 账号，不剥离时 BSON **16.16 MB，已经超过
上限**。所以「等 Mongo 停写后再全局清空」这条路，对 Mongo 而言从来就不是「风险」，而是
**确定失败**。

**修订后的做法**：剥离下沉到 Mongo 的写入分支，PG 拿完整数据。

| | Mongo | PostgreSQL |
|---|---:|---:|
| 完整数据 | **16.16 MB → 写不进** | 1.36 MB ✅ |
| 剥离后 | 4.73 MB ✅ | 0.45 MB |

由此带来四个变化：

1. **Mongo 侧的剥离不是「临时」的，要一直保留到 Mongo 下线（第 11 步）为止。** 它从「维护者
   不想要的数据丢弃」变成了**防止超限的硬性保护**。
2. **PG 从此刻起开始累积全量数据**，不必等窗口、不必等观察期。数据只能向前积累 —— 已上传
   的部分从未被存储过、无法回填，所以**越早启用，放开时已经拿到完整数据的账号越多**。
3. **响应不变，因此翻转读源没有额外负担。** 20 个剥离键与 `public_api_allowed_keys`（32 个）
   的交集只有 `userProfileHonors` 一个（`userMaterials` 在配置里已被注释、当前并未剥离），
   把它留在双边剥离名单里，翻转前后公开响应字节一致。
   ⚠️ 但 `/api/private/*` 不走这个白名单，会开始返回这 19 个键。
4. **回滚不再是单向的。** 任何时刻把读源翻回 Mongo，服务的仍是它一直在存的剥离版数据 ——
   回滚后功能降级（那些键回到 `[]`），但不会失败，也没有数据损失。

**容量影响**：需要新增存储的只有当前真正被剥离的 3,956 行（jp 3,859 + en 97）。按 §1.1.1
的重度账号上限 (1.36 − 0.45) MB 计，约 **+3.6 GB**，实际平均更低。

⚠️ **前置条件：toolbox 的 PostgreSQL 数据目录当前 bind 在根盘**
（`/data/HarukiService/toolbox-postgres-data`，49 G 盘只剩 11 G），而同机的 `/data2`
（98 G）只用了 5.9 G。**迁盘要排在启用之前。**

⚠️ **同时必须修 §1.1.2 的 compact 拼写洞。** 剥离一旦成为防爆手段，「cn／tw／kr 的三个大键
从未被剥离」就不再是无害的历史遗留：这三个区现在靠 compact 形态本身的小体积侥幸没撞上限，
一旦客户端改发行形态就会静默失败。剥离必须同时匹配 `catalog.CompactPairs` 的 compact 键名。

⚠️ **`cleanSuite` 是原地改写**（`suite[key] = []any{}`），直接复用会把传给 PG 的那份也清空。
Mongo 分支必须拿浅拷贝。

一次切换相比双写砍掉的内容：§7.3 的双写语义与差异检测、修复队列、`game_data_read_source` 的灰度开关、以及迁移 CLI 的 `delta` / `repair` 子命令与 `WHERE upload_time <= EXCLUDED.upload_time` 守卫（只读窗口内不存在并发写，不会用旧代际覆盖新数据）。

### 7.2 开关

放在已有的 Redis 支撑的 runtime config（`internal/platform/runtimeconfig/`，管理端 `internal/modules/admin/config.go`）：

- `game_data_write_mode`：`mongo` / `dual` / `postgres_shadow` / `postgres`
- `game_data_read_source`：`mongo` / `postgres`，可按 data_type 分别设置

⚠️ 早期阶段"缺省值回落到 mongo"是安全的，**但第 7 步之后就反了** —— 那时缺省回落到 mongo 意味着读一个已经停写的库。开关的缺省语义必须随阶段调整，或者干脆在第 7 步后移除 mongo 分支。

### 7.3 只读窗口期间的保障（原「双写与差异」已作废）

一次切换不做双写，因此原本为双写设计的差异检测、修复队列、`game_data_read_source` 灰度开关**全部不需要**。取而代之的保障是：

- **窗口内源库无写入** —— 上传入口停掉，Mongo 数据在整个窗口内不变，`migrate` 可以任意重跑
- **`verify` 是放行闸** —— 抽样重建两边的 API 响应并规范化比对，不过则不翻转读源
- **Mongo 保持只读直到第 10 步** —— §7.1 第 6～10 步之间 MongoDB 不再被写入但数据完整保留，是随时可用的回滚快照
- **回滚 = 把读源翻回 Mongo** —— 按 §7.1.1 修订后，回滚在任何时刻都只是一个配置项：Mongo 一直在存剥离版数据，翻回去功能降级（19 个键回到 `[]`）但不会失败

### 7.3.1 写路径已接入（2026-08-26）

§6.2 描述的 PG 写语义此前只存在于 `utils/database/gamedata/writer.go`：`WriteSuite` /
`WriteMysekai` / `WriteBirthdayParty` 三种模式都实现了，但**全仓库没有任何调用方** ——
`Store.Write` 只被迁移 CLI 用（`WriteMigrate`），而 `DBManager.GameData` 在生产代码里
只出现在 `ReadsFromPostgres()` 判断里。也就是说这个库当时只能读、不能写。

后果是：如果直接翻 `read_source` 到 postgres，**窗口结束后的每一笔上传都会对读取不可见**
—— 上传返回 200、审计日志记 success，读取却拿着一行冻结的数据。这不是数据陈旧，是功能性失效。

`utils/handler/gamedata_write.go` 把三种模式接进了 `PersistUploadData`（三种上传类型
唯一的写入点）。

**与本方案原设计的一处偏差**：§7.3 废弃的是「灰度期双写 + 差异检测 + 修复队列」那一整套，
而 §5.4 第 4 步要求切换后「停止 Mongo 写」。当前实现是**两边都写**（Mongo 先、PG 后）：

- 好处：Mongo 始终保持最新，§7.1 第 6～10 步之间的回滚快照永远是当前的，回滚窗口不再有
  时间边界
- 代价：Mongo 继续增长。这与 §1.1 的 16 MB 上限无关（21 键不在 Mongo 侧恢复），但等
  第 10 步之后应该用一个开关把 Mongo 写关掉

三个刻意的选择：

- **同步写**。异步镜像会让客户端上传后立刻读到自己的旧行 —— 那正是切换后绝不能有的窗口。
  代价是给上传加了延迟，因此设 20 s 上限，并用 `context.WithoutCancel` 脱离请求 deadline
  （大文档解码后调用方的 deadline 可能只剩几秒，半途放弃的镜像比给它独立预算更糟）
- **永不让上传失败**。走到这一步 Mongo 已经落库；把 game-data 故障变成用户可见的上传失败，
  是拿一个可恢复的分歧换一个不可恢复的。分歧交给 `verify` 发现
- **绝不选 `WriteMigrate`**。那个模式整行替换，而上传是部分文档，用错会抹掉上传未携带的
  所有键。有测试锁死这一点

§6.2 标为「移除 Mongo 时最容易漏掉的安全回归」的显式上界已确认到位：`DefaultLimits()`
给出 `MaxKeyBytes` 64 MiB / `MaxRowBytes` 192 MiB / `MaxExtraKeys` 128 /
`MaxExtraBytes` 32 MiB。三个历史合并列的解码也确认用 `UseNumber` 而非默认 `float64`。

### 7.4 生产普查结果与放行判定

M1–M4 已于 2026-08-23 在生产主库完成（数据见 §3.0）。M5–M6 已于 2026-08-24 在全量装载后的本地 PG 上完成（§7.5）。

| 编号 | 测量 | 阈值 | 实测 | 判定 |
|---|---|---|---|---|
| M1 | `$bsonSize` 分布 | 恢复全键后 max < 14 MB | p99 → 22.62 MB，max → 23.95 MB | 🔴 **不通过** |
| M2 | suite 顶层键并集 | > 400 重评估，> 1400 作废 | 抽样 400 篇 **205**；**全量扫描 251** | ✅ 通过 |
| M3 | 文档总数 | 定阶段时长 | suite 10,928 / mysekai 4,124 | ✅ 规模很小 |
| M4 | mysekai `updatedResources` 一级键 | 定列清单 | **46** | ✅ 通过 |
| M5 | 单行 upsert p99（生产最大行） | < 200 ms | **p99 7 ms**（p50 1.47／p90 2.70，n=101,036，16 worker × 12 s，打表中最大 40 行）—— 用**上线代码**对**上线 CLI 装载的表**测（§7.6） | ✅ 通过 |
| M6 | PG 连接池 `waitCount`（2× 峰值） | 保持 0（预热基线之上） | **0**（16 worker、`MaxConns` 20、0 错误）。⚠️ 基线是 **1** 而不是 0：`NewPool` 先 Ping 再预热，那次 Ping 在池还空时取连接。**告警要按增量,不能按 > 0**，否则每次重启都误报 | ✅ 通过 |

**M1 不通过是本方案存在的理由，不是阻断项** —— 它恰好证明"留在 MongoDB 上恢复 21 键"不可行（见 §1.1，并由 §1.1.1 的实测样本坐实：真实账号不剥离时 BSON 16.16 MB，已超上限）。**按 §7.1.1 修订后它不再约束顺序**：剥离只作用于 Mongo 写入路径，Mongo 永远拿剥离版、永远不会被推过上限，因此 PG 存全量这件事可以在双写期间就开始，不必等写源切换。

**M3 的规模远比预想小**：suite 只有 10,928 篇、磁盘 3.30 GB。**全量实测装载 16m26s（suite）+ 56.7s（mysekai）= 约 17.5 min**，窗口按 45–60 min 申请即可（§7.1）。

> 早先按 `-rate 30` 估的「约 6 分钟」偏乐观：那是按限速倒算的，没有计入编码与 upsert 的真实代价。实测单线程 90 ms／篇（带守卫）、65 ms／篇（不带），`-rate 30` 根本达不到 —— **限速不是瓶颈，管线才是**。

复用的查询：

```js
// M1
db.suite.aggregate([{$sample:{size:200}},{$project:{sz:{$bsonSize:"$$ROOT"}}}])
// M2
db.suite.aggregate([{$sample:{size:400}},{$project:{k:{$objectToArray:"$$ROOT"}}},
  {$unwind:"$k"},{$group:{_id:"$k.k"}},{$count:"n"}])
// M4
db.mysekai.aggregate([{$sample:{size:400}},{$project:{k:{$objectToArray:"$updatedResources"}}},
  {$unwind:"$k"},{$group:{_id:"$k.k"}},{$count:"n"}])
```

> ⚠️ 不要在生产主库上跑不带 `$sample` 的全量 `$bsonSize` 聚合 —— 那是全集合扫描，会把 WiredTiger cache（3 GB）冲掉。抽样 200–400 篇即可，实测耗时 3.7 s。

### 7.5 离线演练结果

演练分两轮。**第一轮（2026-08-23）** 用 262 篇分层抽样的真实生产 suite 文档与全部 4,124 篇 mysekai 文档验证正确性；**第二轮（2026-08-24）** 用**生产 `suite` 集合的全量副本**（10,928 篇，本地 mongodump 还原后逻辑 30.82 GB／磁盘 3.21 GB，与生产的 3.30 GB 吻合）跑完整装载。演练代码调用的是**仓库里真实的展开路径** `data.GetValueFromResult`，不是复刻实现；`schemagen/compactrestore/vendor_sync_test.go` 断言 vendored 副本与 `utils/compactrestore/compactrestore.go` 逐字节相同，该断言通过。

#### 正确性（第一轮，262 篇 + 4,124 篇）

| 验证项 | 结果 |
|---|---|
| **compact round-trip（正向）** | jp／en 行式 → Compact → 生产展开器 → 行。5 个键在**规范化比较下 100% 通过**（137/137、14/14、135/135、136/136…，约 300 万行）；`userCostume3dStatuses` 在 14/14 篇上**正确拒绝**（键集合参差） |
| **compact round-trip（反向）** | 689 篇 cn／tw／kr 存储的 compact 值（约 690 万行）展开后重编码，**全部服务出与原始一致的行** |
| **响应等价（第一轮）** | 35 种响应形状 × 4,385 篇文档 = 47,256 次比对，mismatch = 0，errored = 0。⚠️ **这一轮跑的是 `internal/pgstore` 布局**（bytea+zstd blob 列），**不是最终 schema** —— 见下方「响应等价（最终 schema）」 |

> ⚠️ 第一轮的响应等价是在**被否决的探索布局**上跑的。`internal/pgstore` 按公开白名单把大键切到 bytea+zstd，而最终 schema 是 246 列全 `json` 的宽表（§3.5、§4.7.2 否决了 zstd）。**对前者的通过结论不能顺移到后者。** 这个缺口在 2026-08-24 被发现并补上：新增 `internal/schemastore` 直接读 `schemagen` 装出来的宽表，`respcompare -layout schemagen` 成为默认。

> ⚠️ 上表第一行早先写作"全部精确"，**不准确**。round-trip 报告有 EXACT 与 CANONICAL 两列：`userCharacterMissionV2Statuses` 是 EXACT 14 / **CANONICAL 137**，`userMusicResults` 是 EXACT 14 / CANONICAL 136。差额 123／122 篇全部落在 **DRIFT（元素间键顺序不同）**。这不是缺陷 —— 键顺序今天本就不保留（§2.5）—— 但它是下一节那个 401 MB 问题的**早期信号**，当时没被读出来。

响应等价覆盖的形状包括：公开 suite 的无 key／10 种单键（含 `userGamedata`、缺失键）／多键／**重复键**／非法键，私有 suite 的无 key／单键／多键／**点路径**／含空格／空元素，公开与私有 mysekai 的无 key／单键／`?key=_id`／`?key=server`／多键／点路径。

两个边界行为也逐一确认两侧一致：`suite.public.badkey` 与 `mysekai.public.dotted` **在两侧以相同的 400 拒绝**；私有面点路径**两侧都返回 `null`**（印证 §2.5）；重复请求键**两侧都产生重复 JSON 键**。

演练同时包含**负向对照**：8 种伪造的数据损坏（int64→float64、整数宽度变化、数组重排、丢行、错误 enum 索引、enum 列置空、padding 补 null、字段重排）必须全部被检出。这保证「全过」不是比对器空转。

#### 响应等价（最终 schema，全量，2026-08-24）

第一轮的 47,256 次比对跑在被否决的 `pgstore` 布局上（§7.5 上表的警告）。这一轮换成 `internal/schemastore` —— **直接读 `schemagen` 装出来的宽表**，compact 列出列时走 vendored 生产展开器，扁平子键重新拼回 `updatedResources`。`respcompare -layout schemagen` 现在是默认。

**全量语料**（10,928 篇 suite + 4,123 篇可寻址 mysekai）：

| | |
|---|---:|
| **比对次数** | **269,032** |
| **mismatch** | **0** |
| errored | 0 |
| 有意差异：denied 丢弃 | 10,716 篇 |
| 有意差异：compact 展开修复 | 19,394 篇次 |
| 有意差异：去重文档数 | 23,542 |
| 逐 shape 自查 `match+mismatch+errored+intended == docs` | **全部通过** |

35 种形状全过，含两侧以相同 400 拒绝的 `suite.public.badkey` 与 `mysekai.public.dotted`。有差异的只有三个形状，全部是私有面：

| 形状 | docs | match | denied | compact | intended |
|---|---:|---:|---:|---:|---:|
| `private.suite.nokey` | 10,928 | 210 | 10,716 | 6,570 | 10,718 |
| `private.suite.single.userMusicResults` | 10,928 | 4,516 | 0 | 6,412 | 6,412 |
| `private.suite.multi` | 10,928 | 4,516 | 0 | 6,412 | 6,412 |

**公开面 15 种形状、mysekai 全部 11 种形状：逐篇全等，零差异。** 对外可见的变化只落在私有面，而私有面没有外部调用方（§9.1 #4）。

##### 两个独立交叉验证

这两个数字来自**完全不同的代码路径**，事先没有对齐过：

| 来源 | 测量 | 数字 |
|---|---|---:|
| 装载期（`pgwriter`，**写**侧） | 丢弃了多少篇的 `userRegistration` | **10,716** |
| 比对期（`respcompare`，**读**侧） | 多少篇的私有响应因 denied 键而不同 | **10,716** |

同理，`private.suite.nokey` 的 compact 差异 **6,570 篇**，与直接在表上数出来的「`user_costume3d_statuses_j` 存 compact 对象的行数 **6,570**」（§4.7.1）一致。

写侧说"我丢了这么多"，读侧独立地说"我看到这么多响应因此改变" —— 两边吻合，才说明丢弃是完整的、且爆炸半径恰好是声称的那些键。

##### 「有意差异」怎么判，才不至于变成免罪符

两类差异是**设计要的**（§4.7.3 丢弃 denied、§9.1 #4 修掉私有面 compact 缺陷），但**绝不折进 `match`**。判定规则：

1. **逐条 diff 分类**，每一条都必须能解释成 denied 或 compact，**有一条解释不了整篇退回 mismatch**。嵌套路径、值变化、方向反了的缺失，一律不接受。
2. **无上限重跑一次比对**再确认 —— 否则 `-max-diffs` 截断之后，真正的回归可能藏在有意差异后面。
3. **`match + mismatch + errored + intended == docs`**，逐 shape 自查，不成立就把数字打出来并让整轮判定为失败。

第 1 条是被数据逼出来的：最初写成「整篇必须全属同一类」，结果一篇 tw 文档同时带着 6 个 compact 键和一个 `userRegistration`，两个分类器都不收，被误报成 blocker。而 **cn／tw 账号必然是这种混合形态**。

第 3 条也是被 bug 逼出来的：汇总时漏加了一个计数器，`compact-expansion fix` 显示成 0，读起来像「这个改动没有副作用」—— 恰好是最不该出错的方向。两类计数**互相重叠**（同一篇文档可同时计入），所以只有 `intended` 这个唯一文档数能拿来做恒等式自查。

> 另有一处 harness 口径错误也在这一轮暴露：`-allowlist union` 把全部 237 个键当公开白名单，其中包含 5 个 denied 键，于是公开面 288/400 报差异。**生产的 35 条白名单里一个 denied 键都没有，而且 shipping 系统里 denied 键的列根本不存在**，运行期白名单怎么填都只会返回 `[]`。已修正为排除 denied 键（232 个）。

#### 全量装载（第二轮，10,928 篇）

同一份数据跑了三次，差异只在编码器与守卫：

| 跑次 | 键顺序不同的元素 | round-trip 守卫 | `denied` | 装载耗时 | compact 成功 | **最终体积** |
|---|---|---|---|---:|---:|---:|
| v1 | 判为参差、拒绝 | 开 | 未实现 | 11m30s | 约 1,513 / 24,888 | 3,518 MB |
| v2 | 规范化后转换 | 开 | 未实现 | 16m26s | 12,701 / 24,888 | 3,117 MB |
| v3 | 规范化后转换 | 关 | 未实现 | 11m51s | 12,701 / 24,888 | 3,117 MB |
| **v4** | **规范化后转换** | **开** | **已实现** | **15m10s** | **12,701 / 24,888** | **3,113 MB** |

*(对照：现在的 MongoDB 3,297 MB)*

`mysekai` 同样跑了全量（4,124 篇，带守卫）：**56.7 s**，零隔离，零未知键，1 篇没有 `server` 字段被停进 `ServerUnknown`。

| 集合 | MongoDB | PostgreSQL | 差 |
|---|---:|---:|---:|
| `suite` | 3,297 MB | **3,113 MB** | **−5.6%** |
| `mysekai` | 205 MB | **266 MB** | **+30%** |
| **合计** | **3,502 MB** | **3,379 MB** | **−3.5%** |

⚠️ **合起来只小 3.5%，基本是持平，不是净省。** `mysekai` 反向的原因是它**一个 compact 键都没有**（注册表 53 个 json 列、0 个 compact 列），而 suite 的优势几乎全部来自 compact 化；剩下的就是 pglz 打不过 WiredTiger 的 zstd（PG 1,282 MiB 参数字节 → 266 MB 落盘 = 4.8×，Mongo 侧是 7.4×）。

**这不改变决策** —— §1 已经说清楚驱动这次改造的不是存储成本，而是 16 MB 上限。但"迁过去还能省空间"这个副作用**只在 suite 上成立，合起来是持平**，对外说明时不要夸大。

**v1 → v2 省下的 401 MB 全部来自一个判定**（§4.7.1「元素间键顺序不同不是参差」）。修之前 PG 比 MongoDB 大 6.7%，修之后小 5.5%。

**v2 与 v3 的产物完全一致**（compact 成功数 12,701 相同，表体积 3,268,214,784 vs 3,268,206,592 字节，差 8 KB 是页内空闲空间取整）—— 这本身就是守卫「只验证、不改变输出」的一次对照验证。守卫的代价是 **4m35s（28%）**，不是早先估计的 4 倍。

compact 化在全量上的实际效果：**5 个键的行式值 6,307.95 MiB → 1,459.39 MiB（23.1%，4.32×）**。12,701 次成功转换里 **11,188 次（88%）依赖键顺序规范化** —— 也就是说不修那个判定，compact 化基本等于没做。

拒绝的分布全部符合设计：

| 原因 | 次数 | 说明 |
|---|---:|---|
| `empty array` | 11,947 | jp 侧被 `cleanSuite` 置空的键，没有可转换内容 |
| `ragged: key set differs` | 240 | **全部是 `userCostume3dStatuses`**，正是 §4.7.1 决定不转的那个键 |

#### 其余全量装载发现

| 发现 | 数字 | 处置 |
|---|---|---|
| **隔离文档** | 1 篇，`_id` 不是数字（生产实际是 **`null`**，bson 0x0a）而不是 game user id | 已实现隔离机制（§5.2）：计数、打印、继续。**放行前必须先在源库处理这一篇** |
| **顶层键并集** | 全量 **251** 个（400 篇抽样只看到 205，**低估 22%**）；扣掉 5 个 denied 键后**注册表 246 列** | 表 250 列（246 + 3 元数据 + `extra`），远低于 PG 的 1600 上限 |
| **`denied` 丢弃**（v4） | **27,300 次**：`userRegistration` 10,716 篇，`userInherit`／`userPlatformInheritIos`／`userPlatformInheritAndroid`／`userPlatforms` 各 4,146 篇 | 建表后核对：250 列里 **denied 列 = 0**；全表扫描 `extra`，含 denied 键名的行 = **0** |
| **未知键落进 `extra`** | **0 个** | 注册表完整覆盖生产数据 |
| **同时带行式与 compact 两种形态** | 12 篇 | 行形态胜出进列、compact 形态存进 `extra`，与生产 `GetValueFromResult` 的优先级一致，无损。**不需要为此加第二列** |
| **单行全列 upsert 延迟** | 开守卫 p50 22.5／p90 37.5／**p99 152.4** ms／max 1.345 s；关守卫 p50 21.4／p90 31.8／**p99 97.5** ms／max 383 ms（n=10,928） | M5 阈值 200 ms，**两种口径都通过** |
| **参数字节量** | 18,844 MiB | 决定装载耗时的主要项；要压窗口应针对它上并发，而不是关守卫 |
| **pgx 池惰性建连** | 不预热时 `EmptyAcquireCount` 恰好等于 worker 数（conc 4／8／16 → 4／8／16） | 看起来像池打满，其实是建池。必须设 `MinConns` 预热；监控告警也要排除预热期（§4.3） |

> ⚠️ 耗时数字是在**开发笔记本的 Docker 内**单线程测的，机器上还有其他负载，**只能看量级与相对关系**。三次跑的口径一致（同一台机、同一份数据、同一顺序），所以跑次之间的比较是有意义的。§7.1 的 45–60 min 窗口预算就是按「17.5 min 实测 + 足够余量」排的，不是把实测值直接当预算。
>
> 体积数字不受这条限制 —— 语料是**生产全量副本**，落盘大小与机器负载无关，可直接用于容量规划。

### 7.6 实现落点与切换 runbook

#### 已落地的代码（2026-08-25）

| 单元 | 位置 | 内容 |
|---|---|---|
| U1 | `utils/database/gamedata/catalog/` | 编译期列目录：246 + 56 列、denied／compact／metadata 三分类、DDL 生成、区服码 |
| U2 | `utils/database/gamedata/pool.go`、`config`、`internal/bootstrap` | 专用 pgx 池（`MinConns` 预热）、`game_data:` 配置、组合根接线 |
| U3 | `utils/database/gamedata/schema.go` | 建表 + `game_data_catalog` checksum 校验 |
| U4 | `store.go`／`response.go`／`compactjson.go` | 行读取、字节拼接、compact 展开 |
| U5 | `utils/api/data/fetch_postgres.go` | 四个非 private 切面接上读源开关 |
| U6 | `internal/modules/userprivateapi/` | 私有面接上开关 |
| U7 | `utils/database/gamedata/gamemerge/` | 三个历史合并，Mongo 与 PG **共用同一实现** |
| U8 | `utils/database/gamedata/writer.go`、`validate.go` | 四种写语义、denied 丢弃、上传上限 |
| U9 | `cmd/gamedata-migrate/` | `estimate` ／ `migrate` ／ `verify` |

**开关**：`game_data.read_source`（`mongo` 默认 ／ `postgres`），环境变量 `GAME_DATA_READ_SOURCE`。默认值意味着**部署新代码不改变任何行为**，翻转是单独一步、同样一行可回滚。

#### 上线代码的端到端验证（2026-08-25）

§7.5 的演练用的是 scratchpad 里的一次性 harness，**那些代码不会上线**。这一轮用**实际要上线的 `cmd/gamedata-migrate`**，对着本地还原的生产全量副本跑完整流程。

| | |
|---|---:|
| 语料 | 10,928 suite + 4,124 mysekai（生产全量副本） |
| `migrate -apply` | suite 16m39s ／ mysekai 1m10s |
| **`verify` 差异** | **0**（两个集合都是 differing 0、missing 0） |
| denied 丢弃 | **27,300** 次 |
| 两种拼写并存 | **12** 次（行形态入列，compact 形态存 `extra`） |
| 落盘 | suite 3,201 MB ／ mysekai 247 MB |

**三个数字与一次性 harness 独立吻合**：denied 27,300、并存 12、`verify` 的 `denied-key drop only` 10,716 恰好等于写入侧报告的 `userRegistration` 篇数。两条互不相关的实现路径得出同一结果。

##### 读翻转彩排（`utils/api/data/cutover_dress_rehearsal_test.go`）

`verify` 证明两个库**装着相同的数据**，但没有证明**翻转之后服务出的字节相同** —— 而那正是维护窗口里打开、且请求进行中无法撤回的那一步。彩排把同一批账号分别用 `read_source=mongo` 和 `postgres` 走一遍真实读入口，逐字节（规范化后）比对：

```
dress rehearsal: 720 responses compared across 60 suite + 40 mysekai users; 0 mismatched
```

覆盖 suite 的 8 种形状（无 key／单键／单 compact 键／单 `userGamedata`／缺失键／多键／含缺失的多键／重复键）与 mysekai 的 6 种（无 key／`updatedResources`／多键／`_id`／`server`／缺失键）。

测试留在仓库里，靠 `GAMEDATA_REHEARSAL_MONGO` ／ `GAMEDATA_REHEARSAL_PG` 开关，CI 默认跳过。

##### M5／M6 放行闸（`utils/database/gamedata/gate_bench_test.go`）

之前的 M5／M6 是拿演练 harness 的表测的。这一轮用**上线代码**对**上线 CLI 装载的表**重测，并且同时量了三种形状 —— 它们代价差一个数量级，而放行闸只卡其中一种：

| 形状 | p50 | p90 | p99 | 是什么 |
|---|---:|---:|---:|---|
| **M5 upsert** | 1.47 ms | 2.70 ms | **7 ms** | 闸的定义：单行写 |
| 白名单读（25 键） | 11.3 ms | 20.8 ms | 213 ms | 公开面真实走的形状 |
| 整行读（246 列） | 61.1 ms | 103.1 ms | 281 ms | 私有面无 `?key=` |

M5 以 7 ms 对 200 ms 的门槛通过，余量很大。

⚠️ **但白名单读的 p99 是 213 ms，整行读 281 ms** —— 这两个不在放行闸里，却是真实流量的形状。口径要看清楚才不至于误读：**16 worker 反复打表中最大的 40 行**（约 12 MB／行），在开发笔记本的 Docker 里。真实流量分布在 10,928 行上、p50 只有 2.88 MB，而且不会人人都命中最大行。**这组数字是「病态访问模式 × 笔记本」的上界，不是生产预期**，但上生产硬件后值得复测一次，尤其是私有面的整行读。

⚠️ M6 的基线是 **1**，不是 0（见上表）。

##### 写路径集成测试（`utils/database/gamedata/writer_integration_test.go`）

writer 的单测只覆盖**编码那一半** —— 生成什么 SQL、写哪些列、丢弃什么 —— 都不执行语句。只在运行时才存在的部分（`COALESCE` merge 真的保住一列、suite 事务真的把已存历史读回来再合并）从未跑过。**读错只是响应不对，写错会污染行，下一次上传还在它上面继续累积。**

对着真实 PostgreSQL 跑，9 项全过：

| 断言 | 挡住什么 |
|---|---|
| 部分上传不清空未提及的列 | `COALESCE` 写成普通 `EXCLUDED` 赋值 → **每次部分上传都删数据** |
| migrate 重跑会清掉源里已没有的列 | 重跑留下陈旧值 |
| 三个历史键跨上传累积 | 客户端只发当前知道的事件，替换＝抹掉历史 |
| 更高 `eventPoint` 即使先到也胜出 | 合并不是 last-write-wins |
| 2^53 以上的 id 经 merge 往返不失真 | 任何 float64 中转 |
| denied 键既不落库也不进响应 | 安全控制 |
| 生日会写入只动自己那三列 | 清掉无关列 |
| 首次上传即建行 | upsert 退化成 update |
| 同账号两个区服互不覆盖 | 复合主键失效 |

靠 `GAMEDATA_WRITE_TEST_PG` 开关，CI 默认跳过。

##### 这一轮抓到 7 个 bug，单元测试一个都没发现

| # | 位置 | 性质 | 只有真实数据能暴露的原因 |
|---|---|---|---|
| 1 | CLI dry run 丢弃统计，报 `denied: none observed` | 报告失真 | 需要真的含 denied 键的语料 |
| 2 | 无 `server` 文档被隔离而非停放 | **丢一行 + 误阻断切换** | 全生产只有 1 篇 |
| 3 | `verify` 拿列式比行式 | 6,572/10,928 篇误报 | 需要 cn／tw 的 compact 文档 |
| 4 | 修 #3 时按**值的形状**判定 | 误伤全部对象型键 | 需要对象键与数组键混合 |
| 5 | **writer 无别名优先级** | **写入不确定** | 需要同带两种拼写的文档，全生产 2 篇 |

| 6 | `RawValue` 不认识扁平父键 | **主要查询 404** | `?key=updatedResources` 是 mysekai 的主查询（该键占文档 97.9%），118／675 的响应在 PG 侧 404 |
| 7 | 派生字段 `_idString` ／ `userIdString` 未复现 | **对外响应缺字段** | 这两个字段存储里根本不存在，由 `NormalizeProviderResponse` 合成 |

**#5、#6、#7 都会坏生产**，#1–#4 是工具与报告问题。#5 的表现是：`encode()` 遍历 Go map（顺序随机）后直接覆盖，所以同一篇文档**两次迁移可能存进不同的形态**。已按生产 `GetValueFromResult` 的优先级修正（行形态入列，compact 形态存 `extra` 不丢），测试跑 50 次循环 —— 非确定性实现会经常失败而不是从不失败。

#4 值得单独记：修 #3 时我写的判断是 `IsCompactValue(wantBytes)`，而它只看首字符是不是 `{`，于是**每个对象型键**都被丢进列式还原。识别特征是失败列表**清一色对象键、一个数组键都没有** —— 这种整齐的模式说明是系统性判定错误而不是数据问题。改成按**键**判定（必须是 `compactUserX` 拼写且该列是 compact 类）。

#7 的两个字段值得单独记：`_idString` 出现在任何带顶层 `_id` 的响应里，`userIdString` **只出现在嵌套的** `userGamedata` 里（裸 `?key=userGamedata` 不带 —— 合成是按 `objectName` 门控的，根值传的是空串）。它们正是**承载 id 的那个字段**：数值形态超过 JavaScript 能精确表示的范围，客户端依赖字符串形态。

> 结论：`gofmt`／`build`／`vet`／`staticcheck`／`go test` 全绿**不代表迁移正确**。这七个 bug 全部是在单元测试通过之后、靠真实语料跑出来的，其中三个会坏生产。任何后续改动都应重跑 `gamedata-migrate verify` **与**读翻转彩排，而不是只看测试。

#### U10 切换执行

```bash
# ---- 窗口之前（可提前数天，不影响线上）----
gamedata-migrate estimate -data-type all
gamedata-migrate migrate  -data-type all              # DRY RUN：证明每一篇都能编码
#   -> 隔离数必须为 0。非零就先在源库处理，再重跑。

# ---- 只读维护窗口开始：停止接受上传 ----
gamedata-migrate migrate -data-type all -apply -create-schema
gamedata-migrate verify  -data-type all               # differing 与 missing 必须为 0

#   ---- 翻转读源 ----
#   1) game_data.read_source: "mongo" -> "postgres"，重启
#   2) 立刻清空 game-data 响应缓存：
#        POST /admin/config/game-data-cache/purge      （super admin + 重认证）
#      ⚠️ 这一步不是卫生措施，是必需的：没有任何缓存键记录 body 是哪个库产的，
#         不清就会用 A 库铸的 stamp 去回答 B 库建的 body 的 304。
#   3) 抽查若干账号的公开／私有响应

#   窗口内也可以直接跑一次读翻转彩排（需要能同时连到两个库）：
#     GAMEDATA_REHEARSAL_MONGO=... GAMEDATA_REHEARSAL_PG=... \
#       go test ./utils/api/data -run TestCutoverReadFlip -count=1 -v

# ---- 只读窗口结束：恢复上传 ----
```

**回滚**：把 `read_source` 改回 `mongo`、重启、**再 POST 一次 purge**（同样的理由：缓存里存着另一个库建的 body）。窗口内 MongoDB 一直是只读的完整快照，第 6～10 步之间都可回退。

#### U11 下线 MongoDB

> §9.2 D（摘掉 Mongo 会让响应缓存静默失效）**已在实现期拆除**，不再是本步的前置雷。

观察期（≥ 7 天，一个 `GameDataCacheTTL`）通过后：

1. 删除 MongoDB 的 suite ／ mysekai 数据
2. 删除 `utils/database/mongo/`、`DBManager.Mongo`、健康检查里的 `"mongo"` 分支、`cmd/gamedata-migrate/`
3. 两个架构基线里的 `Mongo` 和 `GameData` 条目**一起删** —— 聚合最终比改造前少一个字段

⚠️ **`MongoDBConfig` 不能整块删**：它同时装着 `private_api_secret` 和 `private_api_user_agent`，两者在 `internal/bootstrap/runtime_config.go` 里给运行期配置播种，生产用 `PRIVATE_API_SECRET`／`PRIVATE_API_USER_AGENT` 注入。连带删掉会让私有 API 静默 fail closed。

⚠️ 健康检查里留着 `"mongo"` 依赖项会让 `/health` **永远 503**，而现有的 health 测试传的是 nil helper，抓不到。

#### U12 停止丢弃这 20 个键（已按 §7.1.1 拆成两步）

原本这是「清空 `suite_remove_keys`」一次不可逆发布。§7.1.1 把它拆成了两件独立的事：

**U12a — 剥离下沉到 Mongo 写入路径（可逆，越早越好）**

- 改 `utils/handler/preprocess.go:36`：预处理阶段不再剥离；改在 `PersistUploadData` 里
  给 Mongo 分支单独造**浅拷贝**再剥离，PG 分支拿完整 `data`
- ⚠️ `cleanSuite` 原地改写，必须拷贝，否则 PG 那份一起被清空
- 同时补上 `catalog.CompactPairs` 的 compact 键名（§1.1.2）
- `userProfileHonors` 留在双边剥离名单里，保证公开响应字节不变
- 前置：PostgreSQL 数据目录迁到 `/data2`（§7.1.1）
- 各账号在**下次上传之前** PG 里仍是 `[]`：数据从未被存储过，无法回填 ——
  **这就是「越早越好」的全部理由**

**U12b — 放开 `userProfileHonors`（对外变更，可逆）**

单独一次发布，gate 在 U11 的观察期之后，需要显式人工确认。

- 公开 API 对它从恒返 `[]` 变成真实数据，**随本步一并公告**
- 它会反转 `utils/handler/suite_restore_test.go` 里的三条断言，同一次提交改掉，别等它
  以失败的形式出现
- 回滚 = 把它加回名单，一行配置

⚠️ **Mongo 侧的剥离永不解除**，直到第 11 步 Mongo 下线为止。§1.1.1 实测：真实重度 jp 账号
不剥离时 BSON 16.16 MB，已超上限 —— 对 Mongo 而言这不是风险，是确定失败。

## 8. 被否决的方案及原因

| 方案 | 否决原因 |
|---|---|
| 什么都不做 | 恢复 21 键后已在 16 MB 的 95%，且持续增长 |
| MongoDB + 列式 compact 重编码 | 15.14 → 8.03 MB，只有 1.89×，且**不改变增长形状**（156 个键的增量仍累加到同一文档），约 2 年又满 |
| MongoDB 按顶层键拆文档 | 能解上限与读放大，但保留两个数据库，且**必须自行实现一致性协议**（一次上传变成 ~156 次非原子写） |
| MongoDB 应用层压缩整档 | 与 WiredTiger 的 zstd 重复，磁盘零收益，且让块压缩器失去结构信息 |
| PostgreSQL 单 jsonb 列 | 精确复现 MongoDB 今天的读放大（取一个键 6.5 ms vs 宽表 0.13 ms） |
| 大数组键规范化成关系表 | 比 jsonb 大 17.5×、读更慢，唯一优势（跨用户查询）当前零需求 |
| 改 `default_toast_compression = lz4` | 实测对本数据 lz4 比 pglz 差 20% |
| 大键用 `bytea` + 应用层 zstd | 262 篇真实文档实测只多省 0.9 GB（全量外推），而 compact 化后已优于现状 MongoDB；代价是一整层编解码 + 23 个列不可读（§3.5） |
| 列类型用 `jsonb` | 比 `json` 慢 1.4–1.9×，体积相同；且拒绝 NUL、丢重复键、改写数字字面量（§4.1） |
| 用 Ent 建这两张表 | 读取时把 JSON 解成 Go 结构，正好重建要消除的编解码往返（§4.2） |
| 只用 `lib/pq` | 文本协议导致 `bytea` 十六进制传输，字节翻倍（§4.3） |
| 以 `data/suite_user.avsc` 为唯一键来源 | 实测不完整（缺 `userBillingShopItems`），且只对 `tw` 区生效（§4.6） |

## 9. 开放问题（需要维护者拍板）

### 9.1 已解决

| # | 问题 | 结论（2026-08-23 生产普查） |
|---|---|---|
| 1 | 生产文档数与体积分布？ | suite 10,928 篇 / 3.30 GB，mysekai 4,124 篇 / 0.20 GB。分布见 §3.0。**规模远小于预期**；全量实测装载约 17.5 min（§7.5） |
| 2 | 顶层键并集基数？ | suite **251**（全量扫描；400 篇抽样只看到 205，低估 22%）、mysekai **14**、mysekai `updatedResources` **46**。**宽表方案通过**（PG 上限 1600） |
| 3 | 私有 API 点路径 key | **今天并不工作** —— `bsonDGet` 是平铺扫描，返回 `null`（§2.5）。迁移只需保持返回 `null`，不需要实现嵌套下降 |
| 17 | `userCostume3dStatuses` 是否跟 cn 一样 compact（需 padding）？ | **不转**。padding 会让 jp 响应凭空出现 `"obtainedAt": null`，是对外可见变化；该键本来就在 `suite_remove_keys` 里，没必要为它引入差异（§4.7.1） |
| 8 | 双写 vs 只读维护窗口 | **一次切换 + 只读窗口**。规模仅 10,928 篇，全量实测装载约 17.5 min，窗口按 45–60 min 申请；砍掉双写语义／差异检测／修复队列／读源灰度（§7.1） |
| 14 | 旧数据何时清理 | **第 10 步**，即确认新架构正常运行、观察期通过之后。第 6～10 步之间 MongoDB 保持只读作为回滚快照（§7.1） |
| 15 | 禁止存储哪些键 | `userInherit`、`userPlatformInheritIos`、`userPlatformInheritAndroid`、`userPlatforms`、`userRegistration` 直接丢弃；`userChargedCurrency` 及其余保留（§4.7.3） |
| 16 | 未知键怎么处理 | 进 `extra` json 列并告警；`denied` 类别的键直接丢弃，不进 `extra`（§4.7.4） |
| 5 | 大键清单 / 是否压缩 | **不压**。262 篇真实文档实测，compact 化后全 `json` 已优于现状 MongoDB（3.0 vs 3.3 GB），加 zstd 只多省 0.9 GB，不值一整层编解码（§3.5、§4.7.2） |
| 10 | compact 白名单如何确定？ | **以 cn 服实际形态为准**，恰好 6 个键。自建判定标准会误判：「含嵌套值」会误杀 5 个，「跨文档形状不同」会误杀 2 个。真正的障碍只有**单文档内字段集互斥**（§4.7.1） |
| 4 | 私有 API 的 compact 展开缺陷：谁通知调用方、什么时间窗？ | **私有 API 只有本团队自己的服务在用**，没有外部调用方。**不需要通知窗口**，修复与切换同批发布即可。§7.1 第 1 步不再是需要等待外部确认的前置 |
| 6 | `userProfileHonors` 的配置修正何时走？ | **作为对外变更修复**。按 §7.1.1 修订后它从第 9 步拆出，成为独立的 **U12b**：它是 20 个剥离键里**唯一**在 `public_api_allowed_keys` 中的（`userMaterials` #13 在配置里已被注释、当前并未剥离），所以放开它是唯一会改变公开响应的动作，必须单独发布并公告，gate 在观察期之后 |
| 7 | 第 9 步之后，21 个键在各账号下次上传前仍是 `[]`。接受这个窗口？ | **接受，等用户自行更新数据**。不做回填 —— 这些数据从未被存储过，回填在技术上不可能。账号下次上传即恢复 |
| 9 | `cmd/gamedata-migrate` 切换完成后保留还是删除？ | **删除**。因此进度表不必进 Ent schema，用手写 DDL 即可；CLI 随第 11 步一起下线 |
| 11 | 迁移前是否先补上公开 mysekai 端点的键白名单？ | **不补。** 改为把 `public_api_allowed_keys` 改名合并成 `allowed_keys`，一份配置供所有非 private API 使用（private API 不受约束）；**mysekai 端点不过白名单** —— 实测其 `updatedResources` 子键里 `userBillingRefunds`／`userUnprocessedOrders`／`userPresents` 全为空、`userInformations` 是游戏公告，无付款金额与 PII；而直接套用现有 35 条会让公开 mysekai 端点全部返回空。前提与复评触发条件见 **§4.8** |
| 19 | mysekai 侧放行哪些键？ | **不设白名单**（见 #11）。实测无敏感内容；残余风险是「现有校验只管字段名与深度、不管键集合」，用上传期键集合告警兜底，不用读端白名单（§4.8） |
| 12 | 生产 `public_api_allowed_keys` 末尾的 `tw` `cn` `kr` 三条 | **配置误写，删掉**。它们是区服名不是数据键名；留着会让 `?key=tw` 之类被接受并返回空数组。随 #11 的合并一起做（§4.8） |
| 13 | `userMaterials` 同时在 `public_api_allowed_keys` 和 `suite_remove_keys` 里 | **这个键需要用**，因此从 `suite_remove_keys` 移除，公开 API 返回真实数据。与 #6 同批（第 9 步） |
| 18 | 生产 `suite` 里 `_id` 为 `null` 的那 1 篇文档 | **直接删除 —— 已于 2026-08-24 执行完毕。** 它没有 `userGamedata`、没有任何玩家进度，装的是 `policy`／`version`／`appVersion`／`assetVersion`／`cdnVersion`／`dataVersion`／`suiteMasterSplitPath`／`configs`，是客户端启动配置响应而非用户存档（`server: tw`，2025-09-24，属上传校验落地前的遗留污染）。删前已单篇备份，`deletedCount = 1`，两个集合的非数字 `_id` 均归零（§7.0 ③） |

### 9.2 待拍板

**决策**层面已全部拍板。下面是 2026-08-24 复盘出来的**未验证项** —— 不是等谁拍板，是等做与等测。

| # | 缺口 | 严重度 | 说明 |
|---|---|---|---|
| A | ~~**`denied` 三分类从未被实现或演练**~~ **已于 2026-08-24 实现并验证** | ✅ 已闭环 | `schemagen` 里**完全没有 `denied` 概念**（全仓 grep 零命中）。全量装载把 §4.7.3 规定要丢弃的 5 个键**全部存进了表**，各占一列。实测内容：`userRegistration` 在 **10,716 / 10,928 行非空**，字段含 `age`／`dayOfBirth`／`yearOfBirth`／`deviceModel`／`operatingSystem`／`platform` 以及一个 **HS256 JWT `signature`**；`userPlatforms` 3,047 行非空，`userInherit` 238 行非空。**这正是 §4.7.3 要挡的东西，而挡它的机制当时只存在于文档里。** 现已补上三层实现（扫描期不建列／校验期拒绝走私／装载期编码前丢弃）加 4 个断言测试，v4 全量实测丢弃 27,300 次，建表后核对 denied 列 = 0、`extra` 残留 = 0。详见 §4.7.3。体积影响可忽略（v4 比 v3 只小 4 MB），**这从头到尾是安全项，不是容量项** |
| B | ~~**响应等价是在另一套 schema 上验的**~~ **已于 2026-08-24 补上** | ✅ 已闭环 | 新增 `internal/schemastore` 直接读 `schemagen` 装出来的宽表（246 列、全 `json`、compact 出列时展开、扁平子键回拼），`respcompare -layout schemagen` 成为默认。全量 **269,032 次比对，mismatch 0**，并与装载期的丢弃计数交叉验证一致。两类有意差异（denied 丢弃、私有面 compact 展开修复）逐条分类、无上限复核、并有 `match+mismatch+errored+intended == docs` 的逐 shape 自查。详见 §7.5 |

#### 明确不做离线演练的项（2026-08-24 决定）

C／D／E **不进离线演练**，理由是一次切到 PG 之后这几处是全新代码，拿 Mongo 语义去演练它们收益有限 —— 它们由常规实现与单元测试覆盖。**记录在此是为了它们不被当成"演练已覆盖"。**

| # | 项 | 归属 |
|---|---|---|
| C | 写路径：三个历史合并函数的 int64→float64 危害、生日会单列 UPDATE、四种写语义 | ✅ **实现期已覆盖**：合并逻辑抽到 `gamemerge`，Mongo 与 PG 共用一份实现，现有 15 个合并测试原样通过即是移植等价的证明；另加 11 个语义测试。四种写语义分开实现（§7.6 U8） |
| D | 缓存：`ConfirmGameDataCacheWrite` 在 `Mongo == nil` 时返回 false，Mongo 一摘响应缓存全部静默失效 | ✅ **已拆除**（2026-08-25）。两个调用点改走 `readUploadTime`，按 `read_source` 解析代际；store 侧新增窄查询 `Store.UploadTime`（只取一个 bigint，不为了读代际拉整行宽表）。无 store 配置时返回**错误**而不是静默 false —— 后者会让写栅栏拒绝每一次写、响应缓存永久为空，而**空缓存和冷缓存看起来一模一样，没有任何东西会报出来** |
| E | 上传大小上限（单键／`extra` 成员数与字节数／整行） | ✅ **已实现**：`gamedata.Limits`，四道闸按实测生产分布留余量（单键 64 MiB ≈ 最大观测键 6.5 MB 的 10 倍；整行 192 MiB ≈ 最大观测文档 13.52 MB 的 8 倍）。字段名校验另加 NUL／非法 UTF-8 拒绝与 256 层嵌套上限 |

F 已定稿：**一列 + 别名**，见 §4.7.1。外部调用方一般只取 `userCostume3dStatuses`，不取 compact 名。


## 10. 测量条件与口径

**§3.0 是生产实测**，可用于容量规划：全量 `stats()`（零扫描），分布与键并集用 `$sample` 抽样 200–400 篇（耗时 3.7 s）。抽样对 p50/p90 可靠，**p99 与 max 是低估** —— 200 篇只能看到分布最右端的约 0.5%，真实 max 大概率高于 13.52 MB。

**§3.1 起是本地对照实验，绝对数字不能用于容量规划**：

- **n = 1**。suite 样本是单个 rank 693、注册五年半的重度账号。生产 p50 是 2.71 MB，而该样本（21 键置空后）是 4.80 MB —— **它比中位数大，但远小于 p99 的 12.19 MB**，属于中上而非极端。
- **单行、热缓存、单连接、无并发**，在本机 Docker 内测得。并发下的连接池竞争、WiredTiger cache 争抢、PostgreSQL 的 TOAST 缓冲区行为均未覆盖。
- 本地单文档实验测到 MongoDB 压缩 16–18×，**生产实际是 9.4×（suite）／7.4×（mysekai）** —— 单文档实验高估了 MongoDB 一侧。§3.1 的 Mongo vs PG 对比应按此折算，PG 的相对劣势比表中所示更小。
- suite 样本是 **2026-04** 快照；mysekai 样本是 2026-08-20。
- **比值**（bytea vs json、宽表 vs 单列、方案 ①②③）应比绝对值稳定，因为访问代价主要由数据形状决定。

**§7.5 第二轮（2026-08-24）是生产全量副本上的实测**，口径与上面几条不同，可以用于容量规划：

- 语料是**生产 `suite` 集合的完整 mongodump**（10,928 篇）与**全部 4,124 篇 mysekai**，本地还原后磁盘 3.21 GB，与生产 `stats()` 的 3.30 GB 吻合。**不是抽样，不需要外推。**
- 因此 §7.5 的**体积数字可直接用于容量规划**；§3.5 那张表的 3.0 GB 外推已被它取代。
- **耗时数字仍不能直接当生产窗口预算**：在开发笔记本的 Docker 内单线程测得，机器上有其他负载。三次跑（v1／v2／v3）用同一台机、同一份数据、同一顺序，所以**跑次之间的比较有效**，绝对值只看量级。
- M5／M6 的压测取表中**最大的 50 行**反复读写，是最坏情况而非平均情况。

M5／M6 结果见 §7.4，压测细节见 §7.5。

## 11. 相关文件

| 路径 | 作用 |
|---|---|
| `utils/database/mongo/data_ops.go` | 四个存取方法与三个历史合并函数 |
| `utils/database/mongo/fixture_search.go` | 唯一的跨用户聚合（零调用者） |
| `utils/api/data/fetch.go` | `?key=` 解析、投影构建、响应组装 |
| `utils/api/data/utils.go` | `GetValueFromResult`、compact 展开、`userGamedata` 列白名单 |
| `utils/api/data/stamp.go` | 分代缓存的 upload_time 解析与写栅栏 |
| `utils/handler/suite_restore.go` | `cleanSuite`（`suite_remove_keys`）与 avsc 位置数组还原 |
| `utils/compactrestore/` | 列式 → 行式展开 |
| `internal/modules/userprivateapi/` | 私有 API（核心服务在用） |
| `cmd/suite-rec-backfill/main.go` | 迁移 CLI 的风格模板 |
