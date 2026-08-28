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
| [Ory 套件使用说明](ory-suite-usage.zh-CN.md) | Kratos / Hydra / Oathkeeper 各自的职责、登录态验证方式、社交登录（Google / Apple）接入、可信代理与转发 IP 的取值规则、为什么大量旧接口返回 410 |
| [爱发电赞助集成](afdian-sponsor-integration.zh-CN.md) | 赞助墙的 webhook 与同步行为 |

## 进行中的计划

计划文档只在对应工作还没做完时保留；做完即删除，历史留在 git 里。

| 文档 | 状态 |
| --- | --- |
| [数据库合并计划](database-consolidation-plan.zh-CN.md) | 已切换：2026-08-29 起从 PostgreSQL 读取，18 个曾被丢弃的键已对外开放（待公告）。观察期后删除旧数据、下线 MongoDB |
