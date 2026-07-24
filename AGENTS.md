# AGENTS.md

本文件是进入 AMSFTP 仓库进行下一阶段开发的工程约束。用户入口见 [README.md](README.md)，详细文档见 [docs/README.md](docs/README.md)。

## 当前状态

- 主线已经包含公开预览渠道自动化；当前用户能力与限制以 [README](README.md) 为准，具体版本历史以 GitHub Releases 为准。
- 规范仓库名与 Go module 均为 `awesome-sftp-cli` / `github.com/TyrantLucifer/awesome-sftp-cli`。
- 项目许可证为 Apache License 2.0；当前公开预览仍未签名，不是公开 1.0。
- Level 0 SFTP 可用；production Helper 和 production Level 2 保持 CLOSED。

## 开始工作前

按需阅读，不要先加载整个文档树：

1. `README.md` 与 `docs/README.md`。
2. 对应的 `docs/user/`、`docs/help/` 或 `docs/architecture/` 英文文档。
3. 受影响包的实现和测试。

英文文档是事实源，`README.zh-CN.md` 与 `docs/zh-CN/` 是对应的简体中文翻译。历史 Stage 计划、ADR、Verification 流水和一次性目标文件只通过 Git 历史查阅，不应重新复制回活动文档。

## 代码发现

本仓库维护 codebase-memory 图谱。代码发现优先级：

1. `search_graph` 查找函数、类型、入口和路由。
2. `trace_path` 分析调用、影响范围和数据流。
3. `get_code_snippet` 读取已定位符号。
4. `query_graph` 或 `get_architecture` 处理跨包关系。
5. 只有图谱不足，或搜索字符串、配置、Markdown、shell、workflow 时才使用 `rg`。

不要用大范围 grep 代替符号和调用关系分析。

## 架构边界

- `cmd/amsftp`：单一可执行文件入口和构建元数据。
- `internal/app`：CLI/TUI 命令编排，不能绕过 daemon/Provider 契约直接写远端。
- `internal/tui`：纯 model/reducer、动作和渲染；大目录视图必须窗口化。
- `internal/daemon` 与 `internal/ipc`：owner-only 本地 RPC、会话和后台生命周期。
- `internal/domain`：Endpoint、Location、能力、错误码、Job 等稳定领域类型。
- `internal/provider`：LocalFS、SFTP、fake 及能力驱动接口。
- `internal/transfer`：计划、路由、临时文件、验证、提交、恢复和调度。
- `internal/state`、`internal/statefs`：SQLite schema、迁移和持久 Job 状态。
- `internal/cache`、`internal/cachefs`：内容缓存、租约、配额和安全文件系统操作。
- `internal/search`、`internal/helper`、`internal/directprotocol`：有界搜索、可选 Helper 和关闭中的 Level 2 控制面。
- `internal/platform`、`internal/transport/openssh`：平台私有路径、权限校验和系统 OpenSSH 进程边界。

新增跨包依赖前先确认边界是否已经由接口或领域类型表达；不要把 UI 状态泄漏进 Provider，也不要把底层路径/凭据泄漏进公共错误。

## 必须保持的不变量

1. 系统 OpenSSH 是 SSH 配置、认证和 host-key 的事实源；不新增应用内凭据栈。
2. 密码、私钥、Agent 内容、Kerberos 票据和认证答案不得写入配置、SQLite、Job、日志或支持包。
3. TUI/CLI 不直接执行 Provider mutation；写操作先冻结计划，再由 daemon/transfer 层执行。
4. 未完成内容不能暴露为最终目标；目标验证和提交完成前不得删除 move 来源。
5. 重试和恢复只能跨越已证明幂等或已检查后置条件的边界。
6. 目录、搜索、预览、事件、日志和传输缓冲必须有界。
7. owner-private 目录、文件和 Unix socket 的 no-symlink、owner、mode 与 peer-UID 校验不能降级。
8. Helper 是可选增强；故障或不兼容时必须安全退化到 Level 0。
9. production Helper/Level 2 的 CLOSED 状态不能由配置、测试 fixture 或文档措辞绕过。
10. 人类和 JSON 错误只暴露稳定安全字段，不输出 raw cause、命令、路径或认证内容。
11. 仓库 URL、Go module、构建元数据、发布链接和文档必须使用规范的 `awesome-sftp-cli` 身份。

## 开发与验证

稳定工具链为 Go 1.26.5，oldstable 为 Go 1.25.12。所有 Make recipe 支持 `GO=/absolute/path/to/go`，并通过 `GOTOOLCHAIN=local` 禁止隐式下载或切换工具链。

