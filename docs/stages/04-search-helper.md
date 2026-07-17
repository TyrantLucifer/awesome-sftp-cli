# Stage 4 — Search & Optional Helper

- **状态**：In Progress
- **阶段类型**：可选远端能力增强
- **前置条件**：[Stage 3 — Preview, Edit & Cache](03-preview-edit-cache.md) 已通过退出门禁
- **完成后进入**：[Stage 5 — Direct Transfer & Scale](05-direct-transfer-scale.md)

## 1. 阶段目标

在标准 SFTP 零部署基线上增加一个由用户明确批准、版本化、按需运行的远端 Helper。Helper 通过现有 SSH 标准输入输出提供高速遍历、内容搜索、强 hash、磁盘信息、tail/watch 和同主机复制等能力；它不监听端口、不常驻、不提权，并且任何失败都能降级回 SFTP。

同时交付 Vim-first 搜索模型：`/` 保持当前目录过滤，`f` 做递归文件查找，`g/` 做内容搜索；结果必须流式、可取消、可声明部分完成，并受时间、数量、字节和并发预算约束。

## 2. 范围

### 2.1 包含

1. Helper 的跨平台构建产物、manifest、校验和、协议版本与能力声明。
2. 用户确认的探测、安装、校验、原子激活、握手、升级、禁用和移除流程。
3. Helper 通过 SSH stdio 的单次/按需会话，不开放网络监听。
4. 三级能力模型中的 Level 0（标准 SFTP）和 Level 1（Helper 增强）；Level 2 直传留给 Stage 5。
5. `f` 递归文件查找：Helper 快路径和有界 SFTP 降级路径。
6. `g/` 内容搜索：Helper 快路径和能力受限、明确较慢的 SFTP 降级路径。
7. 强 hash、磁盘空间、快速 walk、tail/watch 与同 Endpoint 服务端复制能力。
8. Helper 缺失、崩溃、超时、被替换、版本错配和权限变化的安全降级。

### 2.2 非目标

1. 不要求任何远端安装 Helper；拒绝安装的用户仍有完整浏览、基本传输、预览和编辑能力。
2. 不以 root、sudo、系统服务、登录启动项或常驻 daemon 方式运行 Helper。
3. 不开放 TCP/UDP/Unix 网络监听，不穿透防火墙。
4. 不实现跨主机直传、凭据转发或 Kerberos 委派。
5. 不建立永久全盘索引或远端数据库；搜索按请求执行。
6. 不承诺所有 SFTP-only 服务器都能高效内容搜索；降级限制必须明示。

## 3. 能力层级与不变量

### 3.1 阶段依赖

- Stage 1–3 的标准 SFTP、守护进程会话、后台 Job、预览流、缓存预算和 TUI 抽屉保持为 Level 0 基线。
- Helper 安装和调用必须继续使用系统 OpenSSH/SFTP 与现有认证边界，不建立第二套凭据或网络入口。
- Helper 的 same-host copy、hash 和 tail/watch 必须接入既有 Planner、Job、Preview 与诊断接口，不能形成不可恢复旁路。
- Helper 分发必须实现 [ADR-0010](../architecture/adr/0010-helper-artifact-trust-and-distribution.md)。生产 key custody/恢复/轮换未满足，因此生产分发与普通运行时安装面保持 **CLOSED**；Stage 4 仍可用仅由同包测试显式注入、production verifier 永不信任的 `testdata` non-release fixture 闭合完整 Level 1 lifecycle/protocol/Planner 验证，不能把该证据表述为 production asset 或 custody 完成。

### Level 0 — 标准 SFTP

始终可用的基线：浏览、元数据、范围读取、基本文件操作、流式传输/续传、本地中继、当前目录过滤，以及受限递归文件/内容搜索。

### Level 1 — Helper 增强

握手成功后可用：快速 walk、流式内容搜索、强 hash、磁盘空间、tail/watch、同主机复制和更精确的能力/错误信息。

### Level 2 — 直传预检

需要额外网络、认证和策略条件，由 Stage 5 实现；Stage 4 只为协议扩展保留兼容空间，不激活直传。

不变量：

