# 测试策略

## 1. 目标

测试体系要证明的不只是“能复制文件”，而是以下架构不变量在 macOS、Linux、真实 OpenSSH 和故障条件下持续成立：

1. 系统 OpenSSH 配置、ProxyCommand 与 Kerberos/GSSAPI 可以原样工作。
2. 客户端退出不终止后台任务，守护进程崩溃后可从安全边界恢复。
3. 部分写入不会冒充最终文件，move 不在目标验证前删除来源。
4. helper 缺失、崩溃或版本不兼容时，标准 SFTP 基线仍可用。
5. 目录、搜索、预览和百 GB 传输使用有界队列与流式处理。
6. direct 与本机中继具有相同的提交、冲突和完整性语义。
7. 认证回答、ticket、私钥和 agent 数据不进入数据库、日志或诊断包。

测试分为快速提交门禁、真实集成、故障/恢复、规模/性能和发布兼容五层。能力不能只靠手工演示宣告完成；每项 Feature Matrix 能力都需要链接到自动化或明确的主机 smoke 证据。

## 2. 测试层级

### 2.1 单元测试

单元测试不启动外部服务，覆盖纯领域规则和错误边界：

- Location 规范化、provider 路径语义与 Unicode 文件名。
- Vim action 到 OperationIntent 的映射、计数与点重复，不绕过确认。
- Planner 的路由、能力、冲突和风险确认矩阵。
- Job 状态迁移，非法迁移必须被拒绝。
- copy/move/delete 的验证门槛与 completed_with_source_retained。
- 分段检查点、恢复审计和非幂等后置条件判断。
- 目录分页代次、迟到结果丢弃、搜索预算和取消传播。
- Auth Broker challenge 的创建、一次消费、超时和无客户端行为。
- RPC request ID 去重、事件序号与快照衔接。
- cache LRU、pin、lease、配额与重启后的过期 lease 回收。
- helper capability 交集、主次版本协商和降级选择。
- 错误分类与日志脱敏。
- Vim/two-pane reducer 与 cell layout 使用项目纯 renderer/screen fake 做确定快照；生产 tcell adapter 使用 `tcell/v3/vt.NewMockTerm` 覆盖按键、resize、宽字符、组合字符和两栏焦点，不能引用旧 major 的 `SimulationScreen`。
- `log/slog` handler 使用 `testing/slogtest`、字段 allowlist 与 secret canary；rolling writer 覆盖并发写、精确大小/份数、rename/flush 故障和轮转边界。

时间、随机数、文件系统、命令执行和网络均通过显式依赖注入。测试使用 fake clock 和固定 fixture，不能依赖 wall clock 睡眠或执行顺序。

### 2.2 Provider contract 测试

所有 provider 共享同一套行为 contract，避免 LocalFS、SFTP 和 helper 快速路径产生不同用户语义。

最小 contract 包含：

| 领域 | 必测行为 |
| --- | --- |
| Location | 规范路径、父目录、根目录、Unicode、空格、前导短横线 |
| List | 分页稳定性、取消、迟到页、条目类型、错误分类 |
| Stat | 文件/目录/符号链接、缺失、权限、版本指纹强度 |
| Read | offset、短读、EOF、取消、连接中断 |
| Write | offset、短写、临时文件、flush/close 错误、恢复前缀 |
| Rename | 同目录提交、目标已存在、完成响应丢失后的后置条件 |
| Delete | 文件/目录、非空、权限、trash 能力、不可逆标记 |
| Capability | 逐项声明、运行时撤回、版本不兼容、保守默认 |
| Error | not_found、permission_denied、conflict、auth_required、capability_lost、transport_interrupted、integrity_failed |

contract 依次运行于：

1. LocalFS 临时目录。
2. 可脚本化 fake provider，用于精确注入短读、短写和 stale stat。
3. 真实 OpenSSH sshd 的标准 SFTP 子系统。
4. 已安装兼容 helper 的扩展路径。

若 provider 不支持某项能力，测试应验证它明确返回 unsupported；不得通过 skip 掩盖错误实现。

