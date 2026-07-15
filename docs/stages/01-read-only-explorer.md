# Stage 1 — Read-only Explorer

- **状态**：In Progress
- **阶段类型**：第一个端到端用户切片
- **前置条件**：[Stage 0 — Foundation & Knowledge](00-foundation.md) 已通过退出门禁
- **完成后进入**：[Stage 2 — Durable Transfers](02-durable-transfers.md)

## 1. 阶段目标

交付安全、可恢复的只读双栏文件浏览器。任一栏都可以独立指向本机或任意 SSH 主机；认证、代理、主机配置和 Kerberos/GSSAPI 行为完全交给系统 OpenSSH。该切片要验证进程模型、IPC、认证提示、Provider 抽象、增量目录显示和 Vim-first 交互能够在真实环境协同工作。

“只读”是明确的安全边界：本阶段不能通过 UI、RPC 或命令旁路修改本地或远端文件。

## 2. 范围

### 2.1 包含

1. 单个 Go 二进制的 TUI/客户端与守护进程运行角色。
2. 守护进程自动启动、单用户私有 Unix Socket、客户端重连和协议协商。
3. LocalFS Provider 与基于 ADR-0001 精确 system-ssh argv 标准输入输出的 SFTP Provider。
4. 复用 `~/.ssh/config`、Host 别名、ProxyJump/ProxyCommand、Agent、硬件密钥及 Kerberos/GSSAPI 等系统 OpenSSH 能力。
5. 无终端守护进程的认证提示代理：附着 TUI 可响应，未附着时进入可恢复的 `waiting_auth` 状态。
6. 两栏独立 Endpoint/Location、栏间切换、目录导航、排序、选择状态和增量列表。
7. SSH 主机模糊选择、CLI 路径启动和保存/恢复工作区。
8. Vim-first 基础导航、当前目录过滤和有界文本/元数据预览。
9. 连接状态、错误、退化和重连反馈。

### 2.2 非目标

1. 不提供复制、移动、上传、删除、重命名、建目录或任何其他写操作。
2. 不建立持久化传输队列或后台作业状态机。
3. 不调用或安装远端 Helper。
4. 不做递归文件搜索、内容搜索、图片协议预览或外部编辑。
5. 不承诺最终规模性能；但本阶段实现必须是增量、有界的，不能阻塞未来优化。

## 3. 依赖与边界

### 3.1 Stage 0 输入

- IPC 信封、Provider 契约、能力模型和 Fake Provider。
- macOS/Linux 构建测试入口。
- 文档真相链和阶段验证模板。
- [ADR-0006](../architecture/adr/0006-public-identity-toolchain-and-runtime-libraries.md) 冻结的 tcell v3.4.0、pkg/sftp v1.13.10 与 `log/slog` 边界。
- [ADR-0007](../architecture/adr/0007-platform-paths-runtime-socket-and-ipc.md) 冻结的平台路径、peer UID、单实例和 `control-v1.sock` 规则。
- [ADR-0009](../architecture/adr/0009-supported-platform-ci-and-packaging-baseline.md) 冻结的 macOS 15/Ubuntu 22.04、架构与 OpenSSH 测试基线。

### 3.2 安全边界

- 守护进程 Socket 必须位于 ADR-0007 规定的当前用户私有运行目录，权限不宽于 `0600`；TMPDIR/XDG/override 与完整 ancestor chain 必须通过 owner/mode/no-symlink/ACL 验证，Darwin 允许不扩权的标准 deny ACL，Linux 拒绝扩权 access/default ACL，唯一宽松 DAC 祖先特例是 runtime 回退使用的 root-owned sticky `01777 /tmp`。Linux/macOS 均须核验对端 uid，查询失败时拒绝连接。
- 不把密码、私钥、SSH Agent 内容或 Kerberos ticket 写入配置、数据库、日志或缓存。
- 主机密钥检查遵循用户 OpenSSH 配置，不自动关闭或绕过。
- 认证提示只在明确关联的连接尝试上展示，响应有超时和单次消费语义。
- 命令参数通过参数数组传递，Host 别名不得拼接进 shell 字符串。

