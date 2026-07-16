# 架构总览

## 1. 文档目的

本文定义项目的稳定架构边界。它描述系统必须保持的行为与安全不变量，不规定每个 Go 包的最终命名。阶段规格可以在不破坏这些边界的前提下细化实现。

项目面向 macOS 与 Linux 终端用户，提供 Vim 优先、双栏、可持久运行的本地与远端文件工作台。任一栏都可以指向本地文件系统或任意 SSH 端点；认证、代理和主机匹配以用户现有的 OpenSSH 配置为准。

### 当前实现边界

本文主体描述 1.0 目标架构，不代表所有运行时组件已经存在。Stage 0 foundation、Stage 1 Read-only Explorer，以及 Stage 2 的 SQLite/Job 与 durable transfer implementation 已交付到候选分支：CLI Location/Host picker、secret-free workspace、双栏恢复、packet-bounded SFTP cursor、V1 持久状态、冻结传输计划、part/verify/commit、目录/双远端中继、move/rename/delete 和有界 Jobs 视图均有测试证据。Stage 2 的 final exact-SHA Hosted promotion 仍以 [Stage 2 verification](../verification/stage-02.md) 为准。内容缓存、外部编辑、remote helper、direct transfer 与发行加固仍未交付；生产/发行就绪只由 Stage 6 门禁判定。

### 已冻结兼容与发行基线

| 领域 | 规范值 |
| --- | --- |
| 身份 | 产品 `AMSFTP`，公开命令/包 slug/Homebrew formula `amsftp`，Go module `github.com/TyrantLucifer/awesome-mac-sftp` |
| Go | module 语义 `go 1.25.0`；稳定/生产工具链 1.26.5；oldstable 1.25.12，`GOTOOLCHAIN=local` |
| 1.0 主机平台 | macOS 15+ arm64/amd64；Ubuntu 22.04+ arm64/amd64，Ubuntu 24.04 为当前测试线；其他 Linux best-effort |
| OpenSSH | 8.9p1 是最低集成测试基线，不按版本字符串硬拒绝，系统 OpenSSH 始终是认证/配置事实源 |
| CI | GitHub Actions 固定 action SHA；原生 `ubuntu-22.04`、`ubuntu-24.04`、`macos-15`、`macos-15-intel`；darwin/linux × arm64/amd64 交叉构建不替代原生证据 |
| 发行 | package ID `io.github.tyrantlucifer.amsftp`；不可变 GitHub Release 四归档、`checksums.txt`、`sbom.spdx.json`、attestation/provenance；darwin 两 binary 强制 Developer ID+hardened runtime+timestamp+notarization；Homebrew 为第二渠道 |
| 用户级服务 | launchd `io.github.tyrantlucifer.amsftp.daemon`；systemd `amsftp-daemon.service`，均不是额外产品名 |

这些值由 [ADR-0006](adr/0006-public-identity-toolchain-and-runtime-libraries.md) 与 [ADR-0009](adr/0009-supported-platform-ci-and-packaging-baseline.md) 冻结。Linux arm64 原生安装/冒烟在 Stage 6 正式支持声明前必须完成；Stage 0 交叉构建不能提前证明生产支持。

详细决策见：

- ADR-0001：使用系统 OpenSSH 作为 SSH 传输与认证入口
- ADR-0002：客户端与持久守护进程分离
- ADR-0003：可选 helper 与能力阶梯
- ADR-0004：崩溃安全的传输语义
- [ADR-0005：冻结 Stage 0 基础合约](adr/0005-foundation-contracts.md)
- [ADR-0006：公开身份、工具链与运行时库](adr/0006-public-identity-toolchain-and-runtime-libraries.md)
- [ADR-0007：平台路径、运行时套接字与 IPC](adr/0007-platform-paths-runtime-socket-and-ipc.md)
- [ADR-0008：pure-Go SQLite 与前向迁移](adr/0008-modernc-sqlite-and-forward-migrations.md)
- [ADR-0009：支持平台、CI 与首发包装](adr/0009-supported-platform-ci-and-packaging-baseline.md)
- [ADR-0010：Helper 产物信任与分发](adr/0010-helper-artifact-trust-and-distribution.md)
- [ADR-0011：SFTP 流式目录游标窄 fork](adr/0011-pkg-sftp-streaming-directory-cursor.md)
- [ADR-0012：Stage 2 Job schema、事件序列与保守重启状态机](adr/0012-durable-job-schema-and-restart-state-machine.md)
- [ADR-0013：Stage 2 Provider mutation、提交、move 与 delete 后置条件](adr/0013-stage2-mutation-and-postconditions.md)
- [ADR-0014：Stage 3 cache、Preview 与 edit-session 持久契约](adr/0014-stage3-cache-preview-and-edit-session-contracts.md)
- [ADR-0015：Stage 3 external process、shell 与 TTY 契约](adr/0015-stage3-external-process-shell-and-tty-contracts.md)

