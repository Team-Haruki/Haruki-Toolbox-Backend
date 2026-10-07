# HarukiProxy 上传维护

main（#94 已合并、尚未发布）的 v3 使用 OAuth2 game-data:write，接收原始游戏载荷；不再读取 v3_secret/v3_unpack_key，不接受共享密钥作为 v3 身份。预发布 v9.0.0-rc4/rc5 中的 v3 仍是固定密钥版本。

- [客户端协议与联调](harukiproxy-v3-client-integration.zh-CN.md)
- [Toolbox 前端读写授权对接](toolbox-upload-grants-frontend.zh-CN.md)
- [方案与权限边界](upload-oauth2-write-grants-design.zh-CN.md)

## 配置与部署

启用 Hydra 并配置现有客户端禁用检查。HarukiProxy 使用已登记的公开 OAuth2 client，需 game-data:write；上传目标查询另需 bindings:read，自动刷新另需 offline_access。原生回调必须先登记并联调。无浏览器的部署若改走设备授权，该 client 还要登记设备授权许可、`devicePolicy.allowWrite=true` 和 user:read scope，并且全站设备授权处于开启状态（[OAuth2 接入](oauth2-integration.zh-CN.md) §10，开关见 [Ory 套件使用说明](ory-suite-usage.zh-CN.md) §10.6.2）。

haruki_proxy.v3_client_policy 保留渠道与最低版本设置；v3 必须携带平台 UA，默认允许 stable/preview/beta/rc/dev 的 3.0.0 对应版本。旧 secret/unpack_key 仅服务过渡期 v2，不能作为 OAuth2 故障降级。

部署前依次执行 `harukiproxy-v3-schema.sql`、`upload-write-grants-schema.sql`，或使用受控 Ent 自动迁移。后端启动会校验新增权限字段和上传审计字段。历史授权迁移为 can_read=true/can_write=false。

先前生产仅完成统计字段迁移；不能据此认为读写授权迁移、OAuth2 v3 部署或设备联调已完成。回滚镜像必须理解 can_read/can_write，禁止回滚到把 write-only grant 当成读权限的旧程序。

Oathkeeper 现有 direct-auth 规则覆盖 HarukiProxy 及 `/api/oauth2/game-data/*`；这些入口在后端自行校验 bearer。手动上传继续使用浏览器可信会话，iOS 脚本保留用户码，iOS 模块代理保持原有游戏账号验证。

v2 在 **2026-11-01 00:00（UTC+8）** 返回 410，Sunset 头为 `Sat, 31 Oct 2026 16:00:00 GMT`。v3 没有旧固定密钥兼容模式。

## 上传日志与管理分析

上传日志按 uploadMethod、版本、平台、协议记录，并区分 toolboxUserId（数据所属用户）、actorUserId（实际操作者）、authMethod、authorizationSource、grantId。未经验证的目标声明不归属到用户；历史记录不回填猜测值。

`GET /api/admin/statistics/upload-analytics` 提供最多 31 天、最多两个白名单维度、可选 hour/day UTC 分桶的统计，包含成功率、已验证活跃账号、延迟、元数据覆盖率和 v2/v3 迁移指标。Redis 入口指标仅为最佳努力总量，不保证完整。普通管理员查询同时排除涉及超级管理员的 owner/actor 记录。

新客户端须先完成后端部署确认，再构建 OAuth2 启用版；原固定密钥 APK 不能直接开启新 UA 后用于新版 v3。

# 生日材料监听

按材料名配置监听时，支持 `diamond`（12）、`yuugiri` / `yugiri`（5）、
`clover`（20）、`battery`（17，电池）、`amethyst` / `quartz`（11，紫水晶，
主数据名称为闪耀石英），以及对应 `mysekai_material_<ID>` 名称。
显式 `material_ids` 仍按 ID 过滤；事件继续返回 `matched_material_ids`；
载荷只保留订阅材料的掉落，以及与这些掉落位置相同的采集点，
不再附带同一采集点或同类采集物的其他掉落。
