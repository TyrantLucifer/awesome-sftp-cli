# 当前功能矩阵

本表只记录活动产品能力，不保存历史阶段、分支、CI 运行号或命令流水。`Verified` 表示已有稳定自动化或契约依据；`Implemented` 表示主线已提供但仍需更广泛真实环境验证；`In Progress` 表示公开承诺所需门禁尚未关闭；`Deferred` 表示当前明确关闭。

| ID | 能力 | 状态 | 用户契约 | 实现与测试依据 |
|---|---|---|---|---|
| CORE-001 | 单一可执行文件与多运行角色 | Verified | client、daemon、askpass 与受限 helper 由同一源码构建且角色互斥。 | [入口](../../cmd/amsftp/main.go)与 [CLI 测试](../../internal/app/public_cli_test.go) |
| CORE-002 | 版本化本地 IPC | Verified | 不兼容协议在业务处理前失败，消息和帧有大小边界。 | [握手测试](../../internal/ipc/handshake_test.go)与[帧测试](../../internal/ipc/frame_test.go) |
| CORE-003 | Provider 统一契约 | Verified | LocalFS、SFTP 和 fake 使用统一列表、读取、能力与 mutation 语义。 | [Provider contract](../../internal/provider/contracttest/suite.go) |
| CORE-004 | 严格配置 | Verified | 配置有版本、确定默认值和未知字段拒绝，安全字段不被静默忽略。 | [配置 schema 测试](../../internal/config/schema_test.go)与[配置参考](../user/configuration.md) |
| AUTH-001 | 系统 OpenSSH | Verified | SSH 配置、认证和 host-key 由系统 `/usr/bin/ssh` 决定。 | [ADR-0001](../architecture/adr/0001-system-openssh-transport.md)与[OpenSSH 测试](../../internal/transport/openssh/config_inspection_test.go) |
| AUTH-003 | Kerberos、密钥与 Agent | Implemented | 复用 OpenSSH 已支持的认证链，不把认证材料搬入产品。 | [首次使用](../user/getting-started.md)与[威胁模型](../security/threat-model.md) |
| AUTH-006 | askpass broker | Verified | 交互答案只存在于当前受信任认证会话，没有 TUI 时任务进入等待。 | [认证 RPC 测试](../../internal/ipc/auth_rpc_test.go) |
| AUTH-009 | 秘密不持久化 | Verified | 配置、SQLite、缓存、日志和支持包不保存密码、私钥、票据或认证答案。 | [威胁模型](../security/threat-model.md)与[支持包测试](../../internal/supportbundle/bundle_test.go) |
| CONN-001 | LocalFS Endpoint | Verified | 任一栏可以浏览和操作本地绝对路径。 | [LocalFS Provider 测试](../../internal/provider/localfs/provider_test.go) |
| CONN-002 | 结构化 SFTP | Verified | 通过系统 OpenSSH stdio 使用 SFTP 协议，不解析交互式 sftp 文本。 | [SFTP Provider 测试](../../internal/provider/sftp/provider_test.go) |
| CONN-003 | 双位置启动 | Verified | 支持本地路径和 `host:/absolute/path` 的任意两栏组合。 | [CLI 参考](../user/cli.md) |
| CONN-005 | 多远端隔离 | Verified | 不同远端的连接、能力、错误和认证提示不会串线。 | [daemon Provider 路由测试](../../internal/daemon/provider_router_test.go) |
| CONN-006 | 断线与重连 | Verified | 只读请求可安全恢复，写任务交给持久状态机判断。 | [SFTP Provider 测试](../../internal/provider/sftp/provider_test.go)与[运维手册](../operations/runbook.md) |
| PANE-001 | 平等双栏 | Verified | 左右栏拥有独立 Endpoint、路径、光标、选择和加载状态。 | [TUI model 测试](../../internal/tui/model_test.go) |
| PANE-004 | 增量目录加载 | Verified | 首屏不等待完整目录读取，列表可取消并报告 partial。 | [Provider listing](../../internal/provider/listing.go)与[TUI 测试](../../internal/tui/render_test.go) |
| PANE-005 | 窗口化渲染 | Verified | 大目录只构造可见行与有界 overscan。 | [渲染测试](../../internal/tui/render_test.go) |
| WORK-001 | 持久工作区 | Verified | 保存两栏位置和视图策略，不保存认证秘密。 | [workspace 路由测试](../../internal/daemon/workspace_router_test.go) |
| WORK-006 | 工作区兼容迁移 | Verified | 历史 schema 确定迁移并保留原始备份，失败不覆盖来源。 | [兼容性边界](compatibility-boundaries.md) |
| VIM-001 | Vim-first 模式 | Verified | 默认 Normal 模式，导航、选择、计数与危险动作分层。 | [键位测试](../../internal/tui/keymap_test.go)与[键位参考](../user/keymap.md) |
| VIM-002 | `h/j/k/l` 与双栏操作 | Verified | 核心浏览和选择不依赖鼠标。 | [TUI reducer](../../internal/tui/reducer.go) |
| DAEMON-001 | owner-only daemon | Verified | daemon 只为当前用户服务，TUI 退出不隐式取消 Job。 | [daemon server 测试](../../internal/daemon/server_test.go) |
| DAEMON-002 | 安全 IPC 与生命周期 | Verified | socket、peer UID、协议和停止确认 fail closed。 | [daemon command 测试](../../internal/app/daemon_command_test.go) |
| JOB-001 | 持久 Job | Verified | 复制、移动和删除拥有稳定状态、事件、检查点和控制命令。 | [Job manager 测试](../../internal/transfer/manager_test.go)与[CLI 参考](../user/cli.md) |
| JOB-002 | 重启恢复 | Verified | daemon 重启后只跨越安全边界恢复，不把未知结果标记成功。 | [Job journal 测试](../../internal/transfer/job_journal_test.go) |
| COPY-001 | 流式安全复制 | Verified | 先写 part，验证和提交后再暴露最终目标。 | [worker 测试](../../internal/transfer/worker_test.go) |
| MOVE-001 | 安全移动 | Verified | 目标验证并提交前不删除来源。 | [move 实现](../../internal/transfer/move.go)与[持久传输指南](../user/durable-transfers.md) |
| DELETE-001 | 显式删除 | Verified | 删除与剪切分离，不可逆递归删除需要独立确认。 | [delete 实现](../../internal/transfer/delete.go)与[持久传输指南](../user/durable-transfers.md) |
| CONFLICT-001 | 冲突和并发修改 | Verified | overwrite、skip、rename 与编辑回传冲突都有明确选择，不静默覆盖。 | [Plan 测试](../../internal/transfer/plan_test.go)与[编辑测试](../../internal/tui/edit_session_test.go) |
| PREVIEW-001 | 有界预览 | Verified | 文本、图片和外部预览受字节、时间和终端能力限制；行滚动、EOF range 与空文件不会产生越界或零长度读取。 | [预览测试](../../internal/tui/preview_test.go)、[drawer 测试](../../internal/tui/drawer_test.go)、[编排测试](../../internal/app/preview_orchestration_test.go)与[预览指南](../user/preview-edit-cache.md) |
| EDIT-001 | 远端编辑和 opener | Verified | 使用缓存租约并在回传前复查远端版本。 | [edit 路由测试](../../internal/daemon/edit_router_test.go) |
| CACHE-001 | 配额与租约缓存 | Verified | LRU、ephemeral、pinned offline 不回收活跃租约。 | [缓存生命周期测试](../../internal/cachemanager/lifecycle_test.go) |
| SEARCH-001 | 有界文件名和内容搜索 | Verified | 搜索有时间、结果、深度和字节预算，耗尽时明确 partial。 | [搜索 contract](../../internal/search/filename_contract_test.go)与[搜索指南](../user/search-helper.md) |
| HELP-001 | 可选 Helper 协议 | Verified | Helper 不可用或不兼容时安全退化到 Level 0。 | [Helper 协议测试](../../internal/helper/protocol_test.go) |
| HELP-013 | Production Helper 分发 | Deferred | 没有真实签名托管、最终产物和公开批准前保持 CLOSED。 | [Helper 信任交接](../security/helper-trust-handoff.md)与[路线图](roadmap.md) |
| DIRECT-001 | Level 2 控制面 | Implemented | 预检不携带凭据，失败在提交前可解释地降级。 | [Level 2 预检测试](../../internal/transfer/level2_preflight_test.go) |
| DIRECT-002 | Production Level 2 数据面 | Deferred | 真正进程和网络隔离数据面完成前保持 CLOSED。 | [路线图](roadmap.md)与[架构总览](../architecture/overview.md) |
| SCALE-001 | 有界调度与资源账本 | Verified | 并发、连接、队列、内存、FD 和事件有硬上限与公平调度。 | [scheduler 测试](../../internal/transfer/scheduler_test.go)与[资源账本](../../internal/transfer/resource_ledger.go) |
| SEC-001 | 路径与本机权限 | Verified | 私有目录、文件和 socket 校验 owner、mode、symlink 和 peer UID。 | [威胁模型](../security/threat-model.md) |
| SEC-002 | 供应链与 workflow 策略 | Verified | action 固定 SHA，工具链和发行输入有可执行策略校验。 | [依赖策略](../security/dependency-policy.md)与[docscheck](../../internal/docscheck/check.go) |
| OBS-001 | 结构化诊断 | Verified | 日志、doctor 和支持包只暴露 allowlist 安全字段。 | [doctor 测试](../../internal/app/doctor_command_test.go)与[支持包测试](../../internal/supportbundle/bundle_test.go) |
| PLAT-001 | macOS 与 Linux | Implemented | macOS 15、Ubuntu 22.04/24.04 为当前目标，其他 Linux best-effort。 | [环境矩阵](environment-compatibility.md) |
| PLAT-005 | 标准 SFTP 基线 | Implemented | 非 OpenSSH SFTP server 可使用 Level 0，扩展缺失时不伪造能力。 | [标准兼容测试](../../internal/provider/sftp/standards_compatibility_test.go) |
| PLAT-006 | Unicode 与原始文件名 | Verified | UTF-8 正确显示，不可解码名称通过 list、stat 和 read 保持原始字节。 | [标准兼容测试](../../internal/provider/sftp/standards_compatibility_test.go)与[环境矩阵](environment-compatibility.md) |
| PLAT-009 | OpenSSH 范围 | Verified | 8.9p1 为最低验证基线，不按版本字符串做无依据硬拒绝。 | [环境矩阵](environment-compatibility.md) |
| REL-001 | 完整配置与 CLI | Verified | 配置、help、man、completion、退出码和 JSON 契约一致。 | [CLI 参考](../user/cli.md)与[配置参考](../user/configuration.md) |
| REL-002 | 内部预览发布 | Implemented | unsigned owner-only 预览可按 commit 和 checksum 安装。 | [内部预览说明](../release/internal-preview.md)与[当前状态](../development/status.md) |
| REL-003 | 数据迁移与回滚 | Implemented | 升级失败保留数据库、工作区和 pinned cache 的恢复路径。 | [升级与回滚](../release/UPGRADE.md)与[迁移测试](../../internal/statefs/historical_upgrade_test.go) |
| REL-004 | 公开签名发行物 | In Progress | 最终四平台字节、Darwin 签名公证和渠道绑定完成前不公开发布。 | [公开打包](../release/public-packaging.md)与[RC 门禁](../release/RC-GATES.md) |
| REL-006 | 原生兼容矩阵 | In Progress | 支持平台、OpenSSH、Kerberos、SFTP server 和终端声明必须有真实证据。 | [环境矩阵](environment-compatibility.md)与[路线图](roadmap.md) |
| REL-007 | 最终安全审查 | In Progress | 当前无未处置高风险项，但 production Helper、Level 2 和最终字节需独立复审。 | [发现台账](../security/finding-ledger.md)与[RC 门禁](../release/RC-GATES.md) |
| REL-009 | 用户与运维文档 | Implemented | README 覆盖安装、首次运行、操作、架构、限制和恢复路径。 | [README](../../README.md)与[文档导航](../README.md) |
| REL-010 | 公开 release 零缺口 | In Progress | 公开发布前所有非终态能力必须关闭或以明确决定延期。 | [RC 门禁](../release/RC-GATES.md) |
| REL-012 | 发布撤回与回滚 | In Progress | 公开渠道完成已发布字节的升级、回滚和撤回演练后才能关闭。 | [升级与回滚](../release/UPGRADE.md)与[路线图](roadmap.md) |
| REL-013 | 一键安装与升级 | Implemented | 严格 X.Y.Z 预览支持 checksum 验证安装脚本与 Homebrew formula；Apache-2.0、tap 与受保护凭据已准备，首个 tag 尚未发布。 | [安装说明](../release/INSTALL.md)、[升级与回滚](../release/UPGRADE.md)与[发布 workflow](../../.github/workflows/release.yml) |

## 更新规则

1. 行为变化时保留稳定 ID，更新状态、用户契约和稳定依据。
2. 新能力必须链接实现、自动化测试、ADR 或用户文档，不能引用聊天、临时分支或一次性日志。
3. `Verified` 需要可重复的契约或测试；环境相关能力在真实原生证据不足时保持 `Implemented` 或 `In Progress`。
4. 删除或延期能力时保留 ID，并链接说明原因的 ADR 或路线图。
