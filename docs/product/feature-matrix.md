# 1.0 功能矩阵

> - 产品：`AMSFTP`（公开命令 `amsftp`；仓库名 `awesome-mac-sftp`）
> - 基线：已批准产品设计
> - 实现状态：Stage 0 已完成；Stage 1 正在实施；Stage 2–6 尚未开始
> - 最后更新：2026-07-15

## 1. 使用规则

本矩阵是 1.0 交付范围的逐项事实源。每条能力使用稳定 ID；ID 一经进入矩阵不得复用，能力被取消时保留原行并将状态改为 `Removed`，同时链接对应 ADR。实现不得只在代码或聊天中增加能力而不更新本矩阵。

Stage 0 行已按完成证据标记为 `Verified`；M1.1 的本地候选能力标记为 `In Progress`，其余 Stage 1–6 行仍为 `Planned`。实现后必须用可复现的实际证据替换预期证据，包括测试名或命令、验证平台、测试夹具/环境和结果记录；只有代码、测试、阶段验证记录、功能矩阵与 `PROJECT_STATE.md` 一致时，状态才能改为 `Verified`。

状态词义：

- `Planned`：范围和验收标准已确定，尚无实现证据；
- `In Progress`：当前阶段正在实现，尚未满足全部验收标准；
- `Implemented`：实现已存在，阶段验证尚未全部完成；
- `Verified`：自动测试与要求的人工/真实环境验证均有证据；
- `Deferred`：经 ADR 明确移出 1.0，原 ID 保留；
- `Removed`：能力明确取消，原 ID 保留且不得复用。

## 2. 阶段索引

| Stage | 名称 | 交付主题 |
|---|---|---|
| 0 | Foundation & Knowledge | 工程、契约、测试基座和可续接文档 |
| 1 | Read-only Explorer | OpenSSH/SFTP 连接、任意双端点浏览和 Vim 导航 |
| 2 | Durable Transfers | 持久任务、安全复制/移动/删除、冲突与恢复 |
| 3 | Preview, Edit & Cache | 日常预览、Vim 编辑、本机打开、缓存、命令与 shell |
| 4 | Search & Helper | 可选 helper、递归搜索、强哈希和远端增强能力 |
| 5 | Direct & Scale | 服务端快速路径、跨主机直传、超大规模与资源控制 |
| 6 | Hardening & Release | 配置、兼容、安全、迁移、诊断和发布 |

## 3. 基础契约与工程

