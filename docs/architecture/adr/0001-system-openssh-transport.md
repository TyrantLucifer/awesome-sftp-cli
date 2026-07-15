# ADR-0001：使用系统 OpenSSH 作为 SSH 传输与认证入口

- 状态：Accepted
- 日期：2026-07-14
- 最后同步：2026-07-15
- 影响范围：所有 SSH/SFTP 会话、认证提示、代理链路和远端命令启动

## 背景

目标用户已经在 macOS 或 Linux 上使用终端开发机，并依赖现有的 ssh_config。真实环境可能同时包含 Kerberos/GSSAPI、ProxyCommand 或 ProxyJump、ssh-agent、硬件密钥、known_hosts、Match 规则和企业定制 ssh。

如果应用使用纯 Go SSH 客户端，它就必须重新解释上述配置并补齐 Kerberos、代理和 agent 行为。即使基础密码和密钥能工作，也会产生“终端 ssh 能连，应用不能连”的分裂体验。反过来，如果完全通过交互式 sftp、scp 或 rsync 命令做业务，目录、进度、错误和断点语义又依赖文本输出，难以形成可靠领域模型。

## 决策

SSH 连接与认证使用用户系统中的 OpenSSH 客户端；文件操作使用 Go 的结构化 SFTP 协议层。

每个标准远端会话由守护进程以以下**精确参数数组**启动；参数次序、单参数 `-oKey=value` 形式和固定值都是契约的一部分：

```text
[
  "<validated-absolute-ssh-path>",
  "-T",
  "-oEscapeChar=none",
  "-oForwardAgent=no",
  "-oForwardX11=no",
  "-oPermitLocalCommand=no",
  "-oClearAllForwardings=yes",
  "-oRemoteCommand=none",
  "-oStdinNull=no",
  "-oForkAfterAuthentication=no",
  "-oTunnel=no",
  "-oGSSAPIDelegateCredentials=no",
  "-s",
  "<validated-host-alias>",
  "sftp"
]
```

正式支持平台的默认值固定为 `/usr/bin/ssh`，不会通过 `PATH` 查找。企业环境需要自定义 OpenSSH 时只能显式配置 absolute path；默认值与 override 在每次启动前都从 filesystem root 逐级 `lstat`，要求所有祖先是真实目录、owner 为 root 或当前 euid、group/other 不可写且无 symlink，final 是 owner 为 root 或当前 euid、group/other 不可写、当前进程可执行、无 setuid/setgid/sticky special bits 的真实 regular file。整条链还必须复用 ADR-0007 的 `integrity-only` ACL/alternate-permission validator，查询/解析失败 fail closed。override 中的相对路径、空值、前导短横线、NUL/控制字符、任意 symlink component、可写/异主祖先、扩写 ACL 或启动前替换一律拒绝；Homebrew 等 symlink 入口必须先解析为完整真实路径再显式配置。root 与当前 euid 对已验证 binary/path 的主动替换仍是本机信任边界。

这些固定参数只关闭会向本机会话增加命令执行、转发、TTY、交互式 escape 处理、后台化或非 SFTP 数据通道的行为，并保证 stdin 可承载 SFTP。系统 ssh 仍负责 host key、用户/端口、GSSAPI/Kerberos认证、登录所用 ssh-agent、IdentityFile、ProxyJump/ProxyCommand、Match/Include 和 ControlMaster。`ForwardAgent=no`不妨碍ssh用本机agent认证；`GSSAPIDelegateCredentials=no`保留GSSAPI认证但不为新transport请求ticket委派。标准SFTP为兼容用户配置仍可复用既有ControlMaster；若该master早已按用户配置建立forward或委派credential，其既有transport状态属于明确的用户本机配置/remote same-euid环境信任边界，应用不能声称撤销。

守护进程将子进程标准输入与标准输出直接接入 SFTP 客户端协议实现。系统 ssh 负责主机解析、认证、代理、known_hosts 与链路建立；应用负责目录、stat、读写、恢复和错误映射。

应用不自行展开 ssh_config，也不复制其中的认证数据。Endpoint 保存的是用户可识别的 host alias。除上述固定的无副作用 SFTP 会话参数外，显式产品设置只能作为可审计的参数加入调用，不能静默覆盖或削弱用户的 host-key 与认证策略。用户的 ssh_config 是受信任的本机配置；为连接所需的 ProxyCommand 或 Match exec 仍可能按 OpenSSH 语义在本机执行，不属于本契约试图屏蔽的会话副作用。