## 4. 详细交付物

### S1-D01 守护进程生命周期与 IPC

- 客户端发现现有守护进程，或安全地启动一个当前用户实例。
- 并发启动通过锁或等价机制收敛为一个实例。
- Socket 创建、权限、陈旧 Socket 清理和正常退出有明确规则。
- 客户端连接时交换协议版本和能力；不兼容时给出可操作错误。
- 守护进程为 Endpoint 会话、工作区快照和事件流提供 RPC。
- 客户端断开不导致守护进程崩溃；只读请求可取消，连接资源可回收。

### S1-D02 系统 OpenSSH 传输适配

- 使用 ADR-0001 验证后的 absolute OpenSSH binary 和精确 argv 建立 SFTP 子系统通道，由 Go SFTP 协议层处理结构化文件操作；正式平台默认 `/usr/bin/ssh` 且不搜索 `PATH`，显式 override 每次启动前重验完整 owner/mode/ACL/no-symlink/special-bits 链。
- 保留用户的 Host 别名和 OpenSSH 配置解析权，不自行重新解释完整 SSH 配置。
- 固定关闭 TTY、交互式 escape、agent/X11/端口转发、LocalCommand/RemoteCommand、后台化和 tunnel，并保证 stdin 可用；不得关闭 host key、认证、GSSAPI/Kerberos、登录所用 agent、ProxyJump/ProxyCommand 或 ControlMaster。
- host alias 必须拒绝空值、前导短横线、NUL 和 ASCII 控制字符；使用直接进程 argv，禁止经过 shell 或允许输入插入额外 ssh 选项。poisoned `PATH` fake ssh 必须 0-hit，不安全 absolute override 在 exec 前拒绝。
- 捕获可分类的启动失败、主机密钥失败、认证失败、子系统缺失、网络断开和远端退出。
- 进程标准错误以脱敏、限长方式关联到连接诊断，不混入 SFTP 数据流。
- 连接取消和守护进程退出时正确终止子进程并回收管道。

### S1-D03 认证提示 Broker

- 通过受控 `SSH_ASKPASS`/等价机制接收系统 OpenSSH 需要的交互提示。
- 提示包含连接显示名、提示类型和超时，不展示未经处理的敏感环境内容。
- 已附着的 TUI 可一次性响应；多个客户端同时附着时有唯一归属策略。
- 无客户端、客户端退出或超时后连接进入 `waiting_auth` 或失败，不无限阻塞。
- Broker 只转发输入，不持久化、不缓存、不回显秘密。

### S1-D04 LocalFS 与 SFTP Provider

- 两者通过 Stage 0 的同一契约提供 Endpoint、Location、目录条目和范围读取。
- 路径规范化尊重各自根、分隔符和符号链接语义。
- 目录枚举流式进入守护进程/客户端，慢网络时 UI 仍可取消和导航。
- 文件类型、大小、时间、权限、链接目标等缺失或不可靠字段明确标示。
- Provider 能力随会话发布；SFTP 扩展只在服务器明确支持时启用。

### S1-D05 双栏 TUI

- 两栏具有独立 Endpoint、当前目录、光标、选中项、排序和加载状态。
- `Tab` 切换活动栏；`h/j/k/l` 完成返回父级、上下移动和进入目录。
- 数字计数适用于可安全重复的导航动作；未知组合不修改状态。
- `v`/`V` 和 `Space` 可建立选择/标记状态，为 Stage 2 的操作剪贴板做准备，但本阶段不执行写操作。
- `/` 对当前已加载/流入的目录结果做即时过滤，清除过滤后恢复原视图。
- 超大列表使用增量数据源；渲染只处理可视窗口和有限预取区。
- 状态栏显示活动 Endpoint、规范路径、连接/加载状态和只读标识。

### S1-D06 启动、主机选择与工作区