按风险从小到大选择命令：

```sh
GOTOOLCHAIN=local go test ./internal/<affected-package>
make fmt-check
make vet
make docs-check
make check
make ci
```

- 文档变更：`go test ./internal/docscheck`、`make docs-check`、`git diff --check`。
- 单包行为：先跑受影响包和直接契约测试。
- Provider/传输/状态/IPC 安全语义：补定向测试，再根据影响运行 `make check` 或 race。
- 发布自动化、工具链、workflow、依赖或平台实现发生变化：完成相关定向测试后运行完整 `make ci`；普通发版本身按下方 Release 规则执行。
- 不要为了形式感反复跑与改动无关的全量矩阵；也不要用较小测试冒充发布证据。

行为变更和 bugfix 先写能复现预期的失败测试，再实现最小修复。纯文案和机械链接调整不需要制造无价值测试。

## 文档规则

- `README.md` 与 `README.zh-CN.md`：用户现在能做什么、如何开始、当前限制。
- `docs/user/`：安装、入门、日常使用、传输、预览/编辑/搜索、配置和参考。
- `docs/help/`：按用户症状组织的故障排查与恢复。
- `docs/architecture/`：当前架构、安全边界和核心数据流。
- `docs/zh-CN/`：与英文文档一一对应，目录和章节顺序保持一致。
- `docs/man/amsftp.1`：由公开 CLI facts 生成并校验的英文 man 手册。

`docs/` 只保留上述人类可读 Markdown 与 `docs/man/amsftp.1`。英文是唯一事实源；用户可见行为变化必须在同一 change 中更新相关英文页面和中文翻译。不要重新引入状态账本、路线图、功能矩阵、ADR、发布门禁、测试证据流水、机器 registry 或发布元数据，也不要写入临时分支名、容易过期的 CI 运行号或聊天上下文。

## Git 与工作区

- 从最新 `origin/main` 创建 `codex/` 前缀分支；需要隔离时使用 worktree。
- 保留用户已有改动，不覆盖、不 reset、不清理不相关文件。
- 构建、coverage、临时 fixture 和 `.superpowers/` 不是持久事实源，不应提交。
- 提交保持单一意图；提交前检查 `git status`、`git diff --check` 和相称的测试结果。

### Bugfix / Feature 完整交付闭环

用户明确要求实施 bugfix 或 feature，且没有限定“只分析”“只修改本地”或“只提交 PR”时，默认目标不是停在代码完成，而是交付一个可安装的新版本。Agent 必须连续完成下面的闭环；仓库权限、必要 secret 或外部服务确实不可用时才停止，并明确报告已经完成的步骤和唯一阻塞点。

纯文档、测试整理、重构、CI 或一般维护改动不自动触发产品发版，除非用户明确要求发布；不要为了满足流程形式制造无意义版本。

1. **同步并建分支**
   - 先检查 `git status -sb`，保留并隔离用户已有改动。
   - `git fetch origin main --tags`，确认本地基线来自最新 `origin/main`。
   - 创建语义明确的 `codex/fix-<description>` 或 `codex/feat-<description>` 分支；不得直接在 `main` 开发或提交。
2. **先证明问题或契约**
   - bugfix 先补能复现问题的失败测试，再做最小修复。
   - feature 先补用户可观察的验收测试或契约测试，再实现最小完整行为。
   - 同时检查安全、不变量、有界资源、恢复语义和跨包边界；不得用 UI fixture 绕过 daemon/Provider/transfer 契约。
3. **同步产品事实**
   - 用户可见行为或能力状态变化必须在同一分支更新相关英文用户文档及其 `docs/zh-CN/` 中文翻译。
   - 若本 change 将直接发布，PR 中同时把双语 README 和其他版本化入口准备为目标版本；禁止在 tag 推送后再补版本文档。
4. **完成本地验证**
   - 先运行受影响包和直接契约测试，再按“开发与验证”规则扩大到 `make check`、race 或 `make ci`。
   - 提交前必须通过 `git diff --check`，并再次检查 `git status` 与实际 diff；不得把 coverage、构建产物或无关改动带入提交。
5. **提交并创建 PR**
   - 只显式 stage 本 change 的文件，使用下方 Conventional Commit 规范提交。
   - 推送当前分支并创建面向 `main` 的 PR。需要继续合并发布时创建 ready-for-review PR，而不是停在 draft。
   - PR 正文至少说明 `What changed`、`Why`、用户/开发者影响、根因（bugfix）和实际验证命令。