## 运行契约

1. 使用直接进程调用，不经过 shell；经过验证的 absolute ssh path、固定参数和经过验证的 host alias 分别作为参数传入。host alias 不得为空、不得以 `-` 开头、不得包含 NUL 或 ASCII 控制字符。
2. 标准输出只承载 SFTP 子系统协议；诊断读取标准错误并在脱敏后关联到 endpoint session。
3. ssh 退出码、信号、协议 EOF 和 stderr 分类共同映射为结构化 transport 或 auth 错误，业务逻辑不解析面向人的目录文本。
4. 子进程生命周期归 Session Manager 所有。会话无人使用时按策略回收，异常退出时让受影响操作进入可恢复状态。
5. 交互提示通过受限 SSH_ASKPASS 角色交给 Auth Broker；回答只存在于单次 challenge 的内存中。
6. Kerberos ticket、ssh-agent socket、硬件密钥与私钥仍由系统 ssh 直接访问。应用不读取、导出、续期或持久化它们。
7. helper 同样通过系统 ssh 启动，但 OpenSSH exec channel 发送的是一个通常由远端登录 shell `-c` 解释的 command string，不是远端 argv。它只能使用 ADR-0010 的受限固定 command grammar 与独立版本化 stdin 协议；它不能替代标准 SFTP 基线。

### 用户明确授权的 `!` / `gs` shell surface

Helper 是唯一**应用生成、非用户命令**的 restricted remote command string；Stage 3 的 `!`/`gs` 是用户明确进入、UI先展示Endpoint/cwd的独立shell surface，不属于文件操作或Helper例外。它们仍复用本ADR的validated absolute ssh resolver与host validation，但为避免复用一个可能已委派credential/建立forward的旧master，固定使用fresh transport：`GSSAPIDelegateCredentials=no`、`ControlMaster=no`、`ControlPath=none`、`ControlPersist=no`必须同时存在。它们不会禁用GSSAPI认证；remote same-euid原本可访问的credential/cache、登录shell/startup、sshrc、root/admin与server仍是环境信任边界。

远端一次性`!`的本地argv固定为：

```text
[
  "<validated-absolute-ssh-path>",
  "-T",
  "-oEscapeChar=none",
  "-oForwardAgent=no",
  "-oForwardX11=no",
  "-oPermitLocalCommand=no",
  "-oClearAllForwardings=yes",
  "-oRemoteCommand=none",
  "-oStdinNull=no",
  "-oForkAfterAuthentication=no",
  "-oTunnel=no",
  "-oGSSAPIDelegateCredentials=no",
  "-oControlMaster=no",
  "-oControlPath=none",
  "-oControlPersist=no",
  "-oSessionType=default",
  "<validated-host-alias>",
  "<one-fixed-bootstrap-command>"
]
```

最后恰好一个command argument按byte确定生成，语义固定为：

```text
case ${SHELL-} in /*) cd <Q(canonical-absolute-cwd)> && /usr/bin/printf '%s\n' amsftp-shell-wire-v1 && IFS= read -r AMSFTP_COMMAND && exec "$SHELL" -c "$AMSFTP_COMMAND";; *) exit 126;; esac
```

Stage 3在连接前只读验证远端`/usr`、`/usr/bin`与`/usr/bin/printf`为root-owned真实、group/other不可写且printf可执行；`Q`是受测的POSIX byte single-quote encoder：整体单引号包裹，每个单引号byte替换为`'\''`，不做locale、变量、反斜杠或命令展开。cwd必须是Provider返回、重新canonicalize且byte-round-trip一致的absolute path，无NUL；raw cwd与最终bootstrap各≤32768 bytes，无法无损表示或custom/non-POSIX login shell不支持该bootstrap时明确不支持remote-cwd command，不能猜测目录。stdout必须从byte 0开始恰好读到`amsftp-shell-wire-v1\n`才发送命令；banner、cd/utility/shell失败、marker前EOF/超限均保证发送0个用户命令bytes。命令是用户在确认UI输入的UTF-8、NUL/CR/LF-free单行，≤32768 bytes；marker后通过stdin发送一行并关闭stdin，原文是唯一用户shell文本，不拼入bootstrap。v1 remote `!`不提供交互stdin。

