# 当前项目状态

最后更新：2026-07-20。

## 当前结果

- PR #6 已合并到 `main`，合并提交为 `1f8bef8c3055150c6e47d05eab30f79d02396e04`。
- 一键安装与发布自动化 PR #9 已合并到 `main`，合并提交为 `7e6f6986af45712ae81d826001f0dc2454804a15`。
- 内部预览版 [`v0.1.0-internal.20260719.1`](https://github.com/TyrantLucifer/awesome-sftp-cli/releases/tag/v0.1.0-internal.20260719.1) 已发布；候选提交为 `541c3c7434d05bc5366950c53c8b1f1774d72e38`。
- 公开预览版 [`v0.1.0`](https://github.com/TyrantLucifer/awesome-sftp-cli/releases/tag/v0.1.0) 已发布，tag 固定到 `e3338026e407aa9091cd332d275ad9841d1a78c1`，包含四平台归档、checksum、SBOM、一键安装脚本与 Homebrew formula。
- Stage 0–6 的实现阶段已经结束；历史计划和验证流水从活动文档中移除，通过 Git 历史追溯。
- 仓库现在进入“内部预览反馈与下一阶段迭代准备”状态，不宣称公开 1.0 已完成。
- 内部预览反馈已修复 Preview drawer 的逐行滚动、小文件 EOF range 越界与空文件零长度读取问题。
- CI 已拆为普通 PR/main 使用的影响感知 `Fast CI`、release 分支/tag/手动触发的完整 `Release Gates`，以及定时 `Nightly`；普通改动不再重复运行发布矩阵。
- 严格 `X.Y.Z` 公开预览的一键安装/升级脚本、Homebrew formula 生成器与 tag 发布 workflow 已进入主线；脚本校验 checksum、原子替换 binary、保留上一版并按 daemon 契约完成启停验证。
- 首批安装反馈发现 `v0.1.0` 的 Homebrew 符号链接入口会被认证 helper 完整性检查拒绝；修复会冻结到 Cellar 真实二进制并保持最终路径的 owner、mode、ACL 与祖先目录校验。

## 可用边界

- Vim-first 双栏浏览、LocalFS/SFTP Provider、持久 daemon、Job、预览、编辑、缓存、搜索、诊断与内部发布路径已进入主线。
- 标准 Level 0 SFTP 是当前支持的数据路径。
- production Helper 分发与生命周期仍为 **CLOSED**。
- production Level 2 跨主机直传仍为 **CLOSED**。
- 当前内部归档与公开预览均未经 Apple 签名或公证；公开预览不等于公开 1.0。
- `TyrantLucifer/homebrew-tap`、`public-preview` environment、tag 规则与两个受保护写入 secret 已配置并用于 `v0.1.0` 发布。

## 下一步

下一步完成 Homebrew 认证 helper 回归修复的原生 macOS 与发布门禁，随后通过新的严格 patch tag 发布，不移动或复用 `v0.1.0`。之后继续从[产品路线图](../product/roadmap.md)的“内部预览反馈闭环”收集真实问题；production Helper、Level 2 和公开 1.0 仍使用独立门禁。

开发前阅读根目录 [AGENTS.md](../../AGENTS.md)，并只运行与改动风险相称的定向测试。公开 release 仍必须单独通过 [RC 门禁](../release/RC-GATES.md)。