## 2. 架构原则

1. **系统 OpenSSH 是认证事实源**：不在应用内重新实现 Kerberos、ProxyCommand、ssh-agent 或主机配置解析。
2. **零远端部署可用**：只有标准 SFTP 时，浏览、基础操作、流式传输、断点续传和本地中继必须可用。
3. **能力驱动而非后端猜测**：计划器只依据已探测的能力选择路径；能力消失时显式降级。
4. **前台可退出，任务不丢失**：客户端负责交互，守护进程拥有会话、任务、缓存和持久状态。
5. **写操作先计划后执行**：来源、目标、冲突策略、路由和安全要求在任务入队前冻结；高风险计划需确认。
6. **有界资源**：目录、搜索、预览和传输均采用分页、流式或有界队列，不以文件或目录树总量决定内存占用。
7. **移动是复制、验证、再删除**：目标未提交或验证不足时绝不删除来源。
8. **安全降级优于隐式提权**：不默认转发 agent、不默认委托 Kerberos、不复制密钥，也不为加速启动常驻远端服务。

## 3. 进程与信任边界

同一个 Go 可执行文件承担不同进程角色，但角色之间保持明确边界：

    TUI / CLI
        |
        | 版本化的本机 RPC + 事件流
        | Unix domain socket，权限 0600
        v
    本机守护进程
        |-- Workspace Service
        |-- Session Manager
        |-- Transfer Engine
        |-- Preview & Cache
        |-- Auth Broker
        |
        +-- LocalFS Provider ------------------> 本地文件系统
        |
        +-- SFTP Provider --> ADR-0001 system-ssh argv --> 远端 sshd
        |
        +-- Helper Provider --> ssh stdio ------> 一次性远端 helper
        |
        +-- External App Adapter --------------> Vim / 默认打开器 / shell

### 3.1 TUI 与 CLI

客户端只拥有以下职责：

- 采集键盘输入，维护纯展示状态并渲染双栏、预览、任务和日志。
- 将用户动作转换为查询或 OperationIntent，不直接改写远端状态。
- 展示计划、确认高风险操作、处理冲突与认证提示。
- 在启动 Vim、默认打开器或交互 shell 时暂停终端界面，并在返回后恢复。
- 订阅任务、会话、搜索和认证事件；重连后从守护进程快照恢复。

客户端退出不得取消后台任务。显式取消必须作为 RPC 命令发送给守护进程。

渲染边界固定使用 `github.com/gdamore/tcell/v3 v3.4.0`；项目自己的纯 Go model/reducer 维护 Vim 模式和两栏状态，tcell 只处理终端事件与可见 cell。目录视图必须窗口化，不能为 5 万条记录构造完整 View。结构化日志统一使用注入的标准库 `log/slog`，不得依赖全局 logger。精确约束见 [ADR-0006](adr/0006-public-identity-toolchain-and-runtime-libraries.md)。

Stage 1 的实际 daemon 为每个已附着 client session 创建 Provider 路由：一个共享只读 LocalFS 基线，加上该 session 明确连接的 SSH Provider。远端切换先创建候选 endpoint、获取完整 capability snapshot，并在目标位置第一批列表成功后原子提交 PaneState；随后才释放旧 SSH Provider/游标。连接 epoch、列表 generation 和预览 generation 分别阻止被取消或旧 daemon/session 的迟到结果覆盖当前状态。client 分离会关闭其远端 Provider；持久 workspace 用 Host alias 和路径在下一次附着时重建会话，而不是持久化活连接或认证材料。

