# Toolbox 前端：读写授权与委托上传对接

本文件对应当前分支后端实现；前端页面未实施，生产上线状态需另行确认。

## 1. 用户流程与文案

B 在已验证的游戏账号上选择工具箱用户 A，分别配置 suite / mysekai 的「只读」「只写（允许代传）」「读写」和到期时间。profile 仅允许只读。现有 mysekai_birthday_party 增量上传沿用 mysekai 写权限，不新增独立授权类型。

提示：「只写允许对方上传并更新该游戏账号的数据，不允许读取已有数据，也不允许修改绑定、隐私设置或转授。」上传会更新目标账号的数据，应在授予写权限前清楚展示目标区服和游戏账号。

A 手动上传时可选择本人账号或获写授权账号。抓取数据的方式不参与授权判断。HarukiProxy/其他 OAuth2 应用通过 token 识别 A，iOS 脚本通过用户码识别 A；它们与手动上传使用同一账号写权限。iOS 模块代理不关联工具箱用户，保持既有流程，不为它增加授权页面。

## 2. 授权接口

沿用现有登录会话，经 Oathkeeper 访问。路径 toolbox_user_id 必须为当前登录用户；只有账号 verified binding 的当前所有者可以管理授权。

```http
GET /api/user/:toolbox_user_id/game-account-grants
GET /api/user/:toolbox_user_id/game-account-grants/received
PUT /api/user/:toolbox_user_id/game-account-grants/:server/:game_user_id/:data_type/:grantee_user_id
DELETE /api/user/:toolbox_user_id/game-account-grants/:server/:game_user_id/:data_type/:grantee_user_id
```

PUT 示例：

```json
{"expiresAt":"2026-11-01T00:00:00+08:00","permissions":["write"]}
```

- permissions 支持 `["read"]`、`["write"]`、`["read","write"]`；顺序无关，重复归一化。
- 空数组、未知权限、profile write 返回 400。
- expiresAt 必须为未来时间；整条授权共用一个到期时间。
- 新前端始终显式传 permissions。旧请求缺省/null：创建时为 read，更新时保持当前权限，仅更新有效期。
- 显式数组完整替换原权限。取消全部权限请 DELETE，不发送空数组。
- 禁止给自己授权；被授权者须存在且未封禁。

列表的 updatedData.items 与更新响应的 updatedData.grant 均增加 permissions，其他 id、ownerUserId、granteeUserId、server、gameUserId、dataType、expiresAt、createdAt、updatedAt 字段保留。

列表页分「我授予的」和「我收到的」，显示数据类型、权限和有效期。收到的授权不能转授或管理原授权。历史授权均为 read-only，不默认勾选 write。权限修改成功后重新拉取列表，避免保留过期表单值。

## 3. 手动上传账号选择器

```http
GET /api/user/:toolbox_user_id/accessible-game-accounts?action=write
```

仍使用统一 updatedData 包装：

```json
{"status":200,"message":"ok","updatedData":{
  "generatedAt":"2026-10-06T00:00:00Z","total":1,"accounts":[{
    "server":"jp","gameUserId":"456","ownership":"granted","verified":true,"isDefault":false,
    "capabilities":{},"writeCapabilities":{"suite":{"expiresAt":"2026-11-01T00:00:00Z"}},"owner":null
  }]
}}
```

- 上传页面仅以 writeCapabilities 中存在的 suite/mysekai 为可上传依据。不要按 ownership 硬编码本人才能上传。
- action 默认 read；原来的读页面继续使用 capabilities，返回行为不变。
- action=write 不包含 profile/recommend 能力，capabilities 为空；不展示所有者私人信息。
- 未验证本人绑定可能保留在列表，但没有可写能力，须禁用选择。
- 游戏 ID 用字符串，不转 JavaScript Number。
- 列表不代替最终鉴权；遇到权限拒绝，刷新列表并提示授权可能已失效，不清空整个登录会话。

提交路径保持：

```http
POST /api/manual/:server/:user_id/:data_type/upload
Content-Type: application/octet-stream

<原始游戏载荷>
```

身份仍为当前浏览器登录用户，不增加 OAuth2 登录要求，也不在请求体指定所有者。服务器校验本人或 write grant，并核对载荷账号；前端不要自动改绑目标账号。

