# Stage 3 — Preview, Edit & Cache

- **状态**：In Progress
- **阶段类型**：日常使用体验闭环
- **前置条件**：[Stage 2 — Durable Transfers](02-durable-transfers.md) 已通过退出门禁
- **完成后进入**：[Stage 4 — Search & Optional Helper](04-search-helper.md)

## 1. 阶段目标

让项目成为 Vim 用户可以长期自用的日常工具：在双栏之间快速预览，在 `$EDITOR` 中编辑远端文件，用系统默认软件打开文件，观察后台作业和诊断，并能从当前本地或远端位置执行一次性命令或进入临时 shell。

本阶段增加便利能力，但不降低 Stage 2 的数据安全保证。所有编辑回传都必须通过同一 Operation Intent、Planner、Job、冲突检查和临时提交链路；任何外部程序都不能获得项目持有的认证秘密，因为项目本身不持有这些秘密。

## 2. 范围

### 2.1 包含

1. 底部 Preview、Jobs、Log 三个可切换抽屉。
2. 文本、代码、JSON、元数据和有限二进制预览；受支持终端的图片预览。
3. 可配置外部预览器管线，具备清晰的能力检测、超时和失败回退。
4. 本地内容缓存：短期 LRU、工作区临时缓存和显式固定离线条目。
5. 缓存配额、租约、引用、内容去重、逐出和异常退出恢复。
6. `e` 使用 `$EDITOR`（Vim-first 默认）编辑，`o` 使用系统默认打开器打开。
7. 编辑退出后的本地变化检测、远端并发变化检测、确认上传、冲突处理和另存为。
8. `!` 在当前 Location 执行一次命令，`gs` 暂停 TUI 并进入本地或远端交互 shell。
9. 外部进程期间的终端挂起/恢复、目录刷新和错误反馈。

### 2.2 非目标

1. 不内嵌终端模拟器，不实现完整 shell 窗格或会话复用器。
2. 不构建远端全量索引、递归内容搜索或文件监听服务；这些由 Stage 4 Helper 增强。
3. 不做跨主机直传和高规模性能优化。
4. 不自动合并并发编辑冲突；可以调用用户配置的 diff 工具，但最终决策由用户确认。
5. 不把缓存当远端真相源；离线固定项必须明确显示可能陈旧。

## 3. 依赖与关键约束

### 3.1 Stage 2 输入

- 可持久化 Job、冲突状态、进度事件和安全提交路径。
- Provider 的有界读取、临时写、验证和提交能力。
- 守护进程重启恢复和 Endpoint 认证等待语义。

### 3.2 外部程序边界

- 外部编辑器、打开器和预览器只接收受管本地文件路径，不接收守护进程 Socket、认证响应或内部数据库路径。
- 严格owner/ACL executable resolver只适用于OpenSSH。编辑器/opener是同UID用户授权代码执行边界：显式配置保存`executable+argv[]`；`VISUAL`/`EDITOR`只做受限POSIX词法切分（quotes/backslash可组词，拒绝expansion、substitution、glob、pipe/redirection/operator与控制字符），不经shell。bare name/PATH只用于发现，启动前解析/显示并重验absolute regular executable；文件使用canonical absolute path独立末参数。
- `!`/`gs`是仅有的用户明确shell surface；执行位置、Endpoint、cwd/fallback与命令文本在运行前展示，不能由Provider或任意RPC隐式触发。本地`!`用进程cwd+shell`-c`独立arg，本地`gs`用进程cwd；remote严格服从ADR-0001 fresh transport/marker/TTY契约。

### 3.3 Helper 信任交接门禁

Stage 3 不实现 Helper，但在进入 Stage 4 的任何下载、上传或安装工作前，必须按 [ADR-0010](../architecture/adr/0010-helper-artifact-trust-and-distribution.md) 完成第一组 Ed25519 public key/key ID、离线私钥 custody/恢复 runbook、撤销窗口和双 key 轮换演练。缺少任一项时仍可规划/实现 Level 0 SFTP 搜索，不得开始 Helper 分发实现。

## 4. 详细交付物

### S3-D01 底部抽屉框架

- `K` 打开/聚焦 Preview，`J` 打开/聚焦 Jobs，`L` 打开/聚焦 Log；再次操作或退出键返回文件栏。
- 抽屉高度可调并适配窄终端；主文件栏始终知道当前焦点。
- Preview 绑定当前光标或固定条目，快速移动时取消过期请求，避免旧结果覆盖新选择。
- Jobs 展示 Stage 2 作业状态、进度、等待原因、最近错误和可用动作。
- Log 使用有界环形视图，支持按 Job/Endpoint 过滤和复制诊断；默认脱敏且不读取无限历史。
- 抽屉状态可按工作区保存，但不保存敏感内容或完整预览正文。

