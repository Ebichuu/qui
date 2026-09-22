# C14：异步分析与完整替代验收

## 已实现的 Peer 历史

分析按实例单独开启，默认关闭，不授予接种、删除或额外重汇报权限。当前只分析已确认接收的任务，在确认后的 20 分钟窗口内尽力每 5 秒采样；独立单工作器每秒最多启动一个任务，网络请求最长 2 秒。负载超过采样能力时保留缺测，不让分析占用接种、回收评估或未决核实的工作槽。

候选仅以已验证 v1／v2 hash 关联真实 qB 任务，请求使用 qB 当前索引 hash。记录 added_on，采样前后核对代次；首次采样也拒绝确认接种之后重加的代次。不以名称或大小推测相同任务。

每个任务最多保留 240 次尝试和 1000 个 Peer 端点。端点保存 IP、BT 端口、首次／最后可见、完整样本中的消失时间、可见次数、最高已知进度、首次见到完成的时间和计数器重置。记录的是连接端点，不能据此确定原始发种者；换端口也不能证明换了人。下载／上传值是最后观测到的连接计数器，不是跨重连总量。

只有完整成功的 Peer 快照才能证明一个端点暂时不可见。接口失败、陈旧快照、代次变化、解析失败和截断都有独立样本状态；失败与部分采样不增加成功数。完整窗口一次都没有采样的任务也从持久接种确认记录生成零成功报告，仍保留应采样分母。重启和关闭开关期间的时间不会从分母中扣除。

报告在接种历史中查看，按页显示端点和时间；页面关闭时停止请求历史。分析历史保留 30 天，定期分批清理，不修改原选机或接种决策。

## 验证与未完成项

已通过 SQLite／PostgreSQL 持久化与调度测试、端点与覆盖率测试，并实跑合成 qB：默认关闭、IPv6、失败缺口、重启、消失、同 hash 换代、关闭采样以及慢分析期间独立接种。

现已接入可选 GeoLite2-ASN 离线数据库；未配置时 ASN 保持 unavailable。目标站点榜单适配器尚缺，因此排名保持 unsupported。qB 国家字段、本地上传与 Tracker 工作状态均不能代替 ASN 查询或站点排名／入账证据。站点榜单仍需实现，ASN 仍需实际数据验证；现有 Peer 历史不代表 C14 全部完成。

全量替代仍需 C12 的两个真实站点、C13 的旧执行者交接／恢复对账，以及 R01—R40、P01—P12、QV01—QV15 适用项的完整现场证据。不能用合成数据宣布完整替代。

## 2026-09-21 开发验收续检

- 使用仓库 `.dev/bin` 工具链运行 `make precommit`、`make build`、`GOFLAGS='-race -count=1' make test-openapi` 和 `pnpm check:i18n`，均通过；保留现有前端 lint、翻译覆盖和打包警告。
- `make test-frontend` 的 108 个文件、1090 项测试通过。新增 `PeerHistory.test.tsx` 的两项定向测试通过，覆盖展开才读取、折叠后不因缓存失效发起请求，以及请求失败不伪装成空历史。
- `go test -race -count=1 ./internal/services/racing ./internal/models ./internal/api/... ./internal/database/...` 通过；本轮未重复运行全仓 Go 测试，检查范围集中于当前分析实现涉及的服务、存储、接口和迁移。
- 为隔离 PostgreSQL 配置正确测试连接后，`go test -race -count=1 -v ./internal/models -run TestRacingAnalysis` 的 SQLite 和 PostgreSQL 子测试均通过。首次 PostgreSQL 连接使用不存在的角色，修正测试连接后重跑通过。
- 构建后的真实 qui 进程通过 `make smoke-racing-analysis`，输出 `PASS Peer history`，覆盖默认关闭、IPv6 端点、失败缺口、重启、消失、同 hash 换代及慢分析期间独立接种。下载器使用合成数据，没有操作生产实例。
- 本次未重新进行浏览器视觉验收；页面布局未改，既有截图不能替代后续真实场景判断。没有构建或发布镜像；ASN、站点榜单与现场交接仍未完成。