## 4. OAuth2 授权页面

继续现有 login/consent 流程。game-data:write 的说明应包含「上传你拥有或获写授权的游戏账号数据」；station:room:write 同样是写权限（「以你的身份向 Sekai Station 提交车牌」），同意页与设备审核卡都标红。game-data:read 与 write 独立展示；offline_access 说明后台持续授权用途。用户不授予 read 时不能暗中补选。

login / consent 端点还有以下拒绝情形，页面沿用现有的失败提示即可，不需要新文案。需要区分时读 `updatedData.code`；`message` 只是给人看的英文短句，不作为判断依据：

- consent accept（`POST /api/oauth2/consent/accept` 和旧版 `POST /api/oauth2/authorize/consent` 的同意分支）在 client 已被管理员停用或已删除时返回 403，`updatedData.code` 为 `client_disabled`；后端查询 client 失败时返回 503，`message` 为 `oauth2 client validation unavailable`，不带 `updatedData`。
- login 与 consent 的查询、接受、拒绝端点遇到设备授权模式的 challenge 时返回 403，`updatedData.code` 为 `device_flow_challenge`。正常的浏览器授权不会出现这种 challenge：设备授权不经过这两个页面，由 `/device` 页面和后端的设备端点完成（接入方契约见 [OAuth2 / OIDC 接入](oauth2-integration.zh-CN.md) §4A，`/device` 依赖的后端端点与错误码见 [Ory 套件使用说明](ory-suite-usage.zh-CN.md) §10.5.5）。
- login accept 不接收 `acr`，传了也会被忽略；前端不应发送。

OAuth2 应用可通过 `GET /api/oauth2/game-data/upload-targets` 获取可写目标，要求 game-data:write + bindings:read；响应见 [HarukiProxy 对接](harukiproxy-v3-client-integration.zh-CN.md)。该接口不会返回已保存的游戏数据。

## 5. 管理统计页面对接设计

沿用上传日志和 upload-analytics 接口。新增上传日志字段：actorUserId、authMethod、authorizationSource、grantId；原 toolboxUserId 表示数据所属用户。展示「所属用户」与「实际上传者」，不可混成一列。

- authMethod：browser_session / oauth2 / ios_user_code / game_session_proxy；历史或旧入口可能为空。
- authorizationSource：owner / grant；无工具箱用户授权的模块代理不填。
- grantId：用于历史审计快照，不保证对应授权仍存在。
- oauthClientId、uploadMethod、版本、平台与 requestId 继续展示。
- iOS 模块代理不伪造 actor，不将其显示成账号所有者亲自上传。
- 无权限或未验证身份的记录可能缺少所属用户；页面不能用 claimedGameUserId 自行关联用户。

服务器负责角色范围过滤；前端不得通过拼接条件替代角色权限。统计仍按已有支持的 group_by 维度查询，本次不新增 actor/grant 分组维度。

## 6. 错误与交互

授权管理错误使用现有 status/message；权限数组非法为 400。上传目标不可写时拒绝，不区分账号不存在或属于他人。不要把 403 当作登录过期；只有明确身份失效才走登录流程。

手动上传及 iOS 脚本保留现有回执格式，不假设它们已拥有 HarukiProxy 专用 error_code。iOS 分片收到成功回执只表示接收/入队，不表示最终写入成功；不得展示未经确认的最终完成状态。

## 7. 前端验收

- 历史 read-only 授权展示正确，新建默认不选 write。
- 三种权限可创建、更新、撤销；profile 禁用 write。
- A 获得 B 的 suite write 后可选择并上传 B/suite，不能读取 B 的数据或上传 B/mysekai。
- 只写账号不会出现在默认读选择器中，不产生 recommend 能力。
- 授权到期、撤销、绑定变化后刷新目标；不无限重试或错误触发重新登录。
- 审计正确区分 actor 与 owner；iOS 模块代理界面不新增登录/授权要求。

部署前需执行 `docs/upload-write-grants-schema.sql`（此前 v3 统计迁移仍需保留），禁止回滚到把所有 grant 都当成读权限的旧后端。前端可先保持旧版，后端会保留缺省 permissions 的兼容语义。
