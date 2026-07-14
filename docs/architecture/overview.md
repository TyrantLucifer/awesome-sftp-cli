# 架构总览

## 1. 文档目的

本文定义项目的稳定架构边界。它描述系统必须保持的行为与安全不变量，不规定每个 Go 包的最终命名。阶段规格可以在不破坏这些边界的前提下细化实现。

项目面向 macOS 与 Linux 终端用户，提供 Vim 优先、双栏、可持久运行的本地与远端文件工作台。任一栏都可以指向本地文件系统或任意 SSH 端点；认证、代理和主机匹配以用户现有的 OpenSSH 配置为准。

详细决策见：

- ADR-0001：使用系统 OpenSSH 作为 SSH 传输与认证入口
- ADR-0002：客户端与持久守护进程分离
- ADR-0003：可选 helper 与能力阶梯
- ADR-0004：崩溃安全的传输语义

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
        +-- SFTP Provider --> ssh alias -s sftp --> 远端 sshd
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

标准远端文件协议通过参数化进程调用启动：

    ssh <host-alias> -s sftp

守护进程把该子进程的标准输入与标准输出交给 Go SFTP 协议层。调用不经过 shell；host alias 必须通过格式校验并禁止以短横线开头，应用附加选项只来自固定 allowlist，因此用户输入不能伪装成 ssh 选项。系统 ssh 负责读取 ssh_config、known_hosts、Kerberos credential cache、ProxyCommand、agent 与硬件密钥配置；应用只处理结构化 SFTP 请求。

### 3.4 可选远端 helper

helper 是按需启动、通过 SSH 标准输入输出通信的版本化程序。它不监听端口、不常驻、不提权，仅使用登录用户权限。标准 SFTP 永远是可回退的基线。

### 3.5 外部应用

Vim 或由 EDITOR 指定的编辑器优先用于编辑；操作系统默认打开器用于用户显式执行的打开动作。外部程序不直接获得守护进程套接字或远端凭据，只接收本地缓存租约指向的文件路径。

## 4. 本机 RPC

### 4.1 传输与权限

- 使用 Unix domain socket；套接字文件权限必须为 0600，父目录必须只对当前用户可写。
- 在平台支持时校验 peer UID；否则依赖安全父目录和套接字权限。
- 协议采用长度分帧 JSON，避免以换行作为消息边界。
- 握手包含协议版本、客户端版本、支持的事件类型与请求能力。主版本不兼容时拒绝连接并给出升级方向；次版本通过能力协商兼容。
- RPC 包含请求/响应和服务端事件。事件携带单调递增序号；重连时客户端先取快照，再从可用序号续接。
- 会改变状态的请求带客户端生成的 request ID。守护进程在有限窗口内去重，防止重连重放重复创建任务。

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

普通、无冲突的 copy 可以直接入队。覆盖、不可逆删除和跨主机直接执行必须展示计划并确认。

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

所有文件系统 provider 实现同一组最小语义：

- 规范化和解析 Location
- 分页列目录与 stat/lstat
- 打开有偏移的读写流
- 创建目录、重命名、删除
- 可取消的流式传输
- 能力探测与健康检查

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

1. y 或 d 产生内部 Clipboard；p 创建 OperationIntent。
2. Planner 重新 stat 来源和目标，探测能力并冻结 Plan。
3. 高风险计划进入 awaiting_confirmation，其余直接 queued。
4. Worker 写 .part 文件并持续保存安全检查点。
5. 写完进入 verifying；满足策略后原子提交目标。
6. copy 完成；move 只有在目标提交且验证满足要求后才删除来源。
7. 来源删除无法确认时结果为 completed_with_source_retained，不把重复文件伪装成失败或完整 move。

跨端点路由优先级为：

1. 同 endpoint 的原子 rename、copy-data 或 helper 快速路径。
2. 跨主机 direct，但仅在网络可达、helper 兼容、目标空间足够、来源主机已有非交互认证且策略允许时。
3. 守护进程有界内存中继，不默认落本地磁盘。

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
- helper 安装需展示计划，上传临时文件，校验 SHA-256，设置 0700，再原子安装到用户目录的版本路径；启动时完成协议与能力握手。
- helper 不监听网络、不常驻、不使用 sudo。
- 不把远端文件名拼入 shell 字符串；SFTP 使用结构化协议，helper 使用长度分帧参数。
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

错误必须区分可重试、需认证、需冲突决策、能力丢失、完整性失败与永久权限错误。客户端 Log 视图展示可读摘要，并允许导出已脱敏的诊断包。

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
