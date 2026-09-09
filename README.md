# 竞速扩展计划（项目正式名称待定）

基于 [qui](https://github.com/autobrr/qui) 的竞速扩展，在 [Ebichuu/qui](https://github.com/Ebichuu/qui/tree/racing/develop) 的 `racing/develop` 分支开发。项目正式名称待定。

更新日期：2026-09-10。已开始 C00，导入 qui v1.28.0 开发底座并完成本地构建、空库启动、登录及重启验证。定制功能迁移和竞速功能尚未完成，当前版本不用于替换现有部署。

## 开发入口

- [启动批次记录](development-c00.md)：基线、验证结果、迁移冲突和下一步。
- [基线锁定记录](development-baseline.json)：上游正式版、定制提交及工具版本。
- [上游 README](README.upstream.md)：保留原项目介绍；许可证和 Go 模块名不变。

开发环境需要 Go 1.27.0、Node.js 24 或更新版本、pnpm 11.1.2，以及 golangci-lint 2.13.0。安装对应工具并加入 PATH 后运行：

```sh
make build
make smoke-baseline
make precommit
```

`smoke-baseline` 使用 Python 3 标准库，自动建立临时空数据库和随机测试账号，只监听本机回环地址，验证后停止并清理。不会使用已有 qui 配置，也不会连接现有 qB。

## 开发计划

[公开开发计划](DEVELOPMENT.md) 列出 C00—C14 的顺序、交付范围和执行边界；当前已启动 C00。

主方案承接最新确认：以 qui 为底座保留定制；RSS 与网页互补，谁先发现且必要信息齐备就先推进；慢来源、整批元数据和通知不得拖住接种；官种优先，合格低效种可用于腾空间；前端简洁，过程查日志。所有镜像只通过 GitHub Actions 构建发布。

## 分支与验证

`develop` 保留现有定制版本。`racing/develop` 使用新上游底座推进迁移，GitHub Actions 执行构建、Go/前端测试和启动验证；此分支当前不发布镜像。

原始需求、现场检查与历史运维资料保留在本地，不纳入公开源码。当前没有接入现有下载器，也没有交接自动添加、删除或汇报权限。