### S3-D02 内建预览管线

- 先读取有限探测窗口，结合扩展名、内容特征和编码判断预览类型；判断不确定时优先安全的十六进制/元数据摘要。
- 文本/代码支持语法着色、行号、换行控制、分页和可取消的更多/尾部读取。
- JSON 等结构化文本可提供格式化视图，但必须限制解析输入大小、深度和输出量。
- 大文件预览始终按块读取，显示“仅部分内容”和偏移范围，不假装完整。
- 权限、断线、内容变化、解码失败和超时在抽屉内表达为结构化状态。
- 预览结果带来源指纹；远端文件变化后旧结果标陈旧或被替换。

### S3-D03 图片与外部预览器

- 检测 Kitty、iTerm2、Sixel 等终端图片能力，仅在明确支持时启用。
- 不支持图片协议时显示尺寸、格式、文件大小等元数据，并允许 `o` 交给系统打开器。
- 图片数据的下载、解码、像素和内存设上限；超限时缩略或拒绝，不能拖垮 TUI。
- 外部预览器是有序规则：匹配条件、可执行程序、参数、超时、最大输入和是否需要完整本地文件。
- 外部预览失败时记录受限诊断并回退内建预览，不影响 Endpoint 或后台 Job。

### S3-D04 缓存与租约

缓存至少区分：

- **临时预览缓存**：短 TTL、可随时逐出。
- **工作区缓存**：当前工作区复用，受 LRU 和配额控制。
- **固定离线条目**：用户显式选择，逐出前需确认；仍保留来源和陈旧标识。

行为契约：

- 缓存根目录仅当前用户可访问，内容文件权限不宽于用户私有。
- 逻辑条目由 Endpoint、规范路径和远端指纹关联；内容可按强内容标识去重，但不能仅凭路径认为未变化。
- 打开的编辑器/打开器持有租约，租约期间内容不被逐出或原地替换。
- 配额同时约束总字节、条目数和可选工作区份额；逐出忽略活跃租约与固定项。
- 守护进程崩溃后重建引用和租约；过期进程租约可回收，仍在运行的外部进程不被误删。
- 清缓存操作展示将释放内容和受影响的固定/租约状态，不触及用户原文件。

### S3-D05 `e` 编辑工作流

1. 守护进程记录来源 Location、下载前远端指纹和编辑会话 ID。
2. 文件进入受管缓存并获得编辑租约；内容完整性在交给编辑器前验证。
3. TUI正确挂起；解析优先级为显式结构化配置、`VISUAL`、`EDITOR`、`nvim`、`vim`、`vi`。按上项受限lexer解析并把first token经PATH发现/realpath固化为本次absolute executable，余项保持argv；拒绝相对含slash、空命令、shell operator/expansion。启动前显示最终program/args，direct exec并把canonical absolute cache file作为最后独立arg。
4. 编辑器退出后比较本地内容与初始本地内容，同时重新读取远端指纹。
5. 根据结果处理：
   - 本地未改：不上传；若远端变化则提示刷新。
   - 仅本地改：展示目标与变化摘要，用户确认后创建 Stage 2 Job。
   - 本地与远端都改：进入冲突，不静默覆盖；提供查看远端、diff、覆盖、放弃本地或另存为。
   - 无法可靠判定：按冲突处理。
6. 上传通过 `.part`、验证和提交；成功后刷新缓存指纹，失败时保留本地编辑副本和恢复入口。

编辑器非零退出不等于丢弃修改；仍检查本地变化，但默认不自动上传。

### S3-D06 `o` 默认打开器工作流

- 本地/缓存文件以canonical absolute path独立参数direct exec；macOS默认固定`/usr/bin/open`，Ubuntu默认固定`/usr/bin/xdg-open`，不存在时要求显式结构化opener。自定义项按上节解析为本次absolute executable；不经shell，下游同UID GUI/app是用户/OS信任边界。
- 远端文件下载到受管缓存并持有打开租约；UI 明确这是本地副本。
- 若打开器退出可观察，按与编辑类似的变化检测执行；若平台打开器立即返回，租约采用心跳/宽限期和显式“检查更改”机制。
- 默认不因外部程序可能写入而自动上传；用户明确选择同步时才建立 Job。
- macOS 与 Linux 分别通过平台标准打开机制，缺失时给出可配置替代方案。

### S3-D07 `!` 一次性命令