daemon 启动后在平台 LogFile 打开 owner-only JSON 日志。业务 server 显式接收 `*slog.Logger`；持久 handler 在 JSON 编码前把消息替换为固定 token，只保留已注册且验证过的 `component`、`event`、`endpoint_id`、`job_id`、`request_id`、`error_code` 字符串。rolling writer 在互斥写入下限制为 4 MiB 当前文件与三份备份；每次打开/轮转均复用 ADR-0007 私有目录/文件验证。RPC RemoteError 保留 request ID，TUI 可显示不含路径、命令或 raw cause 的稳定诊断摘要。

### 3.2 本机守护进程

守护进程是运行时事实源，负责：

- 建立、复用、失效和重连端点会话。
- 探测 provider 能力并生成不可变执行计划。
- 持久化任务状态、冲突决定、检查点、工作区和缓存元数据。
- 调度有界并发的传输、搜索、预览与校验任务。
- 代理 OpenSSH 的交互认证提示。
- 在客户端断开、网络抖动或自身重启后恢复可恢复任务。

守护进程只以当前 OS 用户身份运行。它不需要 root 权限，不开放 TCP 监听，不接受其他用户通过宽松权限套接字访问。

### 3.3 系统 OpenSSH 子进程

标准远端文件协议通过参数化进程调用启动，精确 argv 为：

```text
<validated-absolute-ssh-path> -T -oEscapeChar=none -oForwardAgent=no -oForwardX11=no -oPermitLocalCommand=no -oClearAllForwardings=yes -oRemoteCommand=none -oStdinNull=no -oForkAfterAuthentication=no -oTunnel=no -oGSSAPIDelegateCredentials=no -s <validated-host-alias> sftp
```

正式平台默认 absolute path 是 `/usr/bin/ssh`，不查找 `PATH`；自定义 binary 只接受 ADR-0001 逐级验证 owner/mode/ACL/no-symlink/special-bits 后的显式 absolute real path，并在每次启动前重验。守护进程把该子进程的标准输入与标准输出交给 Go SFTP 协议层。调用不经过 shell；host alias 必须通过格式校验。固定参数关闭 TTY、escape、agent/X11/端口转发、LocalCommand/RemoteCommand、后台化、tunnel与新GSS delegation；系统ssh仍负责认证/config/proxy/known_hosts且SFTP可复用ControlMaster，既有master先前的delegation/forward属于用户配置边界。Helper与用户授权remote `!`/`gs`则强制GSS delegation off及ControlMaster/Path/Persist off的fresh transport；完整argv、marker/cwd quoting、TTY与取消契约见[ADR-0001](adr/0001-system-openssh-transport.md)。

SFTP 协议 client 保持 `github.com/pkg/sftp` import path，根 module 按 [ADR-0011](adr/0011-pkg-sftp-streaming-directory-cursor.md) pin upstream v1.13.11，并 replace 到精确 immutable cursor fork `github.com/TyrantLucifer/sftp v1.13.12-0.20260715132526-f947b886400b`；它只连接已经由 ADR-0001 精确 system-ssh argv 建立的 stdio，不接管 SSH 握手、凭据或配置解析。

### 3.4 可选远端 helper

helper 是按需启动、通过 SSH 标准输入输出通信的版本化程序。它拒绝 remote euid 0，不监听端口、不常驻、不提权，仅使用非 root 登录用户权限。OpenSSH exec 发送的是远端 shell command string 而非远端 argv；它是唯一app-generated/non-user restricted string，只由ADR-0010 fixed template、fresh strict-safe canonical home与freshly verified typed fields派生，业务路径/模式全走framed stdin。用户明确授权的`!`/`gs`是ADR-0001独立shell surface，不属于Helper或Provider文件操作。标准SFTP永远是基线。