1. 能力来自当前 Helper 会话握手，不能仅凭文件存在推断。
2. Helper 结果必须可追溯到 Endpoint、Helper 版本、请求 ID 和搜索范围。
3. Helper 崩溃不会使 SFTP 会话失效；客户端自动标记降级并可继续基础操作。
4. Helper的fresh ssh transport固定不请求GSS delegation、不复用ControlMaster；framed protocol不传入或持久化Askpass回答/ticket/agent内容，也不主动注入无关环境。remote same-euid既有cache、root/admin/server与登录环境仍是声明的信任边界，不能绝对声称远端“无任何secret”。
5. 任何增强操作都遵守当前远端用户的文件权限和路径边界。

## 4. 详细交付物

### S4-D01 Helper 产物与 Manifest

- 为受支持远端 OS/架构构建同一 Go 程序的 Helper 角色产物；它与客户端共享代码库和版本，但远端支持矩阵单独记录。
- 每个产物的签名 distribution manifest 严格只含 ADR-0010 Manifest v1 固定字段；未知字段必须拒绝，不能把 provenance 或运行时能力临时塞入 v1。
- 构建来源由 release provenance/attestation 记录；minor 兼容范围由版本兼容表记录，实际能力只在启动后的受限握手中声明。
- 每个manifest使用ADR-0010 canonical bytes+Ed25519 detached signature；客户端只接受production内置trust set，绝不从remote响应学习hash/key。
- 客户端同时执行每个协议/平台的 release floor、精确 artifact denylist 和已安装版本单调 high-water mark；全新安装也不得重放低于 floor 的旧但签名有效产物，break-glass 不得绕过撤销/denylist/floor。
- Helper 与客户端版本不要求完全相同，但必须在协商的协议/能力范围内。
- Stage4只用`testdata` non-release Ed25519 fixture闭合parser/verifier/install/handshake与四target测试：fixture key只可由test-only constructor显式注入，production trust set永不接受；fixture private key/manifest/artifact不得进入installable binary、curated`dist/`归档或production helper asset，静态门禁扫描。repository与托管平台自动source archive可包含公开testdata fixture，不得误称production asset/secret。所有production helper assets/manifests在Stage4保持Planned/unpublishable。Stage6须先冻结Linux final unsigned bytes；Darwin须Developer ID+hardened runtime+timestamp+strict verify+notary`Accepted`并确认Accepted ZIP bytes后，受保护release job才生成/离线签manifest，再验证与final tar一致。Stage4不得给dev/pre-sign Darwin bytes生成production manifest或声称release-ready。

### S4-D02 用户批准的安装生命周期

安装有两个不同确认点。preliminary consent只展示Endpoint、manifest声明target、`shared-session-stable-home`含义、即将运行的read-only probe及可能的sshd/audit/atime/startup副作用，并明确observed uid/home/path尚未知；未确认不probe。final consent在probe与全部read-only plan完成后展示fresh observed non-root numeric uid、OS/arch/home、create目录、final/temp/mode、version/key/source/hash/floor/high-water、能力/移除与mapping/object/ACL边界；probe不解析/承诺username。plan任一字段变化必须重新确认，确认前app install tree零create/零content。

执行顺序严格服从ADR-0010：

1. current-policy parse/signature/revoke/denylist/protocol/`min_client`/floor；Stage4仅可使用test-only fixture，production asset不可发布。
2. preliminary mapping/probe consent；default unknown、node-local/per-session/异构mapping保持Level0。
3. SFTP验证root-owned `/usr/bin` utilities，再以validated absolute fresh ssh fixed probe取得non-root uid/cwd/OS/arch；PATH planted 0-hit，stdout byte0/4096与stderr65536边界。
4. cwd/RealPath只作compatibility preflight；safe-home component≤255，target match后检查Endpoint high-water。
5. 之后才local artifact expected+1/size≤128MiB/SHA-256；再派生≤1000-byte path，read-only逐级验证ancestor并列create plan。
6. final actual-plan consent；取消或变化不得创建目录/temp/content。
7. 确认后创建/复核exact0700目录，以44-byte CSPRNG basename和`O_EXCL`开temp；首byte前handle`Chmod(0600)`+handle/path attrs复核。写signed size、client expected+1回读复算，通过才chmod0700并以ADR-0016要求的OpenSSH hardlink target-exists-fails publish；扩展缺失即fail closed，不SFTP rename/posix-rename/delete-first，final重验。
8. 每次enable/exec重跑current policy→fresh binding/target/high-water→ancestor/final/hash；helper argv含GSS delegation off与ControlMaster/Path/Persist off，最后恰一restricted string。byte0 preface、stderr cap与framed handshake通过才enabled；业务path/pattern只走stdin。

