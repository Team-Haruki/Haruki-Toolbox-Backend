# 后端重构与 Suite Restore 重置计划

本文档记录一次后端结构体检结论，以及围绕 suite restore 使用
[Haruki-Nuverse-StructTool v2](https://github.com/Team-Haruki/Haruki-Nuverse-StructTool/tree/v2)
重置结构来源的可行方案。

目标不是立即大规模重写，而是把已经明确的风险点、改造顺序和验证方式沉淀下来，方便后续拆成可执行任务。

---

## 0. 当前进度快照

更新时间：2026-06-09。

本分支已经完成一轮按职责拆分和 suite restore 重置，当前重点已经从“大文件纯移动式拆分”转向“收口、去重和后续高风险模块治理”。

已完成：

- 最近 main commit 已按能力拆分为 game user ID 精度、upload audit、ordered msgpack、stream json 四组提交。
- `SessionHandler` 已拆出 support types、Kratos helpers、verification paths。
- `internal/bootstrap` 已拆出 validation/schema/log/dependencies/Fiber app setup。
- `config` 已拆出 types/defaults/path/load/env/SMTP URI fallback。
- OAuth2 / AdminOAuth Hydra handlers 已按 route、login/consent、Hydra request/response、admin client/authorization/audit/revoke 等职责拆分。
- Birthday subscription、User game account binding、User social、Upload、User ticket handlers 已完成纯移动式拆分。
- public/private/oauth2 数据查询已经增加更积极的 Redis 响应缓存，并迁移 `public_access` 到统一 `game_data` namespace。
- StructTool / Avro 已接入离线 compare 工具，支持 raw upload 样本解密解码验证。
- 已从本地 Unity `DummyDll` 生成 `data/suite_user.avsc`，运行时 suite restore 结构来源已切到 StructTool/Avro schema。
- 旧 `data/suite_structures.json` 已移除，`restore_suite.structures_file` 示例配置已指向 `./data/suite_user.avsc`。
- compact restore 的重复展开算法已抽到 `utils/compactrestore`，`utils/api/data` 和 `utils/nuverse` 只保留各自 BSON / orderedmap 适配。
- Docker workflow PR path filter 已清理旧 Rust 路径并改为 Go 项目真实路径。
- `internal/modules/usercore` 已增加组合 route guard helper，并替换等价的用户路由 guard 链。
- `utils/orderedmsgpack` 已完成解码硬化，并移除内部自定义 `OrderedMap` msgpack ext；生产入口只保留标准 msgpack 解码。

仍未纳入或清理的本地项：

- `7445104842642643749`：本地 raw upload 真实样本，仅用于 StructTool 恢复验证。
- `8D6B082F-4898-4B41-AC0D-7AC9ABA31015`
- `cmd/suite-rec-backfill/`
- `registration-handoff.cn.md`
- `suite_rec/`
- 本文档当前作为工作记录维护，是否提交另行决定。

当前下一步优先级：

1. 对 StructTool/suite restore 增加更正式的 schema 生成说明和小型 golden fixture，避免依赖本地真实样本。
2. 明确 `utils/nuverse` 作为离线/reference master restorer 的定位，后续再评估是否删除未接入 runtime 的 master 逻辑。
3. 下一批大文件拆分优先从 `adminwebhook`、`harukibotneo`、`userprivateapi`、`adminusers` 中选择。

---

## 1. 总体判断

当前项目的主分层方向是健康的：

- `main.go` 保持很薄，只负责加载配置并进入启动流程。
- `api/route.go` 只做模块路由注册，不承载业务逻辑。
- Ory / Kratos / Hydra 的关键约束集中在启动校验、`SessionHandler` 和 OAuth2 helper 中。
- CI 工作流已经覆盖 `gofmt`、`go build`、`go vet`、`staticcheck` 和 race test。
- 测试覆盖意识较强，Ory session、OAuth2、route guard、Redis Lua、上传审计等关键路径都有测试。

主要问题不是方向错误，而是部分文件和职责在自然演进中变重了，需要分阶段拆薄。

---

## 2. 做得好的部分

### 2.1 启动入口克制

`main.go` 没有塞入业务逻辑，只加载配置并调用 `internal/bootstrap.Run`。

这符合当前架构约束，也让后续重构可以集中在 bootstrap 内部，而不影响程序入口。

### 2.2 Ory / Hydra 约束有启动期保护

启动流程会校验：

- `user_system.auth_provider` 只支持 `kratos`
- `user_system.kratos_public_url`
- `user_system.kratos_admin_url`
- `oauth2.provider` 只支持 `hydra`
- `oauth2.hydra_public_url`
- `oauth2.hydra_admin_url`
- Auth Proxy 启用时必须配置 trusted header、subject header 和 session header

这能避免很多错误配置在运行时才暴露。

### 2.3 浏览器认证迁移边界清楚

当 `SessionHandler.UsesManagedBrowserAuth()` 成立时，旧本地浏览器认证入口会被禁用。

这避免了在 Kratos / Oathkeeper 作为浏览器身份入口时，旧本地密码体系被重新打开。

### 2.4 Hydra subject 兼容策略清楚

OAuth2 subject 采用“优先 Kratos identity ID，兼容 fallback 本地 user ID”的过渡策略。

这对已有 OAuth2 授权、token introspection 和 consent 记录都比较重要，后续相关重构必须保留。

### 2.5 模块测试基础较好

仓库中已有大量 `_test.go`，尤其是：

- `utils/api/session_handler*_test.go`
- `utils/oauth2/*_test.go`
- route guard 测试
- upload audit 测试
- Redis cache / Lua 测试

这让移动式重构有比较好的安全网。

---

## 3. 需要重构调整的部分

### 3.1 `SessionHandler` 已完成第一轮拆薄

早期 `utils/api/session_handler.go` 同时承担：

- SessionHandler 配置
- Auth Proxy header 校验
- Kratos whoami 查询
- Kratos login / register / recovery / settings flow
- Kratos identity admin API patch / put
- Kratos identity 到本地 user 的映射
- 自动 link by email
- 自动 provision local user
- profile sync
- Redis 旧 session 清理

当前已经保留 `SessionHandler` 作为对外门面，并完成第一轮纯移动式拆分：

- `session_handler.go`：公开门面、`VerifySessionToken`、公共类型
- `session_auth_proxy.go`：Auth Proxy 相关逻辑
- `session_kratos_client.go`：Kratos HTTP client、endpoint、flow submit
- `session_kratos_identity.go`：identity 解析、自动绑定、自动建用户
- `session_profile_sync.go`：profile sync
- `session_local_store.go`：Redis 旧 session 清理

后续如果继续调整，应仍然避免把 Kratos / Auth Proxy 会话解析复制到业务模块。

### 3.2 `internal/bootstrap/run.go` 已完成第一轮拆分

早期 bootstrap 同时处理：

- 配置校验
- schema 兼容检查
- Mongo / Redis / PostgreSQL / Bot DB 初始化
- logger 初始化
- Fiber app 配置
- CSP / access log middleware
- route 注册
- server listen 和 graceful shutdown

当前已经拆成：

- `validate.go`
- `schema_compat.go`
- `dependencies.go`
- `fiber.go`
- `run.go`

其中 `run.go` 保留主流程编排；后续可以继续收缩细节，但不建议引入复杂 dependency container。

### 3.3 schema 兼容 SQL 已从 bootstrap 主文件移出

users / webhook 的兼容 SQL 已移动到 `internal/bootstrap/schema_compat.go`。

后续仍需明确：

- `backend.auto_migrate=true` 时允许自动补齐哪些兼容项
- `backend.auto_migrate=false` 时只校验，不执行 DDL
- 哪些变更必须走正式迁移或 Ent schema 生成

### 3.4 多个大业务文件已完成第一轮拆分

已完成第一轮拆分的文件包括：

- `internal/modules/adminoauth/hydra_handlers.go`
- `internal/modules/oauth2/hydra_routes.go`
- `internal/modules/upload/handler.go`
- `internal/modules/usergamebindings/gameaccount.go`
- `internal/modules/usersocial/social.go`
- `internal/modules/usertickets/tickets.go`

后续仍可按风险从高到低继续处理其它大文件：

1. Admin users / integrations
2. Admin webhook
3. HarukiBot NEO
4. User private API
5. User password reset / social authorization

每次只拆一个模块，先移动逻辑和补测试，不同时做行为调整。

### 3.5 用户路由 guard 组合已收口

多处重复出现：

```go
apiHelper.SessionHandler.VerifySessionToken,
userCoreModule.RequireSelfUserParam("toolbox_user_id"),
userCoreModule.CheckUserNotBanned(apiHelper),
```

当前已在 `internal/modules/usercore` 增加组合 helper：

- `RequireAuthenticatedUser(apiHelper)`
- `RequireAuthenticatedSelf(apiHelper, "toolbox_user_id")`
- `RequireAuthenticatedVerifiedSelf(apiHelper, "toolbox_user_id")`

等价的用户路由 guard 链已经替换；后续新增用户路由时应优先复用这些 helper，避免漏挂 guard 或顺序不一致。

### 3.6 配置文件已完成第一轮拆分

早期 `config/config.go` 同时包含：

- 所有 config struct
- 默认值
- config path 解析
- YAML 读取
- 环境变量替换
- 环境变量覆盖
- SMTP URI fallback

当前已经拆成：

- `types.go`
- `defaults.go`
- `load.go`
- `env.go`
- `smtp_uri.go`

拆分时已保持 `config.Load` 和 `config.LoadGlobalFromEnvOrDefault` 行为不变。

### 3.7 Workflow path filter 已清理旧痕迹

Docker workflow 的 PR path filter 曾包含 `Cargo.toml`、`Cargo.lock`、`src/**`。

当前已经替换为 Go 项目真实路径：

- `main.go`
- `go.mod`
- `go.sum`
- `Dockerfile`
- `.dockerignore`
- `.github/workflows/docker.yml`
- `api/**`
- `cmd/**`
- `config/**`
- `ent/**`
- `internal/**`
- `utils/**`
- `version/**`

### 3.8 未跟踪数据目录需要明确归属

当前仍有未跟踪项：

- `cmd/`
- `suite_rec/`
- `8D6B082F-4898-4B41-AC0D-7AC9ABA31015`
- `registration-handoff.cn.md`

其中 `cmd/suite-rec-backfill` 如果是正式维护工具，可以纳入仓库；`suite_rec/` 更像本地样本数据或一次性处理输入，应避免作为运行期产物重新引入主仓库。

建议：

- 正式工具提交到 `cmd/...`
- 样本数据移到外部目录，或只保留少量脱敏 fixture
- 大批 JSON 输入加入 `.gitignore`

---

## 4. Suite Restore 现状

当前 suite restore 相关逻辑已经从旧 JSON 结构文件迁移到 StructTool/Avro schema，但仍保留轻量执行器和查询侧 compact 恢复能力。

### 4.1 StructTool/Avro 结构来源

`data/suite_user.avsc` 由 Unity MsgpackSchemaExporter / StructTool schema exporter 从本地 `DummyDll` 生成。

运行时通过 `utils/nuversestruct` 读取 Avro schema，生成 `utils/suiterestore` 可执行的字段定义，再将 suite 中的数组字段恢复为 keyed dict。

旧 `data/suite_structures.json` 已移除；`utils/suiterestore` 现在是执行器，不再负责从旧 JSON 文件加载结构来源。

当前接入点在 `DataHandler.PreHandleData`：

```go
if dataType == utils.UploadDataTypeSuite {
    data = cleanSuite(data)
    if shouldRestoreSuiteForDB(server) {
        if r := getSuiteRestorer(server); r != nil {
            data = r.RestoreFields(data)
        }
    }
}
```

结构文件通过：

```yaml
restore_suite:
  enable_regions:
  structures_file:
```

按 region 配置。

### 4.2 离线 compare 工具

当前新增 `cmd/nuverse-restore-compare`，用于本地验证：

- 读取 Avro schema。
- 支持 msgpack 样本和 raw upload 样本。
- raw upload 可通过本地 `sekai_client` shared AES 配置解密解码。
- 输出恢复结果 diff/report，不写 Mongo、不改上传 API。

本地真实样本 `7445104842642643749` 仅用于人工验证，不提交仓库。

### 4.3 Compact restore 兼容层

compact restore 展开算法已经抽到 `utils/compactrestore`：

- `utils/api/data` 负责 BSON 适配，供 public/private/oauth2 查询路径恢复 Mongo 中的 `compact*` 字段。
- `utils/nuverse` 负责 orderedmap 适配，保留离线 master restorer 的 enum / compact 行为。

这两条路径不和 StructTool suite restore 冲突：

- StructTool suite restore 发生在上传预处理、Mongo 写入前。
- API compact restore 发生在数据读取时，用于兼容 Mongo 中已有 compact 字段。
- `utils/nuverse.NuverseMasterRestorer` 当前仍是离线/测试参考，不接入上传 runtime。

### 4.4 Nuverse compact/master restorer

`utils/nuverse` 已经具备更完整的恢复能力：

- 读取结构定义
- 处理 `_compact*` 前缀数据
- 恢复 enum 列
- 恢复结构化数组数据
- 对部分字段做 ID merge

这套能力目前更像独立 restorer。后续如果要删除或替换它，应先确认 master data compact 场景和 API 查询 compact 恢复都不受影响。

---

## 5. StructTool v2 可行性判断

Haruki-Nuverse-StructTool v2 的核心能力是：

- 读取 Unity MsgpackSchemaExporter 产出的 custom Avro schema
- 根据 Avro record / array / map / union 和 `msgpack_key` 描述 decode compact msgpack
- 支持 string-keyed map 和 int-keyed compact array
- 支持 encode 回 compact msgpack 做 round-trip 验证

它更适合作为“结构来源生成器”和“结构正确性校验器”，而不是直接放进后端上传请求链路。

原因：

- 上传路径需要稳定、低延迟、可回退
- Avro schema 和游戏版本、region、class name 有版本漂移风险
- 运行时动态解析 schema 会增加失败模式
- 结构生成可以离线完成，生成结果再由后端加载

---

## 6. Suite Restore 后续收口方案

### 阶段 0：已完成离线验证入口

已新增 `cmd/nuverse-restore-compare`，用于离线验证 StructTool/Avro schema 恢复结果。

已完成：

- 不写 Mongo。
- 不改变上传 API。
- 支持 raw upload 样本解密解码。
- 可用本地真实样本验证 StructTool schema 生成结果。

### 阶段 1：已完成 StructTool schema 运行时接入

已完成：

- `data/suite_user.avsc` 作为运行时结构文件。
- `utils/nuversestruct` 负责从 Avro schema 生成 suite restore definitions。
- `utils/handler/suite_restore.go` 只通过 StructTool/Avro schema 加载 restorer。
- 旧 `data/suite_structures.json` 已移除。

### 阶段 2：补正式 schema 生成说明

schema 生成依赖本地 `DummyDll` 和外部 exporter，不把 Unity DLL 或真实 raw sample 放入仓库。

当前推荐流程：

```bash
# 1. 准备 exporter
git clone --branch main https://github.com/middlered/unity-msgpack-schema-exporter.git /tmp/unity-msgpack-schema-exporter
cd /tmp/unity-msgpack-schema-exporter
dotnet build UnityMsgpackSchemaExporter.Cli/UnityMsgpackSchemaExporter.Cli.csproj

# 之后回到 Haruki-Toolbox-Backend 项目根目录执行
cd /path/to/Haruki-Toolbox-Backend

# 2. 确认 root class；本地 DummyDll 路径不提交
dotnet run --project /tmp/unity-msgpack-schema-exporter/UnityMsgpackSchemaExporter.Cli -- list SuiteUser --dll ~/Desktop/pjskida/tw/DummyDll

# 3. 生成运行时 schema
dotnet run --project /tmp/unity-msgpack-schema-exporter/UnityMsgpackSchemaExporter.Cli -- avro Sekai.SuiteUser --dll ~/Desktop/pjskida/tw/DummyDll > data/suite_user.avsc

# 4. 确认后端 adapter 可消费该 schema
go run ./cmd/nuverse-restore-compare -schema data/suite_user.avsc -generate-only
go test ./utils/nuversestruct ./utils/suiterestore ./utils/handler
```

离线行为校验可以继续使用 StructTool v2：

```bash
git clone --branch v2 https://github.com/Team-Haruki/Haruki-Nuverse-StructTool.git /tmp/Haruki-Nuverse-StructTool-v2
cd /tmp/Haruki-Nuverse-StructTool-v2
go run . --schema /path/to/data/suite_user.avsc --class Sekai.SuiteUser --hex <compact-msgpack-hex>
```

真实 raw upload 样本只用于本地人工验证：

```bash
go run ./cmd/nuverse-restore-compare \
  -schema data/suite_user.avsc \
  -sample 7445104842642643749 \
  -input-format raw-upload \
  -server jp
```

验收标准：

- 新 schema 生成方式可复现。
- 输出稳定，适合 code review。
- 不要求把真实 `DummyDll` 或真实 raw sample 放入仓库。

### 阶段 3：补小型 golden fixture

当前已经有 parser/generation 单元测试、generated structures golden 和 compare report golden；真实 raw upload 样本不应提交。

- 最小 Avro schema。
- 最小 compact msgpack payload。
- compare report golden。

已完成：

- 已有最小 Avro schema 与 generated structures golden。
- 已补 compare report golden，确保离线 compare 输出在 CI 中可审阅。

验收标准：

- 不依赖本地 `7445104842642643749`。
- 可以在 CI 中验证 StructTool adapter 基本行为。

### 阶段 4：评估 `utils/nuverse` 的去留

`utils/nuverse` 当前没有接入上传 runtime，但仍有独立 tests 和 master compact 逻辑。

可选方向：

- 保留为离线/reference 包，并补 package comment。
- 删除未使用的 master restorer，但保留 API 查询所需的 `utils/compactrestore`。
- 如果未来 master data 也要恢复，另开阶段评估，不混入 suite upload restore。

当前默认采用第一种：`utils/nuverse` 保留为离线/reference master restorer；上传 runtime 继续走 `utils/nuversestruct` + `utils/suiterestore`。

### 阶段 5：考虑统一 `RestoreSuite` 门面

为上传路径提供统一接口：

```go
type SuiteRestoreReport struct {
    Region string
    Source string
    RestoredFields int
    FailedFields []string
}

func RestoreSuite(
    server utils.SupportedDataUploadServer,
    data map[string]any,
) (map[string]any, SuiteRestoreReport, error)
```

内部当前应包裹 StructTool/Avro schema 生成的 `utils/suiterestore.Restorer`。

验收标准：

- 上传路径只依赖统一接口
- restore 失败时能选择保留原始数据或返回明确错误。
- 日志包含 region、结构来源和失败字段

---

## 7. 风险与约束

### 7.1 不要在上传请求中直接依赖外部仓库

StructTool v2 不应作为运行时外部命令被调用。

推荐方式：

- vendor / copy 必要 parser 代码，或重新实现最小生成逻辑
- 生成结果作为配置或构建产物输入
- 运行时只读稳定结构文件

### 7.2 样本数据不能重新污染仓库

`suite_rec/` 这种大批本地数据应作为本地输入或脱敏 fixture 管理。

如果要提交测试样本，应满足：

- 数量少
- 脱敏
- 命名清楚
- 放在明确的 `testdata/` 下

### 7.3 结构生成要可复现

生成工具输出必须稳定：

- map key 排序稳定
- JSON indentation 稳定
- region 和 class 映射显式配置
- 生成命令文档化

### 7.4 不要破坏第三方同步格式

当前第三方同步目标存在 `restore_suite_*` 配置。

改动 suite restore 时必须确认：

- 本地 Mongo 存储格式
- 第三方同步格式
- `send_json_zstandard_*` 路径
- 公开 API 读取路径

没有被混在一起意外改变。

---

## 8. 建议执行顺序

1. 继续审查 StructTool schema 生成结果；如果游戏版本更新，按文档流程重新生成 `data/suite_user.avsc` 并跑 compare/test。
2. 明确但暂不提交本地未跟踪项：`7445104842642643749`、`8D6B...`、`cmd/suite-rec-backfill/`、`registration-handoff.cn.md`、`suite_rec/`。
3. 下一批大文件拆分优先从 `adminwebhook` 开始，再处理 `harukibotneo`、`userprivateapi`、`adminusers`。
4. 若继续治理 suite restore，再评估是否引入统一 `RestoreSuite` 门面。

---

## 9. 推荐验证命令

重构 Ory / session 时：

```bash
go test ./utils/api ./internal/modules/oauth2 ./internal/modules/userauth ./internal/modules/userpasswordreset ./internal/modules/userprofile
```

重构 bootstrap / config 时：

```bash
go test ./config ./internal/bootstrap
```

重构 suite restore 时：

```bash
go test ./utils/suiterestore ./utils/nuverse ./utils/handler
```

涉及上传和第三方同步时：

```bash
go test ./internal/modules/upload ./utils/handler
```

涉及 Ory、OAuth2、Auth Proxy 或公开数据格式的大范围调整时：

```bash
go test ./...
```
