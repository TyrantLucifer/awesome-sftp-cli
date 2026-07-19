# ADR-0006：冻结公开身份、工具链与运行时库选择

- 状态：Accepted
- 日期：2026-07-15
- 影响范围：公开命令、模块路径、Go 兼容线、Stage 1 TUI/SFTP 依赖、结构化日志
- 修订：本 ADR 的 `github.com/pkg/sftp v1.13.10` 初始 pin 已由 [ADR-0011](0011-pkg-sftp-streaming-directory-cursor.md) 精确取代；2026-07-19 将仓库与 Go module 规范身份统一为 `awesome-sftp-cli`

## 背景

批准设计要求在 Stage 1 前确定公开名称、精确 Go 支持线、依赖版本、TUI/渲染库和结构化日志方案。Stage 0 当前代码有意只使用标准库，因此这里冻结的是后续阶段必须采用的兼容性选择；它不把 TUI、SSH/SFTP 或 SQLite 实现伪装成 Stage 0 已交付能力。

产品需要同时满足双栏 Vim 状态机、5 万项目录的窗口化渲染、daemon 事件流、外部 Vim/shell 的终端暂停恢复，以及复用系统 OpenSSH stdio 的 SFTP 协议。选择必须能在 Go 1.25 兼容线上测试，并避免在业务层绑定重量级框架或全局日志状态。

## 决策

### 公开身份

以下标识自本 ADR 起为规范值：

    Product display name: AMSFTP
    Public command: amsftp
    Filesystem/package slug: amsftp
    Application ID: io.github.tyrantlucifer.amsftp
    Go module: github.com/TyrantLucifer/awesome-sftp-cli

仓库名固定为 `awesome-sftp-cli`。仓库 URL、Go module、构建元数据、用户文档、发行物和包管理器必须使用同一身份；产品显示名与公开命令仍分别为 `AMSFTP` 和 `amsftp`。一个二进制继续通过角色参数承担 client、daemon、askpass 和 helper；不创建独立 daemon/helper 产品名。

### Go 支持线

- `go.mod` 的语言与 module 语义基线保持 `go 1.25.0`。
- 日常和稳定 CI 精确使用 Go 1.26.5。
- oldstable 兼容门禁精确使用 Go 1.25.12，并设置 `GOTOOLCHAIN=local`。
- 产品代码不得依赖 Go 1.26 才出现的 API；提升语言基线、首选工具链或 oldstable 版本必须新增 ADR。

Go 1.25.12 和 1.26.5 均为 2026-07-07 发布的受支持补丁版本。`go 1.25.0` 是源码语义下限，不表示允许用已被补丁版本取代的 1.25.0 工具链生产发行物。

### 精确依赖选择

Stage 0 根 module 保持无第三方产品依赖。下表是后续 owning stage 的规范版本 pin；第一次导入前必须以精确版本写入 `go.mod`/`go.sum`，运行许可证、`govulncheck`、双 Go 工具链和四目标构建门禁。版本变更是有意依赖升级，不得使用 `latest`、pseudo-version 或宽松范围。

| Owning stage | 用途 | 规范 module/version |
| --- | --- | --- |
| Stage 1 | 终端输入与 cell 渲染 | `github.com/gdamore/tcell/v3 v3.4.0` |
| Stage 1 | system-ssh stdio 上的 SFTP 协议 client | 由 [ADR-0011](0011-pkg-sftp-streaming-directory-cursor.md) 修订为上游 `github.com/pkg/sftp v1.13.11` 加不可变窄 fork/replace |
| Stage 2 | SQLite driver | `modernc.org/sqlite v1.53.0`，并保留其精确 `modernc.org/libc v1.73.4` 解析结果 |

`tcell/v3 v3.4.0`、ADR-0011 的 `pkg/sftp v1.13.11` fork 基线与 `modernc.org/sqlite v1.53.0` 都兼容 Go 1.25.0。SQLite 的使用与迁移机制由 [ADR-0008](0008-modernc-sqlite-and-forward-migrations.md) 约束。

### TUI 与渲染

使用 tcell v3 直接构建两栏界面，不在其上叠加 Bubble Tea、tview 或另一套组件状态机：

- 两栏共用项目自己的纯 Go model/reducer；tcell 只负责终端事件和 cell 输出。
- 目录视图只构造可见行和有界 overscan，禁止为 5 万条记录生成完整 View 字符串。
- tcell `EventQ`、daemon 事件、定时器和取消统一进入有界应用事件队列。
- 启动 Vim、默认打开器前台等待或交互 shell 时必须 suspend/restore；正常退出、信号和 panic 路径都恢复终端。
- reducer/cell 快照使用项目自有的纯 renderer/screen fake；按键、resize、宽字符、组合字符、两栏焦点和真实终端适配层使用 `tcell/v3/vt.NewMockTerm` 经生产 `Tty`/screen adapter 测试。不得引用只存在于旧 major 的 `SimulationScreen` API。

