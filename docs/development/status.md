# 当前项目状态

最后更新：2026-07-20。

## 当前结果

- PR #6 已合并到 `main`，合并提交为 `1f8bef8c3055150c6e47d05eab30f79d02396e04`。
- 一键安装与发布自动化 PR #9 已合并到 `main`，合并提交为 `7e6f6986af45712ae81d826001f0dc2454804a15`。
- 内部预览版 [`v0.1.0-internal.20260719.1`](https://github.com/TyrantLucifer/awesome-sftp-cli/releases/tag/v0.1.0-internal.20260719.1) 已发布；候选提交为 `541c3c7434d05bc5366950c53c8b1f1774d72e38`。
- Stage 0–6 的实现阶段已经结束；历史计划和验证流水从活动文档中移除，通过 Git 历史追溯。
- 仓库现在进入“内部预览反馈与下一阶段迭代准备”状态，不宣称公开 1.0 已完成。
- 内部预览反馈已修复 Preview drawer 的逐行滚动、小文件 EOF range 越界与空文件零长度读取问题。
- CI 已拆为普通 PR/main 使用的影响感知 `Fast CI`、release 分支/tag/手动触发的完整 `Release Gates`，以及定时 `Nightly`；普通改动不再重复运行发布矩阵。
- 严格 `X.Y.Z` 公开预览的一键安装/升级脚本、Homebrew formula 生成器与 tag 发布 workflow 已进入主线；脚本校验 checksum、原子替换 binary、保留上一版并按 daemon 契约完成启停验证。
- 项目所有者已选择 Apache License 2.0；`v0.1.0` 是首个严格版本的公开预览候选。

## 可用边界

- Vim-first 双栏浏览、LocalFS/SFTP Provider、持久 daemon、Job、预览、编辑、缓存、搜索、诊断与内部发布路径已进入主线。
- 标准 Level 0 SFTP 是当前支持的数据路径。
- production Helper 分发与生命周期仍为 **CLOSED**。
- production Level 2 跨主机直传仍为 **CLOSED**。
- 当前内部归档未经签名或公证，不得作为公开发行物重新分发。
- 新公开预览渠道同样不宣称 Apple 签名或公证；`TyrantLucifer/homebrew-tap`、`public-preview` environment、tag 规则与两个受保护写入 secret 已配置。首个严格 tag 发布前，历史内部预览仍是最后一个已发布版本。

## 下一步

下一步合入 Apache-2.0 许可证与 `v0.1.0` 发布准备变更，等待精确 `main` CI 成功，再创建一次性严格 tag 打开 checksum 验证的一键安装渠道。之后继续从[产品路线图](../product/roadmap.md)的“内部预览反馈闭环”收集真实问题；production Helper、Level 2 和公开 1.0 仍使用独立门禁。

开发前阅读根目录 [AGENTS.md](../../AGENTS.md)，并只运行与改动风险相称的定向测试。公开 release 仍必须单独通过 [RC 门禁](../release/RC-GATES.md)。
