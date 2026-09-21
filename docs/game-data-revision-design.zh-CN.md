# 数据 revision 与缓存失效设计

2026-09-08：本阶段完成问题复现、候选数据库机制和本机 dummy 验证。**这是独立于 codec 发布的设计原型，尚未接入正式读取/缓存/条件响应代码，也没有在生产增加字段或触发器。** 生产当前仍采用 upload_time 缓存版本及 SCAN 失效。

## 已复现的问题

使用现有 Store.Write、ResolveGameDataStamp、ClearUploadedGameDataCaches、ConfirmGameDataCacheWrite、CheckNotModified，加上临时 PostgreSQL 18 和 miniredis，测试合成账户 `900178`：

1. 活动 178 的旧积分为 100956916，上传时间设为已过去的一个秒值。读取者 A 取到旧 body 和 stamp 后暂停。
2. 上传结算积分 144520517，沿用同一个 upload_time，正常执行缓存清理。
3. A 恢复运行。现有写缓存 fence 仍返回 true，允许把旧 body 写回同一版本键。
4. 新请求读取到这个旧 body；携带已知 upload_time 的条件请求仍能得到 304。

此例并不要求清缓存失败，也不局限于上传发生的那一秒；只要时间值没有变化，等待到下一秒仍不能识别两份数据。数据库中的积分已更新，错误发生在版本与缓存的绑定上。

## 候选数据库机制

每个 suite/mysekai 行增加内部 `data_revision bigint`。使用独立 sequence 和 BEFORE INSERT OR UPDATE 行触发器，在数据库写事务内给行分配新值；客户端上传字段不得控制它。保持现有 upload_time 的业务含义，不再人为加 1 秒来表达内容变化。

触发器覆盖现有 writer、birthday 局部写和直接 SQL 人工修正；保留现有合并规则、行锁和用户/区服主键。原型不比较整份大 JSON 来跳过无内容变化的写入：多发一个版本只降低命中率，漏发版本会返回错误内容。PostgreSQL 的 BEFORE 行触发器可以修改待写行，符合这一机制。[CREATE TRIGGER](https://www.postgresql.org/docs/18/sql-createtrigger.html)

sequence 值只作为不复用的版本标识，允许有空洞；事务回滚和 ON CONFLICT 都可能消耗没有成为最终行版本的序号。只发布已提交的行版本，不用 sequence 的 last_value 代替某一行的版本。删除后重建同一用户也必须获得新版本，不能每行重新从 1 开始。[Sequence functions](https://www.postgresql.org/docs/18/functions-sequence.html)

正式实现需固定 schema、明确 sequence 权限及触发器定义，在启动检查中核对字段/触发器存在；不把 revision 混入游戏字段 catalog 或公开 JSON。数据库恢复旧备份时还必须切换缓存 namespace epoch，不能让 Redis 中较新的历史键与恢复后的版本碰撞。

## 读取、缓存和 304 必须共同修改

建议第一版先用 PostgreSQL 主库的一次窄查询读出 `{revision, upload_time}`，作为请求的权威版本。Redis 只存 body，不能把未经过持久化协调的 memo 当成强一致的新鲜度证明。这样增加一次轻量 PG 查询，是可测量的正确性成本；后续若要恢复 Redis-only 热路径，需要另行验证可靠的提交/发布协议和失败策略。

body key 与 singleflight key 都必须包含 revision，并保留 surface、server、dataType、属主、原始请求投影和 allowlist digest。现有 generation-independent singleflight 需要调整，否则新版本请求可能加入旧版本正在进行的读取。理想的读取结果在同一 SELECT 快照中携带 body 对应的 revision；读取过程中发现版本变化，应重试或以实际版本返回，不给旧 body 标注新 revision。

旧版本 body 可以继续带 TTL 留存；一个延迟完成的旧请求只能写自己的旧版本键，不能覆盖新键。设置缓存前仍需验证读取结果与目标版本相符。Redis 故障时从 PG 读取；PG 无法确认版本时，禁止从 fallback 生成 304。是否允许返回显式标注的旧 body，应作为独立的可用性策略，不能伪称最新。

`known_upload_time` 不能单独证明 revision 相同。正式接入时需选择并测试兼容方案：新客户端使用与响应 body 绑定的 revision validator；只有旧 upload_time 的请求在无法证明未变化时返回完整 200。不得把新的 revision 偷换成 X-Upload-Time，也不得对无权限、缺失文档、非法 key 或 allowlist 不允许的字段提前返回 304。若采用 ETag，必须包含投影/allowlist 等表示版本信息；仅比较行 revision 仍不能覆盖不同表示。

## 发布分期

1. **增加数据库能力**：短事务安装列、sequence、触发器，设置锁等待/执行超时；触发器就位后分批回填 revision=0 的旧行。避免在一次线上大事务中更新整张表；读端对尚未回填的零版本禁用条件响应和版本缓存。ALTER TABLE 的锁和重写行为应按实际变更核对。[ALTER TABLE](https://www.postgresql.org/docs/18/sql-altertable.html)
2. **接入读写验证**：发布使用新 namespace/版本键的 canary，老版本应用仍可写入，触发器负责分配新 revision。此时保留旧 SCAN 清理，保护仍在运行的旧缓存读者。
3. **推广读取版本**：完成新旧写者并存、授权、响应投影、故障及并发测试，推广所有读者。回滚应用时保留新增列/触发器，不降版本计数、不破坏新缓存命名空间。
4. **去除上传 SCAN**：确认没有旧读者后再移除每次上传的全库扫描；历史 body 由 TTL 回收。绑定、授权和 allowlist 更新的失效语义独立处理，不能因数据 revision 不变而复用过期权限结果。回滚到旧 reader 前应清掉旧 namespace 或切换旧 namespace 版本，防止旧键复活。

## 本轮验证范围

独立无网络的本机 PostgreSQL 18 容器内，用实际项目 Store 写入合成数据。测试结束后删除容器，无生产数据库写入。

| 情况 | 原型验证结果 |
| --- | --- |
| 同一 upload_time 再写 suite | revision 改变，upload_time 保持不变 |
| suite / mysekai / birthday 局部写 | 均产生新版本 |
| SQL 人工修正而不改时间 | 产生新版本 |
| 事务回滚 | 行的已提交版本不变，允许 sequence 留空洞 |
| 同一主键的 8 个并发 upsert | 返回 8 个不同版本，最终行版本正确 |
| 删除后重建 | 不复用删除前的版本 |
| 延迟写旧版本缓存 | 不进入新版本键 |
| Redis 关闭 | 仍能从 PG 读取权威版本；未模拟正式端点降级 |
| 当前旧 fence 与旧 304 | 已成功复现错误，作为待修复回归场景 |

这些结果验证数据库候选机制及失败条件，不代表正式 API 接入完成。仍需落地并测试：同一快照的 body/revision、跨版本 singleflight、304/200 的客户端迁移、PG 故障策略、旧新进程并存、批量回填的锁时间，以及真实读取负载下新增 PG 查询的成本。

源码与无敏感数据的运行记录保留在本轮 codec 发布验收附件中；原型不安装到生产，不作为自动迁移脚本使用。
