# ADR-0001：使用系统 OpenSSH 作为 SSH 传输与认证入口

- 状态：Accepted
- 日期：2026-07-14
- 影响范围：所有 SSH/SFTP 会话、认证提示、代理链路和远端命令启动

## 背景

目标用户已经在 macOS 或 Linux 上使用终端开发机，并依赖现有的 ssh_config。真实环境可能同时包含 Kerberos/GSSAPI、ProxyCommand 或 ProxyJump、ssh-agent、硬件密钥、known_hosts、Match 规则和企业定制 ssh。

如果应用使用纯 Go SSH 客户端，它就必须重新解释上述配置并补齐 Kerberos、代理和 agent 行为。即使基础密码和密钥能工作，也会产生“终端 ssh 能连，应用不能连”的分裂体验。反过来，如果完全通过交互式 sftp、scp 或 rsync 命令做业务，目录、进度、错误和断点语义又依赖文本输出，难以形成可靠领域模型。

## 决策

SSH 连接与认证使用用户系统中的 OpenSSH 客户端；文件操作使用 Go 的结构化 SFTP 协议层。

每个标准远端会话由守护进程以参数数组启动：

    ssh <host-alias> -s sftp

守护进程将子进程标准输入与标准输出直接接入 SFTP 客户端协议实现。系统 ssh 负责主机解析、认证、代理、known_hosts 与链路建立；应用负责目录、stat、读写、恢复和错误映射。

应用不自行展开 ssh_config，也不复制其中的认证数据。Endpoint 保存的是用户可识别的 host alias。显式产品设置只能作为可审计的参数加入调用，不能静默覆盖或削弱用户的 host-key 与认证策略。

## 运行契约

1. 使用直接进程调用，不经过 shell；host alias 和选项分别作为参数传入。
2. 标准输出只承载 SFTP 子系统协议；诊断读取标准错误并在脱敏后关联到 endpoint session。
3. ssh 退出码、信号、协议 EOF 和 stderr 分类共同映射为结构化 transport 或 auth 错误，业务逻辑不解析面向人的目录文本。
4. 子进程生命周期归 Session Manager 所有。会话无人使用时按策略回收，异常退出时让受影响操作进入可恢复状态。
5. 交互提示通过受限 SSH_ASKPASS 角色交给 Auth Broker；回答只存在于单次 challenge 的内存中。
6. Kerberos ticket、ssh-agent socket、硬件密钥与私钥仍由系统 ssh 直接访问。应用不读取、导出、续期或持久化它们。
7. helper 同样通过系统 ssh 启动，但使用独立的版本化协议；它不能替代标准 SFTP 基线。

## 安全边界

- 不开启应用自带的 agent forwarding 或 Kerberos delegation。用户在 ssh_config 中明确配置的行为属于系统 ssh 策略，应用必须在 direct 计划中如实提示相关安全含义。
- 不拼接 shell 字符串，不把远端文件名作为 shell 参数执行。host alias 经过格式校验且不得以短横线开头，应用生成的 ssh 选项来自固定 allowlist。标准文件操作通过 SFTP 消息完成。
- 不加入跳过 known_hosts、自动接受 host key 或弱化算法的隐藏选项。
- Auth Broker 日志只记录提示类别、endpoint 和结果，不记录回答。
- 诊断包必须清理环境变量、命令参数中的敏感值以及认证提示回答。

## 降级与失败行为

- 系统 ssh 不存在或不可执行：endpoint 标记为不可用，给出发现路径和修复建议；不回退到行为不同的内置 SSH 栈。
- 认证需要交互但没有客户端附着：相关 Job 进入 waiting_auth，后台不重复猜测密码。
- ProxyCommand、Kerberos 或 host-key 校验失败：保留系统 ssh 的可诊断原因，不转换成模糊的“连接失败”。
- SFTP 子系统不可用：标准 SFTP 能力为零；即使 helper 可启动，也不能把该端点伪装成完整基线，除非未来 ADR 明确改变 provider 合约。
- direct 路径预检或连接失败：在目标提交前且冻结 Plan 允许时降级为本机有界中继；否则失败并展示原因。

## 替代方案

### 纯 Go SSH 客户端

优点是单进程、协议控制直接。拒绝原因是需要重复实现 ssh_config、Kerberos/GSSAPI、复杂代理、agent 和硬件密钥兼容，且容易偏离用户已验证的终端连接。

### 编排 sftp、scp 或 rsync 命令

优点是能继承系统 ssh 行为，初期代码量小。拒绝原因是文本输出、区域设置、交互提示、进度和错误难以稳定解析，也无法自然提供统一 provider、任务状态机和精确恢复。

### 仅挂载远端文件系统

优点是让本地应用直接访问路径。拒绝原因是挂载缓存、一致性、断线语义和大目录行为不受本产品控制，也不能覆盖双远端传输、任务队列和能力降级。

## 后果

### 正面

- 与用户已经可用的 ssh 命令保持认证和代理兼容。
- SFTP 文件操作仍是结构化、可测试和可恢复的。
- 产品无需保存 SSH 凭据，也不需要把企业认证逻辑嵌入应用。

### 代价

- 系统 ssh 的版本与发行版差异进入兼容矩阵。
- 后台交互认证必须有 Auth Broker 和 waiting_auth 状态。
- 进程、管道、stderr 和取消传播需要严格测试。
- 某些 SSH 扩展无法由应用直接观察，能力探测必须保守。

## 验证要求

- 真实 OpenSSH sshd 的 SFTP contract 测试。
- ProxyCommand 链路测试。
- MIT Kerberos KDC 与 GSSAPI sshd 的获取、过期和重新认证测试。
- askpass 在有客户端和无客户端时的挑战生命周期测试。
- 子进程异常退出、半关闭、stderr 大量输出和取消时无 goroutine 泄漏的 race 测试。