### 2.3 RPC 与持久化 contract

RPC contract 覆盖：

- 长度分帧、最大消息、截断消息和多消息粘连。
- 握手主版本拒绝、次版本能力交集和未知字段兼容。
- request ID 重放最多生效一次。
- 快照与事件序号之间无丢失窗口。
- 慢客户端背压不会无限增长守护进程内存。
- 非当前用户或错误权限套接字不能连接。
- config/state/cache/log/runtime 应用根和完整 ancestor chain 按 ADR-0007 逐级验证；覆盖 XDG/TMPDIR 相对值、非系统 symlink、other-owner、group/other-write、`0777` non-sticky、祖先替换、Darwin `/var`/`/tmp` 系统别名、标准 deny-delete；`integrity-only` 拒绝 allow-write/delete，`owner-private` 还拒绝 named/inherited allow-read/list/search/execute/read-attr/xattr。Linux 覆盖 access mask、named-user/group effective read/write 与 default ACL。只有受测 APFS/ext4/XFS（runtime可含受测tmpfs）明确返回 no-ACL/unsupported时允许按ADR-0007回到DAC；真实查询/解析错误、NFSv4/CIFS/未知FS、显式override或持久路径不安全均fail closed且不创建隐蔽状态。
- challenge 回答只能消费一次且不能跨 endpoint 使用。

SQLite contract 覆盖：

- 每个 schema 版本向下一版本迁移；ADR-0008 checksum v1 的 97-byte encoding/digest golden、BE 整数/长度/count、原始 statement 顺序与 bytes、UTF-8/NUL/空值/上限/overflow 边界必须逐 byte 固定；历史 version/name/checksum 被改写、缺口或数据库新于二进制时 fail closed。
- state filesystem gate覆盖APFS/ext4/XFS exact type/magic、NFS/SMB/CIFS/FUSE-network/unknown/query failure；identity后临时probe覆盖非`wal`、真实未checkpoint frame、跨进程可见、锁busy/release/shared-memory/fsync/child crash。失败不创建真实/隐藏库、不降级journal。
- 唯一pristine是final main与exact WAL/SHM/journal全不存在；预置zero-byte/valid-empty appid0均拒绝。existing先raw header/page-size/app-id identity；wrong path逐byte/attrs/父目录listing+mtime/ctime不变且无probe。无sidecar才immutable whole-schema/history，有合法sidecar先probe/recovery再strict view，tampered history测试与wrong-app-id零写测试分开。
- final-absent bootstrap覆盖durable singleton intent、rollback-journal temp、exact runner DDL/v1 row/whole-schema、Darwin fullsync、no-replace/dir-sync与逐边界crash；连续crash最多一个generation且不删诱饵。
- SQL lexer/admission覆盖modernc tail multi-statement、quotes/comments/semicolon、transaction/attach/vacuum/pragma/trigger/temp/virtual、main target与temp shadow；只有exact v1 app-id special op。第二connection证明statement不能提前结束runner-owned `BEGIN IMMEDIATE`。
- 每个head比较committed canonical whole-main SchemaContract；额外/缺失/改写object及runner row invariant均拒绝。每次启动/迁移后仍要求`quick_check=ok`、`foreign_key_check`零行及领域断言。
- existing pending batch以original..target全集digest创建唯一attempt/backup；destination sanitize删除captured attempt并设置restore hold。仅ready可自动推进；preparing/running/interrupted/failed及marker写失败均零自动执行，显式resume复用。success按清attempt→retention→quick/FK/TRUNCATE/close/immutable，restore不自动重入。
- backup覆盖WAL未checkpoint数据、hidden destination fixed pragma URI/Commit后读回、Linux/Darwin fullsync/no-replace、deterministic temp crash、catalog/retention=2/deleting恢复、space/disk-full/overflow/损坏/权限/碰撞；active/sole/latest/诱饵不删，禁止raw DB/WAL/SHM copy。
- read transaction 2秒、1000-page autocheckpoint、30秒/批次PASSIVE、idleTRUNCATE、4096 page/8GiB main、64MiB soft/256MiB stop、4MiB statement/8MiB reservation/264MiB absolute WAL边界与多worker safe-chunk必须有界；超线不领取Job/启动外部chunk且不手工截断WAL。
- 迁移失败保留source DB/WAL/同一backup与attempt，进入只读；不自动重建、重试或恢复。
- 状态迁移和检查点事务原子性。
- daemon kill -9 后数据库能打开并进入恢复审计。
- 数据库、WAL、日志和导出中不存在测试凭据标记。

