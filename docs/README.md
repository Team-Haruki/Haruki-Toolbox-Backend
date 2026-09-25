# 文档索引

本目录按读者分组。找不到入口时先看这里，不要凭文件名猜。

## 给外部集成方

对接 Haruki Toolbox 的第三方开发者从这里开始。

| 文档 | 回答什么问题 |
| --- | --- |
| [用 Haruki 账号登录](oidc-provider.zh-CN.md) | **想让用户用 Haruki 账号登录自己站点的外部服务商看这篇。**issuer、client 申请、ID Token 校验、登出，以及一处必须绕开的 Discovery 偏差 |
| [OAuth2 / OIDC 接入](oauth2-integration.zh-CN.md) | OAuth2 客户端接入：公开与保密两种客户端、授权码流程、token 与刷新、用户信息与绑定、游戏数据读取与**代理上传**、数据更新 Webhook、可申请的 scope |
| [Public API Webhook 接入](webhook-integration.zh-CN.md) | 基于 token 自行订阅具体游戏账号的旧版 webhook |

## 给站内前端

| 文档 | 回答什么问题 |
| --- | --- |
| [游戏账号数据授权](game-account-data-grants.zh-CN.md) | 把自己的账号数据授权给其他 Toolbox 用户；可访问账号聚合接口的字段与语义 |

## 给本项目开发者

| 文档 | 回答什么问题 |
| --- | --- |
| [后端架构与渐进重构约定](backend-architecture.zh-CN.md) | 目标目录结构、依赖方向、模块边界。代码评审的架构基线 |
| [JSON 与数字精度约定](json-conventions.zh-CN.md) | JSON v2 的字段匹配、nil 表示、大整数精度及生成代码维护 |
| [MessagePack codec 与 OrderedMap](msgpack-codec.zh-CN.md) | 共同字节游标、旧包退役、provider 字段规则、小对象合并分配和本机基准 |
| [游戏数据加密配置](game-data-crypto.zh-CN.md) | 按区服配置 crypto key/iv；9.0.0 配置迁移要求 |
| [iOS 模块 URL 重写](ios-url-rewrite.zh-CN.md) | Surge/Loon/Stash 透明转发与 Quantumult X 307 兼容策略 |
| [MYSEKAI 采集数据复原](mysekai-restore.zh-CN.md) | CN 6.4.0 schema、上传与历史读取、TW/KR 按区服切换及缓存发布要求 |
| [Ory 套件使用说明](ory-suite-usage.zh-CN.md) | Kratos / Hydra / Oathkeeper 各自的职责、登录态验证方式、社交登录（Google / Apple）接入、可信代理与转发 IP 的取值规则、为什么大量旧接口返回 410 |
| [爱发电赞助集成](afdian-sponsor-integration.zh-CN.md) | 赞助墙的 webhook 与同步行为 |

## 待实施设计

| 文档 | 状态 |
| --- | --- |
| [数据 revision 与缓存失效设计](game-data-revision-design.zh-CN.md) | 同秒旧缓存复现、数据库版本原型、条件读取和分阶段发布约束；尚未接入生产 |

已完成的一次性迁移计划、调研流水账和旧部署记录不在此保留，可通过 Git 历史查阅。
