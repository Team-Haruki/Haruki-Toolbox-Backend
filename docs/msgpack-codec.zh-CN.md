# MessagePack codec 与 OrderedMap

`utils/codec/msgpackcodec` 统一 MessagePack marker、受检字节游标、结构校验、有序解码和 JSON 输出。生产及测试调用直接使用新包；`utils/orderedmsgpack`、`utils/streamjson` 已删除。`utils/orderedmap` 独立保留连续 entries、8 字段线性查找和大对象索引，并对已知容量的小对象合并分配。

## 调用与分层

| 入口 | 用途与边界 |
| --- | --- |
| `msgpackcodec.WriteJSON(w, data, options)` | 完整 MessagePack 输入，先校验结构和深度，再逐步输出 JSON；不构建完整对象树 |
| `msgpackcodec.ValidateMaxDepth(data, maxDepth)` | 只扫描，不构建解码结果；拒绝长度越界和尾部多余字节 |
| `msgpackcodec.DecodeOrdered(data)` | 完整顶层 map 解码，最大深度 512；返回拥有字符串和二进制存储的 OrderedMap |
| `msgpackcodec.Marshal/MarshalWrite` | 保留 OrderedMap 顺序；普通 Go map 不承诺稳定顺序。MarshalWrite 仍先完整编码再写 |
| `msgpackcodec.Unmarshal/UnmarshalRead` | 保留有序目的对象与外部库通用目的对象的兼容分支；通用分支处理不可信输入前须显式校验深度 |
| `msgpackcodec.DecodeOrderedRead` | 完整读入后解码，调用方负责 Reader 的字节上限 |
| `data.ProviderJSONOptions()` | 业务层返回 userGamedata/userId/userIdString 规则；调用方直接传给 `msgpackcodec.WriteJSON` |

新核心不依赖游戏字段名、HTTP、数据库或压缩器。provider 规则通过 `JSONOptions.DerivedStringField` 指定；零值 options 不派生字段。具体字段名位于 `internal/platform/api/data`，每次返回独立的配置值。上传 handler 直接调用新包，不再经过旧转换函数。Sekai 的普通值解码也统一从新包进入；外部 MessagePack 库仅保留在 codec 内部作为类型兼容实现。

游标输入仅在同步调用期间借用，调用结束前不能修改输入。游标切片不会作为 OrderedMap 的持久字符串或二进制值逃逸。没有 unsafe 转换、全局字段名缓存或可变对象池。

## 保留的行为

JSON 与 OrderedMap 是不同的适配策略：

| 行为 | JSON 输出 | OrderedMap 解码 |
| --- | --- | --- |
| 非字符串 map key | 拒绝 | 按旧规则字符串化 |
| 重复 key | 拒绝 | 后值覆盖，保留首次位置 |
| bin/ext | 输出 null | 复制 payload 为 []byte；保留旧的丢弃 ext type 行为 |
| NaN/Inf | 输出 null | 保留浮点值 |
| 非法 UTF-8 | 拒绝 | 保留旧字符串解码行为 |
| 大整数 | 精确 int64/uint64 token | 保留整数类型及精度 |

因此 OrderedMap 的 ext 解码再编码不构成无损 MessagePack 往返，也不能用 OrderedMap 的 JSON 编码直接代替当前 processed 转换。

JSON 入口继续预校验 256 深度。结构损坏、深度超限和尾部多余字节会在写出之前拒绝；JSON 语义错误或 writer 错误仍可能产生部分输出，调用方必须丢弃失败结果。错误文本前缀可能随实现迁移变化，不作为稳定接口；错误接受边界和合法输出保持兼容。

`StringFieldRule` 匹配通过指定字段直接到达的对象；不会把匹配状态继承给中间数组中的对象。源字段保留原位，派生目标在对象尾部写出；可用源值覆盖 supplied 目标，没有可用源值时保留 supplied 值。重复源/目标字段仍拒绝。

