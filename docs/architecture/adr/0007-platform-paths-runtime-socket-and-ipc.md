# ADR-0007：冻结平台路径、运行时套接字与 IPC 端点

- 状态：Accepted
- 日期：2026-07-15
- 影响范围：配置、状态、缓存、日志、daemon 单实例与本机 IPC

## 背景

daemon、TUI 和 CLI 必须在 macOS/Linux 上独立找到同一份配置、状态库和 owner-only Unix socket。把 socket 放在持久 cache/state 中会留下陈旧端点或落到网络文件系统；直接拼接很长的 home/Application Support 路径又可能超过 Darwin 的 `sockaddr_un.sun_path`。配置格式已经由 Stage 0 严格 JSON schema 固定，路径决策不能另行引入 TOML/YAML。

## 决策

### 规范路径

所有路径由一个平台适配包计算，业务包不得自行读取环境并拼接路径。`~` 仅用于下表说明，实际代码必须使用 OS API 返回的绝对路径。

| 用途 | macOS | Linux |
| --- | --- | --- |
| 配置 | `~/Library/Application Support/io.github.tyrantlucifer.amsftp/config.json` | `${XDG_CONFIG_HOME:-~/.config}/amsftp/config.json` |
| 状态根 | `~/Library/Application Support/io.github.tyrantlucifer.amsftp/state/` | `${XDG_STATE_HOME:-~/.local/state}/amsftp/` |
| SQLite | `<state>/amsftp.db` | `<state>/amsftp.db` |
| daemon 日志 | `~/Library/Logs/io.github.tyrantlucifer.amsftp/daemon.jsonl` | `<state>/log/daemon.jsonl` |
| 缓存 | `~/Library/Caches/io.github.tyrantlucifer.amsftp/` | `${XDG_CACHE_HOME:-~/.cache}/amsftp/` |
| 首选运行时目录 | `${TMPDIR}/amsftp-<uid>/` | `${XDG_RUNTIME_DIR}/amsftp/` |
| 回退运行时目录 | `/tmp/amsftp-<uid>/` | `/tmp/amsftp-<uid>/` |
| 控制 socket | `<runtime>/control-v1.sock` | `<runtime>/control-v1.sock` |

macOS 的 config/state/cache 根分别从 `os.UserConfigDir`、`os.UserCacheDir` 与用户 home 的 `Library/Logs` 约定得到；Linux config/cache 使用对应 Go API，state/runtime 遵循 XDG Base Directory Specification。只接受绝对 XDG 路径；相对值视为未设置。

提供 `--config`、`--state-dir`、`--cache-dir` 和 `--runtime-dir` 作为测试、容器和故障恢复覆盖项。覆盖路径不降低权限、owner、symlink、绝对路径或 socket 长度检查，也不能来自配置文件本身改变正在读取的配置位置。

### 权限与路径验证

