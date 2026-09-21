# 文档索引

本目录按读者分组。找不到入口时先看这里，不要凭文件名猜。

## 给外部集成方

对接 Haruki Toolbox 的第三方开发者从这里开始。

| 文档 | 回答什么问题 |
| --- | --- |
| [用 Haruki 账号登录](oidc-provider.zh-CN.md) | **想让用户用 Haruki 账号登录自己站点的外部服务商看这篇。**issuer、client 申请、ID Token 校验、登出，以及一处必须绕开的 Discovery 偏差 |
| [OAuth2 / OIDC 接入](oauth2-integration.zh-CN.md) | 唯一的 OAuth2 文档：公开与保密两种客户端、授权码流程、token 与刷新、用户信息与绑定、游戏数据读取与**代理上传**、数据更新 Webhook、可申请的 scope |
| [Public API Webhook 接入](webhook-integration.zh-CN.md) | 基于 token 自行订阅具体游戏账号的旧版 webhook |

## 给站内前端

| 文档 | 回答什么问题 |
| --- | --- |
| [游戏账号数据授权](game-account-data-grants.zh-CN.md) | 把自己的账号数据授权给其他 Toolbox 用户；可访问账号聚合接口的字段与语义 |

## 给本项目开发者

| 文档 | 回答什么问题 |
| --- | --- |
| [后端架构与渐进重构约定](backend-architecture.zh-CN.md) | 目标目录结构、依赖方向、模块边界。代码评审的架构基线 |
| [Go 1.27 与 JSON v2 迁移](go127-jsonv2-migration.zh-CN.md) | Go 1.27.1、泛型方法、全面 JSON v2 和自有 MessagePack 有序容器；行为变化及验证记录 |
| [性能优化调研（2026-09-08）](performance-review-2026-09-08.zh-CN.md) | 生产延迟分布、SQL/compact/压缩对照实验、优化优先级与验收边界；排除引继耗时 |
| [MessagePack codec 与 OrderedMap](msgpack-codec.zh-CN.md) | 共同字节游标、旧包退役、provider 字段规则、小对象合并分配和本机基准 |
| [游戏数据加密配置](game-data-crypto.zh-CN.md) | 按区服配置 crypto key/iv；9.0.0 配置迁移要求 |
| [MYSEKAI 采集数据复原](mysekai-restore.zh-CN.md) | CN 6.4.0 schema、上传与历史读取、TW/KR 按区服切换及缓存发布要求 |
| [数据 revision 与缓存失效设计](game-data-revision-design.zh-CN.md) | 同秒旧缓存复现、数据库版本原型、条件读取和分阶段发布约束；尚未接入生产 |
| [Ory 套件使用说明](ory-suite-usage.zh-CN.md) | Kratos / Hydra / Oathkeeper 各自的职责、登录态验证方式、社交登录（Google / Apple）接入、可信代理与转发 IP 的取值规则、为什么大量旧接口返回 410 |
| [爱发电赞助集成](afdian-sponsor-integration.zh-CN.md) | 赞助墙的 webhook 与同步行为 |

## 进行中的计划

计划文档只在对应工作还没做完时保留；做完即删除，历史留在 git 里。

| 文档 | 状态 |
| --- | --- |
| [数据库合并计划](database-consolidation-plan.zh-CN.md) | 2026-09-05 已完成 U11：PostgreSQL 唯一读写源，Mongo / canary 已下线；备份与验收结果见 §0 |