任何 application-managed install-tree bytes 都服从两次确认与固定顺序：当前 policy 的 Ed25519/key/artifact 撤销、签名目标/协议/`min_client`、release floor 与兼容检查后，先展示 preliminary mapping/probe consent；取消时必须产生零次 probe。获准后才做 fresh root-owned utility/observed non-root numeric uid/SFTP-exec namespace probe、目标匹配与 Endpoint `(version, hash)` high-water 检查、本地产物 size/hash 校验，并派生 strict-safe canonical home、1000-byte remote path ceiling 和完整安装祖先计划；再展示 final actual-plan consent，取消时 application-managed install tree 必须保持零 create/content。最终获准后才上传，且由客户端回读复算 hash。probe/hash/path string 只在受信跨 session shared-home mapping 下成立，不证明同 node/mount/object。每次执行还要从 canonical raw manifest/signature 按当前客户端 policy 重验并重新派生 path/command。GitHub attestation 与 TLS 只能补充、不能替代该离线信任根；完整生命周期见 [ADR-0010](adr/0010-helper-artifact-trust-and-distribution.md)。

### 3.5 外部应用

Vim 或由 EDITOR 指定的编辑器优先用于编辑；操作系统默认打开器用于用户显式打开。严格owner/ACL executable resolver只用于OpenSSH。编辑器/自定义opener是同UID用户授权代码执行边界：配置保存`executable+argv[]`，环境值只做无expansion/operator的受限词法切分，PATH只用于发现；启动前解析/显示并重验absolute regular executable，直接exec且canonical absolute文件路径是独立末参数。macOS默认固定`/usr/bin/open`，Ubuntu默认`/usr/bin/xdg-open`；下游GUI/app行为是用户/OS边界。外部程序不获得daemon socket或remote credential，只接收缓存租约文件。

本地`!`通过用户shell的`-c`独立参数执行用户原文并用进程cwd设置Location；本地`gs`从该cwd进入用户shell。remote `!`只在stdout byte0 bootstrap marker后经stdin发送单行用户命令，remote `gs`用fresh `ssh -tt`并明确current-cwd probe或home fallback。输出上限、取消后remote effect unknown以及termios/foreground-pgrp恢复均服从ADR-0001；它们不是可由任意RPC注入的daemon shell API。

## 4. 本机 RPC

### 4.1 传输与权限

- 使用 Unix domain socket；套接字文件权限必须为 0600，父目录必须只对当前用户可写。
- Linux 使用 `SO_PEERCRED`、macOS 使用 `getpeereid` 或等价机制强制校验 peer UID；查询失败时拒绝连接。
- 协议版本为 1.0，采用 4 字节 uint32 大端长度头和 JSON payload；单个 payload 最大 8 MiB，超限帧在分配前拒绝。
- 路径、文件名和符号链接目标在线格式中使用原始字节的 base64；展示文本不作为路径身份回传。
- JSON envelope/control strings 与原始 `Payload` JSON bytes 必须 strict UTF-8；decode/encode 都拒绝非法字节而不允许 U+FFFD 归一。Code/Retry/Effect 只接受冻结枚举，重试延迟不得为负；错误 Location 的 base64 路径仅是原始诊断上下文。
- 握手包含协议版本、客户端版本、支持的事件类型与请求能力。主版本不兼容时拒绝连接并给出升级方向；次版本通过能力协商兼容。
- RPC 包含请求/响应和服务端事件。事件游标由 epoch 字符串和该 epoch 内单调递增的序号组成；重连时只有 epoch 相同才能按序号续接，否则客户端先取快照。
- 会改变状态的请求带客户端生成的 request ID。守护进程在有限窗口内去重，防止重连重放重复创建任务。

配置、状态、缓存、日志与 `control-v1.sock` 的规范位置、权限、短路径回退和陈旧 socket 规则由 [ADR-0007](adr/0007-platform-paths-runtime-socket-and-ipc.md) 定义。业务包不能各自解释 XDG/macOS 目录。

### 4.2 RPC 安全边界

- RPC 不传输或持久化私钥、Kerberos ticket、agent 内容及明文凭据。
- 认证回答只允许发送给当前待处理的单次 challenge；challenge 过期或已消费后必须拒绝重放。
- 日志与错误事件必须在进入持久化前脱敏，不能包含密码、口令、完整环境变量或认证响应。
- 客户端不能通过 RPC 注入任意守护进程 shell 命令。感叹号命令与交互 shell 是明确的本地用户动作，使用当前 pane 的位置上下文启动，并与 provider 文件操作分离。

## 5. 领域模型

### 5.1 Endpoint

Endpoint 表示一个可寻址文件系统：