公开命令已冻结为 `amsftp`，支持以下入口语义：

- `amsftp <left-location> <right-location>`：两个显式 Location，分别为本地路径或 `host-alias:/remote/path`。
- `amsftp <location>`：一个显式 Location 加默认另一栏。
- `amsftp`：从保存工作区或 SSH Host 选择器进入。
- `amsftp --workspace <name>`：通过工作区名称恢复两个 Pane State。

主机选择器读取系统 OpenSSH 可见的 Host 别名，过滤明显的通配模板；用户仍可手工输入有效别名。工作区只保存位置、视图和非敏感偏好，不复制 SSH 认证配置。

### S1-D07 只读预览

- 对文本、JSON、代码和未知二进制提供元数据或有界内容预览。
- 默认只读取开头的有限字节；大文件可请求下一段或尾部，所有读取可取消。
- 编码失败、权限错误、断线和读取中断在预览区显式展示。
- 预览不会下载整个大文件，也不会把完整内容写入长期缓存。

### S1-D08 连接恢复与可观察性

- 每个 Endpoint 显示 `connecting`、`ready`、`waiting_auth`、`degraded`、`disconnected` 等用户可理解状态。
- 瞬时网络故障采用有上限、带抖动的重连；认证或配置错误不盲目重试。
- 重连后重新协商能力，并验证当前目录；目录不存在或权限变化时进入最近可访问父级并告知用户。
- 日志使用关联 ID，脱敏路径以外的秘密；用户可复制诊断摘要。

## 5. 里程碑顺序

### M1.1 — 本地只读端到端

1. 按 ADR-0006 精确引入 tcell v3.4.0，完成许可证/module graph/双 Go/四目标 intake；先以测试实现 ADR-0007 的 Paths、单实例 lock 与 peer UID 平台边界。
2. 守护进程自动启动与 IPC 连接。
3. LocalFS Provider 接入。
4. 双栏 TUI 在本地/本地模式完成窗口化导航、过滤和基础预览。

门禁：完全离线环境下可稳定浏览，客户端多次退出/重进不泄漏守护进程资源。

### M1.2 — 真实 SFTP Endpoint

1. 按 ADR-0006 精确引入 pkg/sftp v1.13.10 并完成依赖 intake，再按 ADR-0001 精确 argv 通过系统 OpenSSH 启动 SFTP 子系统。
2. 接入 SFTP Provider 和错误映射。
3. 完成本地/远端与远端/远端独立浏览。

门禁：临时 `sshd`、Host 别名及非默认端口均通过集成测试。

### M1.3 — 认证与复杂 SSH 配置

1. 接入认证 Broker。
2. 验证 ProxyCommand/ProxyJump、Agent 和 Kerberos/GSSAPI 路径。
3. 覆盖无 TUI、超时、取消和错误凭据行为。

门禁：认证秘密不落盘；无附着客户端时不会永久挂起。

### M1.4 — 工作区与恢复

1. Host 选择器、CLI Location 和保存工作区。
2. 连接断开、守护进程重启和位置失效后的恢复。
3. macOS/Linux 交互与终端尺寸冒烟。

门禁：三个 Endpoint 组合和所有恢复路径都有可追溯证据。

## 6. 可验证退出标准

- [ ] 本地/本地、本地/远端、远端/远端三种组合可在任一栏独立切换和导航。
- [ ] UI、RPC 和 CLI 均没有文件写入入口；权限受限夹具证明只读边界。
- [ ] config/state/cache/log/runtime 根、完整 ancestor chain、Darwin `/var -> /private/var` 与 `/tmp -> /private/tmp` 别名/deny-vs-allow ACL、Linux XDG/access+default ACL、sticky `/tmp` 回退、Unix Socket 权限和单实例行为在 macOS/Linux 均验证；不安全持久/override 路径 fail closed 且不产生隐蔽回退状态。
- [ ] 系统 OpenSSH 的 Host 别名、ProxyCommand/ProxyJump 与至少一种非密码认证通过。
- [ ] MIT Kerberos/GSSAPI 受控验证成功，且 ticket/秘密未出现在日志、工作区或数据库。
- [ ] 认证等待在无客户端、超时和取消场景可恢复。
- [ ] 目录结果增量出现；慢 Provider 不冻结输入，导航可取消旧请求。
- [ ] 大文件预览读取量有明确上限，二进制不会误当完整文本载入。
- [ ] SSH 断开、守护进程重启和当前目录失效均有可预测恢复。
- [ ] 两个平台的键位、窄终端、调整尺寸和退出重入冒烟通过。