- 应用创建的 config/state/cache/log/runtime 目录默认 `0700`；配置、数据库、日志和 lock 文件默认 `0600`；socket bind 后强制 `0600`。
- config/state/cache/log/runtime 的应用根和所有显式 override 都必须是绝对、词法清理后的路径。应用根最终组件必须由当前 euid 所有、`lstat` 为真实目录而非 symlink、mode 精确 `0700`；其中配置、数据库（含 SQLite sidecar）、日志、lock 等常规文件必须为当前 euid 所有的非 symlink regular file 且 mode 精确 `0600`。已存在但 type/owner/mode 不匹配时不 chmod、删除或接管。
- 从 filesystem root 到应用根父目录的每个已有组件都必须用 `lstat` 逐级验证为 directory，owner 只能是 root 或当前 euid，且 group/other 均不可写（`mode & 0022 == 0`）。原始路径中的 symlink 一律拒绝，唯一允许的别名是 Darwin 上 root 控制的系统 `/var` 与 runtime 回退 `/tmp`，其 link target 从 `/` 按 symlink 规则规范解析后必须分别精确等于 `/private/var` 与 `/private/tmp`；系统常见 raw relative targets `private/var`、`private/tmp` 是合法输入，不能按 raw 字符串误拒。验证从规范绝对目标重新逐级开始；错误目标、额外多跳 symlink 或非 root owner 均拒绝。不得只对最终目录调用一次 `EvalSymlinks` 后忽略被隐藏的祖先权限。
- 新建应用根使用 owner-only umask/显式 mode 的逐级 mkdir + `lstat` 复核；创建前后都重验最近安全父目录。该保守规则以无第三方可写祖先消除其他 UID 的 rename 能力；若实现改用 dirfd/openat no-follow，必须保持相同可观察拒绝语义。与 Helper 一样，同 euid 主动进程属于登录用户信任边界。
- mode 不是 ACL 的替代品。平台 validator 冻结两个 profile。`integrity-only` 用于普通祖先链与 ADR-0001 的本机 ssh binary：Darwin build-tag 实现必须以不启用 CGO 的原生 `getattrlist(ATTR_CMN_EXTENDED_SECURITY)`/等价 kernel ABI 读取并严格 bounds-check ACL；没有 ACL 或只有不扩权 deny（包括标准用户目录常见的 `everyone deny delete`）可通过，任何 allow 只要授予 write/append/add-file/add-subdirectory/delete/delete-child/write-attr/write-xattr/write-security/chown 或等价 rename 能力就拒绝。Linux 实现读取 POSIX access/default ACL xattr，access ACL 的有效 mask 必须与 `stat` group bits 一致且不得给 owner 之外条目有效写能力，任何会让新对象继承额外权限的 default ACL 都拒绝。`owner-private` 用于 config/state/cache/log/runtime 应用根和配置、数据库/sidecar、日志、lock 等关键文件：除上述 integrity 规则外，Darwin 在不安全猜测 UUID→owner 映射的 v1 实现中保守拒绝**所有 allow ACE**，包括 named/inherited allow-read/list/search/execute/read-attr/read-xattr；Linux access ACL 不得让 owner 之外的 named user/group/mask 获得任何有效权限，且仍禁止 default ACL。两种 profile 的未知 entry/flag、截断、查询/解析失败均 fail closed。`ENODATA`/`ENOTSUP` 只有在平台适配已把实际 filesystem 识别为受测的 APFS、ext4、XFS 或仅用于 runtime 的 tmpfs 权限模型时，才可按纯 POSIX DAC 继续；NFSv4、CIFS/SMB、其他网络/alternate ACL 或 unknown filesystem 即使返回“无 ACL/不支持”也 fail closed。新建/chmod 应用根和关键文件后必须重新执行 `owner-private` ACL+mode 复核。
- macOS 的 `${TMPDIR}` 与 Linux 的 `${XDG_RUNTIME_DIR}` 只有在绝对、最终 base directory 为当前 euid 所有的真实 `0700` 目录、且完整祖先链通过上述规则时，才能作为首选 runtime base；相对、symlink、其他 owner、宽松 mode 或第三方可写祖先均发出一次不含原始敏感路径的诊断并使用短 `/tmp` 回退。典型 Darwin `/var/folders/.../T` 先按受信 `/var` 别名解析为 `/private/var/folders/.../T` 后验证。
- runtime 回退唯一额外允许的祖先是 root-owned、mode `01777` 的真实 `/tmp` sticky directory；Darwin 先验证 root-owned `/tmp -> /private/tmp` 系统别名，再对真实 `/private/tmp` 做同一检查。`/tmp/amsftp-<uid>`（Darwin 实际落在 `/private/tmp/amsftp-<uid>`）使用不跟随 symlink 的 mkdir/inspect 流程创建并复核 euid/真实目录/`0700`；已存在但不匹配时 fail closed，不删除或接管。sticky 例外不得用于 config/state/cache/log 或任意显式 override。
- 不安全的持久路径和 `--config`/`--state-dir`/`--cache-dir`/`--runtime-dir` 显式 override 均直接 fail closed，不能静默回退到另一份配置、状态或运行端点。只有不安全/过长的系统首选 TMPDIR/XDG runtime base 可以按上条回退。
- bind 前把 socket 路径编码为字节并要求不超过 100 bytes；首选路径过长时仅允许回退到短 `/tmp/amsftp-<uid>`，仍过长则给出可操作错误。

### daemon 单实例与 peer 身份

runtime 目录还包含 `daemon.lock` 和必要的原子启动元数据。daemon 在初始化持久状态前取得单实例 lock；遇到已有 socket 时先通过受限连接和握手判断健康，不能凭文件存在直接 unlink。只有确认 socket 属于当前用户、端点不可达且单实例 lock 已取得后，才能删除陈旧 socket。

Linux 使用 `SO_PEERCRED`，macOS 使用 `getpeereid`/等价本机 API。client 与 daemon 都要求 peer uid 等于自身 euid；查询失败即拒绝连接。父目录和 socket mode 是纵深防御，不替代 peer 检查。

### IPC v1 端点