升级并行安装新版本，成功后切换；旧版本在无活跃会话后清理。移除只删除本项目版本化目录内已验证文件，不递归删除模糊路径。SFTP uid/mode 依赖受信 sshd/root/admin/ACL/LSM/export policy 如实执行普通 POSIX DAC，不能证明所有 other principal 都不可访问；same-euid/root/admin/server 主动进程属于信任边界。受管环境只有第二 principal 真实拒绝测试通过后才可声明用户隔离。扩大边界必须新增稳定 file-id/handle-publish 与可验证 authorization 能力 ADR。

### S4-D03 Helper 会话与协议

- 客户端通过系统 OpenSSH 的精确**本地 argv**发送 ADR-0010 唯一受限 remote command string；明确承认服务端通常交给登录 shell `-c`，不把它误称为远端结构化 argv。通信只使用 stdio。
- remote command 只由 fixed template + 本次 fresh probe 的 strict-safe canonical absolute home + 当前重新验签的 protocol/version/os/arch/hash typed fields组成；用户路径、文件名、搜索模式和 arbitrary manifest value 永不进入 command，全部作为 framed request data。
- 首帧协商协议版本、最大帧、能力、路径/时间语义和资源限制。
- 请求、流式结果、进度、部分完成、结构化错误和取消有明确关联。
- 帧大小、结果数量、字符串长度、嵌套深度和并发请求有硬上限。
- Helper 标准错误限长并脱敏地进入诊断，不能混入协议通道。
- 无心跳、超时、无效帧或能力违约时终止 Helper 会话并降级。

### S4-D04 `f` 递归文件查找

- 搜索作用域默认是活动栏当前目录，可明确更改，启动后范围冻结。
- 支持文件名/相对路径匹配、文件类型、是否跟随符号链接、隐藏文件和忽略规则等明确选项。
- Helper 以流式 walk 返回相对路径和必要元数据；排序在流式/完整语义间明确取舍。
- 无 Helper 时由 SFTP 做有界递归，限制并发目录请求、队列大小、最大结果和时限。
- 结果抽屉可逐项跳转、选择、预览和创建 Stage 2 Operation Intent。
- 达到预算、权限边界或取消时返回 `partial_results` 与停止原因，不伪装完整。

### S4-D05 `g/` 内容搜索

- 搜索输入包含模式类型、大小写、文件过滤、作用域、二进制策略、上下文行和预算。
- Helper 优先安全调用可用的 `rg` 或内建扫描器；用户模式作为参数/数据，不拼接 shell 命令。
- 输出为文件、行/偏移、有限预览片段和匹配元数据；单行、单文件和总输出均有限制。
- 对编码错误、二进制、大文件、权限和变化中的文件给出明确状态。
- 无 Helper 时可用 SFTP 范围读取做受限慢速模式，默认预算更小，并在启动前提示成本。
- 取消从 TUI 传播到守护进程、SSH 会话和远端扫描，超时后强制结束 Helper 子进程。

### S4-D06 增强文件能力

- **Strong hash**：按协商算法返回算法名、摘要、文件指纹和计算时点；文件中途变化则结果无效。
- **Disk stats**：返回可用/总空间及语义来源；未知配额不推断为无限。
- **Tail**：按偏移持续读取新增内容，文件截断/轮转可检测并通知预览层。
- **Watch**：在平台支持时提供目录变化提示；事件可能丢失/合并，客户端仍以重新 `stat`/列目录为真相。
- **Same-host copy**：只在 Helper 明确保证路径边界和错误语义时交给 Planner；结果仍需 Stage 2 验证/提交规则。
- 每项能力独立声明和降级，不能因为 Helper 存在就假设全部支持。

### S4-D07 降级与可观察性

