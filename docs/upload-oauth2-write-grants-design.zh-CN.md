# 上传 OAuth2 与游戏账号读写授权方案

状态：已按确认范围实现于工作分支，等待部署与客户端联调。日期：2026-10-06。

本方案调整尚未发布的 HarukiProxy v3 鉴权方向，并统一手动、脚本、HarukiProxy 和通用 OAuth2 上传入口的账号权限；iOS 模块代理明确排除。此前的 v3 UA、请求 ID、错误码和统计设计继续沿用；固定客户端 secret、外层固定密钥加密和“OAuth2 只能上传本人账号”的约定由本方案替代。实现后的接口以 [HarukiProxy 对接](harukiproxy-v3-client-integration.zh-CN.md) 和 [前端对接](toolbox-upload-grants-frontend.zh-CN.md) 为准，不代表生产已切换。

## 1. 目标与边界

- 取消 HarukiProxy 的工具箱共享鉴权密钥及外层固定 AES-GCM 密钥，使用 HTTPS 与 OAuth2。iOS 脚本保留现有用户码，通过服务端查询用户码关联的工具箱用户识别实际操作者；用户码不是所有客户端共用的固定密钥。
- 游戏账号所有者 B 可以授权工具箱用户 A 读取、上传或读写指定账号的 suite / mysekai。
- A 获得数据后，通过手动上传、iOS 脚本、HarukiProxy 或其他 OAuth2 应用上传，采用相同的账号写权限规则。
- 不定义“代抓权限”，不判断抓取者。iOS 模块代理不关联工具箱用户，只按现有流程校验游戏账号有效性及是否能抓取数据，排除在本次 OAuth2 和用户读写授权改造之外；不为它新增登录、token、owner/write grant 前置要求。
- 不改游戏协议自身的编解码配置，不移除服务端 Oathkeeper 信任密钥或保密 OAuth2 客户端凭据。
- 本轮不实现前端页面；提供页面和客户端后续接入所需的接口、契约及测试。

## 2. 现状及需要替换的位置

当前仓库已有 Hydra OAuth2、公开客户端 PKCE、game-data:write scope，以及：

`POST /api/oauth2/game-data/:server/:data_type/:user_id`

该入口当前调用读取权限查询后显式拒绝 ViaGrant，因此需要改为独立的写权限判定，不能简单删除拒绝分支。现有 GameAccountDataGrant 按所有者、被授权者、区服、游戏账号、数据类型唯一，只有 expires_at，没有读写动作。已有授权支持 suite / mysekai / profile，其中 profile 为实时读取能力。

上传公共流程还包含所有权和账号策略判断，必须同步改造，避免入口放行后被底层“必须本人”规则拦截，或底层绕过入口的授权检查。iOS 模块代理未传入工具箱操作者是现有设计，不作为待修复缺口。公共流程改造应显式区分其服务端入口模式，保持该代理路径现有行为；不得把“操作者为空”做成其他上传入口可利用的权限绕过。

## 3. 权限模型

### 3.1 两层授权取交集

浏览器手动上传：有效登录会话 + 目标账号写权限。

iOS 脚本上传：有效用户码 → 查询关联的工具箱用户 A → 检查目标游戏账号是否属于 A → 若不属于，查询 A 对该账号及数据类型是否有有效 write grant → 允许或拒绝写入。用户码关联用户必须由服务端解析，不能使用请求中自报的工具箱用户 ID。不额外要求 OAuth2 scope。

OAuth2 上传：有效 access token + 当前客户端可用 + game-data:write scope + 目标账号写权限。

目标账号写权限成立条件：操作者为有效绑定所有者，或持有该所有者授予的有效 write grant。所有者与操作者均须满足封禁等现有账号策略。绑定须有效且已验证；已有例外若存在，应在实施时逐一列出并迁移，不能以“某入口可信”为理由绕过统一判断。

OAuth2 subject 始终表示实际操作者 A，不变成 B。目标所有者 B 从当前绑定解析，不能相信请求体传入的 owner_user_id。Hydra subject 保持现有 Kratos ID 优先、本地 user ID fallback 的兼容规则。

### 3.2 Grant 数据模型

在现有授权记录增加：

| 字段 | 类型 | 规则 |
| --- | --- | --- |
| can_read | boolean，非空 | 历史记录迁移为 true |
| can_write | boolean，非空 | 历史记录迁移为 false |

保留现有唯一键和 expires_at；一条记录内两种动作共用有效期。首期不增加独立到期时间，避免重复授权记录及复杂的撤销语义。

