# C10：持续观察恢复（首批）

基于 C09 `73559e0e`，该提交的 GitHub CI 已全部通过。C09 的两站现场试跑及 Vertex 对照仍待验证。

## 本批范围

- SQLite 100 / PostgreSQL 101 保存持续观察检查点与原 FREE_SPACE 删除冷却。
- 记录规则定义版本、任务 hash/加入代次、实测持续时间、最后观测及流量计数。短暂重启保留已测时间，但不计停机间隔；长缺测、时钟回退、计数器重置、规则变化或重新加种使旧资格失效。
- 扫描前撤销旧持久资格，完成观察后才重新保存；实时操作前保存失败即停止，避免重启恢复未完成扫描之前的旧资格。
- 同实例的后台扫描、手动触发和手动 dry-run 串行执行；不同实例使用独立锁。
- 原 FREE_SPACE 五分钟冷却在发送删除请求之前持久化。失败或未知结果也保留冷却，不能通过重启清空。

本批不增加官种回收执行者，不接管现有部署。单机/群组有效回收配置、跨调用者的持久删除意图与统一执行入口仍是 C10 后续工作，不能将本批称为 C10 全部完成。

## 验证

- `make precommit` 通过；前端保留既有 58 条 warning，0 error。
- `go test -race -count=1 ./internal/services/automations ./internal/models ./internal/database` 通过，启用真实 PostgreSQL 测试连接；最终故障分支补测后再次运行完整 automations 包通过。
- SQLite / PostgreSQL 验证短重启保留已观察时间、停机不补时、长缺测与计数回退重置、冷却恢复、无效检查点事务回滚；SQLite 额外注入检查点删除失败，确认内存资格清空。
- `make build` 通过；构建后运行 `python3 scripts/smoke-automation-observation.py`，输出 `PASS C10: persisted measured duration; crash recovery excludes downtime; no downloader mutations`。对应 `make smoke-automation-observation` 已加入 GitHub Actions。
- 本批未修改前端或 API 契约，不重复前端全套与 OpenAPI 检查；全量 Go 测试交由 GitHub CI。本机只运行受影响三包。

使用两引擎合成数据库和本机模拟下载器，不连接真实站点；恢复脚本使用 dry-run，不执行真实删除，冷却存取由双引擎测试覆盖。镜像仅由 GitHub Actions 构建发布。

## 第二批：回收设置持久化与继承 API

- SQLite 101 / PostgreSQL 102 保存下载器显式设置、群组默认及稳定自动化规则引用。所有设置修改纳入 racing 配置版本事务。
- 单机显式值优先，包括显式关闭；删除单机设置恢复继承。仅启用且当前包含实例的群组参与解析，相同默认去重，不同默认返回冲突且不提供有效策略，不扩大预算或合并引用。
- 数量、回收量、近期上传贡献损失、窗口与超额量使用明确单位；零损失预算不是无限制。被引用规则不能删除，改名不丢引用。
- 提供读取、保存、移除设置 API 及 OpenAPI；RSS 保持仅允许开关，没有新增候选规则或预算字段。
- 本批仅配置存储与解析；候选定义的显式复用/旧 FREE_SPACE 转换、预算执行、统一删除归属、前端表单仍待后续接入。保存启用不代表允许实际删除，也不改变原日常动作。

验证：

- `make precommit` 通过，保留既有前端 58 条 warning、0 error。
- `go test -race -count=1 ./internal/models ./internal/api/handlers` 通过；启用真实 PostgreSQL 测试连接。引用错误处理调整后重新运行双引擎 `TestRacingReclaimInheritance` 通过。
- 数据库迁移编号、幂等、完整迁移及 string_pool 外键索引检查通过；新设置生命周期与事务回滚同时在 SQLite / PostgreSQL 验证。
- `make test-openapi` 通过；`make build` 通过。
- 构建后运行 `python3 scripts/smoke-reclaim-settings.py`，输出 `PASS C10 settings: inheritance, conflict, explicit disable, crash recovery, reference protection; no downloader mutations`。对应检查加入 GitHub Actions。
- 未改前端代码，不重复前端全套；全量 Go 回归交由 GitHub CI，本机运行上述受影响包与迁移检查。没有真实站点或真实删除验证，本批不提供删除执行能力。
- 子代理只读复核已整合。引用删除由外键阻止；极窄的并发解除引用窗口可能返回原数据库错误，操作不会误删，后续重试即可。

