# AMSFTP 文档

根目录 [README](../README.md) 是产品、安装和常用操作入口。本页按任务组织长期维护的详细文档；已完成阶段的执行计划和验证流水只保留在 Git 历史中。

## 用户

1. [首次使用](user/getting-started.md)
2. [Vim-first 键位](user/keymap.md)
3. [持久传输](user/durable-transfers.md)
4. [预览、编辑与缓存](user/preview-edit-cache.md)
5. [搜索与可选 Helper](user/search-helper.md)
6. [CLI 参考](user/cli.md)
7. [配置参考](user/configuration.md)
8. [故障排查](user/troubleshooting.md)

## 设计与产品

- [架构总览](architecture/overview.md)：当前组件、信任边界、数据流、持久化和恢复。
- [架构决策记录](architecture/adr/)：OpenSSH、daemon、Provider、SQLite、缓存、Helper、路由和资源预算等长期决策。
- [产品愿景](product/vision.md)：目标用户、体验原则和明确非目标。
- [功能矩阵](product/feature-matrix.md)：当前能力、状态和稳定依据。
- [兼容性边界](product/compatibility-boundaries.md)与[环境矩阵](product/environment-compatibility.md)。
- [下一阶段路线图](product/roadmap.md)。

## 运维、安全与发布

- [运维与恢复手册](operations/runbook.md)
- [威胁模型](security/threat-model.md)
- [依赖与供应链策略](security/dependency-policy.md)
- [安全发现台账](security/finding-ledger.md)
- [安装](release/INSTALL.md)、[升级与回滚](release/UPGRADE.md)、[卸载](release/UNINSTALL.md)
- [内部预览边界](release/internal-preview.md)
- [公开发行门禁](release/RC-GATES.md)
- [`amsftp(1)`](man/amsftp.1)

## 开发

- [当前状态](development/status.md)
- [测试与质量门禁](development/testing.md)
- [工具链](development/toolchain.md)
- [测试策略](testing/strategy.md)
- 根目录 [AGENTS.md](../AGENTS.md)

## 文档维护规则

- README 说明用户现在能做什么；详细参数只在对应参考文档维护。
- 架构变化新增 ADR，不重写旧 ADR 的历史结论。
- 功能状态变化同步更新功能矩阵、状态和受影响的用户文档。
- 发布边界变化同步更新 README、内部预览说明、RC 门禁和安装文档。
- 不在长期文档中保存一次性分支名、命令流水或完整 CI 日志；需要追溯时使用 Git 与 GitHub 历史。