- 命令输入区显示当前活动栏是local还是哪个SSH Endpoint、canonical cwd、所用shell以及remote不支持cwd时的明确降级；命令须再次确认且为UTF-8、NUL/CR/LF-free单行≤32768 bytes。
- local通过进程`Dir`设置cwd并以用户shell的`-c`、用户原文两个独立args direct exec；cwd不拼入command。
- remote使用ADR-0001 exact `ssh -T` fresh-transport argv。app生成的唯一bootstrap只含受测POSIX byte-quoted canonical cwd；stdout byte0 marker成功前发送0 command bytes，成功后才把用户原文作为一行stdin并close。banner、custom/non-POSIX shell、unsafe/non-roundtrip path、cd/printf失败明确拒绝，不猜cwd且v1无interactive stdin。
- stdout/stderr各1MiB ring并发持续drain；超限discard仍防阻塞并显示truncated/discarded count。取消/超时终止local ssh process group并关pipe；remote命令可能detach，结果标`remote effect unknown`，不能宣称终止。
- 命令完成后刷新当前目录，任何文件变化都视为外部变化，不伪装成受管理 Job。
- 工作区可保存命令偏好，但默认不保存命令输出；日志仍执行敏感信息脱敏。

### S3-D08 `gs` 交互 shell

- local栏以进程`Dir`进入用户shell。remote栏使用ADR-0001 exact fresh `ssh -tt`；默认home模式host后无command。current-cwd模式只在独立non-PTY POSIX/Q/cd capability probe成功后用最后恰一bootstrap，正式失败先恢复终端再明确offer home retry，不静默谎称cwd。
- `gs`不capture/log terminal bytes。TUI保存termios，退出alternate/raw并把TTY/foreground pgrp交给ssh；正常/非零/信号返回后reacquire TTY、恢复termios/alternate/cursor、replay SIGWINCH并refresh。守护进程/后台Job继续运行。
- shell 退出后重新握手终端能力、刷新两栏和抽屉，不依赖进入前的陈旧目录快照。
- SSH认证继续由系统OpenSSH负责；fresh transport固定GSS delegation/ControlMaster off，不复用或导出Askpass响应，但remote same-euid既有cache仍是环境边界。
- 不提供嵌入式终端滚屏、分屏或 shell 生命周期管理。

### S3-D09 异常恢复与诊断

- TUI 在编辑器/打开器启动失败时恢复终端并保留租约状态。
- 守护进程重启后可列出未完成编辑会话、缓存文件、本地是否修改和远端是否可检查。
- 用户可继续、另存、放弃或清理孤立会话；任何清理都不删除远端或用户原文件。
- Log 抽屉可关联预览请求、编辑会话、缓存条目和上传 Job，但不输出文件完整内容。

## 5. 里程碑顺序

### M3.1 — 抽屉与缓存基础

1. 建立 Preview/Jobs/Log 抽屉状态与焦点模型。
2. 实现缓存目录、元数据、配额、租约和重启扫描。
3. 用 Fake Provider 验证取消、逐出和陈旧标记。

门禁：缓存达到配额、守护进程崩溃和活跃租约场景均不丢失受保护内容。

### M3.2 — 内建与图片预览

1. 文本/代码/JSON/二进制元数据预览。
2. 终端图片协议检测和安全回退。
3. 外部预览器规则、资源预算和失败隔离。

门禁：大/坏/恶意输入不会无限读取、解析或渲染，快速移动无陈旧结果竞态。

### M3.3 — 编辑与默认打开

1. `e` 的下载、租约、挂起、变化检测和 Job 回传。
2. 并发远端变化冲突与恢复。
3. `o` 的平台打开器和不可观察生命周期处理。

门禁：本地与远端同时修改的所有时序均无静默覆盖，异常退出后可恢复本地副本。

### M3.4 — 命令、shell 与平台收尾

1. `!` 本地/远端一次性命令。
2. `gs` shell 挂起与恢复。
3. macOS/Linux 编辑器、打开器、shell 和终端能力矩阵。

门禁：所有外部程序路径均恢复终端状态，后台作业不依赖 TUI 存活。

## 6. 可验证退出标准

