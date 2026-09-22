---
title: 下载器群组与存储池
---

在“设置 → 实例”页面管理群组、存储池和路径映射。群组与路径配置本身不会触发接种或回收；实例的接种和官种回收权限分别开启。

## 群组

群组包含名称、启用状态和成员。同一下载器可以加入多个群组；组内重复成员只保留一份。改名保留 ID 及规则引用。空组或停用群组不会自动落到组外实例；被规则引用的群组必须先解除引用才能删除。

群组用于目标选择，不会合并磁盘空间或授予删除权限。

## 存储池与路径

一个存储池对应一块实际共享空间。两台下载器共用同一磁盘时，把各自的挂载路径映射到同一个池；不同磁盘使用不同池。仅凭目录名称相同不能判断共盘。

路径必须是下载器上的绝对路径，而不是 qui 所在机器的路径。支持 POSIX 路径、Windows 盘符和 UNC；Windows 路径按不区分大小写规范化，拒绝父目录跳转。同一实例路径只能映射一个池。嵌套磁盘挂载采用最具体的路径映射。

例如：A 的 `/data` 与 B 的 `/downloads` 共盘，可以映射到同一个池。A 的 `/data/other-disk` 如果实际挂载另一块盘，应单独映射。

qB 提供的剩余空间属于其默认保存目录。只有该目录已明确映射、路径信息新鲜且随后取得新的空间样本，才显示该池的观测容量。多实例共盘时不把数值相加；其他目录所在池容量未知时显示“未知”。临时目录与分类目录可属于其他池，不会借用默认目录容量。

## 后台观察

启用实例在页面关闭后仍使用原有共享同步循环。MainData 超过五秒或同步失败会标记陈旧，保留观测时间；未知速度不当作零速空闲。偏好和版本异步更新，慢实例不会阻塞其他实例。

认证 API 在 `/api/racing` 下提供 `configuration`、`observations` 以及 `groups`、`storage-pools`、`path-mappings` 的增改删。配置重启后保留；实时观测重新从下载器取得，不把重启前样本当作当前状态。接种、官种回收仍分别受到实例开关、规则及预算约束。


## 官种回收执行权限

在实例的接种设置里，“允许此下载器为官种执行回收”默认关闭。仅开启接种、加入群组、保存回收预算或勾选 RSS 的允许回收，都不会授予删除权。

开启后，具体官种的目标确实缺少空间、预算内的完整候选组合可补足缺口时，才会逐项删除低效种子及其文件。请先在隔离环境验证，并关闭 Vertex 或其他应用中同范围的自动删除；qui 无法自动检查外部执行者是否已停用。已有日常自动删除与官种回收共用任务占用账本。

当前物理核实要求 Linux amd64/arm64、原生 ext4/XFS/Btrfs、下载器开启本地文件访问，且 qui 能按相同路径访问同池各下载器的文件。共享区段、硬链接、缺失清单、不支持的文件系统及过期样本会阻止回收；不会用逻辑大小替代可释放容量。请将稳定的存储池目录作为路径映射，避免使用会随种子删除的临时目录。

删除前还要求所有实际 Tracker 均有精确账号绑定且删除保护通过。`reported_working` 只代表 qB 报告工作正常，不证明站点已入账；要求站点入账的保护在没有对应证据时持续阻挡。

同一存储池一次只推进一项待核实回收。任务消失不等于文件释放；文件仍存在、净空间未恢复、目录身份变化时继续等待。关闭回收开关阻止新删除，已接受的旧操作仍会继续核实。超时或断连产生的未知操作不会自动重发、退款或解除占用；即使空间后来增加，也不能绕过该事件的未决步骤直接接种。

核实通过后会重新检查缺口和候选，空间足够即走正常接种；已消耗的删除数量、容量预算和近期上传代价不会因重启或改规则而重置。本功能仍处于开发验收阶段，真实多站点与旧执行者交接需单独验证。

## Peer history

Each downloader has a separate **Record Peer history** switch. It is off by default and does not enable reception or deletion. After a confirmed reception, analysis samples connected peers for up to 20 minutes. It stores IP addresses and BT ports for 30 days. View the report from **Reception history**. Saved reports expire 30 days after their last sampling attempt; reports with no samples expire 30 days after reception confirmation. Expired reports are unavailable immediately, even when background cleanup is still catching up after downtime.

Reports show complete samples against expected samples, including missed opportunities during downtime, overload or disabled sampling. An unavailable or partial snapshot cannot prove that a peer disappeared. The report preserves first and last visibility; a later successful complete snapshot can record absence. Endpoint history is limited to 1000 endpoints per torrent. Truncation remains visible in coverage.

The history uses verified torrent hashes and checks the qB task generation. It does not link tasks by name. Connection counters may reset, and seeing a complete peer does not establish who originally uploaded the torrent. Site ranking collection remains unsupported. These reports do not prove tracker accounting or complete replacement of an existing setup.


### Offline ASN lookup

Set `racingASNDatabasePath` in `config.toml` to a local [GeoLite2-ASN database](https://dev.maxmind.com/geoip/docs/databases/asn/) in MMDB format, or use `QUI__RACING_ASN_DATABASE_PATH`. Relative paths resolve against the configuration directory. Obtain and maintain the database separately; qui does not download it or send Peer IP addresses to a lookup service. Empty configuration disables lookup. Restart qui after replacing the database or changing its path.

The same bounded analysis worker enriches observed endpoints during the reception observation window. Each result records its ASN, organization, matching network, lookup time, database build time and SHA-256 fingerprint. The history distinguishes no matching record, lookup errors and endpoints not yet enriched. Its ASN coverage includes completed no-match lookups and does not change Peer sampling coverage. ASN ownership describes an IP network, not the original uploader or the physical location of a seedbox.

An invalid or unavailable database produces a startup warning in the analysis worker; reception and Peer sampling continue. Previously recorded results retain their original database and lookup timestamps, even if lookup is later disabled. On restart with a new database, endpoints in active observation windows are enriched again. Older, finished histories remain unchanged. The reader uses an immutable in-memory snapshot of at most 64 MiB; no new database schema or network worker is needed.
