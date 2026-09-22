# iOS 模块 URL 重写

2026-09-22 核对官方文档后，模块生成器按客户端输出以下 URL 转发动作：

| 客户端 | 生成语法 | 行为 |
| --- | --- | --- |
| Surge | `pattern target header` | 透明修改 URL 和 Host |
| Loon | `pattern header target` | 透明修改 URL；沿用兼容旧客户端的语法，动作位于目标 URL 前 |
| Stash | `http.url-rewrite` 下的 `pattern target transparent` | 透明修改 URL |
| Quantumult X | `pattern url 307 target` | 保留客户端重定向 |

适用于代理模式的 Suite、MySekai、强制刷新和生日上传，以及脚本模式的 MySekai False→True 请求重写。上传响应脚本和 HTTPS MITM 主机配置保持原行为。

透明 URL 重写不让游戏客户端重新处理 307 响应。Quantumult X 官方支持 `request-header` 修改请求行或头字段，但没有核实到与上述跨域 URL 透明转发等价的直接动作，因此保留 307，不能直接输出 `url header`。

发布后用户需要更新模块订阅或重新下载模块；已导入的静态旧模块不会自动因后端代码更新而变化。自动测试覆盖生成语法、捕获组替换及 Stash 代理模式 YAML；客户端实际导入和游戏请求仍需真机验证。

官方资料：

- [Surge URL Rewrite](https://manual.nssurge.com/http/url-rewrite.html)
- [Loon 兼容语法](https://nsloon.app/docs/Rewrite/)，[新语法与兼容说明](https://nsloon.app/docs/Rewrite/rewrite_v2/)
- [Stash HTTP 重写](https://stash.wiki/http-engine/rewrite)
- [Quantumult X 官方示例](https://github.com/crossutility/Quantumult-X/blob/master/sample.conf)