## 7. 测试矩阵

| 层级 | 场景 | 必须证明 | 证据 |
|---|---|---|---|
| 单元 | Location 解析与规范化 | 本地和 `host:/path` 不混淆，边界清晰 | 单元测试 |
| 契约 | LocalFS/SFTP/Fake Provider | 列表、读取、取消和错误一致 | 共享 Provider 套件 |
| IPC | 自动启动、并发启动、重连 | 单实例、版本协商、资源回收 | 集成测试 |
| SSH | 临时 `sshd` | 真实 SFTP 列表、读取、断线 | 可重复夹具 |
| SSH | ProxyCommand/Jump | 完全复用系统 OpenSSH 路径 | 集成记录 |
| Auth | Askpass、无客户端、超时 | 不泄密、不死锁、可重试 | 负向测试 |
| Auth | MIT Kerberos/GSSAPI | 现有 ticket 可完成认证 | 隔离环境记录 |
| TUI | Vim 导航、双栏、筛选 | 键位与状态转换确定 | 模型/快照测试 |
| 性能 | 5 万项合成目录的首屏 | 增量响应、可取消、有界渲染 | 基线基准 |
| 平台 | macOS/Linux | Socket、信号、终端行为一致 | 平台冒烟记录 |

## 8. 失败与回滚策略

- 远端连接失败只影响对应 Endpoint；另一栏和本地浏览继续可用。
- SFTP Provider 出现协议或扩展问题时，禁用可疑扩展并回退标准操作，不绕过主机密钥或认证策略。
- 守护进程升级导致 IPC 不兼容时，客户端拒绝连接并提供安全重启指引；不得静默解释未知消息。
- 工作区损坏时保留原文件用于诊断，加载安全默认双本地工作区；不覆盖用户 SSH 配置。
- TUI 渲染故障或终端不支持特性时回退纯文本视图，核心导航仍可用。
- 回滚 Stage 1 版本前先关闭当前守护进程；由于本阶段无用户文件写入，数据风险限于工作区元数据，需保持向后可读或提供备份恢复。

## 9. 完成时必须更新的文档

- `PROJECT_STATE.md`：真实环境覆盖、已知 SSH 兼容限制、Stage 2 第一动作。
- `IMPLEMENTATION_PLAN.md`：Stage 1 状态和最终验证命令。
- `docs/product/feature-matrix.md`：浏览、认证、工作区、预览功能的证据。
- `docs/architecture/overview.md`：实际进程、IPC、认证 Broker 和 Provider 数据流。
- SSH/OpenSSH 与守护进程相关 ADR。
- `docs/verification/stage-01.md`：各平台和认证模式验证结果。
- 用户文档：连接方式、SSH 配置复用、工作区和只读键位。

## 10. 下一阶段交接信息

Stage 2 开始前必须记录：

1. 守护进程自动启动、Socket 路径与协议版本的实际行为。
2. LocalFS/SFTP Provider 已实现能力与明确缺口。
3. 真实测试过的 OpenSSH 配置和认证模式；Kerberos 环境如何复现。
4. 列表/预览的读取上限、取消语义与性能基线。
5. 工作区持久化格式及兼容承诺。
6. Stage 2 可复用的断线、短读、权限和慢网络夹具。
7. 最后绿色命令以及仍需人工验证的项目。

Stage 2 不得通过绕过 Provider 或系统 OpenSSH 来加速写操作实现。