- suite / mysekai 支持 read、write、read+write。
- profile 继续仅支持 read；profile+write 返回参数错误。
- write 不隐含 read；不能借上传响应、账号列表、统计或 webhook 获取已有游戏数据。
- 至少选择一种动作；撤销整条授权使用 DELETE。
- 仅当前 verified binding 所有者可管理授权，不允许转授，不允许授权给自己。
- 绑定删除、转移或失去验证状态后，旧所有者创建的授权立即失效；查询实时核对 owner 与当前绑定。
- 不因 write 授权赋予修改绑定、隐私设置、凭据、授权、默认账号或删除游戏数据的权限。

示例：B 授予 A 对 jp/123 的 suite write。A 使用有 game-data:write scope 的应用可上传 jp/123 suite，不能读取它，也不能上传该账号 mysekai 或其他账号。

### 3.3 数据库访问与平台层职责

数据库层提供带显式 action 的权限查询，例如 ResolveGameAccountDataAccess(actor, server, gameID, dataType, action, now)。现有读取 helper 保留为 read 包装，逐步迁移调用者，禁止默认把“存在 grant”视为读写都允许。

返回可信的权限决策：actor、当前 owner、目标、action、owner/grant 来源、grant ID、有效期。平台上传层对本次改造范围内的入口统一应用该决策与封禁、区服、数据类型策略，各入口仅适配身份、传输及元数据。iOS 模块代理通过独立的服务端入口模式保留现有处理，不伪造工具箱操作者或授权决策。

决策不由客户端提交。数据库工具层不反向依赖 internal/platform。

## 4. OAuth2 与客户端授权

### 4.1 原生应用

HarukiProxy 注册 public client，采用授权码 + PKCE S256，经系统浏览器登录与同意授权。校验 state，使用精确登记的 redirect URI；移动端优先应用链接，桌面端按平台支持选择受控回调。发布前验证 Hydra 与当前客户端登记校验对原生回调的支持。

最小上传 scope 为 game-data:write。需要刷新时请求 offline_access；需要账号选择器时请求 bindings:read。不默认请求 game-data:read、profile 或 email。

应用安全保存 refresh token，按平台使用安全存储；刷新过程串行化，防止并发刷新覆盖 token。401 最多触发一次刷新和重试；刷新失败暂停上传并重新授权，不能循环重试。具体 token 有效期、刷新轮换和撤销行为沿用并验证 Hydra 配置，不在客户端假设固定时长。

client_id 为公开标识，不是密钥。UA 仅作版本策略与统计；应用身份以通过验证的 token client_id 为准。公开客户端身份也不构成官方二进制或数据真实性证明。

### 4.2 iOS 脚本用户码（保留现有机制）

iOS 脚本不迁移到 OAuth2，继续使用现有用户码。服务端先验证用户码并查询关联的工具箱用户，得到实际操作者，再执行统一账号写权限检查。用户码不存在、失效或关联用户不可用时拒绝请求，不能退回无身份上传。

目标游戏账号属于该用户时，按现有账号策略允许上传；不属于时查询目标账号当前所有者授予该用户的 write grant，按 server、game_user_id、data_type 和有效期判定。只读授权不能上传。权限判定后仍执行载荷与目标账号一致性校验。

用户码保留现有传递与管理方式，本轮不新增脚本 OAuth2 安装、授权码回调、refresh token 或 scope 要求。日志与审计不记录用户码明文。用户码撤销或关联关系变化后，后续请求及尚未执行的任务应重新验证，不继续使用失效身份。

iOS 模块代理不适用上述用户码流程，也不新增工具箱 OAuth2 头；保持现有模块安装、游戏请求转发及游戏账号有效性与可抓取性校验。

## 5. 各入口的目标行为

| 入口 | 操作者来源 | 请求载荷 | 账号权限 |
| --- | --- | --- | --- |
| 手动上传 | 现有 Kratos/Oathkeeper 会话 | 原始游戏载荷/现有文件格式 | owner 或 write grant |
| HarukiProxy v3 | OAuth2 Bearer | 原始游戏载荷，取消工具箱外层固定 AES 封装 | scope + owner 或 write grant |
| 通用 OAuth2 上传 | OAuth2 Bearer | 原始游戏载荷 | scope + owner 或 write grant |
| iOS 脚本/分片 | 用户码查询关联工具箱用户 | 原始游戏载荷或分片 | owner 或 write grant，不要求 OAuth2 scope |
| iOS 模块代理（排除） | 不关联工具箱用户 | 现有游戏代理请求/响应 | 保留现有游戏账号有效性与可抓取性校验，不接入 OAuth2 / grant |

HarukiProxy v3 可保留现有路径作为薄适配器，复用通用 OAuth2 上传逻辑，附加严格 UA/版本策略。不能再维护独立的账号鉴权逻辑。其他客户端不必伪装 HarukiProxy UA。

