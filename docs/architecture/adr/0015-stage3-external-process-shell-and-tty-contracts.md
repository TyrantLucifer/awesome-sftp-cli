# ADR-0015：冻结 Stage 3 external process、shell 与 TTY 契约

- 状态：Accepted
- 日期：2026-07-16
- 影响范围：editor、opener、external previewer、`!`、`gs`、terminal suspend/resume、Stage 2 sync-back
- 依赖：ADR-0001、ADR-0002、ADR-0004、ADR-0013、ADR-0014

## 背景

Stage 3 需要把受管本地文件交给用户选择的 editor/opener，并提供两个用户明确进入的 shell surface。文件路径、Endpoint 和 cwd 不能拼成 shell text；外部程序退出不能授权上传；remote command 必须继续服从 ADR-0001 fresh transport、marker-before-input 与 TTY ownership。Provider、preview、cache 和任意通用 daemon RPC 都不得成为第二条命令或 mutation 路径。

## 决策

### 结构化外部程序配置

规范配置为：

```text
ExternalCommand {
  executable: string
  argv: []string
}
```

最多 128 个 argv，每项最多 4096 bytes，executable+argv 总计最多 32768 bytes；拒绝空 executable、NUL、CR/LF 和 ASCII control。explicit config 可以给 absolute path 或 bare first token；relative path with slash拒绝。bare token只用启动时的 `PATH` 做一次发现，随后 canonicalize为absolute regular executable并在展示后、exec前重验同一file identity。editor/opener/previewer属于同UID用户授权代码边界，不套用 OpenSSH 的root/ACL resolver，但仍拒绝 symlink/special/non-executable final 和发现后替换。执行只用 `exec.Cmd`/等价 direct exec，不经 shell。

`VISUAL`/`EDITOR` 只经过受限 lexer：ASCII space/tab 分词；single/double quotes 与 backslash只负责组词；拒绝未闭合 quote/escape、NUL/CR/LF/control，以及任何位置的 `$`、backtick、`*`、`?`、`[`、`]`、`|`、`&`、`;`、`<`、`>`。不执行 expansion、substitution、glob、pipe、redirection 或 operator。explicit structured argv 不把 metachar解释为语法，但仍服从 control/size上限。

editor解析顺序固定为 explicit config → `VISUAL` → `EDITOR` → PATH discovery `nvim` → `vim` → `vi`。canonical absolute materialization path始终作为最后一个独立 argv追加；不允许模板替换、shell quoting或把路径插入用户 argv中间。启动确认展示 absolute executable、每个 argv、Endpoint、source Location、local cache path、lease/session ID 与“退出不等于上传”。

macOS默认 opener固定 `/usr/bin/open`，Ubuntu固定 `/usr/bin/xdg-open`；不存在/不合格时只接受结构化 explicit opener。文件仍是最后独立absolute arg。opener立即返回时保留 ADR-0014 grace/heartbeat和显式检查入口；默认不监测到变化就自动上传。

external previewer 只是 built-in preview 对 image/unknown binary 的安全降级链；text/code/JSON 和显式 metadata/raw-JSON view 不能启动子进程。rule 按配置顺序匹配 media type/extension，必须同时满足 complete verified materialization、`max_input_bytes`、`timeout_ms` 和 active lease；stdout 丢弃，stderr 只在内存保留经脱敏的单行 4 KiB diagnostic。超时、非零、signal、identity change 或无匹配都回退 built-in 结果，不影响 pane/Endpoint/Job。

### edit/opener 回传

外部进程前只交付 complete verified materialization。editor正常、非零或signal退出都检查本地内容；非零/signal默认不自动提出上传，UI先说明退出状态和local change。无变化不建Job；remote-only提示刷新；local-only展示摘要并确认；both或无法可靠stat/hash进入conflict。opener永不因“可能修改”授权上传。