- 稳定 EndpointID
- kind：local 或 ssh
- 展示名称
- 对于 ssh，保存 OpenSSH host alias，而不是展开后的认证材料
- 最近一次能力快照、连接健康状态与可选策略

同一 host alias 可以在多个工作区复用。能力快照只是缓存，执行高风险操作前必须重新验证关键能力。

### 5.2 Location

Location 由 EndpointID 与 provider 规范化后的绝对路径组成。路径语义由 provider 负责，调用方不能把本地路径规则强加给远端。

所有跨进程消息都传 Location，不传依赖当前工作目录的相对路径。展示层可以显示缩写，但计划和任务保存规范路径。

### 5.3 PaneState

每个 pane 独立保存：

- 当前 Location
- cursor 与滚动锚点
- 排序、过滤和隐藏文件策略
- 连续选择与离散标记
- 导航历史
- 当前目录分页游标与加载代次

加载代次用于丢弃旧目录请求的迟到结果。两个 pane 可以同时指向本地、同一远端或不同远端，且可以随时交换或切换 endpoint。

### 5.4 Workspace

Workspace 是可恢复的用户工作上下文，包含两个 PaneState、布局、缓存策略、端点引用和非敏感偏好。工作区不保存凭据。

启动入口同时支持：

- 直接给出两个位置
- 选择保存的 workspace
- 从 ssh_config 主机列表模糊选择端点

### 5.5 FileRef 与版本指纹

选择和内部剪贴板保存 FileRef，而非文件内容。FileRef 至少包含 Location、条目类型和选择时可获得的版本指纹。版本指纹根据能力由强到弱：

1. 内容哈希或 provider 版本 ID
2. inode/file ID、大小与高精度修改时间
3. 大小与修改时间
4. 只有路径，标记为弱验证

弱指纹不得被宣传为强一致性。计划器根据指纹强度决定冲突提示与验证策略。

### 5.6 Clipboard

Vim 风格操作使用应用内部剪贴板：

- y 保存 copy 语义的 FileRef 集合。
- d 保存 cut 语义的 FileRef 集合，但不立即删除来源。
- p 在当前目标 Location 创建 OperationIntent。
- 计数与点重复作用于已归一化的用户意图，不绕过确认与冲突检查。

实际删除由 D 触发并单独确认。cut 只有在对应 move 任务成功提交后才从来源删除。

### 5.7 OperationIntent、Plan 与 Job

OperationIntent 描述用户想做什么；Plan 描述系统将如何安全完成；Job 是持久执行实例。

Plan 至少冻结：

- 来源 FileRef 集合和目标 Location
- copy、move、delete、rename 或 sync-back 语义
- 路由：同端点快速路径、跨主机直接路径或本机中继
- 冲突、覆盖、符号链接与权限策略
- 临时目标命名和提交策略
- 验证强度与移动后的来源处理
- 所需能力及失效时允许的降级
- 并发、带宽和资源预算

普通、无冲突的 copy 可以直接入队。cut paste、覆盖冲突、不可逆删除和跨主机直接执行必须在各自危险边界展示或等待确认。

Job 保存 Plan、状态、进度、检查点、尝试记录和结果。运行中的任务不依赖某个客户端进程存活。

## 6. Job 状态机

主路径为：

    draft
      -> awaiting_confirmation
      -> queued
      -> running
      -> verifying
      -> completed

可中断状态为：

- paused：用户暂停，保留安全检查点。
- waiting_auth：需要交互认证但当前没有可用客户端，或 challenge 尚未回答。
- waiting_conflict：目标状态与计划不一致，需要用户选择。
- retry_wait：仅在可安全重试的阶段按退避策略等待。

终止结果为：

- completed：目标已按计划提交；move 的来源已安全删除。
- completed_with_source_retained：目标已提交，但来源验证或删除条件不足，来源保留。
- failed：无法继续且没有自动安全降级。
- canceled：用户取消；默认保留可恢复的临时文件和检查点，并在界面中明确展示清理选择。

状态迁移由单一领域组件校验并事务持久化。进程重启后不能凭内存推断状态。对 commit、rename、delete 等非幂等边界，恢复时先检查实际后置条件，再决定继续、完成或进入 waiting_conflict。

