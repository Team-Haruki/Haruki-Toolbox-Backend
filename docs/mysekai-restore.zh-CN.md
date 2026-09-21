# MYSEKAI 采集数据复原

## AVSC 路径配置

```yaml
restore_mysekai:
  structures_file:
    cn: "./data/suite_user_cn_6.4.0.avsc"
    tw: ""
    kr: ""
```

值是 **AVSC 文件路径**，相对路径以程序工作目录为基准，与 `restore_suite.structures_file` 一致。未配置、空映射或空路径均不启用该区服，没有内置 CN 版本或隐式 fallback。示例配置只启用 CN，TW/KR 需核验各自的新版本后填入对应路径。

加载器支持 SuiteUser 独立根（嵌套 record）、schema 列表及具名引用，也支持独立 UserMysekaiHarvestMap 根。优先依据 SuiteUser 的 `userMysekaiHarvestMaps` 引用选择定义。仅加载此字段及其子记录；不将完整 Suite 其他字段强行应用于 MYSEKAI。

文件在启动时读取并编译为不可变映射；上传、同步和数据库读取共享实例。替换文件后重启生效。文件缺失、具名引用无法解析、重复/不连续 key、递归、歧义及不支持的类型会使启动失败，错误含区服与路径。旧字符串 key schema 不允许被当作位置数组映射使用。

## 覆盖范围

普通 MYSEKAI 上传、生日活动上传、Suite 携带的采集地图、MYSEKAI JSON+zstd 同步和 Suite restored 同步共用 `utils/mysekairestore`。照片路径与账号存在性校验保持执行，原始加密同步保持原协议。

数据库读取惰性转换采集地图列，在单次 Row 内复用结果。历史数组快照无需重传或回写，完整 MYSEKAI、updatedResources/字段投影和 Suite 采集地图字段均适用。

兼容旧对象、混合列表、对象中的嵌套数组，保留未知对象字段和精确 JSON 数字。显式 nullable 尾部保留 null，缺失 nullable 尾部不补值；未来多出的数组位置保留在 `_msgpackExtra` 中。必需位置缺失、错误类型及非法记录明确报错，不静默输出空地图。失败不会修改输入或原始数据库字节。

## CN schema 对比（2026-09-21）

新文件 `data/suite_user_cn_6.4.0.avsc` 来自 CN iOS 6.4.0 DummyDll 的 SuiteUser 根及依赖。源 DLL SHA256：`9564c75b5ed0b38880363e51446b636218acfff7b714d0aae3c8bf68cf2df641`，配套 Info.plist 版本为 6.4.0。生成工具为 Haruki-Sekai-API 的 Mono.Cecil schema generator，并修正 ApiData 类型纳入及嵌套类型限定名。

与仓库原 `data/suite_user.avsc` 对比（按短类型名对齐、忽略 namespace 变更）：

| 项目 | 旧文件 | CN 6.4.0 |
| --- | --- | --- |
| SuiteUser 顶层字段 | 209 | 213 |
| 根所引用的记录数 | 243 | 248 |
| MYSEKAI 采集三类记录 key | 字符串字段名 | 明确整数位置 |
| fixture 首字段 | hp | mysekaiSiteHarvestFixtureId |
| drop 首字段 | hp | resourceType |

顶层新增 12 项、移除 8 项，5 个新记录；129 个同名记录的字段 key/类型/字段集合有差异。因此本轮不全局替换旧 schema，也不认为新版 CN 文件适用于 TW/KR。

新版 Suite 中三类采集记录的字段名、位置、类型和 nullable 与此前单独导出的 harvest AVSC 完全一致。文件加载器与复原结果对照测试已确认一致：

- Map 0..2：mysekaiSiteId、userMysekaiSiteHarvestFixtures、userMysekaiSiteHarvestResourceDrops。
- Fixture 0..5：mysekaiSiteHarvestFixtureId、positionX、positionZ、hp、userMysekaiSiteHarvestFixtureStatus、mysekaiSiteHarvestSpawnLimitedRelationGroupId。
- Drop 0..8：resourceType、resourceId、positionX、positionZ、hp、seq、mysekaiSiteHarvestResourceDropStatus、quantity、mysekaiSiteHarvestSpawnLimitedRelationGroupId。

两个末尾字段为 nullable int；Drop 状态字段没有 user 前缀。旧 schema 不适合解析当前 CN 采集位置数组。上述结论来自客户端元数据、schema 对照和合成样例测试；不是整份真实 CN Suite 或生产绘图的端到端验收。

## 缓存与发布

public/private/OAuth2 服务端响应缓存键包含 AVSC 内容 SHA256 及转换版本。同一路径覆盖新内容并重启后，不会复用旧 schema 对应的响应缓存；原有按用户清理仍有效。文件内容指纹相同可共用对应缓存版本，路径本身不暴露给缓存消费者。

数据库 upload_time 不因读取转换改变。客户端 known_upload_time 条件读取仍依据真实上传时间。首次部署或替换 schema 时，需要清理 Cloud 的本地快照/合并/绘图缓存，并发起不带旧条件时间的读取，否则可能继续命中 304。服务端缓存版本不能代替客户端刷新。

## 验证

回归测试覆盖 AVSC 文件加载、独立根和列表具名引用、新 Suite 与单独 harvest 结果一致、旧 schema 拒绝、文件内容更新与实例隔离、五种资源类型、nullable/额外尾部、混合格式、大整数、幂等、错误不修改输入、属主校验、生日上传、同步、历史数据投影、区服隔离及缓存版本区分。所有上传样例均为合成数据。

原工作目录存在被 Git 忽略、引用已退役 Mongo 包的旧 `cmd/suite-rec-backfill`；全仓构建使用受版本管理源码加本轮新增文件的干净副本验证。没有更改生产配置、部署或回写数据库。