- UI 显示当前 Endpoint 的 Level、Helper 版本、失效原因和可恢复动作。
- 进行中的 Helper 搜索失败时保留已返回结果并标为部分；用户可用 SFTP 降级重试。
- Helper hash/复制失败回到标准读取/中继路径；已经提交的目标不盲目重做。
- 反复崩溃采用会话级熔断，避免启动循环；用户可手工重试或禁用。
- 日志记录协商能力和降级原因，不记录搜索完整内容或认证秘密。

## 5. 里程碑顺序

### M4.1 — SFTP 搜索基线

1. 定义搜索请求/结果/预算契约和 TUI 结果视图。
2. 实现 `f` 的有界 SFTP 递归和 `g/` 的受限慢速模式。
3. 覆盖取消、权限、符号链接、部分结果和大输出。

门禁：没有 Helper 时搜索功能行为诚实、可取消且不会无界占用资源。

### M4.2 — Helper 安装与握手

1. 建立 manifest、远端探测和安装计划确认。
2. 完成临时上传、校验、权限、原子激活和版本握手。
3. 覆盖升级、禁用、移除、错误架构和篡改。

门禁：Helper 永不监听/常驻/提权，任何生命周期失败均保持 Level 0 可用。

### M4.3 — Helper 搜索

1. 快速 walk 接入 `f`。
2. 安全 `rg`/内建扫描接入 `g/`。
3. 实现流式结果、取消、预算和百万节点夹具。

门禁：百万节点搜索可开始即返回、可取消、内存有界，结果完整性状态明确。

### M4.4 — 增强能力与退化闭环

1. strong hash、disk stats、tail/watch 和 same-host copy。
2. 独立能力协商、故障回退和会话熔断。
3. macOS/Linux 远端 OS/架构兼容矩阵。

门禁：逐项拔掉、崩溃或降级 Helper 能力时，基础浏览/传输仍绿色。

## 6. 可验证退出标准

- [ ] preliminary consent明确observed uid/path未知且取消时0 probe；final consent展示fresh numeric uid/actual plan且不伪称username，取消时0 app-tree create/content，任一manifest/probe/attrs变化强制重新probe/确认。
- [ ] Stage4四target只用testdata non-release fixture；production verifier拒绝fixture key，installable binary/curated dist/production helper assets不含fixture材料（自动source archive除外），production manifests保持Planned直到Stage6 final bytes/sign-notary链。
- [ ] Helper manifest 的 Ed25519 签名、key ID、key/artifact 撤销、OS/arch/protocol/`min_client`、canonical 数值版本、fresh-install 旧签名重放、release floor、high-water mark、同版本同/异 hash 和降级策略通过规范/负向/轮换测试；创建 temp 前拒绝分支保持 application-managed install tree 零创建/零内容写入，不把 sshd/shell审计副作用误写成整个远端零写。
- [ ] Helper 经当前-policy Ed25519/撤销/denylist/floor/high-water、128 MiB/expected+1、显式 shared-session-stable-home policy、absolute root-owned utilities、non-root uid+SFTP/exec namespace compatibility、safe absolute home/component 与1000-byte路径上限、无可写祖先、exclusive-open 后首字节前 `Chmod(0600)`/双复核、上传前后 SHA-256、OpenSSH hardlink target-exists-fails publication、每次 exec raw metadata 重验与受限 command-string 后才启用；扩展缺失时fail closed，不覆盖 final、不递归/模糊删除、不执行校验失败对象，也不误报 node/mount/object 或 ACL/same-euid/root/server 隔离保证。
- [ ] validated absolute local ssh path、OpenSSH 8.9 shell `-c`、最后恰一 remote-command arg、uname 四个支持映射/未知值、poisoned PATH 0-hit、safe absolute home 与 component/path 255/256/1000/1001 边界、profile `cd /tmp`、SessionType/RemoteCommand、ForceCommand/custom shell/`sshrc` banner、SFTP `-d`/chroot、双节点同 path 异 bytes、stdout byte0 preface、stderr 65536/65537 和 client-upgrade revoke/floor 负向矩阵全部安全降级 Level 0。
- [ ] Helper不监听/常驻/提权；fresh transport不请求GSS delegation、不复用ControlMaster，protocol/日志/DB不传入或持久化Askpass/ticket/agent内容；报告明确remote same-euid既有cache/root/server是环境边界。
- [ ] 缺失、错误架构、被篡改、版本错配、协议畸形、超时和崩溃均安全降级 Level 0。
- [ ] `f` 和 `g/` 都能流式显示、取消、限制范围与预算，并标识部分结果。
- [ ] 百万节点夹具搜索不全量载入内存，取消延迟和资源占用符合阶段记录的预算。
- [ ] strong hash 能检测计算期间变化，disk stats 不伪造未知配额。
- [ ] tail/watch 的丢事件与轮转行为不会被当成强一致真相。
- [ ] same-host copy 仍服从 Planner、冲突、验证和提交不变量。
- [ ] Helper 崩溃不终止现有 SFTP 会话或无关后台 Job。