对引继上传等其他关联工具箱用户且会产生数据写入的入口进行清单审计，同样接入公共写权限检查；现有游戏登录流程和凭据管理权限保持独立，不新增代抓权限或自动授予凭据读取权限。

本次改造范围内的统一入口流程（不适用于 iOS 模块代理）：身份验证 → 目标参数及上传约束 → 账号写权限 → 有界解码/游戏账号一致性检查 → 写入前复核权限 → 持久化 → 审计与通知。

游戏载荷使用公开协议密钥，内容仍视为不可信。OAuth2、write grant、可解密均不是游戏数据真实性证明；保留深度、长度、字段与账号一致性校验。

### 5.1 分片、异步与撤销

分片会话绑定 actor、凭据来源、server、game ID、data type、upload ID；OAuth2 请求额外绑定 client，iOS 脚本绑定服务端用户码记录标识而非明文。不能跨操作者或凭据来源拼接。创建会话及每个分片请求分别验证 OAuth2 token 或脚本用户码，拒绝目标变化，保留总大小、分片数、TTL 与并发限制。

任务入队保存服务端生成的操作者和目标描述，不把过期 bearer token 当作任务身份存入队列。执行前及写入前按身份来源重新检查 OAuth2 客户端/授权会话状态或脚本用户码有效性及用户关联，同时检查 grant 有效期、绑定所有者及封禁状态；不能只保存“已授权=true”。撤销 OAuth consent 后任务也应失效，实施时需要可重新验证的授权会话引用或取消标记。

撤销响应完成后新开始的权限检查必须拒绝旧授权。已通过最后检查且正在提交的数据可能完成；Toolbox 权限库与游戏数据存储之间不能宣称跨库零竞态撤销。若未来要求严格阻断所有在途提交，需额外的跨实例同步设计，首期不作此承诺。

## 6. 授权管理与账号列表 API

沿用现有授权管理路由，创建/更新请求增加 permissions：

```json
{
  "expiresAt": "2026-11-01T00:00:00+08:00",
  "permissions": ["read", "write"]
}
```

新客户端必须显式提交权限。兼容旧页面：创建时缺省 permissions 表示 read；更新已有记录时缺省 permissions 保持原权限，只更新时间，避免旧页面无意升级或清除 write。显式权限数组采用完整替换语义，拒绝空数组、未知值；重复值归一化。服务端审计记录变更前后权限和有效期。

授权列表返回 permissions。现有 accessible-game-accounts 默认仍返回可读账号及原 capabilities，避免旧前端因只写账号出现而误判可读。

增加 action=write 查询模式：返回可写账号及独立 writeCapabilities，仅含必要的目标标识和有效期，不返回游戏数据或所有者私人信息。OAuth2 提供同等账号发现能力；需 bindings:read，并按 action 额外验证 game-data:write。列表仅作选择器提示，上传时重新鉴权。

已有 recommend 派生能力保持由读取权限推导；write 不产生 recommend/profile/read 能力。游戏数据读取端、OAuth2 webhook 订阅与投递均继续检查 read，不得通过 write 间接订阅数据。

## 7. 响应、重试与审计

继续使用统一 updatedData 响应，包括 request_id、error_code、retryable，所有正常应用层成功/失败响应返回 X-Request-ID。网关/传输层错误不保证相同 JSON，客户端应处理非 JSON 响应。

| 情况 | HTTP | 客户端动作 |
| --- | --- | --- |
| token 缺失、失效或撤销 | 401 | 最多刷新一次；失败后重新授权 |
| scope 不足 | 403 | 请求重新授权，不自动重试 |
| 目标不存在/不可写/grant 失效 | 403 | 统一 upload_not_allowed，不区分账号存在性 |
| 载荷错误或账号不匹配 | 400 | 不重试，检查目标/抓取结果 |
| 限流 | 429 | 按 Retry-After 有限退避 |
| 写入前暂时不可用 | 503 | 仅 retryable=true 时有限重试 |
| 提交结果不确定 | 500 或网络中断 | 不声明安全重试；按现有有限策略避免盲目重复提交 |

审计保留 owner_user_id 与 actor_user_id，不把被授权者记作账号所有者。增加 authorization_source、grant_id 快照、oauth_client_id 和 auth_method；继续保存 request ID、上传来源、版本平台、失败阶段等字段。授权记录删除后审计快照仍可追溯，不级联删除历史。

所有者可以看到谁向自己的账号提交了数据；操作者只能看到自己的提交回执和必要结果。管理员查询必须同时考虑涉及的账号所有者和操作者，避免普通管理员看到超级管理员参与的敏感记录。不得记录 token、游戏会话凭据或原始敏感头。