### 2.4 Fuzz 测试

持续 fuzz 的目标包括：

- RPC frame 长度、JSON envelope 和未知字段。
- helper frame、握手与流事件。
- Location、远端路径、Unicode、无效 UTF-8、极长文件名和控制字符。
- FileRef/Plan/Checkpoint 的序列化与向后兼容。
- Job 事件序列，保证任何输入都不能产生非法状态迁移或 panic。
- 目录分页 token 和搜索结果解码。
- 日志脱敏器，保证植入的 secret marker 不出现在输出。

每个已修复 fuzz crash 都保存为小型回归 corpus。Fuzz 不对真实远端执行破坏操作；解析层之后使用 fake provider。

### 2.5 Race 与泄漏测试

Go race 测试至少覆盖：

- 多客户端订阅、断线与重连。
- Session Manager 的复用、失效和并发关闭。
- transfer worker 的 pause/cancel/retry 与进度事件。
- search 取消和迟到结果。
- cache lease 与清理器竞态。
- Auth Broker challenge 的回答、超时和客户端消失。
- tcell `EventQ`、daemon 事件、timer/cancel 并发进入有界应用队列时的背压、关闭和迟到事件。
- daemon shutdown 时 ssh/helper 子进程与管道回收。
- TUI suspend/restore 与正常退出、signal、panic 恢复路径的竞态和幂等清理。

除 go test -race 外，进程集成测试记录 goroutine、文件描述符和子进程基线。重复连接、取消和 helper crash 后，资源必须回到允许的小幅稳定区间，不能随轮次线性增长。

## 3. 真实 SSH 集成环境

### 3.1 拓扑

集成环境至少包含：

- client：运行测试中的守护进程和系统 ssh。
- sshd-a：标准 SFTP、helper 和来源数据。
- sshd-b：标准 SFTP、helper 和目标数据。
- proxy：透明 ProxyCommand 测试跳板，记录连接元数据但不读取凭据。
- MIT Kerberos KDC：独立测试 realm，为 client 与 sshd 提供短期 principal。

Linux CI 使用隔离容器或 VM 构建可重复环境；macOS runner 使用系统 ssh 连接相同协议拓扑或专用临时 VM。端口、host key、known_hosts 和配置都在测试目录生成，不能污染用户的 ~/.ssh。

### 3.2 OpenSSH sshd 场景

- 仅 public-key 的基础 SFTP。
- 仅 GSSAPI 的 Kerberos SFTP。
- 多种认证顺序与明确失败。
- SFTP 子系统缺失或启动后立即退出。
- host key 首次出现、已知 key 和 key 变化。
- 连接复用、服务器重启与 idle 断开。
- 远端路径含空格、引号、前导短横线、换行和非 ASCII 字符。
- 权限拒绝、只读目录、quota 或模拟 disk full。

测试必须逐参数验证实际启动的是 ADR-0001 冻结的 validated absolute executable+精确 argv，而不是依赖交互式 sftp 文本解析或只做包含字符串断言。默认 `/usr/bin/ssh` 与 explicit safe override 都逐级验证 owner/mode/integrity ACL/no-symlink/final special-bits并在每次 exec 前重验；poisoned `PATH` fake ssh 0-hit，relative/dash/symlink/other-writable ancestor/ACL grant/query failure/replacement override 拒绝。host alias 的空值、前导短横线、NUL、ASCII 控制字符和选项注入必须在创建进程前拒绝。