| ID | 能力 | Stage | 状态 | 验收标准 | 当前证据 |
|---|---|---:|---|---|---|
| CORE-001 | 单一程序、多运行角色 | 0 | Verified | 同一 Go 程序具有稳定、互斥且可测试的 client、daemon、askpass 与 helper 角色分发边界；四个目标 OS/架构由同一源码构建，具体角色功能由所属后续阶段交付。 | 角色参数/退出码/流契约测试、四目标构建、darwin/arm64 本机 help/version 冒烟及 Hosted 四平台 native/oldstable 全部通过。见 [Stage 0 verification](../verification/stage-00.md#core-evidence)。 |
| CORE-002 | 版本化本地 IPC envelope | 0 | Verified | 冻结带协议版本、请求 ID、类型、载荷和结构化错误的 framed JSON 编解码与握手契约；不兼容版本在进入业务处理前被确定拒绝，实际 TUI↔daemon 连接由 Stage 1 交付。 | 信封、握手、帧、字节路径、取消、严格 UTF-8 和 fuzz 测试通过本地完整门禁与 Hosted quality/native 门禁。见 [Stage 0 verification](../verification/stage-00.md#core-evidence)。 |
| CORE-003 | Endpoint/Provider 统一契约 | 0 | Verified | 冻结传输无关的只读 Provider、可选 MutableProvider、分页/游标和句柄契约，Fake 通过可复用 contract suite；LocalFS、SFTP 和 helper 在各自阶段接入同一套件。 | Provider 验证器、共享 contract suite 与 Fake conformance 通过本地双工具链和 Hosted 门禁。见 [Stage 0 verification](../verification/stage-00.md#core-evidence)。 |
| CORE-004 | 能力协商模型 | 0 | Verified | 冻结 SessionID、单调 generation、complete/unknown 语义和能力丢失错误；Fake 可确定复现 complete/incomplete、撤回与恢复，实际路由/UI 由后续阶段验证。 | 能力值对象、Provider contract、D3 session-local bookkeeping 与 E1 typed operation gating 通过完整门禁。见 [Stage 0 verification](../verification/stage-00.md#core-evidence)。 |
| CORE-005 | 可控 fake provider | 0 | Verified | 测试 Provider 可确定注入延迟、gate、短读/短写、权限错误、断线、磁盘满、陈旧 stat 与非原子 rename 报告，且在并发/race 下可重复。 | Fake 故障脚本 Groups A–E、重复/race/fuzz、双工具链和独立终审全部通过，终审为零 High/Medium/Low findings。见 [Stage 0 verification](../verification/stage-00.md#core-evidence)。 |
| CORE-006 | 配置 schema 与默认值 | 0 | Verified | 配置有版本号、确定默认值、严格类型和可定位错误；未知安全关键字段不被静默忽略。 | 严格 schema、默认值、版本、未知字段与 deterministic clock 测试通过本地及 Hosted 门禁。见 [Stage 0 verification](../verification/stage-00.md#core-evidence)。 |
| CORE-007 | 跨平台持续集成基线 | 0 | Verified | 每次变更至少在 macOS 与 Linux 执行 build、unit、contract 和格式检查，Go race/fuzz 按阶段接入。 | 固定 SHA workflow、双平台 native/oldstable、四目标 build、八份独立缓存 reproducibility producer、compare 与 final provenance aggregation 在 Hosted run 29394698864 全部通过。见 [Stage 0 verification](../verification/stage-00.md#core-evidence)。 |
| CORE-008 | 文档真相链 | 0 | Verified | Vision、Feature Matrix、Stage Spec、Verification、Project State 可双向追溯；新会话按固定读取顺序恢复当前阶段。 | docscheck、持久计划和两轮独立 cold-start audit 均通过；完成证据与 Stage 1 M1.1 第一动作已归档。见 [Stage 0 verification](../verification/stage-00.md#core-evidence)。 |

## 4. 认证与信任链

| ID | 能力 | Stage | 状态 | 验收标准 | 当前证据 |
|---|---|---:|---|---|---|
| AUTH-001 | 系统 OpenSSH 为唯一 SSH 实现 | 1 | Planned | 远端会话按 ADR-0001 以 validated absolute OpenSSH path 和精确安全 argv 启动 SFTP subsystem；默认 `/usr/bin/ssh`、不查找 PATH，产品不实现第二套 SSH/Kerberos 协议栈。 | 未实施；预期证据：absolute resolver/poisoned PATH/逐参数 argv 单元测试与真实 OpenSSH SFTP subsystem 浏览测试。 |
| AUTH-002 | 有边界地继承 `~/.ssh/config` | 1 | Planned | Host/Include/Match/Identity/host-key/auth/GSSAPI/proxy/agent按system ssh生效；SFTP固定关闭TTY/escape/forward/commands/background/tunnel及新GSS delegation但保留ControlMaster兼容，既有master状态是用户边界。Helper与remote `!`/`gs`另强制fresh transport、GSS delegation和ControlMaster/Path/Persist off。 | 未实施；预期证据：冲突config、真实sshd、GSSAPI-only、CM新建/复用及fresh helper/shell不复用矩阵。 |
| AUTH-003 | Kerberos/GSSAPI 认证 | 1 | Planned | 有效本机 Kerberos ticket 和 OpenSSH GSSAPI 配置可直接建立 SFTP 会话，产品不要求再次保存密码。 | 未实施；预期证据：MIT Kerberos 临时 realm 的成功、票据过期与续期后重试集成测试。 |
| AUTH-004 | OpenSSH 支持的 agent/密钥/交互认证 | 1 | Planned | SSH agent、文件密钥、硬件密钥、密码与多因素认证由已安装 OpenSSH 处理；产品只转交交互提示和结果。 | 未实施；预期证据：agent、key、password 和双提示 MFA 的矩阵集成测试。 |
| AUTH-005 | ProxyCommand/ProxyJump 链路 | 1 | Planned | 用户现有代理命令与跳板配置无需复制到产品即可用于浏览和任务连接。 | 未实施；预期证据：临时 bastion/ProxyCommand 拓扑端到端测试。 |
| AUTH-006 | askpass broker | 1 | Planned | daemon 将 OpenSSH 交互提示发送到当前受信任 TUI，答案仅在内存和该次认证会话中存在；提示来源与目标端点清晰可见。 | 未实施；预期证据：askpass 请求/取消/超时/多提示测试及内存与持久层秘密扫描。 |
| AUTH-007 | 无附着客户端时等待认证 | 2 | Planned | 任务需要交互认证且没有 TUI 时进入 `waiting_auth`；重新附着后可继续，不进行无限失败重试。 | 未实施；预期证据：后台任务认证过期、TUI 退出/重连状态机测试。 |
| AUTH-008 | Host key 语义不降级 | 1 | Planned | StrictHostKeyChecking、known_hosts 和主机密钥变更完全遵循系统 OpenSSH；产品不自动接受未知或变化密钥。 | 未实施；预期证据：首次未知、已知、变更 host key 三类集成测试。 |
| AUTH-009 | 不持久化认证秘密 | 1 | Planned | SQLite、配置、缓存、日志和诊断包中均不出现密码、私钥内容、askpass 答案、agent 数据或 Kerberos ticket。 | 未实施；预期证据：持久文件/日志扫描测试与安全审查记录。 |
| AUTH-010 | 认证失败可恢复 | 2 | Planned | 认证失败保留任务与安全检查点，展示端点和 OpenSSH 原因；票据/agent 修复后可手动或策略化重试。 | 未实施；预期证据：过期 ticket、锁定 agent、错误密码修复后的恢复测试。 |

## 5. 连接与端点

| ID | 能力 | Stage | 状态 | 验收标准 | 当前证据 |
|---|---|---:|---|---|---|
| CONN-001 | 本地文件系统端点 | 1 | In Progress | 任一栏可浏览本地绝对路径，并使用与远端一致的导航、选择和能力展示模型。 | LocalFS contract、分页/取消/符号链接/范围读取测试与 local/local PTY 已通过；Hosted native evidence pending. See [Stage 1 verification](../verification/stage-01.md). |
| CONN-002 | OpenSSH stdio 上的结构化 SFTP | 1 | Planned | daemon 按 ADR-0001 的 validated-absolute-ssh-path 与精确 argv 从子进程 stdin/stdout 创建 SFTP client；binary/host 均不可经 PATH 或选项注入，也不解析人类可读 `sftp` 命令输出。 | 未实施；预期证据：default/override binary 完整 ancestor+ACL+special-bits、poisoned PATH、argv/host、真实 sshd list/stat/read、副作用冲突配置和协议错误注入测试。 |
| CONN-003 | CLI 双路径启动 | 1 | Planned | `amsftp <left> <right>` 接受本地路径与 `host:path`，两个初始位置均被解析、连接并独立报告错误。 | 未实施；预期证据：local/local、local/remote、remote/remote 参数契约测试。 |
| CONN-004 | 无参数 SSH host 模糊选择 | 1 | Planned | 无参数启动从 OpenSSH 配置可见 Host alias 构建可搜索列表；通配符和不可直接选择项有确定处理。 | 未实施；预期证据：多文件 Include/通配 Host fixture 的 picker 快照与选择测试。 |
| CONN-005 | 多远端并存 | 1 | Planned | daemon 可同时维护 remote A 与 remote B 会话，连接、错误、认证提示和能力不会串线。 | 未实施；预期证据：两个隔离 sshd 并发浏览和断开其中一个的集成测试。 |
| CONN-006 | 自动断线检测与重连 | 1 | Planned | EOF、网络中断和 server restart 被识别为连接状态变化；只读操作可安全重发，写任务交给恢复状态机。 | 未实施；预期证据：断网/重启 sshd 后列表恢复及写任务不重复提交测试。 |
| CONN-007 | 会话级能力探测 | 1 | Planned | 建连后记录 SFTP 扩展、路径/rename 能力和 helper 可用性；重连后重新探测，旧能力不盲目复用。 | 未实施；预期证据：不同 SFTP server capability fixtures 和重连能力变化测试。 |
| CONN-008 | 规范化 Location | 1 | Planned | Location 始终由 Endpoint ID 与规范化绝对路径组成；不同端点的相同路径不会被视为同一对象。 | 未实施；预期证据：路径规范化、根目录、相对路径和跨端点相等性单元测试。 |
| CONN-009 | 连接错误带上下文 | 1 | Planned | 错误至少包含端点显示名、阶段、OpenSSH 退出/标准错误摘要与可行动建议，不只显示“连接失败”。 | 未实施；预期证据：DNS、拒绝连接、host key、认证、SFTP subsystem 缺失错误快照测试。 |
| CONN-010 | 受控会话复用与空闲回收 | 5 | Planned | 同端点操作在安全范围内复用连接；并发和空闲回收受配置限制，不因大任务无限创建 SSH 进程。 | 未实施；预期证据：连接池并发上限、空闲回收和任务隔离压力测试。 |

## 6. 双栏浏览

| ID | 能力 | Stage | 状态 | 验收标准 | 当前证据 |
|---|---|---:|---|---|---|
| PANE-001 | 两个平等文件栏 | 1 | In Progress | 启动后始终存在左右两栏；每栏拥有独立端点、路径、光标、选择、排序和加载状态。 | 双栏 reducer/render tests 与 local/local PTY 通过；排序控制和 Hosted native evidence pending. See [Stage 1 verification](../verification/stage-01.md). |
| PANE-002 | 任意端点组合 | 1 | Planned | local/local、local/remote、remote/local、同 remote 双路径和 remote A/remote B 均能浏览。 | 未实施；预期证据：五种组合的端到端浏览矩阵。 |
| PANE-003 | 栏内独立切换端点 | 1 | Planned | 用户可只改变活动栏的端点，另一栏路径、选择和连接保持不变。 | 未实施；预期证据：端点 picker 交互测试和非活动栏状态保持断言。 |
| PANE-004 | 增量目录加载 | 1 | In Progress | 目录项以流式/分页方式进入模型，首屏无需等待完整目录读取，加载可取消。 | 256-entry RPC pages、generation-safe reducer and cancellation pass locally; delayed real SFTP evidence pending. See [Stage 1 verification](../verification/stage-01.md). |
| PANE-005 | 虚拟化文件列表 | 1 | In Progress | renderer 只构造可见行与有界 overscan，不为数万条目录项创建等量 UI 组件；5 万项结构性基准证明渲染与分配规模只随可见窗口增长。 | Visible-window/overscan tests and 50,000-entry structural benchmark pass locally; Hosted evidence pending. See [Stage 1 verification](../verification/stage-01.md). |
| PANE-006 | 文件元数据与类型展示 | 1 | Planned | 名称、类型、大小、修改时间、权限和链接信息按端点可用数据展示；未知字段不伪造。 | 未实施；预期证据：local/SFTP 元数据 fixture 快照与缺失字段测试。 |
| PANE-007 | 排序、隐藏文件与刷新 | 1 | Planned | 每栏可独立选择排序、显示隐藏文件和手动刷新；刷新尽量保留规范化路径对应的光标/标记。 | 未实施；预期证据：排序稳定性、隐藏切换和刷新选择保持测试。 |
| PANE-008 | 直接路径导航 | 1 | Planned | 用户可输入绝对路径或进入父/子目录；无权限、不存在和非目录路径保留原位置并显示原因。 | 未实施；预期证据：根目录、深路径、权限拒绝和不存在路径交互测试。 |
| PANE-009 | 符号链接可见且递归保守 | 2 | Planned | 链接目标信息可见；递归复制/搜索默认不跟随目录链接，除非用户策略显式允许并有循环保护。 | 未实施；预期证据：链接文件、目录环和跨边界链接的行为测试。 |
| PANE-010 | 部分结果状态 | 1 | Planned | 列表被取消、断线或权限错误截断时明确标为 partial，不把已显示条目误报为完整目录。 | 未实施；预期证据：中途错误/取消的 partial 标记与刷新恢复测试。 |

## 7. 工作区与启动

| ID | 能力 | Stage | 状态 | 验收标准 | 当前证据 |
|---|---|---:|---|---|---|
| WORK-001 | 保存双栏工作区 | 1 | Planned | 工作区保存两栏端点引用、路径、排序/过滤、布局与缓存策略，保存动作原子化。 | 未实施；预期证据：workspace round-trip、写入中断和损坏文件恢复测试。 |
| WORK-002 | `--workspace` 启动 | 1 | Planned | `amsftp --workspace <name>` 恢复工作区；缺失端点或路径逐栏报错且允许修复。 | 未实施；预期证据：正常、缺失 host、路径无权限三类 CLI 集成测试。 |
| WORK-003 | 最近工作区与模糊选择 | 1 | Planned | 无参数启动可选择已保存工作区或 SSH host，最近使用排序可预测且不影响显式搜索。 | 未实施；预期证据：picker 排序/过滤快照测试。 |
| WORK-004 | 工作区不含秘密 | 1 | Planned | 工作区只引用 OpenSSH host alias 和产品设置，不包含密码、私钥、ticket 或认证答案。 | 未实施；预期证据：序列化内容 schema 测试和秘密模式扫描。 |
| WORK-005 | 每工作区缓存模式 | 3 | Planned | 工作区可选默认 LRU、ephemeral 或 pinned offline；恢复时准确应用并显示当前模式。 | 未实施；预期证据：三种工作区缓存策略的重启 round-trip 测试。 |
| WORK-006 | 工作区 schema 迁移 | 6 | Planned | 旧版本工作区在备份后确定性迁移；不可迁移内容不被覆盖，并给出导出/修复路径。 | 未实施；预期证据：至少两个历史 fixture 的升级、失败回滚和幂等迁移测试。 |

## 8. Vim-first 交互

| ID | 能力 | Stage | 状态 | 验收标准 | 当前证据 |
|---|---|---:|---|---|---|
| VIM-001 | 默认 Normal 模式 | 1 | Planned | 启动与退出弹层后回到 Normal；当前模式始终可见，普通字母不会意外输入路径。 | 未实施；预期证据：模式状态机单元测试与状态栏快照。 |
| VIM-002 | `h/j/k/l` 导航 | 1 | In Progress | `j/k` 移动光标，`h` 返回父目录，`l` 打开目录或触发默认文件动作；边界行为稳定。 | Pure reducer and tcell translation tests pass for local navigation; remote evidence pending. See [Stage 1 verification](../verification/stage-01.md). |
| VIM-003 | `Tab` 切换活动栏 | 1 | In Progress | `Tab` 只改变活动栏，两个 PaneState 均完整保留；视觉焦点清晰。 | Pane independence and renderer focus tests pass locally; Hosted PTY evidence pending. See [Stage 1 verification](../verification/stage-01.md). |
| VIM-004 | `v`/`V` 连续选择 | 1 | Planned | Visual 选择可扩展、收缩、确认或取消；刷新后按规范化条目身份重映射，无法重映射时明确移除。 | 未实施；预期证据：选择状态机、目录刷新与条目消失测试。 |
| VIM-005 | `Space` 离散标记 | 1 | Planned | Normal 模式下 `Space` 独立增减当前条目标记，与连续 Visual 选择可合并为操作集合且不重复。 | 未实施；预期证据：离散/连续选择合并与去重单元测试。 |
| VIM-006 | `y` 复制引用 | 2 | Planned | `y` 将选中 Location 引用写入内部剪贴板，不读取文件内容、不立即启动传输。 | 未实施；预期证据：跨端点 clipboard 序列化与源变化前计划测试。 |
| VIM-007 | `d` 剪切引用 | 2 | Planned | `d` 只标记 cut intent，不删除或重命名任何源；状态栏清晰显示待移动项。 | 未实施；预期证据：按下 `d` 后 provider 零写操作断言及 UI 快照。 |
| VIM-008 | `p` 创建操作意图 | 2 | Planned | `p` 以剪贴板和目标栏位置创建不可变 OperationIntent；普通复制直接排队，高风险项进入确认。 | 未实施；预期证据：copy/cut、多选、目标变化和风险分支测试。 |
| VIM-009 | `D` 执行真实删除 | 2 | Planned | 只有大写 `D` 进入删除计划与确认；确认内容包含端点、数量、目录递归和可逆性。 | 未实施；预期证据：大小写键差异、确认取消和不可逆警示快照测试。 |
| VIM-010 | 计数前缀 | 2 | Planned | 支持如 `5j`、`3k` 和适用操作的计数；计数越界有确定截断且不会泄漏到下一命令。 | 未实施；预期证据：键序列解析与边界表驱动测试。 |
| VIM-011 | `.` 重复 | 2 | Planned | 重复最近一次可重复的高层动作，重新绑定当前 Location 并重新执行能力/风险检查，不复用过期确认。 | 未实施；预期证据：rename/transfer 等可重复动作及高风险重新确认测试。 |
| VIM-012 | 可取消的模式与弹层 | 1 | Planned | `Esc` 按层级退出输入、选择、预览或确认，不中断已经排队的后台任务。 | 未实施；预期证据：嵌套 UI 状态的 Esc 优先级测试。 |
| VIM-013 | 可配置键位且 Vim 默认不变 | 6 | Planned | 用户可重映射非保留动作并检测冲突；无配置时始终使用文档化 Vim 默认键位。 | 未实施；预期证据：默认 keymap 快照、冲突校验和自定义 round-trip 测试。 |
| VIM-014 | 1.0 不实现宏与命名寄存器 | 6 | Planned | 帮助和配置 schema 不宣称完整宏/命名寄存器；相关键不会造成未定义破坏性行为。 | 未实施；预期证据：1.0 help 快照和保留键行为测试。 |
| VIM-015 | `r` 重命名 | 2 | Planned | `r` 以当前或单个选中条目创建重命名意图，提交前验证同端点能力、目标冲突与源身份。 | 未实施；预期证据：文件/目录、目标存在、源变化和取消重命名测试。 |
| VIM-016 | `K`/`J`/`L` 抽屉快捷键 | 3 | Planned | 大写 `K` 打开 Preview、`J` 打开 Jobs、`L` 打开 Log；重复按键按定义收起或聚焦，不与小写导航串线。 | 未实施；预期证据：三个快捷键、重复按键和模式优先级 TUI 测试。 |
| VIM-017 | `e`/`o` 文件动作 | 3 | Planned | `e` 始终进入 Vim-first 编辑流程，`o` 始终进入本机 opener 流程；目录或不支持对象给出明确反馈。 | 未实施；预期证据：本地/远端文件、目录和特殊对象的键位路由测试。 |
| VIM-018 | `/`、`f`、`g/` 搜索键序列 | 4 | Planned | `/` 进入当前目录过滤，`f` 进入递归文件名搜索，`g/` 进入递归内容搜索；组合键超时/取消不触发其他写操作。 | 未实施；预期证据：键序列解析、Esc 取消和模式冲突测试。 |
| VIM-019 | `!` 与 `gs` 命令键序列 | 3 | Planned | `!` 进入一次性命令，`gs` 暂停并进入当前端点 shell；键序列只在 Normal 模式解析。 | 未实施；预期证据：模式/组合键解析及 local/remote 路由测试。 |

## 9. 本地 daemon 与 IPC

| ID | 能力 | Stage | 状态 | 验收标准 | 当前证据 |
|---|---|---:|---|---|---|
| DAEM-001 | 按用户自动启动 daemon | 1 | In Progress | 客户端连接不到 daemon 时可安全拉起一个当前用户实例；并发启动最终只有一个有效监听者。 | Binary auto-start is wired; lock convergence, five reconnects, cancellation and clean shutdown pass locally. Ten-process and Hosted native evidence pending. See [Stage 1 verification](../verification/stage-01.md). |
| DAEM-002 | 权限受限 Unix socket 与路径信任链 | 1 | In Progress | config/state/cache/log/runtime应用根为euid-owned真实`0700`，文件/socket不宽于`0600`；ancestor/ssh用integrity-only，owner roots/files用owner-private。仅受测APFS/ext4/XFS（runtime可含受测tmpfs）明确no-ACL/unsupported可回到DAC；真实ACL查询/解析错误、NFSv4/CIFS/未知FS、不安全path/override均fail closed。runtime唯一宽松DAC回退是root-owned sticky`01777 /tmp`；peer非当前uid不可连接且无TCP。 | Paths, ACL parsers/native no-ACL, preparation, lock, stale socket, mode and peer tests pass on darwin; Linux native and hostile UID evidence pending. See [Stage 1 verification](../verification/stage-01.md). |
| DAEM-003 | TUI 可附着/分离 | 1 | Planned | TUI 退出只关闭客户端连接；重新启动可恢复 panes/workspace 并订阅现有任务。 | 未实施；预期证据：运行中任务期间退出/重连端到端测试。 |
| DAEM-004 | TUI 退出后任务继续 | 2 | Planned | 最后一个 TUI 退出后，非交互任务继续运行；需要交互时进入等待状态。 | 未实施；预期证据：大文件复制中退出 TUI 后完成及 waiting_auth/waiting_conflict 测试。 |
| DAEM-005 | 事件流与重放游标 | 2 | Planned | 客户端按游标接收任务/连接事件；短暂断连后可补齐或用一致快照恢复，不重复执行命令。 | 未实施；预期证据：断连、事件缺口、重连重放与快照回退测试。 |
| DAEM-006 | 单写者持久状态 | 2 | Planned | SQLite仅在本地APFS/ext4/XFS且跨进程WAL/full-durability probe通过。唯一pristine是final+sidecars全不存在；existing先raw identity，无sidecar才immutable，有合法sidecar先probe/recovery再验证whole-main schema/history。final-absent intent bootstrap、受测SQL lexer、original..target durable attempt、sanitized/hold backup、非ready状态显式恢复、retention/space/fullsync/no-replace及最终清attempt→retention→checkpoint→immutable均fail closed；online WAL为64MiB soft/256MiB stop/264MiB absolute。 | 未实施；预期证据：FS/probe、wrong DB零写、bootstrap、sidecar recovery、checksum/lexer/schema、attempt/backup/hold/retention、Darwin顺序、WAL预算/最终验证。 |
| DAEM-007 | daemon 重启恢复 | 2 | Planned | 异常退出后重新启动可恢复未完成任务、缓存租约和连接需求，并先验证外部状态再继续。 | 未实施；预期证据：各任务阶段 kill -9 的恢复矩阵。 |
| DAEM-008 | helper 不作为 daemon 前置 | 1 | Planned | 本地 daemon 在所有远端均未安装 helper 时仍提供完整 Level 0 功能。 | 未实施；预期证据：禁止 helper 的端到端 Explorer/Transfer 基线套件。 |

## 10. 预览

| ID | 能力 | Stage | 状态 | 验收标准 | 当前证据 |
|---|---|---:|---|---|---|
| PREV-001 | 基础文本预览 | 1 | In Progress | 活动栏文件可在底部 Preview 抽屉中查看有界文本；预览不阻塞目录导航。 | 64 KiB RPC/model cap, binary detection, UTF-8 chunking and cancellation pass locally; remote/Hosted evidence pending. See [Stage 1 verification](../verification/stage-01.md). |
| PREV-002 | 代码与 JSON 结构化显示 | 3 | Planned | 常见代码按语法高亮，合法 JSON 可格式化且保留原始查看；解析失败回退为纯文本并说明。 | 未实施；预期证据：语言 fixture、合法/非法/超大 JSON 快照测试。 |
| PREV-003 | 元数据预览 | 3 | Planned | 文件、目录、链接和二进制对象均可显示端点、规范化路径、大小、时间、权限、类型和可用哈希。 | 未实施；预期证据：四类对象的 LocalFS/SFTP 元数据快照。 |
| PREV-004 | 有界读取与继续加载 | 3 | Planned | 初次只读取配置上限；用户可继续加载，取消后停止远端读；内存不随文件总大小线性增长。 | 未实施；预期证据：100 GB 稀疏文件预览内存曲线、继续/取消测试。 |
| PREV-005 | 大文本 head/tail | 3 | Planned | 用户可切换头部、尾部和指定偏移附近内容；远端支持 seek 时不下载前置全部字节。 | 未实施；预期证据：大日志 SFTP seek/read 范围断言及 head/tail 快照。 |
| PREV-006 | 终端图片协议探测 | 3 | Planned | 自动探测 Kitty、iTerm2 和 Sixel 能力，仅使用已确认协议；无能力时不输出破坏终端的控制序列。 | 未实施；预期证据：三类能力与无能力环境的终端输出快照测试。 |
| PREV-007 | 图片降级链 | 3 | Planned | 图片依次使用受支持终端协议、配置的外部预览器、元数据提示；失败原因可见。 | 未实施；预期证据：能力矩阵和损坏图片/缺失预览器降级测试。 |
| PREV-008 | 可配置外部预览器管线 | 3 | Planned | 按 MIME/扩展匹配显式 argv 模板，不经隐式 shell 拼接；远端文件使用缓存租约。 | 未实施；预期证据：匹配优先级、含空格/元字符路径和注入防护测试。 |
| PREV-009 | 二进制安全识别 | 3 | Planned | 检测到二进制或解码错误时不向终端倾倒控制字节，改为十六进制摘要/元数据或外部预览。 | 未实施；预期证据：含 NUL/ANSI 控制序列 fixture 的终端安全测试。 |
| PREV-010 | 预览请求可取消且防串片 | 3 | Planned | 快速移动光标时旧请求被取消或结果按请求 ID 丢弃，不会把旧文件内容显示到新选择上。 | 未实施；预期证据：乱序完成和高频导航并发测试。 |

## 11. 编辑与本机打开

| ID | 能力 | Stage | 状态 | 验收标准 | 当前证据 |
|---|---|---:|---|---|---|
| EDIT-001 | `e` 启动终端编辑器 | 3 | Planned | TUI 安全暂停并以当前文件启动终端编辑器，编辑器退出后恢复终端状态和栏位上下文。 | 未实施；预期证据：fake editor、Vim 和 Neovim 的 suspend/resume PTY 测试。 |
| EDIT-002 | Vim-first 编辑器解析 | 3 | Planned | 顺序为显式`executable+argv[]`、`VISUAL`、`EDITOR`、`nvim`、`vim`、`vi`；env只做无expansion/operator的受限词法切分，PATH仅发现，启动前解析/显示absolute executable并direct exec，文件为独立absolute末参数。 | 未实施；预期证据：优先级、quote/backslash与operator拒绝、PATH→absolute、空环境/无编辑器测试。 |
| EDIT-003 | 远端编辑缓存租约 | 3 | Planned | 打开远端文件前获取内容缓存租约并记录端点、路径和基线版本；编辑期间内容不被 LRU 回收。 | 未实施；预期证据：编辑期间触发缓存压力的租约保护测试。 |
| EDIT-004 | 编辑后本地变更检测 | 3 | Planned | 编辑器退出后按内容或可靠元数据判断是否变化；未变化不创建上传任务。 | 未实施；预期证据：未改、仅 mtime 变化、内容变化三类测试。 |
| EDIT-005 | 上传前远端并发检查 | 3 | Planned | 回传前重新 stat/版本检查远端；基线后发生变化则进入显式冲突，不静默覆盖。 | 未实施；预期证据：编辑期间另一客户端修改/删除/替换文件的集成测试。 |
| EDIT-006 | 编辑冲突处置 | 3 | Planned | 冲突允许查看差异、另存为新路径、跳过或显式覆盖；选择被记录到对应任务。 | 未实施；预期证据：四个分支的 UI 与端到端内容断言。 |
| EDIT-007 | 编辑回传复用任务引擎 | 3 | Planned | 修改后的上传使用 `.part`、验证、原子提交和持久任务状态，不走无恢复的旁路写入。 | 未实施；预期证据：编辑上传中断/重启恢复和目标原子性测试。 |
| EDIT-008 | `o` 使用本机默认应用 | 3 | Planned | macOS固定`/usr/bin/open`、Ubuntu固定`/usr/bin/xdg-open`，或结构化显式opener；direct exec且canonical absolute文件路径为独立arg。远端文件先入租约缓存，下游同UID app是用户/OS边界。 | 未实施；预期证据：两平台absolute argv、空格/前导特殊字节路径、立即返回与远端缓存测试。 |
| EDIT-009 | 外部应用修改检测与回传 | 3 | Planned | opener 返回或监测结束后检测缓存内容变化，变更文件走与编辑器相同的远端版本检查和上传流程。 | 未实施；预期证据：fake GUI opener 修改/不修改/延迟持有文件三类测试。 |
| EDIT-010 | 外部进程终端恢复 | 3 | Planned | 编辑器、opener 或 shell 异常退出后恢复 raw mode、光标、备用屏幕和尺寸处理，不需重启 TUI。 | 未实施；预期证据：正常退出、信号终止和非零退出 PTY 快照测试。 |

## 12. 缓存与离线内容

| ID | 能力 | Stage | 状态 | 验收标准 | 当前证据 |
|---|---|---:|---|---|---|
| CACHE-001 | 默认短期 LRU | 3 | Planned | 内容缓存有全局和工作区配额，超过配额时按 LRU 回收未固定且无租约对象。 | 未实施；预期证据：确定时钟下的配额、淘汰顺序和跨工作区测试。 |
| CACHE-002 | 内容寻址与去重 | 3 | Planned | 相同内容只保存一份 blob，不同 Location 通过元数据引用；引用计数/可达性在崩溃后可重建。 | 未实施；预期证据：跨端点同内容去重、引用删除和索引重建测试。 |
| CACHE-003 | Ephemeral 工作区缓存 | 3 | Planned | 标为 ephemeral 的工作区在安全租约释放后回收其非共享内容；共享 blob 不误删。 | 未实施；预期证据：会话结束、活跃租约和共享引用组合测试。 |
| CACHE-004 | Pinned offline 内容 | 3 | Planned | 用户显式固定的内容在配额策略中受保护，可离线打开；重连后显示新鲜、陈旧或冲突状态。 | 未实施；预期证据：离线重启、远端变化后重连和解除固定测试。 |
| CACHE-005 | 缓存租约 | 3 | Planned | preview/edit/open/上传中的 blob 有可续租的 owner；过期 owner 可回收，活跃 owner 不被清理。 | 未实施；预期证据：并发 owner、daemon kill -9 和租约过期恢复测试。 |
| CACHE-006 | 远端版本与失效规则 | 3 | Planned | 缓存记录可用的 size/mtime/file ID/hash；每次需要一致性的操作按策略重新验证，不能仅因路径相同当作最新。 | 未实施；预期证据：同路径替换、mtime 回拨和 hash 可用/不可用测试。 |
| CACHE-007 | 崩溃后清理 | 3 | Planned | daemon 重启识别孤儿下载、过期租约和未引用 blob；只清理可证明不可达项并保留恢复所需 part。 | 未实施；预期证据：各写入阶段 kill -9 后 fsck/cleanup fixture 测试。 |
| CACHE-008 | 缓存权限与秘密隔离 | 3 | Planned | 缓存目录和文件仅当前用户可读写；缓存索引不包含认证秘密，诊断导出默认不包含文件内容。 | 未实施；预期证据：macOS/Linux mode 测试、秘密扫描和诊断包内容断言。 |
| CACHE-009 | 配额不足可解释 | 3 | Planned | 无法为预览/编辑分配空间时报告所需、可用、被固定/租约占用容量，并提供清理入口。 | 未实施；预期证据：磁盘满与全部对象不可淘汰场景的错误快照。 |

## 13. 命令与 shell

| ID | 能力 | Stage | 状态 | 验收标准 | 当前证据 |
|---|---|---:|---|---|---|
| SHELL-001 | `!` 一次性命令 | 3 | Planned | local用进程cwd+shell`-c`独立arg；remote用fresh exact ssh argv与byte-safe cwd bootstrap，stdout byte0 marker后才经stdin发送≤32KiB单行用户命令。双stream各1MiB持续drain；cancel后remote effect可unknown，结束刷新。 | 未实施；预期证据：argv/CM-GSS冲突、cwd quote/path、marker-before-input/banner/custom shell、bounds/drain/cancel/detach/refresh。 |
| SHELL-002 | `gs` 进入交互 shell | 3 | Planned | local从pane cwd进入shell；remote用fresh`ssh -tt`，home模式无command，current-cwd需probe且失败显式home retry。TUI完整移交/收回TTY pgrp并恢复termios/alternate/cursor/SIGWINCH。 | 未实施；预期证据：home/cwd、exact argv/PTY、正常/非零/信号suspend-resume与后台Job测试。 |
| SHELL-003 | 不内嵌终端模拟器 | 3 | Planned | 命令与 shell 使用当前终端和系统进程；产品不实现嵌入式终端状态机。 | 未实施；预期证据：依赖审查、架构检查及帮助文档快照。 |
| SHELL-004 | 命令执行边界清晰 | 3 | Planned | Provider/file动作永不调用shell；Helper是唯一app-generated restricted string。只有用户显式进入`!`/`gs`才有shell语义，app生成部分仅受测cwd bootstrap，user command不与path拼接。 | 未实施；预期证据：元字符/非UTF8路径、RPC/Provider不可达、bootstrap与user bytes分离审计。 |

## 14. 后台任务模型

| ID | 能力 | Stage | 状态 | 验收标准 | 当前证据 |
|---|---|---:|---|---|---|
| JOB-001 | 持久化 Job | 2 | Planned | 每个写操作在执行前生成稳定 Job ID，并将意图、冻结计划、阶段、进度、决策和检查点事务化写入 SQLite。 | 未实施；预期证据：Job round-trip、事务回滚和 daemon 重启测试。 |
| JOB-002 | 主状态机 | 2 | Planned | 支持 `draft → awaiting_confirmation → queued → running → verifying → completed`，非法跃迁被拒绝。 | 未实施；预期证据：全状态跃迁表驱动测试。 |
| JOB-003 | 等待与终止状态 | 2 | Planned | 支持 `paused`、`waiting_auth`、`waiting_conflict`、`retry_wait`、`failed`、`canceled` 和 `completed_with_source_retained`，每个状态记录原因和允许动作。 | 未实施；预期证据：各状态入口、恢复动作和 UI 快照测试。 |
| JOB-004 | 不可变操作计划 | 2 | Planned | 执行开始后源集合、目标、冲突/完整性策略和初始路由被冻结；变更要求新建或显式修订计划并留痕。 | 未实施；预期证据：运行中修改 workspace/clipboard 不影响 Job 的测试。 |
| JOB-005 | 普通复制立即排队 | 2 | Planned | 无覆盖、删除或直传风险的复制由 `p` 直接进入 queued，不要求冗余确认。 | 未实施；预期证据：安全复制的零确认交互测试。 |
| JOB-006 | 高风险计划确认 | 2 | Planned | 覆盖、递归删除、不可逆删除、跨端点移动和直传在执行危险阶段前展示源/目标/数量/策略/可逆性。 | 未实施；预期证据：五类风险计划快照、取消和确认测试。 |
| JOB-007 | 队列与有界并发 | 2 | Planned | daemon 在配置的全局/端点并发上限内调度，公平推进多个任务，不能无限创建 goroutine、文件句柄或 SSH 连接。 | 未实施；预期证据：多端点并发、公平性、句柄和 goroutine 上限压力测试。 |
| JOB-008 | 暂停、继续与取消 | 2 | Planned | 用户可暂停、继续或取消支持的任务；动作在安全检查点生效并明确当前阶段是否可中断。 | 未实施；预期证据：每个传输阶段的 pause/resume/cancel 状态机测试。 |
| JOB-009 | 任务级速度与带宽策略 | 5 | Planned | 可配置全局或任务级带宽上限；限速改变吞吐但不破坏校验、暂停和公平调度。 | 未实施；预期证据：确定性 token-bucket 单元测试和多任务吞吐基准。 |
| JOB-010 | 任务历史与保留 | 6 | Planned | 成功/失败任务按配置保留摘要和验证证据，清理不删除仍被恢复/审计引用的数据。 | 未实施；预期证据：历史保留、清理边界和数据库迁移测试。 |

## 15. 复制与传输

| ID | 能力 | Stage | 状态 | 验收标准 | 当前证据 |
|---|---|---:|---|---|---|
| XFER-001 | 内部 clipboard 保存 Location 引用 | 2 | Planned | `y`/`d` 保存 Endpoint ID、规范化路径、当时元数据与意图；粘贴时重新验证源，不持有完整内容。 | 未实施；预期证据：源修改/删除/重命名后的计划测试。 |
| XFER-002 | 本地与远端任意方向复制 | 2 | Planned | local→local、local→remote、remote→local、同 remote 和 remote A→remote B 都通过同一 Job 模型完成。 | 未实施；预期证据：五种方向的文件/目录端到端矩阵。 |
| XFER-003 | remote A→B 默认本机中继 | 2 | Planned | 不满足服务端路径时，daemon 使用固定大小缓冲从 A 读向 B 写，默认不把完整文件落本地磁盘。 | 未实施；预期证据：两 sshd 传输、进程 RSS 上限和缓存目录无完整落盘断言。 |
| XFER-004 | 有界内存与背压 | 2 | Planned | 读取速度受目标写入背压约束；缓冲总量与并发配置成比例，不随文件大小增长。 | 未实施；预期证据：慢目标/快源的内存曲线和短写压力测试。 |
| XFER-005 | 目标临时 part | 2 | Planned | 新内容先写目标目录中的唯一 `.part-<job-id>`；未验证完成前最终目标路径不出现新内容。 | 未实施；预期证据：写入任意偏移 kill -9 后目标/part 状态矩阵。 |
| XFER-006 | 验证后原子提交 | 2 | Planned | 完成大小与策略要求的完整性验证后，使用端点支持的安全 rename 提交；无法保证原子时计划明确标示并采用保守流程。 | 未实施；预期证据：支持/不支持原子 rename provider 的提交契约测试。 |
| XFER-007 | 断点与检查点 | 2 | Planned | 可恢复端点记录已确认偏移、源版本和 part 身份；恢复前验证三者仍匹配，不匹配则重启或等待决策。 | 未实施；预期证据：多偏移断线、源变化和 part 被替换测试。 |
| XFER-008 | 完整性策略 | 2 | Planned | 基线至少验证大小和传输边界；有 hash 能力时可选择强哈希；策略和实际验证证据记录到 Job。 | 未实施；预期证据：大小不符、hash 不符、hash 不可用及策略升级测试。 |
| XFER-009 | 目录递归复制 | 2 | Planned | 递归遍历流式产生子任务，保留相对结构；权限错误和部分失败按条目记录，不将整树预载入内存。 | 未实施；预期证据：深树、宽树、权限错误和百万 fixture 的流式测试。 |
| XFER-010 | 目录创建与元数据策略 | 2 | Planned | 目标目录按需创建；权限/时间等元数据只按显式支持与策略保留，不宣称无法保证的所有权语义。 | 未实施；预期证据：LocalFS/SFTP 元数据能力组合测试。 |
| XFER-011 | 短读短写安全处理 | 2 | Planned | read/write 返回部分字节时正确推进或失败，不跳过数据、不把短写当成功。 | 未实施；预期证据：fake provider 全偏移短读/短写属性测试。 |
| XFER-012 | 空间预检与运行期磁盘满 | 2 | Planned | 可获取空间时在计划中预检；运行期 ENOSPC 保留可恢复状态且不提交最终文件。 | 未实施；预期证据：预检不足与中途磁盘满集成测试。 |
| XFER-013 | 单文件与批量进度 | 2 | Planned | Job 展示已处理字节/条目、总量已知性、当前文件和阶段；未知总量不伪造百分比。 | 未实施；预期证据：已知/未知总量、多文件进度聚合测试。 |
| XFER-014 | 路由计划可解释 | 2 | Planned | Planner 输出选择的 local、SFTP relay、same-endpoint 或 direct 路径及能力依据，用户可在执行前查看。 | 未实施；预期证据：能力组合路由表驱动测试和计划快照。 |
| XFER-015 | 失败条目可重试 | 2 | Planned | 批量任务可只重试失败/跳过条目，已验证成功项不会重复覆盖；重试继承或重新确认相关策略。 | 未实施；预期证据：部分失败后的 selective retry 测试。 |
| XFER-016 | 传输结果清单 | 2 | Planned | 任务完成时记录每项源、目标、字节、提交方式、验证级别和异常，不仅提供汇总百分比。 | 未实施；预期证据：混合成功/跳过/失败任务结果快照。 |

## 16. 移动与重命名

| ID | 能力 | Stage | 状态 | 验收标准 | 当前证据 |
|---|---|---:|---|---|---|
| MOVE-001 | 同端点优先原子 rename | 2 | Planned | 同一端点且能力允许时使用 rename；跨文件系统或服务器拒绝时不误报成功，转为明确的 copy-delete 计划需重新评估风险。 | 未实施；预期证据：同目录、跨目录、跨文件系统和 rename 拒绝测试。 |
| MOVE-002 | 跨端点 copy→verify→delete | 2 | Planned | 跨端点移动先完成目标复制、验证和提交，之后才进入源删除阶段。 | 未实施；预期证据：每个阶段断电/断网故障矩阵及操作顺序断言。 |
| MOVE-003 | 验证不足保留源 | 2 | Planned | 无法达到移动要求的验证级别时不删除源，任务以 `completed_with_source_retained` 或等待决策结束。 | 未实施；预期证据：hash 缺失、stat 失败和校验不一致测试。 |
| MOVE-004 | 源变化阻止删除 | 2 | Planned | 复制后源版本发生变化时，源删除前检测并保留源；不删除与已复制版本不同的对象。 | 未实施；预期证据：复制期间/之后修改或替换源的竞态测试。 |
| MOVE-005 | 部分移动结果可解释 | 2 | Planned | 批量移动逐项记录已移动、已复制但保留源、未复制和失败，重试不会重复删除。 | 未实施；预期证据：混合结果与 daemon 重启后重试测试。 |

## 17. 删除与回收

| ID | 能力 | Stage | 状态 | 验收标准 | 当前证据 |
|---|---|---:|---|---|---|
| DEL-001 | `D` 专用删除入口 | 2 | Planned | Normal/Visual 中只有显式大写 `D` 创建删除意图；`d` 始终是 cut。 | 未实施；预期证据：键位解析与 provider 调用计数测试。 |
| DEL-002 | 删除计划确认 | 2 | Planned | 计划列出端点、路径/数量、目录递归、trash 能力和不可逆项；取消不产生写操作。 | 未实施；预期证据：单文件/批量/递归确认快照与取消测试。 |
| DEL-003 | 有能力时优先 trash | 2 | Planned | LocalFS 或 helper 声明可靠 trash 时可移动到回收位置并记录恢复信息；能力不确定时不伪装成可恢复。 | 未实施；预期证据：trash 支持/拒绝/跨卷失败测试。 |
| DEL-004 | 纯 SFTP 不可逆警示 | 2 | Planned | Level 0 SFTP 删除明确显示不可逆，必须完成确认后才执行。 | 未实施；预期证据：无 trash capability 的确认文案和操作门控测试。 |
| DEL-005 | 递归删除有界执行 | 2 | Planned | 递归删除流式遍历并逐项记录；符号链接默认只删链接本身，取消后给出已删/未删清单。 | 未实施；预期证据：深树、链接环、取消和部分权限错误测试。 |
| DEL-006 | 删除审计与后置检查 | 2 | Planned | 每项删除记录执行结果；连接在响应前中断时重新 stat 判断后置状态，不盲目重复。 | 未实施；预期证据：删除响应丢失与重连后状态判定测试。 |

## 18. 冲突与覆盖

| ID | 能力 | Stage | 状态 | 验收标准 | 当前证据 |
|---|---|---:|---|---|---|
| CONF-001 | 目标存在冲突检测 | 2 | Planned | 计划和提交前均检测目标存在/变化；陈旧预检不能授权静默覆盖。 | 未实施；预期证据：计划后目标新建、修改和类型变化竞态测试。 |
| CONF-002 | 明确冲突策略 | 2 | Planned | 支持跳过、覆盖、重命名/另存为和取消；目录合并与文件覆盖分别呈现。 | 未实施；预期证据：每种策略的文件/目录结果断言与 UI 快照。 |
| CONF-003 | Job 级 apply-all | 2 | Planned | “对本任务全部应用”只影响当前 Job 和匹配的冲突类型，不写成无意的全局默认。 | 未实施；预期证据：多个并发 Job 冲突策略隔离测试。 |
| CONF-004 | 无客户端时等待冲突 | 2 | Planned | 后台任务遇到未决冲突进入 `waiting_conflict` 并保留安全状态；附着 TUI 后继续。 | 未实施；预期证据：TUI 分离期间冲突与重新附着测试。 |
| CONF-005 | 覆盖仍使用临时提交 | 2 | Planned | 覆盖策略不直接截断最终目标；新内容验证成功后再以端点安全流程替换。 | 未实施；预期证据：覆盖中断时旧目标或新目标完整性测试。 |
| CONF-006 | 决策留痕 | 2 | Planned | 每个冲突记录检测证据、用户/策略决策、作用范围和最终结果，日志默认不包含内容。 | 未实施；预期证据：Job 冲突记录 schema 与脱敏快照测试。 |

## 19. 故障恢复

| ID | 能力 | Stage | 状态 | 验收标准 | 当前证据 |
|---|---|---:|---|---|---|
| RECV-001 | 网络中断恢复 | 2 | Planned | 读写中断进入可恢复状态，重连后验证源/part/目标再从安全偏移继续或重新开始。 | 未实施；预期证据：不同偏移断网与 server restart 恢复矩阵。 |
| RECV-002 | daemon kill -9 恢复 | 2 | Planned | 在 draft、running、verifying、commit 和 delete-source 阶段 kill -9 后，重启不产生损坏最终文件或重复源删除。 | 未实施；预期证据：逐阶段 crash harness 和数据库/文件系统断言。 |
| RECV-003 | 仅幂等阶段自动重试 | 2 | Planned | read、临时写等可证明幂等阶段可退避重试；rename、提交、删除先检查后置状态。 | 未实施；预期证据：每类操作响应丢失的重试调用次数与结果测试。 |
| RECV-004 | 退避与重试预算 | 2 | Planned | 可重试错误使用有上限的指数退避与抖动；不可重试错误立即失败，用户可手动重试。 | 未实施；预期证据：假时钟下预算、抖动范围和错误分类测试。 |
| RECV-005 | 取消默认保留恢复材料 | 2 | Planned | 取消默认保留匹配的 part 和检查点，并显示占用与清理动作；策略禁止时在取消前说明会清理。 | 未实施；预期证据：默认保留、显式清理和无空间策略测试。 |
| RECV-006 | part 身份校验 | 2 | Planned | 恢复仅使用属于同一 Job、目标和源版本的 part；孤儿或冲突 part 不被误接续。 | 未实施；预期证据：交换/篡改/重命名 part 的拒绝测试。 |
| RECV-007 | 磁盘满、权限与短写保持目标安全 | 2 | Planned | ENOSPC、EACCES 和短写使任务失败/等待，但最终目标不出现未验证的新内容，错误包含失败阶段。 | 未实施；预期证据：fake 与真实临时文件系统故障注入测试。 |
| RECV-008 | 恢复证据可审计 | 2 | Planned | 恢复记录原失败、重连/重试次数、验证判断、恢复偏移和最终提交；用户可在 Jobs/Log 查看摘要。 | 未实施；预期证据：多次断线任务的事件序列快照。 |

## 20. 过滤与搜索

| ID | 能力 | Stage | 状态 | 验收标准 | 当前证据 |
|---|---|---:|---|---|---|
| SRCH-001 | `/` 当前目录即时过滤 | 1 | In Progress | 输入时由本地 daemon 对已加载目录项增量过滤，不产生远端命令；清空过滤恢复原列表与合理光标。 | Incremental loaded/incoming filtering, clear and filter-mode key translation tests pass locally; Hosted PTY evidence pending. See [Stage 1 verification](../verification/stage-01.md). |
| SRCH-002 | `f` 递归文件名搜索 | 4 | Planned | 从活动栏路径流式返回匹配路径、类型和必要元数据；结果可逐条打开或加入选择。 | 未实施；预期证据：LocalFS、Level 0 SFTP、helper 三种后端结果一致性测试。 |
| SRCH-003 | `g/` 递归内容搜索 | 4 | Planned | 从活动栏路径流式返回文件、行号和有界摘要；二进制、编码和超长行有明确处理。 | 未实施；预期证据：文本/二进制/多编码/超长行 fixture 的三后端对照测试。 |
| SRCH-004 | 搜索结果流式展示 | 4 | Planned | 首批结果无需等待全树结束；结果带进行中、完成、partial 或 canceled 状态，总量未知时不伪造百分比。 | 未实施；预期证据：延迟百万 fixture 首结果时间和状态快照。 |
| SRCH-005 | 搜索可取消 | 4 | Planned | 取消停止本地遍历、SFTP 请求或 helper 子进程，并回收资源；已返回结果保留并标为 canceled。 | 未实施；预期证据：三后端取消延迟、进程/句柄泄漏测试。 |
| SRCH-006 | 遍历预算 | 4 | Planned | Level 0 搜索受最大深度、条目、字节、时间和并发预算控制；达到预算返回 partial 和具体限制。 | 未实施；预期证据：逐个预算触发及组合预算表驱动测试。 |
| SRCH-007 | 无 helper 的明确降级 | 4 | Planned | `f` 使用有界 SFTP 遍历；`g/` 仅在用户接受受限慢速模式后运行，UI 明示性能/功能差异。 | 未实施；预期证据：helper 缺失时提示、拒绝与接受分支测试。 |
| SRCH-008 | helper 搜索语义一致 | 4 | Planned | helper 可用时优先安全 `rg` 或内置扫描器，但路径范围、排除、链接与结果格式和基线语义一致。 | 未实施；预期证据：同一 fixture 的 helper/基线 golden results 对照。 |
| SRCH-009 | 链接与权限边界 | 4 | Planned | 默认不跟随目录符号链接；权限错误按路径报告并允许继续，结果标为 partial 而非整体假成功。 | 未实施；预期证据：链接环、不可读目录和跨边界链接测试。 |
| SRCH-010 | 搜索结果回到双栏动作 | 4 | Planned | 从结果可定位所在栏/路径、预览、编辑或加入内部 clipboard；定位失败保留结果并解释原因。 | 未实施；预期证据：结果到 open/preview/select 的交互测试及文件被删除场景。 |

## 21. 可选远端 helper

| ID | 能力 | Stage | 状态 | 验收标准 | 当前证据 |
|---|---|---:|---|---|---|
| HELP-001 | Level 0 永久可用 | 1 | Planned | 未安装、禁用或不兼容 helper 时，标准 SFTP 浏览、传输、中继、恢复和有限搜索仍可用。 | 未实施；预期证据：阻止 helper 执行的完整 Level 0 回归套件。 |
| HELP-002 | 用户明确批准安装 | 4 | Planned | preliminary consent在probe前展示Endpoint/mapping/审计副作用并声明uid/path未知，取消=0 probe；probe/high-water/artifact/ancestor plan后，final consent展示observed numeric uid/target/path/create list/hash/mode/capability，取消=0 app-tree create/content。plan drift须重probe/确认。 | 未实施；预期证据：两阶段快照、两类取消0-hit、drift重确认与环境副作用边界。 |
| HELP-003 | OS/架构/目录与 namespace 预检 | 4 | Planned | 默认 unknown/异构 session mapping 禁用 Helper；验证 root-owned `/usr/bin` utilities 后以 absolute fixed command 获取 non-root uid/physical cwd/uname，严格映射 target。cwd/RealPath 只做 compatibility preflight，不证明 node/mount/object；safe ASCII absolute home component≤255，所有 app dir/final/temp absolute path≤1000，全祖先仅 root/uid owner且不可写/symlink。 | 未实施；预期证据：shared/unknown/双节点异bytes、poisoned PATH、uname/uid0、255/256和1000/1001、metachar home、chroot/SFTP-d、ancestor测试。 |
| HELP-004 | 当前策略验签与安全临时安装 | 4 | Planned | current-policy→prelim consent→probe/target/high-water→artifact hash→plan/final consent后才temp/write；首byte前Chmod+双复核、client回读、standard no-replace。Stage4仅non-release testdata key fixture，production trust拒fixture且dist无fixture assets；production manifests到Stage6 final bytes后才可生成。 | 未实施；预期证据：顺序/撤销/升级、fixture隔离扫描、write边界、EXCL/no-replace/path替换与每次exec重验。 |
| HELP-005 | `0700` 原子内容寻址安装 | 4 | Planned | helper 以非 root uid 安装到 fresh safe canonical absolute home 下由 typed fields 派生且≤1000 bytes的 immutable path；44-byte CSPRNG temp、应用树/派生目录 exact `0700`，standard SFTP no-replace 发布，旧任务不被半升级破坏。 | 未实施；预期证据：safe-home/component/path/temp grammar、entropy、全层权限、并发、中断、多版本与 final 碰撞。 |
| HELP-006 | 受限启动、协议与能力握手 | 4 | Planned | validated absolute ssh fresh argv固定GSS delegation/ControlMaster/Path/Persist off，最后恰一restricted string，仅fixed template+fresh safe home+typed fields；byte0 preface、stderr cap、业务framed stdin。remote same-euid cache/shell/server是边界，不声称无secret。 | 未实施；预期证据：OpenSSH8.9 exact argv/预建master/GSS auth、PATH/profile/banner/chroot/preface/stderr与protocol矩阵。 |
| HELP-007 | 非常驻、无监听、无提权 | 4 | Planned | helper 仅由 SSH stdio 按需启动，不监听网络、不注册服务、不请求提权，退出后不留后台进程。 | 未实施；预期证据：进程/端口/服务扫描和会话结束清理测试。 |
| HELP-008 | 快速遍历与内容搜索 | 4 | Planned | helper 提供有界、可取消的 walk 和 content search 流，遵循根路径、链接和排除策略。 | 未实施；预期证据：百万 fixture 性能/内存/取消测试与基线结果对照。 |
| HELP-009 | 强哈希与磁盘空间 | 4 | Planned | helper 可流式计算强哈希并报告文件系统可用空间；错误/不支持以能力结果返回，不伪造数值。 | 未实施；预期证据：已知哈希 fixture、超大文件内存测试和空间报告对照。 |
| HELP-010 | tail/watch | 4 | Planned | helper 支持有界 tail/watch 事件；断线、文件轮转和事件丢失可见，不承诺无证据的零丢失。 | 未实施；预期证据：append、truncate、rename rotation、断线重连测试。 |
| HELP-011 | 同主机复制增强 | 4 | Planned | 同一远端端点可在能力与策略允许时使用 helper 复制，不经本机传完整内容；结果仍走验证与 Job 状态。 | 未实施；预期证据：同机复制与 SFTP relay 内容/元数据对照及故障测试。 |
| HELP-012 | 崩溃安全降级 | 4 | Planned | helper 非零退出、超时、输出损坏或版本变化只使当前增强动作失败/降级，不使 SFTP 会话不可用。 | 未实施；预期证据：四类 helper 故障后立即继续 SFTP 浏览/复制测试。 |
| HELP-013 | 安全升级与清理 | 6 | Planned | 新版本并行安装和握手成功后切换；只删除无活跃任务引用的旧版本，支持显式卸载且不影响 Level 0。 | 未实施；预期证据：升级回滚、活跃旧任务和卸载后 SFTP 回归测试。 |
| HELP-014 | helper 错误边界 | 4 | Planned | helper stdout 从 byte0 preface 起采用版本化结构协议，禁止 banner resync；probe/formal stderr 并发 drain、65536-byte cap、超限取消且仅保留有界脱敏摘要。用户路径/搜索词/畸形帧只作为协议数据。 | 未实施；预期证据：banner/截断 preface、协议 fuzz、stderr 65536/65537/无限流、恶意帧和 command 捕获。 |

## 22. 服务端快速路径与跨主机直传

| ID | 能力 | Stage | 状态 | 验收标准 | 当前证据 |
|---|---|---:|---|---|---|
| DIRECT-001 | 同端点 rename 快速路径 | 2 | Planned | 移动在同端点且 rename 语义满足时优先原子 rename，无需传输内容。 | 未实施；预期证据：能力路由与零 read/write 调用断言。 |
| DIRECT-002 | SFTP copy-data/服务端复制 | 5 | Planned | 端点可靠声明 copy-data 等服务端复制能力时可选用；完成后仍执行策略要求的验证。 | 未实施；预期证据：扩展支持/谎报/中途失败 provider 测试。 |
| DIRECT-003 | helper 同主机复制路径 | 4 | Planned | 无 SFTP server-side copy 但 helper 可用时，Planner 可选择同机复制并记录命令能力和验证策略。 | 未实施；预期证据：helper fast path 与 relay 等价性测试。 |
| DIRECT-004 | 跨主机直传显式策略 | 5 | Planned | 直传默认受策略门控；用户/工作区可禁用，启用时每个高风险计划显示数据路径和认证前提。 | 未实施；预期证据：默认策略、显式启停和计划确认测试。 |
| DIRECT-005 | 直传完整预检 | 5 | Planned | 同时确认源/目标 helper 能力、源到目标网络可达、目标空间、源主机已有非交互认证和 host key 策略；任一失败不启动直传。 | 未实施；预期证据：逐条件失败和全部通过的预检矩阵。 |
| DIRECT-006 | 不自动转发或复制认证材料 | 5 | Planned | 产品不为直传自动开启 agent forwarding、Kerberos delegation，不复制私钥/票据/认证答案；只使用源主机已经具备的非交互认证。 | 未实施；预期证据：SSH argv/config 审计、远端文件扫描和安全集成测试。 |
| DIRECT-007 | 直传计划冻结 | 5 | Planned | 预检结果、端点、认证前提、临时目标和验证策略在 Job 中冻结；执行前过期条件重新预检，不静默放宽。 | 未实施；预期证据：预检后网络/认证/空间变化测试。 |
| DIRECT-008 | 提交前可降级本机中继 | 5 | Planned | 直传在目标提交前失败且源/目标安全状态可证明时，可记录原因并切换有界本机中继；提交后不盲目改路。 | 未实施；预期证据：各直传阶段故障与允许/禁止 downgrade 矩阵。 |
| DIRECT-009 | 直传完整性等价 | 5 | Planned | 相同完整性策略下，直传与本机中继生成相同目标内容、提交语义和可审计验证记录。 | 未实施；预期证据：随机/稀疏/超大文件的 direct-relay golden comparison。 |
| DIRECT-010 | 降级原因可观察 | 5 | Planned | Jobs/Log 显示未选直传或中途降级的具体条件，不只显示“fallback”。 | 未实施；预期证据：每个预检失败码和运行期降级事件快照。 |

## 23. 规模、性能与资源边界

| ID | 能力 | Stage | 状态 | 验收标准 | 当前证据 |
|---|---|---:|---|---|---|
| SCALE-001 | 50,000 项单目录 | 5 | Planned | 目录可增量载入、虚拟化渲染、过滤和导航，内存/渲染成本不要求一次创建全部 UI 节点。 | 未实施；预期证据：50,000 项 fixture 的首屏、滚动、过滤、RSS 和帧响应基准。 |
| SCALE-002 | 百万文件树 | 5 | Planned | 遍历、复制和搜索以流式队列工作，不将全树路径/元数据同时留在内存；可暂停和取消。 | 未实施；预期证据：1,000,000 文件 fixture 的峰值 RSS、取消延迟和完成记录。 |
| SCALE-003 | 数百 GB 单文件 | 5 | Planned | 传输、hash、预览和恢复按块处理；测试基线含 100 GB 稀疏文件，内存不随文件大小增长。 | 未实施；预期证据：100 GB sparse local/remote/relay/direct 基准与 RSS 曲线。 |
| SCALE-004 | 有界工作队列 | 2 | Planned | 递归生产者受消费者和持久队列背压；队列容量可配置并有安全默认，不因深树耗尽内存。 | 未实施；预期证据：慢目标百万条目压力测试和队列长度上限断言。 |
| SCALE-005 | 文件句柄、goroutine 与进程上限 | 5 | Planned | 并发任务受统一资源预算控制，极端工作负载下打开文件、goroutine 和 SSH/helper 进程保持在配置边界。 | 未实施；预期证据：高并发压力测试的资源采样与泄漏检查。 |
| SCALE-006 | UI 与取消响应 | 5 | Planned | 目录加载、搜索、hash 和传输在后台运行时，导航/切栏/取消不会等待完整 I/O 操作结束。 | 未实施；预期证据：注入慢 I/O 下输入到状态变化延迟基准。 |
| SCALE-007 | 带宽与连接公平 | 5 | Planned | 多任务共享带宽和连接额度时无单任务永久饥饿；高优先级交互读取可获得有界服务机会。 | 未实施；预期证据：混合预览/搜索/传输调度公平性基准。 |
| SCALE-008 | 超深路径与目录树 | 5 | Planned | 遍历不依赖递归调用栈；达到端点路径/深度限制时按具体路径失败并继续可安全部分。 | 未实施；预期证据：超深树、长路径和限制触发测试。 |
| SCALE-009 | 性能回归基线 | 5 | Planned | 50k 目录、百万树、100 GB 稀疏文件和双远端中继建立可重复基准，变更报告趋势而非隐藏退化。 | 未实施；预期证据：固定 fixture/命令、环境元数据和首份基准报告。 |
| SCALE-010 | 低磁盘模式 | 5 | Planned | remote relay 不依赖完整本地落盘；缓存/part 空间不足时限制预取并保留任务安全，不拖垮 daemon。 | 未实施；预期证据：小配额/低磁盘的 relay、preview、edit 和恢复测试。 |
| SCALE-011 | 大结果日志有界 | 5 | Planned | 搜索、任务事件和 stderr 采用分页/轮转/截断策略；摘要保留，单个远端不能无限增长本地日志。 | 未实施；预期证据：超大输出的存储上限、截断标记和 UI 分页测试。 |
| SCALE-012 | 规模能力降级可见 | 5 | Planned | 端点不支持分页/seek/hash 等能力时显示实际路径和限制，不以不可控内存换取表面一致。 | 未实施；预期证据：低能力 provider 的降级标记与资源上限测试。 |

## 24. 安全与隐私

| ID | 能力 | Stage | 状态 | 验收标准 | 当前证据 |
|---|---|---:|---|---|---|
| SEC-001 | 本地 IPC 最小暴露 | 1 | Planned | 只创建当前用户 `0600` Unix socket，不监听 TCP；请求关联到已验证的本地用户上下文。 | 未实施；预期证据：两平台权限/peer 检查和端口扫描。 |
| SEC-002 | 认证材料零持久化 | 1 | Planned | 密码、私钥、agent 数据、Kerberos ticket 和 askpass 回答不进入数据库、配置、缓存、日志、core-friendly buffer 或诊断包。 | 未实施；预期证据：持久层扫描、敏感缓冲生命周期测试与安全审查。 |
| SEC-003 | OpenSSH 信任策略与 SFTP 会话隔离 | 1 | Planned | host key、代理、认证与加密由系统 OpenSSH 决定；默认绝对 `/usr/bin/ssh`，安全 override逐级验证，永不搜索 PATH。精确 argv 只关闭 TTY/escape/转发/额外命令/后台化/tunnel。 | 未实施；预期证据：binary resolver ACL/special-bits/替换、poisoned PATH、完整 argv、冲突 config/traffic 和 host-key 测试。 |
| SEC-004 | 最小权限 helper | 4 | Planned | helper 明确拒绝 remote euid 0，只以非 root 登录 uid 按需运行，文件为 `0700`，无 sudo、setuid、服务注册或网络监听；remote root/admin/ACL/LSM/server 是声明的环境信任边界。 | 未实施；预期证据：uid0 拒绝、远端 DAC/第二 principal 说明、进程、端口和服务扫描。 |
| SEC-005 | helper 供应链与每次执行 freshness | 4 | Planned | helper canonical raw manifest+detached sig 必须持久保存；安装及每次 enable/exec 都按当前客户端重跑 Ed25519、key/artifact revoke、target/compat/floor/high-water 和远端 attrs/hash。版本/协议不匹配、旧签名重放、客户端升级后撤销/floor 提高或 metadata 缺失均拒绝。 | 未实施；预期证据：篡改、错误 key/manifest、单产物撤销、fresh install 重放、upgrade-after-install revoke/floor、metadata 删除、降级和双 key 轮换测试。 |
| SEC-006 | 路径规范化与根边界 | 2 | Planned | 所有操作使用 Endpoint+规范化路径；遍历中的 `..`、绝对跳转和链接不会越过用户确认的根范围。 | 未实施；预期证据：路径穿越、链接逃逸和混合分隔符 fuzz 测试。 |
| SEC-007 | 临时目标与符号链接竞态防护 | 2 | Planned | part 名不可预测且绑定 Job；提交前重新验证目标类型/身份，拒绝把预期文件写到被替换的链接/目录。 | 未实施；预期证据：目标路径在预检后被替换的竞态集成测试。 |
| SEC-008 | 外部进程与remote command边界 | 3 | Planned | 只有ssh使用严格owner/ACL validated-absolute resolver。editor/opener是用户授权同UID边界，但须结构化配置、PATH发现后absolute direct exec、文件独立arg。Helper是唯一app-generated restricted string；显式`!`/`gs`用ADR-0001 fresh argv/marker/Q/TTY契约，Provider/RPC不可隐式触发。 | 未实施；预期证据：ssh resolver、external-app lexer/absolute argv、helper/shell command separation、unsafe path/shell/banner/ForceCommand测试。 |
| SEC-009 | 终端输出消毒 | 1 | Planned | 文件名、错误和远端输出中的 ANSI/OSC 控制序列不能执行任意终端动作，必要字符转义后显示。 | 未实施；预期证据：恶意文件名、stderr 和预览内容的终端快照测试。 |
| SEC-010 | 缓存与状态目录私有 | 3 | Planned | 数据库、缓存、日志、socket 和临时文件使用当前用户私有目录/权限；创建时不受宽松 umask 影响。 | 未实施；预期证据：不同 umask 下 macOS/Linux mode 测试。 |
| SEC-011 | 直传不扩张认证权限 | 5 | Planned | 直传只使用源主机已有非交互凭据，不自动 forwarding/delegation/复制密钥；预检结果可审计。 | 未实施；预期证据：源/目标认证拓扑测试和远端秘密文件扫描。 |
| SEC-012 | 日志与诊断默认脱敏 | 6 | Planned | 用户名/主机/路径按策略可匿名化，环境变量、命令敏感参数、文件内容和认证提示答案不导出。 | 未实施；预期证据：构造秘密输入后的日志/诊断 golden scan。 |
| SEC-013 | 破坏性操作显式授权 | 2 | Planned | 不可逆删除、覆盖、跨端点移动源删除和直传危险阶段不能由默认焦点误触；确认可取消且作用域明确。 | 未实施；预期证据：键盘误触、默认按钮和作用域交互测试。 |
| SEC-014 | 依赖与发布物审查 | 6 | Planned | 发布前执行依赖许可证/漏洞、二进制来源、构建可重复性和 SBOM/校验清单审查，阻断未解释高危问题。 | 未实施；预期证据：安全审查报告、依赖扫描与发布清单。 |

## 25. 可观测性与诊断

| ID | 能力 | Stage | 状态 | 验收标准 | 当前证据 |
|---|---|---:|---|---|---|
| OBS-001 | Preview/Jobs/Log 底部抽屉 | 3 | Planned | 三个页签可独立打开/切换/关闭，保留双栏上下文；异常或等待任务有非侵入提示。 | 未实施；预期证据：布局、窄终端和切换状态 TUI 快照测试。 |
| OBS-002 | 任务阶段与等待原因 | 2 | Planned | Jobs 显示状态、当前阶段、源/目标、进度和 `waiting_auth`/`waiting_conflict`/retry 等原因及允许动作。 | 未实施；预期证据：所有 Job 状态的 UI golden snapshots。 |
| OBS-003 | 速度、吞吐与 ETA 诚实展示 | 2 | Planned | 有可靠总量才显示百分比/ETA；瞬时与平均速度有采样窗口，未知值显示 unknown 而非 0 或 100%。 | 未实施；预期证据：已知/未知/变化总量的进度计算测试。 |
| OBS-004 | 结构化连接与任务事件 | 2 | Planned | 事件至少包含时间、级别、端点、Job、阶段、稳定错误码和脱敏消息，可按 Job/端点过滤。 | 未实施；预期证据：事件 schema contract、过滤和序列快照测试。 |
| OBS-005 | 路由、能力和降级说明 | 4 | Planned | UI 可查看端点 Level、缺失能力、选定路由、预检结果和降级原因。 | 未实施；预期证据：Level 0/1/2 与每类 fallback 的快照测试。 |
| OBS-006 | partial/degraded 状态不伪装成功 | 1 | Planned | 列表、搜索、预览或任务只完成部分时显式标识 partial/degraded，并列出失败范围/限制。 | 未实施；预期证据：四类部分结果的状态传播测试。 |
| OBS-007 | 上下文化错误 | 1 | Planned | 错误包含操作、端点、路径（可脱敏）、阶段、底层原因和下一步；保留原始 cause 供诊断。 | 未实施；预期证据：连接/浏览/传输/搜索错误 golden snapshots。 |
| OBS-008 | 验证与提交证据 | 2 | Planned | 完成任务可查看实际大小/hash 策略、part、提交方式、源删除判定和异常保留源原因。 | 未实施；预期证据：copy/move/direct 完成记录 schema 与 UI 测试。 |
| OBS-009 | 脱敏诊断包 | 6 | Planned | 用户可生成包含版本、平台、配置 schema、能力、近期事件和数据库健康摘要的诊断包；默认排除秘密与文件内容。 | 未实施；预期证据：诊断包 schema、脱敏扫描和损坏数据库场景测试。 |
| OBS-010 | 自检命令 | 6 | Planned | 自检可检查 OpenSSH、运行目录权限、socket、数据库、缓存、helper 兼容和端点基础连通，并逐项给出结果。 | 未实施；预期证据：健康/多故障 fixture 的命令输出快照。 |

## 26. 平台与终端兼容

| ID | 能力 | Stage | 状态 | 验收标准 | 当前证据 |
|---|---|---:|---|---|---|
| PLAT-001 | macOS 支持 | 1 | Planned | 最低 macOS 15，Intel 与 Apple Silicon 均可构建、运行 TUI/daemon、调用系统 OpenSSH、编辑器与 `open`；CI 原生覆盖 `macos-15-intel` 与 `macos-15`。 | 未实施；预期证据：两种架构的原生 CI、PTY 与 opener smoke。 |
| PLAT-002 | Linux 功能基线 | 1 | Planned | 最低 Ubuntu 22.04；Stage 1 在 Ubuntu 22.04/24.04 amd64 原生运行 TUI/daemon、系统 OpenSSH、编辑器与 `xdg-open`，Linux arm64 此时只计构建证据且不得提前声明正式支持；其他 glibc Linux 为 best-effort。 | 未实施；预期证据：Ubuntu 22.04/24.04 amd64 原生 CI、PTY 与 opener smoke；Linux arm64 原生安装/冒烟及正式支持由 PLAT-003 在 Stage 6 前验收。 |
| PLAT-003 | arm64 与 amd64 发布架构 | 6 | Planned | macOS/Linux 的 arm64、amd64 均有可启动发布物，行为契约一致。 | 未实施；预期证据：四目标构建、启动和校验记录。 |
| PLAT-004 | 终端能力自适应 | 3 | Planned | 基础 TUI 只依赖通用终端能力；颜色、真彩、图片、鼠标等增强经探测启用，错误探测可由配置覆盖。 | 未实施；预期证据：TERM/COLORTERM/Kitty/iTerm2/Sixel/基础终端矩阵。 |
| PLAT-005 | 远端 Linux 优先、标准 SFTP 基线 | 1 | Planned | 远端 Linux 是 helper/增强能力目标；任何兼容标准 SFTP 的非 Linux 服务可使用 Level 0，未支持增强明确降级。 | 未实施；预期证据：OpenSSH Linux 与至少一个非 helper SFTP server contract suite。 |
| PLAT-006 | Unicode 与原始文件名 | 1 | Planned | UTF-8 名称正确显示；不可解码字节可无损引用和操作，排序/过滤不改变底层路径字节。 | 未实施；预期证据：CJK、emoji、组合字符和非法 UTF-8 文件名测试。 |
| PLAT-007 | 文件系统语义差异 | 2 | Planned | 大小写、权限、rename 原子性、链接和时间精度按端点能力处理，不把 macOS/本地语义强加给远端。 | 未实施；预期证据：大小写敏感/不敏感和不同时间精度 provider 测试。 |
| PLAT-008 | 终端尺寸与恢复 | 1 | Planned | 小窗口显示明确最小布局，resize 后重排；正常/异常退出恢复备用屏幕、光标和输入模式。 | 未实施；预期证据：多尺寸 PTY、SIGWINCH 和信号退出测试。 |
| PLAT-009 | 系统 OpenSSH 兼容范围 | 6 | Planned | OpenSSH 8.9p1 是最低测试基线而非启动时字符串硬拒绝线；发布文档列出最低验证版本和已测版本，缺少实际必需能力时自检给出明确错误。 | 未实施；预期证据：8.9p1 与当前系统 OpenSSH 的端到端兼容矩阵。 |

## 27. 配置、发布与生命周期

| ID | 能力 | Stage | 状态 | 验收标准 | 当前证据 |
|---|---|---:|---|---|---|
| REL-001 | 完整、可验证配置 | 6 | Planned | 连接引用、缓存、并发、带宽、完整性、预览器、键位与安全策略有版本化配置和生成/校验命令。 | 未实施；预期证据：完整示例、schema test、无效值错误与默认快照。 |
| REL-002 | Shell 补全与 man page | 6 | Planned | 支持的主流 shell 获得命令/参数/工作区补全，man/help 与实际 CLI contract 一致。 | 未实施；预期证据：补全生成测试、man lint 和 CLI help golden snapshot。 |
| REL-003 | SQLite 与配置迁移 | 6 | Planned | final-absent v1外，existing pending batch以original..target全集digest和唯一attempt绑定同一online backup；destination发布前删除captured attempt并设置restore hold，catalog/no-replace/fullsync后才迁移。仅ready可自动推进，其他状态显式resume复用；成功按清attempt→retention→quick/FK/TRUNCATE/immutable收口。保留最近两份，space不足阻断；restore保持hold，禁止raw copy/overwrite/newer schema写入。 | 未实施；预期证据：无/单/多pending、WAL backup sanitize/hold、attempt crash/marker失败/显式resume、catalog retention/space、Darwin hidden-destination pragma、no-replace/fullsync/final ordering。 |
| REL-004 | macOS/Linux 可安装发布物 | 6 | Planned | 首发不可变四 tar+checksums+SBOM+attestation；darwin binary 经 Developer ID/runtime/timestamp/notary Accepted 后才生成离线签署 Helper manifest，最终 tar/Accepted ZIP/manifest hash byte-identical；Linux manifest绑定最终 unsigned binary。应用/服务/Formula ID服从 ADR-0009。 | 未实施；预期证据：pre-sign manifest拒绝、四平台manifest↔tar hash、CDHash/notary/quarantine Gatekeeper、attestation/SBOM、clean install/卸载/Homebrew。 |
| REL-005 | 干净安装与原地升级 | 6 | Planned | 无配置首次运行可进入 host/workspace 选择；旧版升级保留工作区、任务历史和 pinned cache，失败可恢复。 | 未实施；预期证据：clean install 与至少一个前版升级端到端测试。 |
| REL-006 | OpenSSH/Kerberos/helper 兼容矩阵 | 6 | Planned | 发布记录已验证的 macOS/Linux、OpenSSH、SFTP server、MIT Kerberos 和 helper 协议组合及已知限制。 | 未实施；预期证据：版本化兼容矩阵与对应 CI/实验室运行链接。 |
| REL-007 | 安全与故障模型审查 | 6 | Planned | 1.0 发布前逐项审查凭据、host key、路径竞态、删除/覆盖、helper、直传、日志脱敏和恢复边界。 | 未实施；预期证据：威胁模型、审查清单、已关闭发现和接受风险 ADR。 |
| REL-008 | Race、fuzz 与故障注入门禁 | 6 | Planned | 全量 test、Go race、协议/路径 fuzz、fake provider 故障和真实 sshd/Kerberos 集成均为发布门禁。 | 未实施；预期证据：发布候选版本的完整绿色命令和 CI 链接。 |
| REL-009 | 用户与运维文档 | 6 | Planned | 文档覆盖安装、SSH/Kerberos 前提、默认键位、工作区、传输安全、helper、直传、恢复、自检和故障排查。 | 未实施；预期证据：文档链接检查、命令示例 smoke 和新用户演练。 |
| REL-010 | 功能矩阵零无解释缺口 | 6 | Planned | 1.0 发布时本矩阵每项为 Verified，或以关联 ADR 明确 Deferred/Removed；不得遗留 Planned/In Progress。 | 未实施；预期证据：release docs-check 输出和人工签核记录。 |
| REL-011 | 版本与协议兼容策略 | 6 | Planned | CLI、daemon、数据库、IPC 和 helper 版本在诊断中可见；不兼容组合失败清楚，兼容窗口有测试。 | 未实施；预期证据：版本组合矩阵、升级顺序和回滚测试。 |
| REL-012 | 发布后可回滚 | 6 | Planned | 二进制回滚不会静默读取不兼容数据库/helper；需要时以只读诊断模式启动并给出恢复步骤。 | 未实施；预期证据：升级后回滚、数据库过新和 helper 过新场景测试。 |

## 28. 覆盖索引

| 产品领域 | 稳定 ID 前缀 | 主要阶段 |
|---|---|---|
| 基础契约与可续接工程 | `CORE` | 0 |
| 认证与 OpenSSH 信任链 | `AUTH` | 1–2 |
| 连接与端点 | `CONN` | 1、5 |
| 双栏浏览 | `PANE` | 1、5 |
| 工作区与启动 | `WORK` | 1、3、6 |
| Vim 键位与模式 | `VIM` | 1–2、6 |
| 本地 daemon 与 IPC | `DAEM` | 1–2 |
| 预览 | `PREV` | 1、3 |
| 编辑与本机打开 | `EDIT` | 3 |
| 缓存与离线 | `CACHE` | 3 |
| 命令与 shell | `SHELL` | 3 |
| 后台任务 | `JOB` | 2、5–6 |
| 复制与传输 | `XFER` | 2 |
| 移动与重命名 | `MOVE` | 2 |
| 删除与回收 | `DEL` | 2 |
| 冲突与覆盖 | `CONF` | 2 |
| 故障恢复 | `RECV` | 2 |
| 过滤与搜索 | `SRCH` | 1、4 |
| 可选远端 helper | `HELP` | 1、4、6 |
| 服务端快速路径与直传 | `DIRECT` | 2、4–5 |
| 规模、性能和资源边界 | `SCALE` | 2、5 |
| 安全与隐私 | `SEC` | 1–6 |
| 可观测性与诊断 | `OBS` | 1–6 |
| 平台与终端兼容 | `PLAT` | 1–3、6 |
| 配置、发布与生命周期 | `REL` | 6 |

## 29. 阶段完成更新协议

每个 Stage 完成时，维护者必须按以下顺序更新本矩阵：

1. 将该阶段实际实现的行从 `Planned`/`In Progress` 更新为 `Implemented`；
2. 把“未实施；预期证据……”替换为实际测试命令、测试名、平台和结果记录链接；
3. 阶段验证通过后改为 `Verified`；若验收失败，保持原状态并在 Verification 与 `PROJECT_STATE.md` 写明阻塞；
4. 对范围变化新增 ADR，并同步 Vision、Stage Spec 和本矩阵；不覆盖或复用旧 ID；
5. 运行 docs-check，确认 ID 唯一、Stage 在 0–6、状态合法、所有非 Planned 行都有实际证据；
6. 最后更新 `PROJECT_STATE.md` 的当前阶段、最后绿色命令和下一步。

这套更新顺序保证即使原始聊天上下文不可用，也能从仓库文档准确恢复最初产品边界、当前实现状态和下一项可执行工作。