远端`gs`使用同一fresh/禁转发前缀，但把`-T`精确替换为`-tt`。默认home模式在`<validated-host-alias>`后**没有**remote command argument，直接进入登录shell；current-cwd模式只有在独立non-PTY capability probe已验证上述POSIX/Q/cd路径后，才允许最后恰一条`case ${SHELL-} in /*) cd <Q(cwd)> && exec "$SHELL" -l;; *) exit 126;; esac`。probe与正式session不是对象绑定；正式启动失败必须先恢复本地终端，再明确提供“从remote home重试”，不能谎称位于原cwd或静默fallback。

`!`的stdout/stderr分别以1 MiB ring并发drain；超限继续drain/discard、防止pipe阻塞，并显示truncated与discarded byte count，raw输出默认不持久化。取消/超时关闭pipes并终止本地ssh process group；远端用户命令可能自行detach，结果必须标为remote effect unknown并刷新目录，不能声称远端已终止。`gs`不capture/log terminal bytes：TUI保存termios，退出alternate/raw并把TTY/foreground process group交给ssh；正常返回或信号后重新取得TTY，恢复termios/alternate screen/cursor，重放SIGWINCH并全量refresh。上述边界不构成嵌入式terminal emulator。

## 安全边界

- 标准 SFTP 会话强制关闭 agent/X11/本地/远端/动态 forwarding、tun/tap tunnel、LocalCommand、RemoteCommand、TTY、后台化与新GSS credential delegation；不影响系统ssh用agent/Kerberos认证。若复用用户既有ControlMaster，其早先transport状态是显式信任边界。
- 标准SFTP启动不查`PATH`、不拼shell字符串，也不把远端文件名作为shell参数；文件操作全走SFTP。helper是唯一app-generated restricted command string，只含ADR-0010 fixed literals/fresh safe home/freshly verified typed fields。`!`/`gs`只在用户明确进入shell surface后按上项独立契约处理：app生成的bootstrap仅编码canonical cwd，用户命令只在marker后走stdin；除此之外数据库path、文件名、搜索词、manifest raw value均不得进入remote command。
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
- argv 单元测试必须逐项比较上述精确数组，并证明任何用户输入都不能插入选项；poisoned `PATH` 中的 fake `ssh` marker 必须保持 0-hit，relative/dash/symlink/可写祖先/异主 final/setuid-setgid-sticky final/扩写 ACL/ACL 查询失败/启动前替换 override 必须在 exec 前拒绝，默认 `/usr/bin/ssh` 与安全 absolute override 分别覆盖。临时 ssh_config 必须分别注入 `RequestTTY force`、`EscapeChar ~`、`SessionType none`、`ForwardAgent yes`、`ForwardX11 yes`、local/remote/dynamic forwarding、`PermitLocalCommand yes` + `LocalCommand`、`RemoteCommand`、`StdinNull yes`、`ForkAfterAuthentication yes` 和 `Tunnel yes`，验证最终会话仍为前台、无 TTY/escape 处理、无额外命令/转发/tunnel 且 stdin/stdout 只承载 SFTP。真实 SFTP traffic fixture 必须包含 newline 与 escape-like bytes，证明二进制内容不被 client escape 处理。
- 同一SFTP矩阵必须证明host key、IdentityFile、登录所用ssh-agent、ProxyJump/ProxyCommand、GSSAPI/Kerberos、Include/Match和ControlMaster仍生效；GSSAPI-only在`GSSAPIDelegateCredentials=no`下仍可认证。ControlMaster新建/复用分别测试，并明确已有master若曾delegation/forward是用户配置边界，不能误报撤销。
- remote `!`/`gs`逐项比较本节精确argv，证明冲突config中的TTY、SessionType/RemoteCommand、forward、GSS delegation与ControlMaster/Path/Persist被固定值覆盖且fresh transport不复用旧master。`!`覆盖canonical cwd与single-quote/metachar/newline/non-UTF8/NUL/32768边界、root-owned printf、stdout byte0 marker/banner、custom/non-POSIX shell、marker前0 command bytes、UTF-8 command/CRLF/size、1MiB双stream继续drain、cancel/timeout/detached effect unknown。`gs`覆盖home无command、current-cwd probe/正式race失败后显式home retry、`-tt`、foreground pgrp、SIGWINCH及正常/非零/信号下termios/raw/alternate/cursor恢复；两者退出后都刷新实际目录状态。
- 子进程异常退出、半关闭、stderr 大量输出和取消时无 goroutine 泄漏的 race 测试。
