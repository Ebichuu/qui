---
title: 定制数据库升级
description: 竞速开发分支的旧定制日统计与排序数据库升级边界。
---

# 定制数据库升级

本说明适用于 `Ebichuu/qui` 的 `racing/develop` 开发分支，将基于旧定制 `bfe94b5e` 的数据库升级到 qui v1.28.0 底座。C01 保护已有数据并验证升级；C02 已恢复日统计、排序及其他定制功能。竞速能力仍在开发，当前不作为生产替代版本。

## 识别与处理

旧定制 SQLite 的 `091_add_daily_transfer_stats.sql`、`092_add_server_stats_sort.sql`，以及 PostgreSQL 的 `093_add_daily_transfer_stats.sql`、`094_add_server_stats_sort.sql`，与上游迁移编号相同但文件名不同。

程序按完整文件名识别迁移。升级保留旧文件名及应用时间，上游 partial pools 等不同文件名的迁移继续正常执行，不需要手工修改迁移记录。升级前核验定制表、字段顺序/类型/默认值/非空约束、主键、外键和日期索引；同时支持只完成日统计迁移的历史中间版本。

C02 新增 SQLite `094_restore_custom_dashboard.sql` 和 PostgreSQL `095_restore_custom_dashboard.sql`。完整旧定制库直接接管已验证结构，在事务内补记新迁移；日统计中间版本补齐排序字段；空库和上游库创建完整结构。旧统计、排序值和旧迁移记录均保留，重启不会重复添加字段。

出现 `legacy dashboard schema does not match migration history` 时，程序停止升级。不要删除迁移记录、伪造文件名或让程序重建统计表。保留失败副本，核对是否拿错数据库、记录缺失或结构曾被修改，再恢复已验证的备份。

## 升级前准备

1. 停止使用该数据库的 qui 进程，核实当前自动操作的执行者和在途动作。测试副本不得连接现有下载器。
2. 记录源提交/镜像摘要、数据库引擎、配置目录和数据目录。保存完整配置目录，尤其加密密钥和账号配置；数据库备份不能替代配置备份。
3. 在独立副本上完成启动、登录、实例/规则数量和关键历史数据核对，再安排生产切换。C01 的合成测试不能代替生产副本核对。

以下示例由操作者填写实际目录；备份目录必须位于配置和数据目录之外。命令仅用于已停止的实例，不负责停机或交接下载器操作权。

### SQLite

```sh
umask 077
QUI_CONFIG_DIR=/path/to/qui-config
QUI_DATA_DIR=/path/to/qui-data
QUI_BACKUP_ROOT=/path/to/backups
backup_dir=$(mktemp -d "$QUI_BACKUP_ROOT/qui-before-upgrade.XXXXXX")
cp -a "$QUI_CONFIG_DIR" "$backup_dir/config"
cp -a "$QUI_DATA_DIR" "$backup_dir/data"
sqlite3 "$QUI_DATA_DIR/qui.db" ".backup '$backup_dir/qui.db'"
sqlite3 "$backup_dir/qui.db" 'PRAGMA integrity_check;'
sha256sum "$backup_dir/qui.db" > "$backup_dir/qui.db.sha256"
```

完整性检查应返回 `ok`。macOS 可用 `shasum -a 256` 代替 `sha256sum`。`.backup` 输出是独立快照，避免只复制主文件而遗漏 WAL；配置及其他数据另行保存。

回退时先停止新进程，保留升级后的数据目录用于诊断。从备份恢复到新的独立目录，再用旧版本启动：

```sh
restore_dir=$(mktemp -d "$QUI_BACKUP_ROOT/qui-restore.XXXXXX")
cp -a "$backup_dir/config" "$restore_dir/config"
cp -a "$backup_dir/data" "$restore_dir/data"
cp "$backup_dir/qui.db" "$restore_dir/data/qui.db"
rm -f "$restore_dir/data/qui.db-wal" "$restore_dir/data/qui.db-shm"
sqlite3 "$restore_dir/data/qui.db" 'PRAGMA integrity_check;'
# 使用保存的旧版本二进制；先核对恢复配置，避免意外连接实际下载器。
/path/to/old-qui serve --config-dir "$restore_dir/config" --data-dir "$restore_dir/data"
```

不要将升级后的库直接交给旧镜像。确认恢复配置内其他绝对路径仍符合回退环境，先核实数据库与在途动作，再恢复对应自动操作。

### PostgreSQL

使用 libpq 服务配置或 `.pgpass` 管理认证，避免把密码写入命令和日志。下例 `qui_before_upgrade` 与 `qui_restore` 是操作者预先配置的服务名；恢复目标必须是新建的独立数据库。

```sh
umask 077
QUI_BACKUP_ROOT=/path/to/backups
QUI_CONFIG_DIR=/path/to/qui-config
backup_dir=$(mktemp -d "$QUI_BACKUP_ROOT/qui-pg-before-upgrade.XXXXXX")
cp -a "$QUI_CONFIG_DIR" "$backup_dir/config"
pg_dump --dbname='service=qui_before_upgrade' --format=custom --file="$backup_dir/qui.dump"
pg_restore --list "$backup_dir/qui.dump" > "$backup_dir/qui.dump.list"
pg_restore --dbname='service=qui_restore' --exit-on-error --single-transaction "$backup_dir/qui.dump"
```

列表检查不等于备份可恢复；必须在独立数据库完成实际恢复并核对记录、规则、统计和排序。权限、数据库所有者及扩展按目标环境保留。回退时停止新进程，使用恢复后的配置、旧版本和独立恢复库，检查实际 qB 状态后再交接；不要对现有目标库使用清空导入来试验恢复。

## 自动验证范围

`internal/database/legacy_dashboard_test.go` 使用旧定制提交的原始迁移 SQL 和合成数据，覆盖空库、上游库、日统计中间版本、完整定制库、重复启动、不一致结构拒绝和事务回滚。SQLite 额外在真实迁移过程中终止子进程；PostgreSQL 在迁移记录写入后中断服务端连接，验证未提交结构及记录回滚。

提供 `QUI_TEST_POSTGRES_DSN` 后执行 `go test -race -count=1 ./internal/database` 可验证两种引擎；测试 DSN 必须指向允许创建/删除测试 schema 的独立测试数据库。GitHub Actions 的 Racing CI 使用独立 PostgreSQL 服务执行这些用例，不连接生产环境、不发布镜像。
