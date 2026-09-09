# C00 开发启动记录

日期：2026-09-10。范围：锁定源码、建立独立开发底座并验证构建、启动和认证。竞速业务、定制功能迁移及生产交接仍按 C01—C14 实施。

## 基线与导入

- GitHub 最新正式版接口仍返回 [qui v1.28.0](https://github.com/autobrr/qui/releases/tag/v1.28.0)；远端 tag 解引用为 `26c648cbf334f6a0393b3f34a7d904ba9fad4458`。
- 定制仓库 `Ebichuu/qui` 的远端 develop 与本地一致，为 `bfe94b5e4d63f213af747fab39719d47aae99d22`。两份 qui 来源工作区均无未提交改动。
- 从已核实的上游本机仓库复制独立 Git 工作副本，保留其提交对象，不依赖原仓库的对象替代目录。当前分支 `racing/develop`；本地 `develop` 指向锁定上游，用作原 lint 命令的比较基准。
- 更新仓库为 `origin`：`https://github.com/Ebichuu/qui.git`；`upstream` 保留为 `https://github.com/autobrr/qui.git`。C00 提交到独立 `racing/develop` 分支，现有定制 `develop` 保持原状。
- 保留根目录原计划及历史参考；上游介绍另存 `README.upstream.md`，AGENTS 中增加本项目约束，保留上游开发规范。未改品牌、Go 模块、许可证、配置目录或镜像仓库。
- 原始计划和历史运行资料仅在本地保存；公开仓库使用 `DEVELOPMENT.md`，不包含本机绝对路径、历史任务链接或真实种子样本。新增 Racing CI 仅验证开发分支，不发布镜像。
- Vertex 只作为行为参考，提交 `d45ee759981c0951fe7f39ede2009f68e8ca6872`；跟踪文件无变更，存在两个未跟踪的本机文件，没有导入。
- 完整提交清单和工具版本见 `development-baseline.json`。本机开发工具入口在 Git 忽略的 `.dev/bin`；Go、Node、lint 复用已有本机工具链，pnpm 安装在 `.dev/tools`，可用 `PATH="$PWD/.dev/bin:$PATH" make build` 复现。本机工具链接不属于可移植源码，其他机器按 README 安装工具。

## 实际验证

| 检查 | 结果与边界 |
|---|---|
| `make build` | 前端与 Go 二进制构建成功；无镜像构建 |
| `make precommit` | 通过；Go 无新增 lint 问题，前端 0 错误、58 条上游既有警告 |
| `go test -race -count=1 ./internal/database ./internal/auth` | 数据库测试通过，耗时约 126 秒；auth 包无独立测试，登录由真实进程验证 |
| `go mod verify` | 所有模块校验通过 |
| `make smoke-baseline` | PASS：空库启动、HTML 入口、账号初始化、退出、未登录拒绝、登录、空实例列表、重启持久化、正常停止 |
| `git diff --check`、Python 语法检查 | 通过 |

Go 依赖首次下载因默认源的存储地址证书不匹配失败；临时使用 `GOPROXY=https://goproxy.cn,https://proxy.golang.org,direct` 下载后构建成功，未关闭 TLS 或模块校验，未修改项目依赖版本。初次启动脚本按 401 检查未登录请求；核对上游认证中间件后按其为反向代理保留的 403 语义修正，重新运行通过。

原始本机验证日志位于 Git 忽略的 `.dev/build-c00.log`、`.dev/precommit-c00.log`、`.dev/tests-c00.log` 和 `.dev/smoke-c00.log`。测试只使用随机合成账号、临时 SQLite 数据库和回环地址，不继承 `QUI__` 配置环境变量；进程结束后清理测试目录。

未执行：PostgreSQL 集成测试（未配置独立测试 DSN）、完整 Go/前端测试套件（本批无业务代码修改）、OpenAPI/i18n 专项检查（契约与文案未改）、浏览器交互验收、真实 qB/站点联调及生产数据迁移。前端 HTML 可用不等于所有交互已验收。

## C01/C02 的明确输入

已有 12 个定制提交覆盖全程平均上传速度条件及列表列、删种持续条件与冷却观察、自动化多选和批量复制、每日统计、服务器排序及发布修正。当前源码仍是上游底座，这些定制尚未迁移，不能作为现用版本替代品。

| 引擎 | 旧定制迁移 | 上游同编号迁移 |
|---|---|---|
| SQLite | `091_add_daily_transfer_stats.sql`：每日传输统计表 | `091_add_cross_seed_partial_pools.sql`：partial pools 表及设置 |
| SQLite | `092_add_server_stats_sort.sql`：dashboard 排序字段 | `092_drop_cross_seed_feed_items_touch_trigger.sql`：移除 touch 触发器 |
| PostgreSQL | `093_add_daily_transfer_stats.sql`：每日传输统计表 | `093_add_cross_seed_partial_pools.sql`：partial pools 表及设置 |
| PostgreSQL | `094_add_server_stats_sort.sql`：dashboard 排序字段 | `094_drop_unused_timestamp_indexes.sql`：移除无用时间索引 |

迁移表以完整 `filename` 为唯一标识，并非只看数字；同编号不同文件不会自动视作已执行，但会违反两引擎的迁移编号唯一性测试。只改编号会让旧数据库再次执行定制 DDL。C01 必须结合旧文件名记录和实际结构确认来源，在升级事务中衔接，保留历史映射，对不一致状态明确停止，并验证重复启动/中断恢复。

可复用的合成夹具在旧定制仓库的 `internal/models/daily_transfer_stats_test.go`、`dashboard_settings_test.go`、`internal/services/automations/condition_duration_test.go`、`evaluator_test.go` 及上游 `internal/testutil/testdb/testdb.go`。生产库副本尚未读取；合成验证和现场副本验证需分别记录。

## 后续顺序

下一批从 C01 的数据库升级衔接开始，然后 C02 逐项保留定制。真实站点清单（含第二个站点）、qB 版本、磁盘布局、当前镜像及配置还需现场复核，不以历史文档代替当前事实；这些信息不阻止通用迁移和只读模块开发。C03—C07 只读流程、C08—C09 可靠接种、C10—C13 回收/汇报、C14 分析继续保留完整范围。