## 7. Provider 与能力模型

### 7.1 Provider 合约

所有文件系统 provider 实现同一组只读核心语义：

- 描述 Endpoint，并返回带会话身份的能力快照
- 规范化和解析 Location
- 分页列目录与 stat/lstat
- 打开可取消、有偏移且有界的读取流

写入流、创建目录、重命名和删除不属于核心 Provider，而由可选的 MutableProvider Facet 提供。只读调用方只依赖核心 Provider；计划器需要变更操作时必须同时验证 Facet 和当前能力。

接口返回结构化错误类别，例如 not_found、permission_denied、conflict、auth_required、capability_lost、transport_interrupted 和 integrity_failed。展示文本不能替代类别判断。

### 7.2 Provider 类型

- **LocalFS**：本机文件系统实现。
- **SFTP**：通过系统 OpenSSH stdio 承载标准 SFTP，是所有 SSH 端点的基线。
- **Helper**：通过一次性远端 helper 提供扩展操作；它与 SFTP 共用 endpoint，但有独立版本和健康状态。

### 7.3 能力阶梯

| 等级 | 能力 |
| --- | --- |
| Level 0：标准 SFTP | 浏览、基础文件操作、流式读写、断点续传、本机有界中继、受限递归文件名搜索 |
| Level 1：helper | 快速 walk、内容搜索、强哈希、磁盘空间、tail/watch、同主机复制 |
| Level 2：direct | 在 Level 1 基础上通过网络、空间、认证和策略预检后允许跨主机直接传输 |

能力是逐项声明，不以等级数字替代细粒度检查。helper 版本不兼容、崩溃或能力撤回时，当前操作按冻结计划决定降级到 SFTP、回到本机中继或失败；浏览会话不应因此中断。

每份能力快照由 SessionID、会话内递增的 generation 和 complete 标志共同标识。会话或 generation 变化后旧判断失效；只有 complete 为 true 时，缺少某项能力才表示明确不支持。

## 8. 认证 Broker

### 8.1 原则

系统 ssh 负责认证，应用不自行解析或复制认证材料。守护进程只代理系统 ssh 发出的交互 challenge，使后台任务可以在附着 TUI 中得到回答。

### 8.2 流程

1. Session Manager 启动系统 ssh，并把 SSH_ASKPASS 指向同一可执行文件的受限 askpass 角色。
2. askpass 角色向 Auth Broker 提交一次性 challenge，包括端点、提示类型和父进程关联信息。
3. 若有已授权客户端附着，守护进程发送 auth challenge 事件；TUI 显示来源端点和原始提示类别。
4. 用户回答只绑定该 challenge，消费后立即从内存释放。
5. 若没有客户端，相关 Job 进入 waiting_auth；守护进程不猜测、不循环弹窗，也不把回答写入磁盘。
6. 客户端重连后可继续 challenge；超时 challenge 失效并由 ssh 会话返回认证失败。

Kerberos credential cache、ssh-agent 与硬件密钥仍由系统 ssh 直接访问。应用不自动执行 kinit，也不延长或导出 ticket。

### 8.3 防误导要求

认证界面必须标明 endpoint 和提示来源，区分 host key 确认、密钥口令、密码及其他 challenge。日志只能记录 challenge 类型、时间和结果，不能记录回答。

## 9. 持久化与缓存

### 9.1 SQLite 状态库

守护进程以 SQLite 保存：

- schema 版本和迁移记录
- workspace、endpoint 非敏感配置和 pane 恢复状态
- job、冻结 plan、状态迁移和尝试记录
- 分段检查点、临时目标与验证结果
- 缓存索引、租约、配额和最后访问时间
- RPC 去重窗口和可恢复事件游标

关键状态迁移与对应检查点在同一事务内提交。数据库不保存密码、私钥、Kerberos ticket、agent 数据或认证回答。损坏或迁移失败时必须停止写任务并给出可诊断错误，不能静默新建空库覆盖旧状态。