Stage 1 必须在 macOS Terminal/iTerm2、Linux 终端、tmux 和 screen 上验证 resize、`SIGTSTP`/`SIGCONT`、Vim 返回和异常恢复；5 万条合成目录的结构性基准要证明渲染/分配只随可见窗口增长。Stage 5 仍负责最终 5 万项生产延迟、内存曲线和长稳门禁。若 tcell v3 出现有证据的阻断问题，只能通过新 ADR 改用精确 `github.com/gdamore/tcell/v2 v2.13.10`，不能临时换框架。

### 结构化日志

使用标准库 `log/slog`：

- 业务组件显式接收 `*slog.Logger`，禁止把 `slog.Default` 当隐式依赖。
- daemon/helper 持久诊断使用 `JSONHandler`；明确的前台调试模式可以使用 `TextHandler`。
- 所有持久化 handler 前置统一脱敏层，并只允许已注册字段；至少支持 `component`、`event`、`endpoint_id`、`job_id`、`request_id` 和 `error_code`。
- Kerberos ticket、认证回答、私钥、完整环境、默认完整路径和未经解析的完整命令行永不记录。
- 用 `slog.LevelVar` 动态调级；Go 1.25 兼容实现不得调用 Go 1.26 新增 API。
- daemon 日志必须由项目内有界 rolling writer 限制大小与份数；不得因选择标准库而允许日志无限增长。

## 替代方案

### Bubble Tea v2

Elm 模型适合普通 TUI，但本项目仍要自行实现大目录窗口化、两栏精确 cell 布局和前台程序 suspend/restore。再叠加一套 renderer 生命周期会降低控制力，故不采用。

### tcell v2 或 tview

tcell v2 是可接受的回退线，但新项目优先采用支持 event channel 和多 rune grapheme 的当前 major。tview 能缩短普通表单开发时间，却会限制 Vim 模式、双栏对称状态和虚拟化渲染，故不采用。

### 自行实现 SFTP 或嵌入 SSH

自行实现 SFTP 会扩大协议与安全维护面；嵌入 SSH 会破坏系统 OpenSSH、Kerberos、ProxyJump、agent 和组织策略的事实源边界。`pkg/sftp` 只承载已经由 ADR-0001 精确 system-ssh argv 建立的 stdio 协议，不接管认证。

### zap、zerolog 或全局 stderr

前两者增加 API/依赖耦合，当前没有性能证据证明标准库不足；只写 stderr 不满足后台 daemon 的有界诊断需求。若未来 profiling 证明 `slog` 是真实热路径，需另写 ADR。

## 安全与可测试性检查

- tcell 只处理终端边界，OperationIntent 与 daemon 权限不进入 renderer。
- SFTP client 不读取私钥、ticket 或 ssh_config；系统 `ssh` 子进程仍是唯一认证入口。
- dependency intake 必须检查许可证、传递依赖、模块校验和、已知漏洞和四目标 `CGO_ENABLED=0` 构建。
- 日志测试使用 `testing/slogtest`、敏感值 canary、错误链、并发写入和轮转边界。
- TUI 测试必须证明取消、事件队列上限、宽字符、终端恢复和 5 万项窗口化；研究/选择不算这些行为已经通过。

## 后果

- Stage 1 的第一项依赖 intake 是机械执行本 ADR 的精确 pin，而不是重新选框架。
- Stage 0 仍不提供可用 TUI 或 SFTP；相关功能只有在 owning stage 的代码、测试和平台证据完成后才能改变功能矩阵状态。
- 直接 cell 渲染需要项目维护自己的布局组件，但换来大目录和 Vim 交互的确定控制。
- 依赖 pin 将随 owning stage 进入 module graph；任何升级单独审查并保留可回滚 diff。

## 参考依据

- [Go release history](https://go.dev/doc/devel/release)
- [tcell v3.4.0 module](https://raw.githubusercontent.com/gdamore/tcell/v3.4.0/go.mod)、[v3 changes](https://raw.githubusercontent.com/gdamore/tcell/v3.4.0/CHANGESv3.md) 与 [`vt.NewMockTerm`](https://github.com/gdamore/tcell/blob/v3.4.0/vt/mock.go)
- [`pkg/sftp` v1.13.11 release](https://github.com/pkg/sftp/releases/tag/v1.13.11) 与 [ADR-0011](0011-pkg-sftp-streaming-directory-cursor.md)
- [`log/slog` package](https://pkg.go.dev/log/slog) 与 [Go slog design](https://go.dev/blog/slog)