## 后处理路径

- processed：已解密 MessagePack → codec JSON sink（传入 provider 数据适配层 规则）→ zstd。
- restored：已解密 MessagePack → 现有普通 Go map 解码 → Restore/Normalize → JSON/zstd。
- 主上传继续使用既有普通 map 解析和属主校验。没有强制改成 OrderedMap 中转。

本阶段仍允许多个消费分支分别校验。输入所有权和端到端收益尚未验证前，不增加公开的跳过校验开关。Reader 字节预算仍由现有调用方负责；本轮没有宣称提供增量 Reader 解码。

## Provider 规范化优化（2026-09-21）

`NormalizeProviderResponse` 仅复制新增/修改派生字段或转换 BSON 容器的分支，未变化的普通 map/slice 与输入共享。输入不会被修改；返回值是只读视图，调用方不得修改共享子对象。仍需遍历整棵树，BSON 文档转普通对象时仍需分配，nil 集合的既有空容器表示保持不变。

MessagePack 直接输出与对象规范化通过 `jsonvalue.ScalarString` 共用数字 token 到字符串的规则。非空字符串和合法 JSON 数字可派生；整数不经过 float64，中间不存在大整数精度损失。小数与指数沿用直接输出路径的 JSON 文本，因此对象路径也会为 `1.5` 派生 `"1.5"`，为 `1e25` 派生 `"1e+25"`。原有源字段不改写；float32 的源字段在两条路径中的历史 JSON 表示差异仍保留，派生字符串按 MessagePack 提升后的 float64 表示对齐。不可用源值保留已有目标值。

Apple M4 / Go 1.27.1 / GOMAXPROCS=10，10,000 行合成普通 map 数据，每行包含 cardId、level、skills，另有一个 userGamedata；三次中位数：

| 规范化实现 | 耗时 | 分配字节 | 分配次数 |
| --- | ---: | ---: | ---: |
| 修改前整树复制 | 1.690 ms | 4,084,585 B | 40,008 |
| 仅复制变化分支 | 0.530 ms | 737 B | 8 |

仅测 Normalize，不含解密、复原、JSON 编码、压缩或网络；不能直接换算为生产 API 收益。契约测试覆盖大整数、小数、指数、float32、空字符串、既有目标、非有限数、空容器、输入不变、分支共享和幂等性。相关 codec、上传和 private API 包的 race 回归通过。本轮未优化解包失败回退路径，也未部署。

## 验证与本机基准

与修改前冻结实现做接受结果和合法 JSON 逐字节比较，包含随机对象树、重复字段、非法 UTF-8、bin/ext、大整数与深度边界。永久测试补充通用字段规则、provider 兼容入口、writer 失败、结构损坏零输出和旧有序 API。

共同 codec 首次落地时，全项目 build、vet、staticcheck、`go test -race -count=1 ./...` 已通过（71 个有测试包、50 个无测试包）。全项目检查使用源码快照，排除工作区原有的已忽略临时 cmd 工具与运行数据。旧实现差分 fuzz 30 秒通过约 123 万次执行，通用 JSON fuzz 20 秒通过约 127 万次执行；这些测试不等于覆盖所有可能输入。该段为实现验证记录，不描述当前生产状态。

Apple M4、Go 1.27.1、GOMAXPROCS=4；每组三次中位数，5,000 行合成 MessagePack：

| 负载 | 修改前 | 共同 codec | 耗时下降 |
| --- | ---: | ---: | ---: |
| 每行 6 字段转换 | 5.991 ms | 3.876 ms | 35.3% |
| 每行 16 字段转换 | 14.993 ms | 11.744 ms | 21.7% |
| 16 字段转换加 zstd | 15.410 ms | 12.920 ms | 16.2% |

6 字段的分配约 759 KB / 84,770 次 → 241 KB / 30,017 次；16 字段约 1,954 KB / 224,034 次 → 641 KB / 80,018 次。