生产 driver 固定为 `modernc.org/sqlite v1.53.0`（精确 `modernc.org/libc v1.73.4`）。状态库只允许通过 statfs allowlist与跨进程 WAL/locking/full-durability smoke 的本地 APFS/ext4/XFS。唯一 pristine 是 final main 与 exact WAL/SHM/journal 全不存在；任意 existing final先做 raw header identity，无sidecar才 immutable strict validation，有合法 sidecar则仅在 probe通过后 recovery并验证完整 history/whole-main schema。runner使用受测单-statement lexer/allowlist、单 writer、runner-owned `BEGIN IMMEDIATE`和前向迁移；pending batch以durable attempt绑定original..target全集、同一已净化/hold的online backup，仅ready可自动推进，其他状态只可显式恢复。Darwin fullfsync顺序、no-replace bootstrap/backup、两份retention/space gate及4/8/264 MiB statement/reservation/absolute WAL预算均按 ADR-0008 fail closed；禁止裸拷贝 DB/WAL/SHM或静默新建空库。不引入通用 migration framework。详见 [ADR-0008](adr/0008-modernc-sqlite-and-forward-migrations.md)。

### 9.2 内容缓存

默认缓存是有配额的短期 LRU；workspace 可以选择临时缓存或明确 pin 以供离线使用。内容与元数据分离：

- 内容按可验证身份去重。
- 缓存条目记录远端版本指纹和来源 Location。
- 编辑或外部打开期间持有 lease，清理器不得删除。
- 守护进程重启后回收过期 lease，并保留仍被任务引用的内容。
- 缓存文件使用当前用户私有权限，不承诺替代凭据隔离或磁盘加密。

### 9.3 传输临时文件

目标写入同目录的 .part-<job-id> 临时名。完成写入后按计划验证，再在 provider 支持时原子 rename 为最终名。临时文件、检查点与 Job 关联，取消和恢复策略对用户可见。

## 10. 关键数据流

### 10.1 浏览与预览

1. 客户端发送 Location 与加载代次。
2. 守护进程从对应 provider 分页读取并流式发送条目。
3. 客户端只虚拟化渲染可见区域；迟到的旧代次结果被丢弃。
4. 文本、代码、JSON 和元数据预览采用有界读取；大文件支持头部与 tail，不整体载入。
5. 图片优先按检测到的 Kitty、iTerm2 或 Sixel 协议显示；无能力时走可配置外部预览器。

### 10.2 copy 与 move

1. `y` 或 `d` 经 daemon capture RPC 产生带 fingerprint/capability revision 的 Clipboard FileRef；`p` 把当前目标和这些不可变引用变为 OperationIntent。
2. Planner 重新 stat 来源和目标，只接受当前 Provider 明确声明的能力，并冻结 Plan、route、conflict、SHA-256 验证、part 名、queue/page/depth/buffer 预算和 source-delete 条件。
3. daemon 先在一个事务中持久化 Plan、Job、steps 和首事件，再将 queued Job 交给有界 scheduler；cut paste 和不可逆删除的 TUI 确认不能由 count 或点重复绕过。
4. Worker 写同目标目录的唯一 `.part-<job>`，并持续事务化保存 part/source identity、offset、hash state、phase 和 bounded per-item result。
5. 写完进入 verifying；Worker 重读 part 计算 SHA-256，重查 final conflict，以 Provider 的受约束 mutation 提交，并再次验证 final。
6. copy 至此完成；move 随后重新验证冻结 source identity 与 delete capability，执行条件删除并检查 source absence。只有同 Endpoint 明确声明 atomic rename 且后置条件可证明时才跳过 stream worker。
7. 来源变化、能力变化、目录逐项验证不足或删除结果无法证明时，目标保持完成，结果为 `completed_with_source_retained`，Jobs 记录 bounded reason；未知 commit/delete 响应先检查后置条件而不盲目重放。

跨端点路由优先级为：

1. Stage 2 已实现：同 Endpoint 且 frozen capability 明确时的原子 rename。
2. Stage 2 基线：守护进程有界内存中继，不落完整本地文件。
3. Stage 5 才会实现：经完整预检的跨主机 direct 或 helper/server-side copy；当前版本不选择这些路线。

direct 在提交前失败且计划允许降级时可回到本机中继；降级原因必须记录和展示。

### 10.3 编辑与回传