## 7. 测试矩阵

| 层级 | 场景 | 必须证明 | 证据 |
|---|---|---|---|
| 单元 | 搜索模式、预算、结果合并 | 边界、取消、部分状态确定 | 单元/模糊测试 |
| 协议 | 握手、未知帧、超大帧、取消 | 兼容且抗畸形输入 | 协议契约测试 |
| 生命周期 | 安装、升级、禁用、移除 | 校验、原子切换、路径边界 | 临时远端测试 |
| 安全 | 篡改、错误权限、命令参数 | 不执行未验证产物，无注入 | 负向测试 |
| 兼容 | 旧/新客户端与 Helper | 能力按交集启用，错配降级 | 版本矩阵 |
| 搜索 | 百万节点、长行、二进制、权限 | 有界、流式、结果状态真实 | 规模夹具 |
| 故障 | Helper kill、SSH 断线、无响应 | SFTP 保持，搜索可降级 | 故障注入 |
| 能力 | hash/tail/watch/copy | 变化、轮转、冲突语义正确 | 能力集成测试 |
| 平台 | 远端 macOS/Linux、架构矩阵 | 正确探测与选择产物 | 平台记录 |
| Race | 搜索取消、Helper 重启、会话切换 | 无泄漏、死锁或结果串流 | race/长稳测试 |

## 8. 失败与回滚策略

- 安装任一步失败时删除已知临时文件并保留当前工作版本；无法确认的文件列入诊断，不递归清理。
- 新 Helper 握手失败不切换 active 版本；已在运行的旧会话继续到自然结束。
- 客户端升级后与旧 Helper 不兼容时使用 Level 0，并提示安装兼容版本；不强制远端变更。
- 搜索失败保留已收结果并标 `partial_results`；降级重试创建新请求，不把两次结果冒充同一完整快照。
- Helper 反复崩溃后会话级禁用；重新连接前不自动循环安装或启动。
- helper 目录或 manifest 损坏时只禁用 Helper，不影响 SFTP Provider 和用户远端文件。
- 回滚客户端前保留多个版本化 Helper；旧客户端只使用其协商兼容版本，不能执行未知版本。

## 9. 完成时必须更新的文档

- `PROJECT_STATE.md`：Helper/协议版本、远端支持矩阵、已知降级、Stage 5 第一动作。
- `IMPLEMENTATION_PLAN.md`：Stage 4 状态和规模/故障验证命令。
- `docs/product/feature-matrix.md`：搜索、Helper 生命周期与各增强能力证据。
- `docs/architecture/overview.md`：Level 0/1 能力与 Helper stdio 数据流。
- Helper 安全边界、协议兼容和安装策略 ADR。
- `docs/verification/stage-04.md`：版本、平台、篡改、崩溃、百万节点搜索证据。
- 用户文档：为何可选、安装计划、权限、禁用/移除、搜索预算和降级提示。

## 10. 下一阶段交接信息

Stage 5 开始前必须记录：

1. 当前 Helper manifest、协议范围、能力名称和版本兼容表。
2. 安装路径边界、活跃版本切换和清理规则。
3. strong hash、disk stats、same-host copy 的准确保证和错误语义。
4. 百万节点搜索的 CPU、内存、首结果和取消基线。
5. Level 2 直传握手允许扩展的字段，但尚未启用的安全边界。
6. Helper 故障熔断与 Level 0 降级的实际行为。
7. 最后绿色命令、远端平台缺口和仍需人工验证项。

Stage 5 只能在现有安全边界上增加直传预检，不能把 Agent 转发、Kerberos 委派或密钥复制设为默认前提。