冲突配置 fixture 必须分别设置`RequestTTY force`、`EscapeChar ~`、`SessionType none`、agent/X11/local/remote/dynamic forwarding、LocalCommand/RemoteCommand、StdinNull、ForkAfterAuthentication、Tunnel与`GSSAPIDelegateCredentials yes`。标准SFTP断言固定argv覆盖副作用且GSSAPI-only仍认证；真实traffic含newline/escape bytes。保留能力矩阵证明host-key、IdentityFile、登录agent、ProxyJump/ProxyCommand、GSSAPI、Include/Match与ControlMaster新建/复用生效，并把既有master早先delegation/forward列为用户边界。Helper/remote `!`/`gs`另断言GSS delegation=no与ControlMaster/Path/Persist=no形成fresh transport。`ssh -G`不能替代真实sshd。

### 3.3 ProxyCommand

测试专用 ssh_config 为端点配置 ProxyCommand。代理 shim 只转发字节，并把非敏感 argv 与生命周期写到测试日志。

必测：

- 通过代理成功浏览、传输和恢复。
- 代理在握手前、传输中和 EOF 边界退出。
- stderr 大量输出不会阻塞协议。
- host alias 与 Match 规则由系统 ssh 生效。
- 固定 argv 不关闭 ProxyCommand/ProxyJump；连接所需代理仍可执行，而 LocalCommand、forwarding、RemoteCommand 与 tunnel 被覆盖为安全值。
- 应用日志不泄露代理环境中的 secret marker。

### 3.4 MIT Kerberos

KDC 使用隔离 realm，例如 TEST.INVALID。测试 harness 创建 client principal、host principal、keytab 和独立 credential cache；应用本身不执行 kinit。

场景包括：

1. 有效 ticket、GSSAPI-only sshd：无需应用密码即可建立 SFTP。
2. ticket 缺失：系统 ssh 失败或产生配置允许的 challenge，Job 分类为 auth_required/waiting_auth，而不是无限网络重试。
3. 短期 ticket 过期：已有会话的行为按 ssh 实际结果处理；新会话在外部重新 kinit 后可恢复。
4. 错误 realm 或 service principal：保留可诊断分类。
5. TUI 附着与未附着两种 askpass 路径。
6. 默认不委托 ticket；direct 预检不能为了成功静默开启 delegation。
7. 数据库、日志、环境快照和诊断包中不存在 ticket 内容或测试密码标记。

Linux 的真实 MIT Kerberos 测试是兼容性门禁。macOS 使用系统 ssh 和系统 credential cache 的兼容 smoke 纳入发布矩阵；它不以 mock 替代 Linux 的真实协议测试。

## 4. macOS 与 Linux 外部应用测试

外部程序分两层验证：

### 4.1 可注入命令执行测试

- editor解析覆盖显式结构化配置、VISUAL、EDITOR、nvim/vim/vi；env quotes/backslash可组词，expansion/substitution/glob/operator/control拒绝。PATH只发现，启动记录absolute executable，文件是canonical absolute独立末参数且不经shell。
- macOS精确`/usr/bin/open`、Ubuntu精确`/usr/bin/xdg-open`或结构化显式opener；下游同UID app边界清楚。
- e/o 期间缓存 lease 有效，TUI 暂停并在子进程结束后恢复。
- 编辑器非零退出、本地文件删除和打开器立即返回均有明确处理。
- local `!`/`gs`用进程Dir而非cwd字符串拼接。remote `!`逐项验证ADR-0001 fresh argv、POSIX byte-Q、root-owned printf、byte0 marker前0 command bytes、32KiB command/bootstrap、1MiB stdout/stderr持续drain、cancel/detach effect unknown与刷新；覆盖banner/custom shell/unsafe/nonUTF8 path。
- remote `gs`覆盖home模式无command、current-cwd独立probe/正式失败显式home retry、`-tt`/foreground pgrp、SIGWINCH，以及正常/非零/信号后termios/raw/alternate/cursor恢复；terminal bytes不capture/log。

### 4.2 真实主机 smoke