1. 守护进程为 FileRef 创建缓存 lease，下载或复用内容，并保存打开时远端指纹。
2. TUI 暂停，启动 Vim 或 EDITOR；默认应用由用户显式执行打开动作。
3. 外部程序退出后，守护进程比较本地内容、打开时指纹和当前远端指纹。
4. 本地未改动则释放 lease；仅本地改动时请求回传确认。
5. 远端也改动时进入 waiting_conflict，提供 diff、覆盖、另存为或放弃选项，绝不静默覆盖。
6. 回传复用标准 Job 与 .part/验证/提交语义。

### 10.4 搜索

- 当前目录过滤由守护进程对已加载条目执行，使用 / 触发。
- 递归文件搜索 f 优先使用 helper；无 helper 时使用有预算、可取消、流式的 SFTP walk。
- 内容搜索 g/ 优先调用 helper 的安全 rg 或内置扫描器；无 helper 时只提供明确标记为受限且较慢的模式。
- 所有结果携带来源 Location、部分结果标记和搜索代次。取消后不得继续无限产生结果。

## 11. 安全不变量

- 不保存或转发用户认证秘密；不绕过 known_hosts 与系统 ssh 策略。
- direct 不通过默认 agent forwarding、Kerberos delegation 或复制私钥获取权限。
- helper 安装必须先在 current-policy 验签/撤销/兼容/floor 后取得 preliminary mapping/probe consent，取消时零次 probe；fresh probe、目标/high-water、本地产物与只读安装计划检查后再取得 final actual-plan consent，取消时 application-managed install tree 零 create/content。最终获准后才上传并做客户端回读 SHA-256、0700 与 no-replace 内容寻址发布；每次启动前重验 raw metadata/binding/final，并完成 byte0 preface、协议与能力握手。字符串相等不被表述为 node/mount/object binding。Stage 4 只可使用 `testdata` 非发布公钥夹具；生产 helper assets/manifests 要等 Stage 6 最终 Darwin 签名/公证 Accepted 或 Linux 最终 unsigned bytes 后生成。
- helper 不监听网络、不常驻、不使用 sudo。
- SFTP文件操作不把远端文件名/用户路径/搜索词拼入shell。Helper是唯一app-generated restricted string，只含ADR-0010 fixed template+typed fields，业务参数走framed stdin。用户明确授权的`!`/`gs`是ADR-0001独立surface：app只生成受测cwd bootstrap，`!`在byte0 marker后经stdin发送用户原文，不能被Provider/RPC隐式调用。
- overwrite、不可逆 delete、direct 需要可见确认；apply-to-all 只在当前 Job 范围内生效。
- 删除优先使用 provider 的 trash 能力；纯 SFTP 无回收站时必须明确标为不可逆。
- 日志、崩溃报告、数据库和缓存元数据不得泄露凭据或认证回答。

## 12. 可观测性与恢复

每个 Job、endpoint session、helper invocation 和 RPC request 都有相关 ID。日志至少记录：

- 路由选择与降级原因
- 能力探测结果与版本
- 状态迁移、重试类别和后置条件检查
- 传输字节、速率、检查点与验证方式
- 用户可操作的等待原因

错误必须提供稳定代码、重试建议和 effect status，并区分可重试、需认证、需冲突决策、能力丢失、完整性失败与永久权限错误。effect status 为未知的非幂等操作必须先审计后置条件。客户端 Log 视图展示可读摘要，并允许导出已脱敏的诊断包。

守护进程重启时：

1. 加载数据库并验证 schema。
2. 将内存态运行任务转为恢复审计。
3. 检查临时目标、最终目标与来源的实际状态。
4. 仅从已持久化的安全检查点恢复；无法证明的 commit/delete 进入冲突或人工审查状态。
5. 恢复 endpoint 时继续使用系统 OpenSSH，认证不足则进入 waiting_auth。

## 13. 明确不做的事情

首个稳定版本不包含：

- 自研 SSH 或 Kerberos 协议栈
- 常驻远端 daemon 或需要 root 的安装
- 内嵌终端模拟器
- Vim 完整宏系统和命名寄存器
- 以缓存代替版本检查的静默双向同步
- 默认开启 agent forwarding、Kerberos delegation 或密钥复制

这些边界用于保护核心产品的可解释性、安全性和可恢复性；未来改变必须新增 ADR。