转换写 io.Discard；zstd 基准包含 Reset/Convert/Close，不含生产最终输出复制、解密、Restore、HTTP 或网络等待。微基准有噪声，不能直接当作真实数据、Linux 或生产 API 百分位收益。上线前仍需目标环境验证。

相关测试入口：

```sh
go test -race ./utils/codec/msgpackcodec ./utils/orderedmap ./internal/platform/api/data ./utils/game/sekai ./utils/game/nuverserestore ./internal/platform/upload
go test ./utils/codec/msgpackcodec -run '^$' -fuzz '^FuzzWriteJSON$' -fuzztime=30s
go test ./utils/codec/msgpackcodec -run '^$' -bench BenchmarkWriteJSONRecords -benchmem
```

OrderedMap 有界字段名复用、只读记录共享 schema、pipeline 内已校验 Document 都尚未启用。它们需要分别解决低重复率退化、可变对象语义和输入所有权问题，避免把未证明的优化捆绑进 codec 迁移。

## 小型 OrderedMap 合并分配与旧包退役

`NewSize(1..8)` 将 map 头和精确容量的 entry 数组放在同一次分配内。OrderedMap 结构本体仍为 32 字节（64 位平台），不改变索引阈值、Get 查找、覆盖保序或 Delete 语义；初始容量大于 8 的对象继续使用原布局。零值与 `New()` 也保持可用。

这不是把固定数组内嵌到每一个 OrderedMap 类型中。小对象扩容后，初始合并分配仍会随 map 头存活，最多保留 8 个已清空 entry 的空间；扩容时清空旧槽位，避免被覆盖/删除的 key 和 value 被额外保活。因此收益是少一次分配，不是总字节或峰值 RSS 必然减少。已测试全部 1～8 容量的扩容清理、随机覆盖/删除，以及 Unmarshal 赋值到调用方后继续扩容和编码。

Apple M4、Go 1.27.1、GOMAXPROCS=4，三次中位数，构造函数基准：

| 初始容量 | 原耗时 | 合并分配耗时 | 原/新分配次数 | 原/新字节 |
| --- | ---: | ---: | ---: | ---: |
| 1 | 24.10 ns | 15.01 ns | 2 / 1 | 64 / 64 B |
| 4 | 60.82 ns | 36.64 ns | 2 / 1 | 160 / 160 B |
| 8 | 74.79 ns | 75.22 ns | 2 / 1 | 288 / 288 B |
| 16 | 312.20 ns | 299.50 ns | 6 / 6 | 1,528 / 1,528 B |

1/4 容量构造耗时下降约 38%/40%；8 容量主要减少分配，耗时基本持平。16 容量的存储路径没有改变，表中小幅差异不能作为优化收益。3,000 行完整有序解码中，每行 1/4/8 字段分别减少 3,001 次分配（含根对象），但总耗时收益不稳定，8 字段样本略慢，不能外推 API 提速。

旧 provider 测试迁入 provider 数据适配层，旧公开有序入口的测试迁入 msgpackcodec。生产与测试源码已无旧包导入；旧包删除属于 Go 源码 API 迁移，仓库外若有使用者需更新 import 和函数名。HTTP 对外协议保持不变。后续部署结果见文末记录。

`SekaiCryptor.UnpackOrdered` 直接接收解码返回的 map 指针，省去先构造空 map 再复制头部的步骤。provider 规则与 `NormalizeProviderResponse` 复用已有字段常量；没有新增 utils → platform 的反向依赖，也没有修改架构检查基线。

差分验证还确认了旧有边界：嵌套对象作为 map key 时，旧 `fmt` 字符串化可能包含内存地址，同一输入的旧输出也可能不同。JSON 路径继续拒绝此类 key；有序解码保留旧接受行为。差分 fuzz 对这些旧输出不稳定的样例比较接受结果，对稳定样例比较编码字节，不把它们误报为此次存储回归。