sync-back只能调用 ADR-0014 的原子 edit-session→Stage 2 Job入口。Plan冻结source edit session、baseline remote expected-destination fingerprint、local SHA-256、target Location、overwrite/save-as decision；worker在开始和commit前都比较baseline precondition。普通 overwrite conflict resolution不能把新观察到的remote identity替换为baseline后静默覆盖；显式覆盖必须属于同一edit session的持久审计决定。

### local `!` 与 `gs`

local shell解析为 explicit absolute shell → absolute `$SHELL` → fixed `/bin/sh`；每次启动前解析/展示并重验absolute regular executable。`!`只接受Normal模式用户动作、UTF-8、NUL/CR/LF-free单行，最多32768 bytes；argv精确为 `[shell, "-c", userText]`，`Cmd.Dir`是focused pane canonical local cwd，cwd不进入command。stdout/stderr各1 MiB ring并发持续drain，超限继续discard并显示count；raw output默认不持久化。整个 client 同时最多一个 one-time command；第二个以 bounded rejection 返回而不取代前一个。确认后 UI 显示 running，Normal 模式 `Esc` 取消当前 context/process group，local 与 remote 都有 15 分钟绝对 timeout；完成/取消后使用 generation 清理恰好那个 active cancel handle 并刷新 pane。

local `gs`使用同一个已解析 shell 和 `Cmd.Dir`；当前 `$SHELL`/`/bin/sh` 路径的 argv 是 `[shell]`，不经 `-c` 或中间 shell text。它直接继承 TTY 且不 capture/log terminal bytes。退出后刷新两 pane 与 drawer snapshot。

TUI 键序列中的 `gs` 表示先按 `g` 进入 path/go 输入，再在空输入时按小写 `s`；`gS` 是明确的 remote-home fallback。它们都不是常驻 shell pane。

### remote `!` 与 `gs`

remote argv、validated absolute ssh、host alias、fresh `ControlMaster/Path/Persist=no`、GSS delegation/forwarding禁用、POSIX Q、root-owned `/usr/bin/printf`、byte-0 marker、32 KiB cwd/bootstrap/command、双1 MiB ring、cancel/detach `remote effect unknown`、`ssh -tt`、home无command、current-cwd probe及显式home retry全部直接引用ADR-0001规范值，不在本 ADR 复制或弱化。

native remote harness 必须在 temporary `sshd`+isolated Host alias 上同时证明 user-visible `!` 的两次 Enter 确认、remote exact cwd marker 与 exit 0，然后在同一 TUI 中证明 `gs` foreground PTY/cwd/return refresh；只测 runner 函数或 mock argv 不代替此证据。

remote utility preflight必须从SFTP attrs同时取得UID/GID数值和“字段确实存在”的presence；当前只暴露mode而不保留ATTR uid/gid presence的Provider不能证明root-owned。不得把缺失字段解码成numeric zero后当成uid0。Stage 3先在现有窄SFTP读取路径保留presence，只有UID字段present且值为0、type/mode/owner检查同时通过才允许remote `!` cwd surface；缺失或不可信一律unsupported并发送0 command bytes。

shell surface只存在于TUI/CLI的显式用户动作。daemon RPC可以提供普通 file/Job/cache查询与用户决定，但不得接受通用command string并在daemon执行；Provider/file action、previewer rule、cache rebuild、workspace restore和事件 replay永远不能触发`!`/`gs`。

### TTY handoff state machine

客户端拥有唯一 terminal handoff controller：

```text
active_tui -> suspending -> external_foreground -> reacquiring -> active_tui
                                    | failure/signal |
                                    +----> cleanup ----+
```

