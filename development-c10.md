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