## 离线 ASN 接入与验证

已新增 `racingASNDatabasePath`／`QUI__RACING_ASN_DATABASE_PATH`，相对路径按配置目录解析，默认关闭，变更后重启生效。后台独立读取并验证最多 64 MiB 的 GeoLite2-ASN MMDB 内存快照，不自动下载，不向第三方发送 Peer IP。数据库缺失或损坏只记录警告，Peer 采样与接种继续。

每个端点保留 ASN、组织、匹配网段、查询时间、数据库构建时间与 SHA-256 指纹。未匹配、查询错误和未查询分别表示；完整查询覆盖率包含明确的未匹配结果，不增加 Peer 成功采样数。重启后新数据库只补充仍在观察窗口内的历史；已结束历史保持原始证据。界面在原 Peer 历史中展示组织与时间，用户文档和配置参考已同步。

本轮检查：

- `make precommit`、`make build`、带 `-race -count=1` 的 OpenAPI 检查、`pnpm check:i18n` 均通过；已有 58 项前端 lint 警告，无错误。
- Racing、models、config、domain 和 `cmd/qui` 的 `go test -race -count=1` 包回归通过；SQLite／PostgreSQL 的 ASN 历史持久化定向测试通过。
- ASN 定向测试覆盖 IPv4、IPv6、映射地址、未匹配、无效数据库、源文件截断不影响内存快照、版本变更重新查询及缺测不变。Peer 历史前端三项测试通过。
- 构建后 `make smoke-racing-analysis` 输出 `PASS Peer history: explicit read-only switch, offline ASN and unavailable database recovery`，并通过其余 hash、缺口、重启、换代与独立接种断言。
- 使用原生 Chrome 验证合成实例中文界面，看到 `AS64512 · Synthetic ASN`、数据库日期和查询时间；未使用生产数据。本轮未重跑全仓 Go／前端测试，已针对实际改动包及组件验证；手机端外观未重新验收。

ASN 数据源使用可重复生成的合成 MMDB 验证，真实 GeoLite2-ASN 文件仍待部署时核对。站点排名没有目标接口证据，保持 unsupported；本轮没有切换现用执行者或构建发布镜像。


## 2026-09-22 保留期限与整体回归

读取历史现在与后台清理使用同一 30 天保留期限：已有报告按最后一次采样时间到期，零采样报告按接种确认时间到期。接口先检查期限，不等待每分钟最多 100 条的物理清理完成，因此停机恢复或清理积压不会继续暴露过期端点。读取不执行删除，也不会在清理后重建过期空报告。

SQLite／PostgreSQL 定向测试覆盖过期记录仍留在数据库、接口不可读、清理后仍不可读。隔离实跑新增停止程序、将合成历史推进至 31 天前、重启后请求历史返回 404 且接种不重发的断言。用户文档同步说明到期计算与后台清理的区别。


本轮 `make precommit`、`make build` 与 `git diff --check` 通过，保留既有 58 项前端 lint 警告。全量前端 109 个文件、1093 项测试通过；`make smoke-racing-analysis` 实跑返回 PASS（包含过期历史不可读），`make smoke-automatic-delete` 的 accepted／disconnect 两种一致性备份恢复均通过。没有更改页面布局，本轮无需重复视觉验收。

首次全量 Go 测试复现已记录的 macOS 临时目录别名问题，失败集中在 cross-seed 文件系统用例；采用真实 `TMPDIR` 并启用隔离 PostgreSQL 重新执行。未修改 cross-seed 业务逻辑。

重跑结果：真实临时路径下、启用隔离 PostgreSQL 的完整 `make test`（`go test -race -count=1 -v ./...`）全部通过，未见竞态告警或未解决失败。原生 Linux ext4 的回收恢复仍由 GitHub Actions 验证；本轮未发布镜像或切换生产实例。
