# AGENTS.md

本文件是进入 AMSFTP 仓库进行下一阶段开发的工程约束。用户入口见 [README.md](README.md)，详细文档见 [docs/README.md](docs/README.md)。

## 当前状态

- 主线已经包含内部预览实现；当前发布为 `v0.1.0-internal.20260719.1`。
- 规范仓库名与 Go module 均为 `awesome-sftp-cli` / `github.com/TyrantLucifer/awesome-sftp-cli`。
- 这是 unsigned、owner-only 的内部版本，不是公开 1.0。
- Level 0 SFTP 可用；production Helper 和 production Level 2 保持 CLOSED。
- 当前迭代入口是[项目状态](docs/development/status.md)与[路线图](docs/product/roadmap.md)，不是历史 Stage 文档。

## 开始工作前

按需阅读，不要先加载整个文档树：

1. `README.md` 和 `docs/development/status.md`。
2. `docs/product/roadmap.md` 中与任务相关的方向。
3. `docs/architecture/overview.md` 与相关 ADR。
4. 对应的 `docs/user/`、`docs/security/` 或 `docs/release/` 文档。
5. 受影响包的实现和测试。

历史 Stage 计划、Verification 流水和一次性目标文件只通过 Git 历史查阅，不应重新复制回活动文档。

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
- 发布、工具链、workflow、依赖或平台变化：完成相关定向测试后运行完整 `make ci` 和对应原生/发行门禁。
- 不要为了形式感反复跑与改动无关的全量矩阵；也不要用较小测试冒充发布证据。

行为变更和 bugfix 先写能复现预期的失败测试，再实现最小修复。纯文案和机械链接调整不需要制造无价值测试。

## 文档规则

- `README.md`：用户现在能做什么、如何开始、当前限制。
- `docs/architecture/overview.md`：当前实现边界和核心数据流。
- `docs/architecture/adr/`：不可逆或跨模块设计决策；ADR 追加而不是改写历史。
- `docs/product/feature-matrix.md`：稳定能力 ID、状态、用户契约和实现/测试依据。
- `docs/development/status.md`：简短当前状态，不记录每日命令流水。
- `docs/product/roadmap.md`：未来方向，不伪装成已完成能力。
- `docs/release/RC-GATES.md`：公开 release 专用，内部预览发布不能据此宣称公开就绪。

功能或状态变化必须在同一 change 中更新相关用户文档、功能矩阵和状态。不要写入临时分支名、容易过期的 CI 运行号或聊天上下文。

## Git 与工作区

- 从最新 `origin/main` 创建 `codex/` 前缀分支；需要隔离时使用 worktree。
- 保留用户已有改动，不覆盖、不 reset、不清理不相关文件。
- 构建、coverage、临时 fixture 和 `.superpowers/` 不是持久事实源，不应提交。
- 提交保持单一意图；提交前检查 `git status`、`git diff --check` 和相称的测试结果。

## Release 规则

- 内部 preview、release candidate 和 public release 是不同语义，文档与命令不得混用。
- public release 前运行 `docscheck --release`，并关闭 `docs/release/RC-GATES.md` 的真实门禁。
- 不得伪造签名、公证、原生平台、物理大文件、Kerberos、Helper 密钥托管或最终字节身份等外部证据。
- 如果门禁依赖当前环境无法取得的真实证据，保持 CLOSED 并明确记录恢复动作。