- macOS 15 的 Apple Silicon 与 Intel runner 使用临时测试应用验证 open 能收到准确文件路径并处理等待语义。
- Ubuntu 22.04/24.04 amd64 runner 使用隔离 XDG 配置和测试 desktop handler 验证 xdg-open 的 MIME 分派；Linux arm64 原生安装/冒烟是公开支持门禁。
- 两个平台实际启动 Vim，脚本化修改缓存文件并退出，随后验证变更检测、远端并发修改冲突与回传 Job。
- 原生终端门禁在 macOS Terminal/iTerm2、Linux 终端、tmux 和 screen 记录 resize、`SIGTSTP`/`SIGCONT`、Vim/shell 返回，以及正常退出、signal、panic 后备用屏幕/光标/输入模式恢复；发布候选复跑并记录到版本验证文档。

无图形会话的 CI 可以跳过真实 GUI smoke，但不能把该 skip 当成发布通过。发布门禁必须有对应平台最近一次成功证据。

## 5. 故障注入矩阵

fake provider、代理 shim、可控 sshd/helper 与进程信号共同注入故障。

| 故障 | 注入点 | 必须证明 |
| --- | --- | --- |
| 网络中断/半关闭 | 来源读、目标写、验证、commit 响应 | 从安全前缀恢复；最终目标不出现部分内容 |
| short read/write | provider stream | 循环正确处理或结构化失败，不跳过字节 |
| disk full/quota | .part 创建、写入、rename | 最终目标不变，状态与清理选择清晰 |
| permission denied | 来源 stat/read、目标 write/delete | 不盲重试，不错误归类为网络故障 |
| daemon kill -9 | running、verifying、commit、delete | 重启先审计后置条件，不重放不明非幂等动作 |
| TUI kill -9 | 传输、搜索、认证/冲突弹窗 | 后台继续或进入等待，不隐式取消 |
| sshd/helper kill -9 | browse、search、transfer | helper 故障不破坏 SFTP；任务按计划降级 |
| stale stat | 计划后来源/目标改变 | waiting_conflict；move 不删变化后的来源 |
| 目标竞态创建 | commit 前 | 不覆盖，除非冻结计划明确允许且已确认 |
| 认证过期 | reconnect/direct preflight | waiting_auth，不复制凭据或开启隐式委托 |
| helper 版本不匹配 | handshake | 禁用扩展并回到 Level 0 |
| 数据库写失败 | 状态迁移/checkpoint | 停止推进不可恢复写操作，保留原状态 |
| 缓存配额耗尽 | preview/edit lease | 不驱逐活跃 lease，给出可操作错误 |

每个故障用例断言领域状态、实际来源/目标、.part、数据库检查点、用户事件和脱敏日志，不能只断言返回了 error。

## 6. 崩溃边界测试

对关键流程使用可重复 failpoint，在每个持久化边界前后终止 daemon：

- 创建 Job 前后。
- 创建 .part 前后。
- 每次检查点事务前后。
- 最后一字节写入前后。
- verifying 结果写库前后。
- 原子 rename 请求发送前、服务端执行后但响应前、响应后。
- move 来源 delete 请求发送前、执行后但响应前、响应后。

重启后的期望由文件系统后置条件决定：

- 目标未提交：恢复或安全失败。
- 目标已提交：不得再次覆盖或重复 rename。
- move 目标已提交但来源仍在：审计并在满足指纹时继续删除，否则 completed_with_source_retained。
- 来源已删除但响应丢失：确认目标正确和来源缺失后完成，不重复失败。

## 7. 规模与性能

规模测试不把大型 fixture 提交到仓库；测试运行时确定性生成，并记录种子、文件系统、OpenSSH 版本、Go 版本和主机资源。

### 7.1 单目录 50,000 条目

Fixture 混合普通文件、目录、符号链接、长名称和 Unicode。

快速结构性门禁使用纯 renderer fake 与 benchmark，证明构造 cell/分配只随可见窗口和有界 overscan 增长，不构造 50,000 个 UI 节点。下面的首屏时延、RSS、连续滚动/过滤与真实 provider 曲线属于更高等级的生产规模门禁；两者不能互相替代。

验收：