## 第三批：日常触发与持续候选条件分离

- `DeleteAction.dailyTrigger` 为可选日常触发条件，保留原 `condition` 不变；日常删除需同时满足两者，持续观察只累计主条件命中。
- 工作流编辑器新增独立条件栏并保留编辑/保存载荷，十种语言补齐标签和帮助说明。
- 预览、分组字段、FREE_SPACE 预加载及冷却分类同时读取两棵条件树，保留旧规则及五分钟冷却。规则编辑仍使旧观察资格失效。
- 旧 FREE_SPACE 混合表达式不自动拆分；本批提供显式编辑分离能力，不授权官种删除。官种候选复用、有效配置表单与统一删除协调仍待后续。

- `make precommit` 通过，0 error、既有前端 58 条 warning；`make build` 通过。
- `go test -race -count=1 ./internal/services/automations ./internal/api/handlers ./internal/models` 通过，模型测试启用真实 PostgreSQL；`make test-openapi` 通过。
- 前端工作流工具测试 32 项通过；`pnpm check:i18n` 全部通过（保留既有翻译警告）。完整前端与 Go 回归交由 GitHub CI。
- 构建后运行 `python3 scripts/smoke-automation-observation.py`，输出 `PASS C10: false daily trigger preserves observation; preview blocks delete; restart excludes downtime; no downloader mutations`。
- 浏览器实测隔离合成实例：打开工作流，确认主条件上传速度 < 10 B/s、独立日常空间门槛；将门槛编辑为 2 MiB 并保存，重新打开确认主条件、触发条件和 10 分钟持续时间均保留，布局无重叠。规则保持停用与模拟运行，不执行真实删除。
- 子代理复核已整合；日常触发中的 FREE_SPACE 参与预计空间停止判断，避免达到日常空间目标后继续删除。正式官种回收仍未接入，不使用此路径绕过日常冷却。

## 第四批：统一自动删除归属

- SQLite 102 / PostgreSQL 103 保存整批提交意图、调用者、hash/AddedOn 代次与结果。日常删除和导出失败清理进入同一单次网络请求入口，保留原变体解析、目录清理和文件缓存失效。
- 发送前重新核对新鲜任务与代次；未知代次不发送。并发调用及重启不能重新认领未决候选，已确认代次保留墓碑。
- 已接受请求可由新鲜任务缺失确认；超时/断开保留 unknown，等待后续显式核实入口。缺失不声称释放物理空间；未知请求不重放，不自动补做空目录清理。手动与代理请求作为外部变化处理。
- 双引擎竞争认领、整批回滚、代次/版本保护与迟到结果测试通过；models、qbittorrent、automations 整包 race 测试通过；`make precommit` 和 `make build` 通过。
- `python3 scripts/smoke-automatic-delete.py` 输出两项 PASS（accepted / disconnect），验证真实进程仅发送一次、持久归属、重启不重发；加入 GitHub Actions。镜像仍仅 Actions 构建。
- 仍需候选复用、回收设置界面和显式未决操作核实；不把本批称为 C10 完成。

## 第五批：显式用途与官种候选观察

- 工作流删除用途为日常、官种或两者；旧规则默认为日常，官种用途不会执行该规则的日常删除，其他动作仍按原定义运行。
- 有效回收设置引用稳定规则 ID，可跨实例复用条件；引用不继承来源实例权限。后台常态扫描及候选 API 共用现有求值器，在任何动作提交前返回。
- SQLite 103 / PostgreSQL 104 保存目标实例独立候选观察；完成、已知代次、做种状态及至少 60 秒持续匹配才有资格。日常触发不阻断候选，旧混合 FREE_SPACE 表达式明确不可复用；文件保留、交叉种扩展及原子分组删除暂不可作为单目标候选。
- 候选观察从启用后积累；不继承缺少历史完成状态证明的旧日常观察。候选返回逻辑体积及累计上传量，不将它们当作实际释放量或近期上传。
- 后端相关包 race 测试（含 PostgreSQL）、OpenAPI 与十语种检查通过。完整构建通过；真实进程 `python3 scripts/smoke-reclaim-candidates.py` 验证持续成熟、重启恢复、新代次重置和零写操作全部 PASS，已加入 Actions。浏览器工具因授权令牌不可用，本轮界面视觉复查暂缺。下载器及群组回收设置界面已接入，显式关闭与恢复继承交互测试通过；预算执行与站点保护继续推进。
