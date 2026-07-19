# 当前项目状态

最后更新：2026-07-19。

## 当前结果

- PR #6 已合并到 `main`，合并提交为 `1f8bef8c3055150c6e47d05eab30f79d02396e04`。
- 内部预览版 [`v0.1.0-internal.20260719.1`](https://github.com/TyrantLucifer/awesome-sftp-cli/releases/tag/v0.1.0-internal.20260719.1) 已发布；候选提交为 `541c3c7434d05bc5366950c53c8b1f1774d72e38`。
- Stage 0–6 的实现阶段已经结束；历史计划和验证流水从活动文档中移除，通过 Git 历史追溯。
- 仓库现在进入“内部预览反馈与下一阶段迭代准备”状态，不宣称公开 1.0 已完成。

## 可用边界

- Vim-first 双栏浏览、LocalFS/SFTP Provider、持久 daemon、Job、预览、编辑、缓存、搜索、诊断与内部发布路径已进入主线。
- 标准 Level 0 SFTP 是当前支持的数据路径。
- production Helper 分发与生命周期仍为 **CLOSED**。
- production Level 2 跨主机直传仍为 **CLOSED**。
- 当前内部归档未经签名或公证，不得作为公开发行物重新分发。

## 下一步

下一轮从[产品路线图](../product/roadmap.md)的“内部预览反馈闭环”开始：先收集真实使用问题并修正用户路径、诊断和兼容性，再决定是否启动 production Helper、Level 2 和公开 1.0 的独立门禁工作。

开发前阅读根目录 [AGENTS.md](../../AGENTS.md)，并只运行与改动风险相称的定向测试。公开 release 仍必须单独通过 [RC 门禁](../release/RC-GATES.md)。