upload_method 表示上传方式，auth_method 对本次改造入口表示 browser_session/oauth2/ios_user_code；二者分开。iOS 模块代理单独标识为 game_session_proxy，actor_user_id、oauth_client_id 和 grant_id 为空，不把数据所属用户当作实际操作者，也不把游戏会话校验记为工具箱用户授权。HarukiProxy 来源结合已登记 OAuth client 分类，UA 不作为权威；所有者的隐私设置、缓存、数据更新通知继续随目标账号处理。write grant 本身不增加操作者的 webhook 接收权限。

## 8. 迁移与发布

当前状态：先前 v3 统计增量字段已迁移到生产，后端仍为旧版本。PR #94 尚未合并；其固定密钥 v3 方案不能按原计划发布。本方案无需回退已添加的统计字段。

实施顺序：

1. 补齐 grant 读写 schema、迁移及公共权限查询；历史授权严格迁移为 read-only，重新生成 Ent。
2. 迁移所有读取调用，确认 write-only 无法读取；更新授权管理与账号发现接口。
3. 统一上传流程与审计，接入 OAuth2，让手动会话、iOS 脚本用户码和 HarukiProxy OAuth2 等身份来源复用账号写权限判断，并回归确认排除的 iOS 模块代理行为不变；保持封禁与载荷校验。
4. 更新 Oathkeeper 所需路由配置：只发布明确的 OAuth2 自鉴权入口，不扩大暴露 internal/private 等其他接口。
5. 更新 HarukiProxy 和脚本协议文档；HarukiProxy 等 OAuth2 应用注册最小 scope 并验证刷新、撤销、回调与安全存储，iOS 脚本验证用户码关联解析及委托写入。
6. 通过完整 CI、真实 PostgreSQL 及多入口联调后部署；客户端启用 OAuth2 版本再验收。
7. 按既定 2026-11-01 00:00（UTC+8）停用 v2。未发布的固定密钥 v3 不提供兼容降级；废弃的共享上传凭据不得作为新入口的兼容旁路。iOS 脚本用户码是保留的用户身份机制，不列入 OAuth2 替换清单；iOS 模块代理也不纳入该迁移清单。

过渡期旧入口仍可能遵循旧权限语义，不能将它们描述成已支持安全委托；新客户端禁止回退。v2 保留至既定截止时间是已有过渡约定，最终状态不保留共享上传密钥，但保留关联工具箱用户的 iOS 脚本用户码。若希望提前关闭全部旧入口，需要另行调整迁移通知与客户端准备时间。

回滚原则：数据库新增权限列保留，不将 write 解释为 read；不回滚到会把 write-only grant 当成读取授权的旧程序。发布前准备兼容新 schema 且保留严格权限的回滚镜像，必要时关闭新上传入口。

## 9. 验收矩阵

- 所有者上传、read-only 拒绝写、write-only 允许写但拒绝读、read+write 双向允许。
- suite / mysekai、区服、游戏账号互不串权；profile write 拒绝。
- token 有 write scope 但无账号授权拒绝；账号有 write grant 但 token 无 scope 拒绝。
- grant 过期/撤销、双方封禁、绑定转移/删除、客户端禁用、OAuth consent 撤销均生效。
- 手动、HarukiProxy、通用 OAuth2、iOS 脚本使用同一授权测试场景；这些入口不接受仅凭游戏会话或 URL 自报身份写入。iOS 脚本必须先用有效用户码解析关联用户，再检查本人账号或 write grant，不要求 OAuth2 token。
- iOS 模块代理无需工具箱登录或 OAuth2，保留现有游戏账号有效性与可抓取性校验；其他入口不能通过缺失身份、请求参数或伪造请求头选择代理专用处理模式。
- 分片不能跨 actor/凭据来源/client（适用时）/目标混用，排队任务执行时权限失效则拒绝。
- 工具箱 token 不泄露到游戏上游、URL、日志、共享脚本或错误响应。
- write-only 不能通过列表、回执、统计、recommend 或 webhook 间接读取游戏数据。
- 历史 grant 维持只读；旧页面更新有效期不改变新权限；旧版本程序不能被用作不安全回滚。
- 统计区分 owner/actor、上传来源/OAuth 客户端，已有请求 ID、UA、错误码和重试行为保持一致。

已同步更新 OAuth2、账号授权及 HarukiProxy 文档；仍需数据库迁移、后端部署和实际客户端联调。iOS 脚本保留用户码，模块代理排除。grant 变更审计记录本请求更新前读取到的权限、更新后的权限与有效期；并发更新时该前置快照不宣称为串行事务历史。OAuth2 上传同步执行，在写入前重新 introspect；iOS 异步任务重新校验用户码和 grant。