6. **等待检查并合并**
   - 等待仓库要求的 PR checks 完成；失败时先读取日志、修复、重新验证并推送，不得绕过或把 pending/failed 描述成成功。
   - 处理所有会影响正确性、安全性或发布的 review feedback。
   - 只有 PR 可合并且必需检查通过后才能合并；合并后记录 PR 号、功能提交和 merge commit，并确认功能提交是 merge commit 的祖先。
7. **选择并推送新 tag**
   - 每次 bugfix/feature 发布都使用未出现过的严格 `vX.Y.Z` tag。当前 `0.1` public-preview 版本线默认递增 patch；minor/major 或兼容性变化必须遵循明确的发布计划或用户决定。
   - 合并后重新执行 `git fetch origin main --tags`，确认目标 merge commit 已在 `origin/main`，且本地与远端都不存在目标 tag。
   - 创建与既有发布一致的 annotated tag，例如 `git tag -a vX.Y.Z <merge-commit> -m "amsftp vX.Y.Z"`；校验 `vX.Y.Z^{commit}` 精确等于预期发布提交后，只推送该 tag ref。
   - 禁止在 PR 分支提交上提前打 tag，也禁止移动、覆盖、删除后重建已经发布的版本 tag。
8. **确认自动发布结果**
   - tag 推送后只跟踪 `.github/workflows/release.yml` 的 `Public Preview Release`；按下方 Release 规则，不以独立 Release Gates 作为普通发布阻断条件。
   - 必须确认构建与发布 job 成功、GitHub Release 指向正确 tag/commit、预期归档与 `checksums.txt`、`install.sh`、SBOM/provenance 已发布，并确认 Homebrew formula 已更新为同一版本和 checksum。
   - 将本地 `main` fast-forward 到已发布的 `origin/main`，确认工作区干净。
9. **失败处理与最终反馈**
   - workflow 的瞬时故障可以在同一不可变 tag/commit 上安全重跑；若需要改代码或产物输入，必须走新 PR 并使用下一个新版本 tag，不能重写旧 tag。
   - 在 PR 合并、tag 推送、release workflow、GitHub Release 和渠道更新全部成功前，不得宣称“完整闭环已完成”。
   - 最终反馈应列出分支、提交、PR、merge commit、tag、release/workflow 链接、验证结果和渠道状态；任何未完成项必须单独标明。

### Commit message 规范

- 所有提交使用 Conventional Commits，格式为 `<type>(<scope>): <description>`；`scope` 可省略，破坏性变更使用 `<type>(<scope>)!: <description>`。
- `type` 必须小写，并从以下类型中选择：`feat`（新功能）、`fix`（缺陷修复）、`docs`（仅文档）、`refactor`（不改变行为的重构）、`perf`（性能优化）、`test`（测试）、`build`（构建或依赖）、`ci`（CI/workflow）、`chore`（其他维护）、`revert`（回退提交）。
- `scope` 使用简短、稳定的小写包名或领域名，例如 `transfer`、`tui`、`provider`、`docs`；不要使用分支名、issue 编号或临时阶段名。
- `description` 使用简洁的祈使语气，首字母小写，结尾不加句号；标题总长度建议不超过 72 个字符。
- 需要正文时，在标题后空一行说明动机和行为变化；关联 issue 或声明破坏性变更时，在 footer 使用 `Refs: #123`、`Closes: #123` 或 `BREAKING CHANGE: ...`。
- 提交前根据实际 diff 选择类型，不得把功能、修复或破坏性变化笼统标为 `chore`。
- 示例：`feat(transfer): add resumable upload planning`、`fix(tui): bound directory viewport rendering`、`docs(release): clarify unsigned preview status`。

## Release 规则

- 发布以不可变的语义化版本 tag 为入口，格式为 `vX.Y.Z`；确认 tag 指向目标提交后，将 tag 推送到远端即可触发 `.github/workflows/release.yml` 自动构建并发布 Release。
- 普通发布使用与日常开发相同的 `make docs-check`，不依赖独立的 Markdown 发布清单或人工汇总的 release-gate 文档。
- `.github/workflows/ci.yml` 中的 Release Gates 当前不作为发布授权或发布完成的判断依据，也不要求 Agent 在发布前查看；其稳定性问题留待后续专项优化。
- 不要手工复制自动发布流程的步骤或伪造发布结果；tag 推送后的构建、制品和 Release 创建由发布 workflow 负责。
