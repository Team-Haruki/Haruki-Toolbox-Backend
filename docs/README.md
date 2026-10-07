# 文档索引

本目录按读者分组。找不到入口时先看这里，不要凭文件名猜。

## 给外部集成方

对接 Haruki Toolbox 的第三方开发者从这里开始。

| 文档 | 回答什么问题 |
| --- | --- |
| [用 Haruki 账号登录](oidc-provider.zh-CN.md) | **想让用户用 Haruki 账号登录自己站点的外部服务商看这篇。**issuer、client 申请、ID Token 校验、登出、令牌端点地址的变化，以及一处必须绕开的 Discovery 偏差 |
| [OAuth2 / OIDC 接入](oauth2-integration.zh-CN.md) | OAuth2 客户端接入：公开与保密两种客户端、授权码流程、设备授权（无头程序，§4A）、token 与刷新、用户信息与绑定、游戏数据读取与**代理上传**、数据更新 Webhook、可申请的 scope、管理员登记 client 的字段（§10） |
| [HarukiProxy v3 客户端对接](harukiproxy-v3-client-integration.zh-CN.md) | 给获授权客户端开发者：OAuth2、原始载荷、UA、updatedData 响应、重试、客户端改造及联调验收 |
| [HarukiProxy 上传维护](harukiproxy-upload.zh-CN.md) | 后端配置、数据库迁移、管理统计、旧路径停用时间及生日材料监听 |
| [Public API Webhook 接入](webhook-integration.zh-CN.md) | 基于 token 自行订阅具体游戏账号的旧版 webhook |

## 给站内前端

| 文档 | 回答什么问题 |
| --- | --- |
| [上传读写授权前端对接](toolbox-upload-grants-frontend.zh-CN.md) | 授权编辑、可写账号选择器、委托上传、OAuth2 同意页面（含设备授权页与按设备撤销的入口）及审计展示 |
| [游戏账号数据授权](game-account-data-grants.zh-CN.md) | 把自己的账号数据授权给其他 Toolbox 用户；可访问账号聚合接口的字段与语义 |

## 给本项目开发者

| 文档 | 回答什么问题 |
| --- | --- |
| [上传 OAuth2 与账号读写授权](upload-oauth2-write-grants-design.zh-CN.md) | 已实现的权限设计、身份来源与部署边界；生产待切换 |
| [HarukiProxy v3 后端设计与实施](harukiproxy-v3-backend-design.zh-CN.md) | 严格平台 UA、版本策略、上传统计、数据库迁移与客户端联调 |
| [后端架构与渐进重构约定](backend-architecture.zh-CN.md) | 目标目录结构、依赖方向、模块边界。代码评审的架构基线 |
| [JSON 与数字精度约定](json-conventions.zh-CN.md) | JSON v2 的字段匹配、nil 表示、大整数精度及生成代码维护 |
| [MessagePack codec 与 OrderedMap](msgpack-codec.zh-CN.md) | 共同字节游标、旧包退役、provider 字段规则、小对象合并分配和本机基准 |
| [游戏数据加密配置](game-data-crypto.zh-CN.md) | 按区服配置 crypto key/iv；9.0.0 配置迁移要求 |
| [iOS 模块 URL 重写](ios-url-rewrite.zh-CN.md) | Surge/Loon/Stash 透明转发与 Quantumult X 307 兼容策略 |
| [MYSEKAI 采集数据复原](mysekai-restore.zh-CN.md) | CN 6.4.0 schema、上传与历史读取、TW/KR 按区服切换及缓存发布要求 |
| [Ory 套件使用说明](ory-suite-usage.zh-CN.md) | Kratos / Hydra / Oathkeeper 各自的职责、登录态验证方式、社交登录（Google / Apple）接入、可信代理与转发 IP 的取值规则、为什么大量旧接口返回 410；设备授权的架构（§10.5）与运维手册（§10.6）、内部令牌校验 API（§10.3.1） |
| [爱发电赞助集成](afdian-sponsor-integration.zh-CN.md) | 赞助墙的 webhook 与同步行为 |

### 设备授权（RFC 8628）资料在哪

| 要找什么 | 去哪里 |
| --- | --- |
| 接入方契约：发起、轮询算法、错误表、展示与令牌保存要求、示例 | [OAuth2 / OIDC 接入](oauth2-integration.zh-CN.md) §4A；scope 限制见 §9，管理员登记设备授权许可与 `devicePolicy` 见 §10 |
| 只做 OIDC 登录的 RP：令牌端点地址为什么变了 | [用 Haruki 账号登录](oidc-provider.zh-CN.md) §1、§7 |
| `/device` 页面与「已授权应用」按设备撤销依赖的后端端点和错误码 | [Ory 套件使用说明](ory-suite-usage.zh-CN.md) §10.5.5、§10.5.6 |
| 架构：Hydra 的缺口、令牌端点兼容层、服务端代驱链、Redis 键与状态机、回收器、限流 | [Ory 套件使用说明](ory-suite-usage.zh-CN.md) §10.5 |
| 运维：配置与启动校验、开关、上线、监控、回滚 | [Ory 套件使用说明](ory-suite-usage.zh-CN.md) §10.6 |
| 部署：compose 环境变量、清理服务、Oathkeeper 规则、真实 Hydra 集成测试 | [Ory 套件使用说明](ory-suite-usage.zh-CN.md) §11.3、§9.1 |
| Sekai Station 校验令牌用的内部 API | [Ory 套件使用说明](ory-suite-usage.zh-CN.md) §10.3.1（不对外，不写进接入文档） |
| 代码落点 | [后端架构](backend-architecture.zh-CN.md) §1「OAuth2 设备授权的代码落点」 |

## 待实施设计

| 文档 | 状态 |
| --- | --- |
| [数据 revision 与缓存失效设计](game-data-revision-design.zh-CN.md) | 同秒旧缓存复现、数据库版本原型、条件读取和分阶段发布约束；尚未接入生产 |

已完成的一次性迁移计划、调研流水账和旧部署记录不在此保留，可通过 Git 历史查阅。
