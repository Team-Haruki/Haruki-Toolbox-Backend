# JSON 与数字精度约定

项目使用 Go 1.27.1，工具链版本以 `go.mod` 为准，CI 与 Release 从中读取。本文件记录当前维护约定；历史迁移、性能测量及部署过程通过 Git 历史查阅。

## JSON v2 覆盖范围

项目自有及生成源码使用 JSON v2，不再依赖 Sonic 或上游 orderedmap。通用对象编解码使用 `encoding/json/v2`，原始值与流式 token 使用 `encoding/json/jsontext`。第三方库内部源码不做 vendor 修改。参见 [官方迁移指南](https://go.dev/doc/jsonv2-migration) 与 [JSON v2 API](https://pkg.go.dev/encoding/json/v2@go1.27.1)。

此约定覆盖 Fiber、Resty 的隐式 JSON 编解码、通用 Redis 缓存、运行时配置、OAuth2/Kratos、pgx JSON/JSONB、Ent 生成代码、游戏 JSON 与 MessagePack→JSON 流式输出。原始游戏 JSON 响应仍直接发送已有字节，避免将 []byte 编码成 base64 字符串。

### 明确的行为选择

| 场景 | 当前行为 |
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

## 包职责

- `utils/codec/jsoncodec`：Fiber、Resty 和通用缓存的 JSON 编解码策略；显式保留 nil map/slice 的 null 表示。
- `utils/codec/jsonvalue`：动态 JSON 的精确数字与标量字符串派生；游戏 ID 不得经过 float64 中转。
- `utils/codec/msgpackcodec`：MessagePack 结构校验、有序解码和直接 JSON 输出。
- `utils/orderedmap`：有序容器及 JSON v2 流式编码；容器可变，不支持并发写。

MessagePack 各输出策略、provider 派生字段和只读共享约定见 [MessagePack codec 与 OrderedMap](msgpack-codec.zh-CN.md)。游戏结构复原见 [MYSEKAI 与 Nuverse 复原](mysekai-restore.zh-CN.md)。

## 维护与验证

两套 Ent 的 JSON 与泛型方法适配通过 `ent/codegen.Modernize` 生成钩子维护，不直接修改生成产物。修改生成规则后运行：

```sh
go generate ./ent/toolbox ./ent/bot
go test -race -count=1 ./...
```

修改编解码时，至少覆盖 nil/空集合、可选布尔值、大整数、重复字段、非法 UTF-8、尾随值和嵌套深度；涉及 HTTP 边界时同时验证 Fiber 与 Resty 的隐式编解码。