进入external前冻结active pane、drawer tab/focus/height、Job cursor、Preview request identity与terminal size；停止tcell input，show cursor，退出alternate/raw，保存termios与foreground pgrp，转交controlling TTY。返回时先重新取得foreground pgrp/TTY，恢复termios，重建alternate/raw/cursor，重放最新SIGWINCH，重新订阅bounded daemon snapshot/events并刷新两pane。每个阶段幂等；spawn失败、正常、非零、signal、auth cancel、PTY loss与panic cleanup都走同一恢复入口。如果 child 已成功 spawn，但 process-group identity 无效或 `GiveForeground` 失败，controller 立即启动 SIGKILL process-group + `Wait` reap；正常 cleanup 必须在恢复 TUI 前完成。为防异常 OS/adapter 永久卡住恢复，post-spawn cleanup 有 2 秒硬上限：超时返回显式诊断、TUI 立即恢复，唯一 buffered cleanup worker 在后台继续完成 terminate+reap。真实 exec adapter 的 terminate 是一次有界 group signal；测试分别冻结 Terminate/Wait，证明恢复不被阻塞并在释放 gate 后 worker 确实结束。daemon/Job不依赖此client存活。

2 秒后仍未结束的 cleanup 设置唯一 `cleanupPending`；该 worker 完成前任何新 handoff 都 fail closed/busy，所以永久 stall 不会累积 worker 或 child。冻结 Terminate/Wait 的测试同时证明 TUI 恢复、第二次 handoff 被拒绝，且释放 gate 后唯一 worker 正常结束。

每次 editor、opener 或 shell 的 foreground handoff 结束后，客户端在恢复 foreground/termios 之后、tcell 重新读取输入之前重做 active terminal-image capability probe。probe 失败会原子清除旧 proof，防止从一个支持终端返回不支持终端后继续输出图片 escape sequence。external previewer 使用 bounded pipes 且不接管 controlling TTY，因此不属于此 handoff。

`gs`不capture terminal bytes。editor/opener/previewer只保留bounded exit status、signal、duration、resolved executable identity与redacted error code；不持久化argv中的用户敏感值、file content、command output或environment。

## 安全与验证要求

- lexer golden/fuzz覆盖quote/backslash、空词、operator/expansion/glob/control拒绝、32 KiB/128 argv边界。
- resolver覆盖PATH discovery一次、absolute freeze、relative slash、symlink/special/replace、leading-dash filename与canonical last arg。
- fake editor/opener覆盖normal/nonzero/signal/spawn failure、no-change/mtime-only/content-change、return-early/grace；真实Vim/Neovim、macOS open、Ubuntu xdg-open native smoke。Ubuntu Hosted quality 作业不允许因镜像缺工具而 skip：先检查 `sshd`/`nvim`/`vim`/`xdg-open`，任一缺失则安装 `neovim openssh-server vim xdg-utils`，再运行真实 editor/opener/temporary-sshd command+PTY 矩阵。
- edit remote unchanged/modify/delete/replace/unstat matrix与diff/save-as/skip/explicit-overwrite四分支；所有upload经过Stage 2 Job并保留dirty recovery。
- local/remote `!` 与 `gs`执行ADR-0001 exact argv/config/cwd/marker/ring/cancel/TTY测试；remote marker失败发送0 command bytes，one-time command single-flight/`Esc` cancel/15-minute timeout 及真实 remote UI+sshd harness 均为必须证据。
- foreground pgrp、termios/raw/alternate/cursor、SIGWINCH、normal/nonzero/signal/PTY loss、post-spawn pgrp/foreground failure 的 terminate+reap-before-resume、handoff 后 image capability re-probe 和后台Job continuation在macOS/Linux native验证。
- architecture/static tests证明Provider、file RPC、preview和cache包不可达shell runner或raw mutation。

## 后果

- 用户配置的editor/opener仍是同UID代码执行边界，但应用不扩大到shell解释或credential传递。
- terminal handoff比简单`screen.Fini`复杂，但所有退出路径有单一可测恢复模型。
- future Helper/search/watch不得复用用户shell runner；Helper继续只用ADR-0010 restricted command+framed stdin。