线格式继续服从 [ADR-0005](0005-foundation-contracts.md)：4-byte uint32 big-endian 长度、JSON payload、8 MiB 单帧上限、原始路径字节 base64、epoch+sequence 事件游标。补充约束为：

- `control-v1.sock` 名称中的 `v1` 是 endpoint major；不兼容 major 使用不同 socket 名，不能在同一路径猜测协议。
- 首个应用帧必须是 handshake，携带 protocol major/minor、双方 build version 和能力；major 不同直接失败，minor 只按能力交集启用。
- 请求、响应与事件 envelope 必须带 type，相关调用带 request ID；payload/error 二者按类型互斥。
- 大目录通过分页/流式事件发送，不允许用接近 8 MiB 的单帧绕过有界消费。
- reader/writer 具有 deadline、取消和有界队列；慢 client 不能阻塞 daemon 全局事件分发。

## 替代方案

### 把 socket 放进 config/cache/state

路径更容易发现，但目录可能持久化、同步或位于网络文件系统，且 Application Support 路径可能超过 Darwin socket 限制，故拒绝。

### 只依赖 socket mode

`0600` 与 `0700` 是必要条件，但陈旧文件、错误 owner 和平台行为仍需 peer credential 与单实例 lock 验证，故不单独依赖 mode。

### NDJSON、net/rpc 或 gRPC

NDJSON 不如长度帧自然地在分配前限制消息；`net/rpc` 的演进控制不足；gRPC/Protobuf 对单用户本地 daemon 增加生成代码和依赖。现有长度帧 JSON 更易审计，故保持。

## 安全与可测试性检查

- 单元测试覆盖 XDG/TMPDIR unset/relative、Darwin raw relative targets `private/var`/`private/tmp` 解析到 `/private/var`/`/private/tmp` 的 golden、绝对等价 target 与错误/多跳 target 拒绝、长 home、非当前 owner、宽松 mode、symlink、普通文件占位、root/current-euid 安全祖先、第三方可写/可 rename 祖先、100-byte 边界和显式 override fail-closed。
- 平台路径集成测试覆盖典型 Darwin `/private/var/folders/.../T` 的 root/current-user 链、Darwin `/tmp` 别名解析后的 `/private/tmp`、Linux 合法 `XDG_RUNTIME_DIR`、root-owned sticky `01777 /tmp` 回退，以及非系统 symlink/other-owner/`0777` non-sticky base、祖先在校验/创建边界变化和预置 `/tmp/amsftp-<uid>`；不安全持久路径不得创建隐蔽回退状态。
- ACL 测试覆盖 Darwin 标准 `everyone deny delete` golden（两个 profile 都必须通过）、`integrity-only` 的 allow write/delete/delete-child、`owner-private` 的 named-principal allow-read/list/search 与 inherited allow-read、未知/截断 ACL 与查询失败；Linux 覆盖 access ACL mask、named-user effective write、owner-private named-user/group effective read、default ACL、无 ACL/不支持 ACL 与查询失败。`ENODATA`/`ENOTSUP` 在 APFS/ext4/XFS/tmpfs allowlist 与 NFSv4/CIFS/unknown 上分别证明 DAC fallback/拒绝；应用根创建前安全但创建后继承扩权 ACL 必须被复核拒绝。
- 平台集成测试覆盖 Linux `SO_PEERCRED`、Darwin peer uid、双 daemon 竞态、陈旧 socket 和 lock owner。
- 所有目录/文件创建测试在受控临时根运行，并断言最终权限；测试 override 不能跳过生产验证。
- IPC 契约继续覆盖超限前拒绝、截断、deadline、慢消费者、handshake 和大目录分页。

## 后果

- Stage 1 需要少量 Darwin/Linux build-tag 适配代码，但业务层得到一个稳定 Paths/PeerCredentials 接口。
- socket 路径不包含 workspace、用户名或 endpoint，避免泄露并保持短小。
- 不安全的环境目录会明确失败或使用经过验证的短回退；不会悄悄降级为世界可写端点。
- 本 ADR 只冻结路径与端点；Stage 0 尚未实现 daemon/socket 生命周期。

## 参考依据

- [Go `os.UserConfigDir` and `os.UserCacheDir`](https://pkg.go.dev/os)
- [XDG Base Directory Specification 0.8](https://specifications.freedesktop.org/basedir-spec/latest/)
- [Apple File System Programming Guide](https://developer.apple.com/library/archive/documentation/FileManagement/Conceptual/FileSystemProgrammingGuide/FileSystemOverview/FileSystemOverview.html)