- 结果分批到达，TUI 在完整枚举前可导航首批条目。
- provider 到客户端的每一级队列都有固定上限和背压。
- 快速切换目录后，旧加载代次的迟到结果不污染新 pane。
- 过滤、选择、滚动和取消保持响应。
- 参考 CI 上 daemon 相对空闲基线的 RSS 增量不超过 256 MiB。
- loopback 参考环境中首批结果目标为 2 秒内；该时延是回归基线，不作为公网承诺。

### 7.2 百万级目录树

Fixture 至少 1,000,000 个节点，包含宽树、深树、权限边界、循环符号链接和变化中的目录。

验收：

- f 与 g/ 结果流式产生，不等遍历完成才显示。
- 遍历 frontier、结果缓冲和 worker 数均受配置上限约束。
- 取消后 2 秒内停止产生新结果并回收 helper/worker。
- helper crash 标记 partial_results；SFTP 浏览仍正常。
- 相对空闲基线 RSS 增量不超过 512 MiB。
- 不跟随计划未允许的符号链接，不因循环无限遍历。

### 7.3 100GB sparse 文件

在支持 sparse file 的测试文件系统生成逻辑大小 100GB、少量实际数据区段的文件。夜间或发布任务执行真实跨 LocalFS/SFTP 读写；快速 CI 使用相同状态机的虚拟大流覆盖偏移边界。

Canonical scale gate 按 [ADR-0017](../architecture/adr/0017-stage5-unified-routing-direct-transfer-and-resource-budgets.md) 分解而不伪造物理吞吐证据：`TestWorkerHundredGiBSparseFileUsesSizeIndependentBoundedCheckpoint` 与 synthetic 对照证明真实 100 GiB metadata、64-bit offset、固定 buffer/checkpoint、cancel/part/final 边界；`TestWorkerHundredGiBContractLifecycleUsesSameCheckpointHashAndRateStateMachine` 在同一生产 Worker/Journal/Scheduler 上以多 quantum sparse-shaped 内容证明 pause、持久 checksum、重启实例续传、限速、完整 SHA-256 和 commit。公开发布门禁仍必须把这些维度组合成一次完整物理 100 GiB LocalFS/SFTP 长跑并记录环境、用时和峰值资源。

验收：

- 偏移和进度使用 64 位，不溢出。
- 单任务相对空闲基线 RSS 增量不超过 256 MiB。
- 双远端本机中继默认不产生完整本地落盘副本。
- 网络中断后从已验证前缀恢复；修改来源后拒绝错误续传。
- cancel/pause 不等待剩余数据完成。
- 完成后按 Plan 验证；move 在验证前来源始终存在。
- direct 与 relay 的最终内容、元数据策略、冲突结果和 Job 终态一致。

具体吞吐受主机和网络影响，不设置跨环境绝对 SLA。每个稳定参考环境保存基线，合并门禁阻止无法解释的显著回退；内存上限和正确性门禁是硬要求。

## 8. 安全测试

- 用唯一 secret marker 注入密码、askpass 回答、Kerberos cache 和代理环境，扫描数据库、WAL、日志、诊断包与错误事件。
- 验证 config/state/cache/log/runtime 根、socket 和完整 ancestor chain 的 owner/mode/type/no-symlink 规则；拒绝其他 UID、第三方可写/可 rename 祖先和不安全显式 override，仅 runtime 系统首选路径允许回退到经验证的 root-owned sticky `01777 /tmp`。
- 文件名、host alias、搜索表达式和 EDITOR 参数的 shell 注入回归。
- known_hosts 变化不能被自动接受。
- helper安装/执行覆盖canonical manifest/Ed25519/revoke/compat/floor/high-water。preliminary consent取消=0 probe且不伪称actual uid/path；final consent取消=0 app-tree create/content，manifest/probe/attrs drift强制重probe/确认。Stage4 testdata fixture只能test constructor注入，production verifier拒绝fixture key；installable binary/curated dist/production helper assets扫描无fixture材料（repository自动source archive除外）。覆盖unknown/shared/双节点mapping、fresh ssh GSS delegation+CM/Path/Persist off、root-owned utilities/PATH 0-hit、non-root uid/cwd/uname/RealPath compatibility、safe-home/path/temp/ancestor、128MiB/expected+1/O_EXCL/首write前Chmod0600、client回读、standard no-replace。shell-c/唯一command/banner/chroot/byte0/stderr边界均Level0；只断言final consent前install-tree零mutation，不外推remote same-euid/root/server无credential或隔离。
- direct 默认不出现 agent forwarding、ticket delegation 或私钥复制。
- 恶意 RPC frame、超大长度、事件洪泛和慢客户端背压。
- 缓存与 .part 文件只对当前用户可访问，诊断导出明确脱敏。