- [ ] Preview/Jobs/Log 抽屉在正常、窄屏和窗口调整时保持可用焦点模型。
- [ ] 文本、代码、JSON、未知二进制和大文件预览均有有界读取与明确的部分内容提示。
- [ ] 至少一种支持的终端图片协议验证通过；不支持时无乱码并安全回退。
- [ ] 缓存配额、LRU、固定项、内容去重和活跃租约行为有确定测试。
- [ ] 编辑无变化不上传；仅本地变化需确认；双方变化必进入冲突；判断不确定也不覆盖。
- [ ] 编辑上传复用 Stage 2 Job，并在失败/重启后保留可恢复本地副本。
- [ ] editor优先级/受限env lexer/PATH发现后absolute direct exec及macOS`/usr/bin/open`、Ubuntu`/usr/bin/xdg-open`通过；路径始终独立absolute arg，立即返回opener不导致过早逐出。
- [ ] `!`覆盖local Dir+shell arg与remote exact fresh argv、byte-safe cwd/marker-before-input、32KiB、1MiB双stream持续drain、cancel/detach unknown/refresh；shell/路径失败发送0 command bytes。
- [ ] `gs`覆盖home无command/current-cwd probe+显式fallback、`-tt`、TTY/foreground pgrp/SIGWINCH，以及正常/非零/信号后termios/alternate/cursor、两栏与Job正确恢复。
- [ ] 日志、缓存元数据和配置中无认证秘密或无界文件内容。
- [ ] Helper key custody/恢复、第一组 public key、撤销窗口与双 key 轮换演练有可审查证据；否则 Stage 4 Helper 分发保持关闭。

## 7. 测试矩阵

| 层级 | 场景 | 必须证明 | 证据 |
|---|---|---|---|
| 单元 | 类型识别、截断、指纹比较 | 不误判完整性，边界输入稳定 | 单元/模糊测试 |
| 单元 | LRU、配额、租约 | 固定/活跃内容不被错误逐出 | 确定性时钟测试 |
| 集成 | Preview 快速切换 | 取消旧请求，无结果串位 | Fake/真实 SFTP 测试 |
| 集成 | 编辑四种变化组合 | 无变化/本地/远端/双方语义正确 | 编辑会话矩阵 |
| 故障 | 编辑器崩溃、守护进程重启、上传失败 | 终端恢复，本地副本可找回 | 故障注入记录 |
| 安全 | 外部程序参数与权限 | 无路径注入、缓存仅用户可读 | 负向测试 |
| 平台 | Vim、默认打开器、shell | macOS/Linux 实际可用 | 平台冒烟 |
| 终端 | Kitty/iTerm2/Sixel/不支持 | 正确检测与回退 | 兼容记录 |
| 资源 | 超大文本/图片/JSON | CPU、内存、输出受预算约束 | 资源测试 |
| Race | 租约、逐出、并发刷新 | 无删除在用文件或陈旧覆盖 | race 测试 |

## 8. 失败与回滚策略

- 预览器失败只关闭该预览并显示元数据，不影响文件栏、Endpoint 或 Job。
- 缓存索引损坏时保留内容目录，进入重建/诊断模式；不能把未知缓存直接删除。
- 配额失效或磁盘满时停止新缓存下载，已有浏览与后台传输继续按各自策略运行。
- 编辑上传失败保留本地副本和初始/当前指纹，允许稍后重试或另存；不把失败副本覆盖为新基线。
- 外部程序启动后 TUI 无法正常恢复时，终端清理逻辑必须独立可执行；下次启动显示孤立会话。
- 新版缓存格式不可被旧版读取时，回滚前使用导出/备份路径；远端数据不因缓存回滚改变。
- 可通过配置总开关禁用图片或外部预览器，保留内建文本/元数据预览。

## 9. 完成时必须更新的文档

- `PROJECT_STATE.md`：缓存格式/路径、外部程序兼容、Stage 4 第一动作。
- `IMPLEMENTATION_PLAN.md`：Stage 3 状态和平台验证命令。
- `docs/product/feature-matrix.md`：抽屉、预览、缓存、编辑、打开器、命令和 shell 证据。
- `docs/architecture/overview.md`：预览读取、缓存租约和编辑回传数据流。
- 缓存/编辑冲突与外部进程安全 ADR。
- `docs/verification/stage-03.md`：平台、终端、故障与安全测试结果。
- 用户文档：键位、缓存策略、冲突恢复、外部程序配置和隐私边界。

## 10. 下一阶段交接信息

Stage 4 开始前必须记录：

1. 缓存目录、元数据版本、指纹和租约的稳定契约。
2. Preview 可复用的流式结果、取消、预算和抽屉事件接口。
3. 实际验证过的终端图片协议、编辑器、打开器与 shell 组合。
4. 外部命令参数化与日志脱敏规则。
5. Stage 4 搜索结果如何进入 TUI，而不污染当前目录列表和缓存真相。
6. Helper 的 tail/watch 如何复用预览边界而不绕过缓存/资源预算。
7. ADR-0010 的 key ID/public key、custody/恢复、撤销与双 key 轮换演练证据。
8. 最后绿色命令、人工验证清单和仍保留的孤立编辑恢复限制。

Helper 增强不得改变本阶段“无 Helper 仍可预览、编辑和传输”的基线。
