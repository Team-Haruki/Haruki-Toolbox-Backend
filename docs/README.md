# 文档索引

本目录按读者分组。找不到入口时先看这里，不要凭文件名猜。

## 给外部集成方

对接 Haruki Toolbox 的第三方开发者从这里开始。

| 文档 | 回答什么问题 |
| --- | --- |
| [OAuth2 客户端接入](oauth2-client-integration.zh-CN.md) | 授权码流程、token、用户信息与绑定、游戏数据读取与**代理上传**、可申请的 scope。公开客户端与整体流程的主文档 |
| [OAuth2 机密客户端接入](oauth2-confidential-client-integration.zh-CN.md) | 持有 client secret 的服务端集成与公开客户端的差异 |
| [OAuth2 Webhook 接入](oauth2-webhook-integration.zh-CN.md) | 用户数据更新时如何收到回调，基于 OAuth2 consent，不要求 `allowPublicApi` |
| [Public API Webhook 接入](webhook-integration.zh-CN.md) | 基于 token 自行订阅具体游戏账号的旧版 webhook |

## 给站内前端

| 文档 | 回答什么问题 |
| --- | --- |
| [前端对接补充说明](frontend-integration-notes.zh-CN.md) | 各功能对接要点的集中入口：游戏账号选择器数据源、账号数据授权、admin 集成管理 |
| [游戏账号数据授权](game-account-data-grants.zh-CN.md) | 把自己的账号数据授权给其他 Toolbox 用户；可访问账号聚合接口的字段与语义 |

## 给本项目开发者

| 文档 | 回答什么问题 |
| --- | --- |
| [后端架构与渐进重构约定](backend-architecture.zh-CN.md) | 目标目录结构、依赖方向、模块边界。代码评审的架构基线 |
| [Ory 套件使用说明](ory-suite-usage.zh-CN.md) | Kratos / Hydra / Oathkeeper 各自的职责、登录态验证方式、为什么大量旧接口返回 410 |
| [HarukiBot NEO 注册与凭据重置](haruki-bot-neo-registration.zh-CN.md) | Bot 侧注册与凭据重置流程 |
| [爱发电赞助集成](afdian-sponsor-integration.zh-CN.md) | 赞助墙的 webhook 与同步行为 |

## 进行中的计划

计划文档只在对应工作还没做完时保留；做完即删除，历史留在 git 里。

| 文档 | 状态 |
| --- | --- |
| [数据库合并计划](database-consolidation-plan.zh-CN.md) | 进行中。代码部分已完成；切换读取来源、下线 MongoDB 需要停机窗口 |