## 9. 测试门禁与频率

macOS release gate 对arm64/amd64分别验证Developer ID Application identity、hardened runtime、trusted timestamp、strict verify、notary`Accepted`、submitted ZIP与final tar内binary byte identity，并在干净macOS15对quarantine下载执行codesign/Gatekeeper/version smoke。PR/fork拿不到signing/notary secrets；失败不得降级unsigned production asset。

Helper release producer必须证明顺序与bytes绑定：Darwin dev/pre-sign或尚未`Accepted`的bytes请求production manifest一律拒绝；只有sign+timestamp+strict verify+notary Accepted并冻结ZIP内bytes后，才生成并offline签manifest。Linux也只绑定冻结的final unsigned bytes。darwin/linux×arm64/amd64四个平台逐一从最终tar解包binary，比较manifest size/SHA-256，并证明Darwin Accepted ZIP、manifest、tar三者同bytes；`checksums.txt`、SBOM与attestation都引用同一四个immutable archives/digests。任一producer order、平台manifest、tar/checksum/SBOM/attestation交叉引用不一致阻止发布。

| 门禁 | 内容 | 频率 |
| --- | --- | --- |
| Fast | unit、fake provider contract、RPC/DB contract、固定 fuzz corpus | 每次变更 |
| Race | 全包 race、并发集成、资源泄漏轮次 | 每次合并或每日 |
| SSH Integration | 真实 sshd、ProxyCommand、双远端、helper | 每次合并 |
| Kerberos | MIT KDC、GSSAPI-only、ticket 过期/恢复 | 每次合并；环境故障需明确标红 |
| Fault | failpoint、网络、disk full、kill -9、stale stat | 每日及发布 |
| Scale | 50k、百万树、100GB sparse | 夜间及发布 |
| Platform | macOS/Linux 构建、系统 ssh、Vim/opener/shell smoke | 每次发布，核心子集每次合并 |
| Release provenance | pre-sign manifest拒绝、Accepted后manifest、四平台manifest↔final tar/checksum/SBOM/attestation | 每次发布候选 |

测试环境不满足时必须记录为 skipped-with-reason，并在 Feature Matrix 中保持未验证状态。安全、Kerberos、move 崩溃边界和发布双平台 smoke 不允许长期以 skip 视为通过。

## 10. 规范化命令

仓库必须提供稳定入口，使本地、CI 和续接会话使用同一命令。底层最小集合为：

    go test ./...
    go test -race ./...
    go test -tags=integration ./...
    go test -tags=kerberos ./...
    go test -tags=fault ./...
    go test -tags=scale ./...

Fuzz 目标使用 Go 原生 fuzz 命令并在 CI 运行固定时长，在发布前运行延长任务。若项目后续增加 Makefile 或任务脚本，它只能封装这些可独立执行的入口，不能把关键环境条件隐藏在个人机器配置中。

## 11. 证据与完成定义

每次需要形成发布或兼容性证据的验收记录：

- commit 或工作树标识
- OS、架构、Go、OpenSSH、SQLite 与 helper 版本
- 执行命令和退出状态
- 集成拓扑及启用能力
- 失败/skip 的原始原因
- fault/scale 的资源与时延摘要
- 对应 Feature Matrix ID

一项能力只有在实现、自动化或规定 smoke、Feature Matrix 与用户契约同时一致时才标记为已验证。测试失败不能通过降低验证强度、删除断言或把必需场景改为永久 skip 来解决。
