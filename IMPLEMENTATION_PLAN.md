# Implementation Plan

本计划是项目的阶段索引。它只描述阶段目标、可验证完成条件与测试入口；详细范围、里程碑、失败处理和交接要求见 `docs/stages/`。阶段必须按顺序通过退出门禁，不以“代码已写完”代替行为、测试与文档证据。

Stage 0–4 已完成；各阶段均由完整本地门禁、exact-SHA Hosted evidence、文档真相链和独立冷启动审计闭环。Stage 5 已在固定分支 `codex/stage5-direct-transfer-scale` 从 verified exact-main `06415e1e9fe5ffa93999f112b64aee0bd35e5c75` 开始；Stage 6 保持 Not Started。

## Stage 0: Foundation & Knowledge

**Goal**: 建立可在 macOS 与 Linux 构建、测试和持续续接的 Go 工程基线，冻结跨阶段共享的协议、端点与文档契约。

**Success Criteria**: 两个平台均可构建；版本化 IPC 信封、Endpoint 能力契约和可控 Fake Provider 有契约测试；公开身份、精确依赖、TUI/日志、平台路径、SQLite/迁移、支持矩阵/包装和 Helper 信任机制均由 Accepted ADR 冻结；Vision、Architecture、Feature Matrix、ADR、Stage、Verification、Project State 的真相链与更新规则可执行。

**Tests**: `make check`、`make lint`、`make supply-chain`、`make ci`；Go 1.25.12 oldstable；最低/当前 macOS/Linux 原生 CI 与四目标构建；协议兼容与 Fake Provider 契约测试；ADR 决策的版本/安全/可测试性审阅；文档链接、功能矩阵字段和严格阶段顺序检查；完整候选树污染检测与 cold-start audit。

**Status**: Complete

**Current checkpoint**: 本地实现、独立审查、两轮 cold-start audit 与最终本地 closeout 均已完成。首轮 Hosted run `29394164471` 暴露并固定了 GNU Make 4.x 安全 `-I` 路径误判。修复提交 `cf8e6efd2814d835f8c1f5c2739608477b5216ed`、树 `e70a8f0c5fc57817f6fa44dda31faaf4652b67c5` 的替代 Hosted run [`29394698864`](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29394698864) 随后 23/23 作业通过：四个 native、四个 oldstable、quality、四目标 build、八个独立缓存 reproducibility producer、compare 与最终 provenance aggregation 均为绿色。Stage 0 的 CORE-001–008 已有可追溯完成证据；其后续状态见下方阶段条目。

## Stage 1: Read-only Explorer

**Goal**: 交付可日常验证的只读双栏浏览器：本地或任意 SSH 远端可放在任一栏，并复用系统 OpenSSH 的 Kerberos、代理与 SSH 配置能力。

**Success Criteria**: 本地/本地、本地/远端、远端/远端均可浏览；守护进程自动启动并通过私有 Unix Socket 服务 TUI；SSH 主机选择、工作区恢复、Vim 导航和有界文本预览可用；认证交互、断线和重连不会卡死或泄露凭据。

**Tests**: 单元与 Provider 契约测试；ADR-0007 路径的 XDG/TMPDIR/override/symlink/owner/mode、Darwin系统别名、integrity-only与owner-private deny/allow-read/write/inherited ACL、Linux access/default ACL、sticky `/tmp` 回退和竞态；ADR-0001 默认`/usr/bin/ssh`/安全absolute override完整链、poisoned PATH、special-bits/ACL/替换与精确argv/冲突配置；临时sshd、ProxyCommand、MIT Kerberos/GSSAPI、重启/断网/认证等待和macOS/Linux TUI冒烟。

**Status**: Complete

### M1.1: 本地只读端到端

**Goal**: 完成 `tcell/v3 v3.4.0` intake、ADR-0007 平台安全边界、daemon/IPC/LocalFS 接入，以及完全离线可用的本地/本地双栏 TUI。

**Success Criteria**: daemon 自动启动并收敛为当前用户单实例；私有 `control-v1.sock`、完整 ancestor/ACL 验证和双向 peer UID 检查 fail closed；LocalFS 通过共享 Provider contract；双栏支持窗口化列表、Vim 导航、过滤、选择和有界基础预览；反复退出重入后资源回到稳定基线。

**Tests**: 精确依赖许可证/module graph/漏洞/Go 1.25.12+1.26.5/四目标 intake；路径、ACL、lock、peer UID 与 stale socket 单元/平台测试；daemon 并发启动、握手、重连、取消和泄漏集成测试；LocalFS contract；纯 reducer/renderer、`vt.NewMockTerm`、50k 结构性基准和离线 PTY smoke。

**Milestone Status**: Complete

**Current checkpoint**: 提交 `8e649f534b500e494ec2984a763e4491711df5fe` 的本地双 Go/四目标/完整门禁、focused race、50k benchmark 与 darwin/arm64 离线 PTY smoke 均通过；Hosted run [29399674061](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29399674061) 的 native、oldstable、quality、build、reproducibility、compare 与 provenance 全部绿色。

### M1.2: 真实 SFTP Endpoint

**Goal**: 完成最初的 `pkg/sftp v1.13.10` intake，并仅通过 ADR-0001 的 validated absolute system OpenSSH stdio 实现 SFTP Provider 与双远端浏览；最终依赖边界由 ADR-0011 修订为 upstream v1.13.11 加 immutable cursor fork。

**Success Criteria**: 默认 `/usr/bin/ssh` 且 PATH fake 0-hit；host alias 与精确 argv 无注入面；stderr 脱敏限长，取消/退出回收子进程；本地/远端和远端/远端可独立浏览并传播 partial/degraded 状态。

**Tests**: 同等级依赖 intake；binary 完整链/ACL/special-bits/替换、逐参数 argv 与冲突 ssh_config；真实临时 sshd、Host alias、非默认端口、两个隔离 sshd、断线与 Provider contract。

**Milestone Status**: Complete

**Current checkpoint**: 提交 `28f8731604201763e48bf43c5a7f7e2a7014ca6c` 的精确 pkg/sftp intake、validated `/usr/bin/ssh`、进程组回收、SFTP contract、双远端路由与错误映射均通过本地双 Go/race/四目标门禁；Hosted run [29401801663](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29401801663) 的真实双 sshd、poisoned PATH 0-hit、冲突 ssh_config、非默认端口、断线隔离及完整 CI/provenance 矩阵全绿。

### M1.3: 认证与复杂 SSH 配置

**Goal**: 实现单次消费的 askpass/Auth Broker，并证明系统 OpenSSH 的 ProxyCommand/ProxyJump、Agent、密钥、交互认证和 Kerberos/GSSAPI 路径可用且不泄密。

**Success Criteria**: challenge 唯一关联 endpoint/connection/client；附着、无客户端、超时、取消和多提示均有界恢复；认证失败不无限重试；秘密不进入日志、配置、工作区、缓存或测试产物。

**Tests**: Broker 状态机/race/秘密扫描；代理与双提示 MFA；agent/key/password；MIT Kerberos 临时 realm 的 GSSAPI-only 成功、ticket 缺失/过期/外部续期恢复；host-key 语义不降级。

**Milestone Status**: Complete

**Current checkpoint**: 提交 `7f0ea00981cecd5799b3c17ee56eff204cfd5a90` 的 daemon-global 单次消费 Auth Broker、owner/attach/detach/timeout/cancel/多提示边界、同二进制 askpass、TUI 提示与安全失败阶段均通过聚焦 race/vet；Hosted run [29408865534](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29408865534) 的 auth job [87330882913](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29408865534/job/87330882913) 真实通过 key、agent、ProxyCommand、ProxyJump、password、错误密码、取消、双提示 MFA、首次 host-key 确认、变化 host-key fail-closed，以及 MIT Kerberos/GSSAPI 有效、缺失、过期和外部续票恢复矩阵与秘密扫描。

### M1.4: 工作区与恢复

**Goal**: 完成 CLI Location、SSH Host 模糊选择、双栏工作区保存恢复，以及 SSH/daemon/能力/位置变化后的可预测恢复。

**Success Criteria**: `amsftp` 的零/一/二 Location 与 `--workspace` 入口可用；工作区原子、无秘密且损坏可恢复；三种 Endpoint 组合和所有恢复路径有证据；macOS/Linux 的窄终端、resize、信号和退出重入通过。

**Tests**: Location/Host parser 与 Include/通配 fixture；workspace round-trip/中断/损坏/秘密扫描；断网、sshd/daemon 重启、能力变化和失效目录；macOS/Linux PTY、SIGWINCH、窄终端、退出重入及完整 Stage 1 gate。

**Milestone Status**: Complete

**Current checkpoint**: 最终实现提交 `90cbfea81bd2d802bd3f7579a0b192c81ba3281b`、树 `53c7b1ac62e809b7046ea366701a21e6dc0bf757` 已通过完整本地 current/oldstable 门禁；[Hosted run 29467496969](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29467496969) 24/24 作业全绿。真实 auth job 同时通过三种 Endpoint 组合、workspace 重入、sshd/daemon 重启、失效目录 nearest-parent 恢复、OpenSSH 认证矩阵和 MIT Kerberos/GSSAPI 四用例；ADR-0011 packet-bounded SFTP cursor、四平台 kernel ACL/lock/hostile-UID 与 PTY resize/退出重入证据均闭环。Stage 1/M1.4 完成，Stage 2 保持 Not Started。

## Stage 2: Durable Transfers

**Goal**: 在持久化后台作业模型上交付崩溃安全的复制、移动和基础文件变更，覆盖本地、单远端和双远端中继路径。

**Success Criteria**: `y`/`d`/`p` 语义、冲突处理、临时目标、验证后提交、暂停/取消/续传和守护进程重启恢复可用；跨端点移动在验证完成前绝不删除源；`kill -9`、断网、短写和目标磁盘故障不会留下被误认成完整文件的目标。

**Tests**: 作业状态机与幂等性；SQLite filesystem/probe、final-absent intent bootstrap、wrong DB零目录写与sidecar recovery、checksum+SQL lexer、per-head whole-schema/exact runner tables、无/单/多pending original..target attempt、backup sanitize/restore hold/非ready显式resume、catalog retention/space、Darwin source/hidden-destination fullsync顺序、no-replace及清attempt→retention→checkpoint→immutable，2秒reader与4/8/264MiB online WAL预算；四类传输、kill/断网/短写/权限/磁盘满、内存与恢复安全。

**Status**: Complete

### M2.1: 持久化状态机骨架

**Goal**: 完成 ADR-0008 持久状态底座、Version 1 schema、Job/step 状态机、事务事件流与确定性 daemon restart recovery。

**Success Criteria**: APFS/ext4/XFS 与 WAL/full-durability probe、identity/bootstrap、migration/backup/retention/WAL 全部 fail closed；Job/step/plan/checkpoint/conflict/event 事务一致，非法转换拒绝；持久层故障保留 Stage 1 只读诊断。

**Tests**: exact modernc intake；checksum/lexer/schema contract；filesystem/probe/identity/bootstrap/sidecar/attempt/backup/hold/retention/space/WAL；Fake Job 状态机、单 writer、busy/disk-full/corrupt/newer-schema 与 crash injection。

**Milestone Status**: Complete

**Current checkpoint**: same-binary cross-process WAL/locking/full-sync probing, bounded WAL enforcement, integrated multi-version upgrade coordination, and process-death recovery at seven bootstrap plus five migration transaction boundaries pass locally, including focused race. Hosted run [29475259444](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29475259444) is green on exact SHA `1ec9097448d0ec40d32f0a87aeeb822e5651d381`; both Linux native legs of `3a8ec31d6a7f7afdaf7f6aa1a44e546cfc2145f6` passed explicit ext4 identity and loop-mounted XFS execution of the complete persistent-state suite. Exact SHA `f83aa45de9b83f42d6f64944401ddde0e1e92d01` then passed both Linux native legs in [run 29476167115](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29476167115), including real XFS `ENOSPC` rollback and clean restart. Corrupt/newer/read-only daemon degradation, foreign-file/parent metadata preservation, and deterministic recovery for every nonterminal Job state pass locally. M2.1 is complete.

### M2.2: 单文件复制、冲突与提交

**Goal**: 交付冻结的 Operation Intent/Plan，以及本地↔本地、本地↔远端双向单文件 copy 的安全 part、验证、提交、控制和冲突处理。

**Success Criteria**: `y`/`d`/`p` 不受后续 UI 状态影响；所有 mutation 经 Planner→Job→Worker；最终目标只在验证后出现；客户端/daemon 重启可恢复；提供真实本地与临时 sshd MVP。

**Tests**: Planner/intent 表驱动；LocalFS/SFTP mutation contract；真实 sshd 双向 copy；短写/断网/final race/kill-9；pause/resume/cancel/retry；IPC event replay、secret scan 与有界内存。

**Milestone Status**: Complete

**Current checkpoint**: Exact SHA `811ce6b90364446612721ba7cb809a284d633521` passed both complete Hosted runs `29482708033` and `29482709588`, closing real PTY/sshd, fault, recovery, auth, native/oldstable, race and provenance gates.

### M2.3: 目录复制与双远端中继

**Goal**: 流式发现目录，使用硬预算队列与缓冲执行同远端及远端 A↔B 复制，并持久解释每项结果。

**Success Criteria**: 目录树不全量入内存；队列、深度、并发和缓冲有固定上限；双远端通过两个独立 session 背压中继且不落完整本地文件；冲突与部分结果可解释。

**Tests**: 大树 synthetic queue、100GB sparse、双临时 sshd、单边断线、慢读/慢写、取消、restart、race 与峰值资源记录。

**Milestone Status**: Complete

**Current checkpoint**: Exact SHA `eb4f152f305812f30e7573a690e570e8ca41b96b` passed both complete Hosted runs `29484442378` and `29484446997`, closing directory/dual-remote, resource, retry, native, auth and provenance gates.

### M2.4: Move、rename、删除与恢复闭环

**Goal**: 交付同 Endpoint rename 快路径、跨 Endpoint 安全 move、显式 `D` 删除、能力感知 trash 与完整 Jobs 恢复视图。

**Success Criteria**: move 只在目标 commit 和 source revalidation 后删除源；不确定时 `completed_with_source_retained`；`d`/`D` 不混淆；危险范围、陈旧 FileRef、确认与 repeat 边界全部 fail closed。

**Tests**: rename/delete 后置条件、source-changed、commit/delete response lost、partial directory move、trash/no-trash、危险路径、repeat/count、无客户端等待、crash/fault matrix 与真实 macOS/Linux PTY。

**Milestone Status**: Complete

**Current checkpoint**: implementation commits `cf10e2031ff4929b5b8bc6882aad473445841f7d` and `29592921b24039a568677e4974541d9656c8f952`, followed by deterministic Hosted fixture repairs through exact SHA `54b0285d7278d58e67c35a280fa8b996a99a321d`, deliver frozen source-delete capability, atomic-rename gating/postconditions, copy-delete source retention, bounded directory source deletion, explicit/trash delete, symlink no-follow, unknown-response proof, commit→delete restart recovery, multi/directory clipboard, count/repeat reconfirmation, `D` two-stage confirmation, `r`, IPC routes and local PTY copy/move/rename/delete/reattach. Complete local current/oldstable gates pass, and exact implementation SHA `54b0285d7278d58e67c35a280fa8b996a99a321d` passed complete Hosted push run `29488697276` (attempt 2) and PR run `29488700235`. M2.4 and Stage 2 are complete.

## Stage 3: Preview, Edit & Cache

**Goal**: 补齐 Vim-first 日常工作流：预览、任务/日志抽屉、外部编辑与打开、缓存租约、单次命令和临时 shell。

**Success Criteria**: 文本/代码/元数据及受支持终端图片预览可用；编辑或本机打开远端文件时可检测本地与远端并发变化；上传必须显式确认且不静默覆盖；缓存配额、租约、清理和异常退出恢复正确；`!` 与 `gs` 有清晰的本地/远端上下文。

**Tests**: 预览截断与流式读取测试；缓存配额/租约/逐出测试；编辑上传并发冲突测试；异常退出恢复测试；macOS/Linux 编辑器、默认打开器和 shell 冒烟测试；命令参数与路径安全测试。

**Status**: Complete

### M3.1: 抽屉与缓存基础

**Goal**: 冻结 Preview/Jobs/Log 抽屉与 Preview generation 接口，并交付 owner-only 内容缓存、引用、配额、LRU、租约和崩溃重建基础。

**Success Criteria**: `K/J/L` 的焦点、切换、窄屏和 resize 行为可解释；缓存按强身份内容寻址且 Location freshness 独立；pinned、dirty、活跃 lease、共享引用和 Stage 2 part 均不会被错误逐出；索引损坏只重建/诊断，不删除未知内容。

**Tests**: reducer/layout/snapshot；manual-clock quota/LRU/lease；跨 workspace reference/dedup；owner/mode/ACL/no-follow；materialization/index kill-9、ENOSPC、all-protected、rebuild、race 与 secret scan。

**Milestone Status**: Complete

**Current checkpoint**: K/J/L、workspace v2、owner-only content-v1 filesystem、SQLite Version 2/3 catalog、serialized live quota、LRU/reference/lease、safe clear、immediate bounded dead-Preview reclaim、exact pinned-offline unknown freshness 和 fail-closed reconciliation 全部通过 normal/race/native gate。catalog 不可从信息不足的 manifest 不安全重建。

### M3.2: 内建、图片与外部预览

**Goal**: 交付有界文本、代码、JSON、元数据、二进制、range/head/tail、终端图片和外部预览器降级链。

**Success Criteria**: 读取、解析、解码、像素、渲染和输出均有硬预算；快速导航不会串片；100 GiB fixture 的资源只随预算增长；失败仅影响 Preview drawer。

**Tests**: golden/fuzz；恶意 ANSI/UTF-8/JSON/image；range/read 断言；100 GiB sparse/synthetic；generation/cancel；Kitty/iTerm2/Sixel/none snapshots；previewer argv/timeout/crash；race/resource curve。

**Milestone Status**: Complete

**Current checkpoint**: bounded text/code/JSON/metadata/binary/head/tail/range、generation/cancel、100 GiB fixture、Kitty/iTerm2/Sixel/none、真实 Kitty 0.47.4 与 bounded external previewer 降级链均完成；directory/symlink/weak metadata 以完整 identity 进入 reducer 且 0 content read/materialization。

### M3.3: 编辑与默认打开

**Goal**: 交付 `e`/`o` 的受限解析、absolute direct exec、cache lease、终端挂起恢复、变化矩阵、冲突和 Stage 2 Job 回传。

**Success Criteria**: 无变化不上传，仅本地变化需确认，仅远端变化提示刷新，双方变化或无法判断进入冲突；上传只经 Planner→Job→part→verify→commit；失败/重启保留 dirty materialization；立即返回 opener 不过早释放 lease且不自动上传。

**Tests**: lexer/argv/PATH/path injection；fake/real Vim/Neovim；macOS open/Ubuntu xdg-open；local/remote change matrix；四类 conflict；upload/restart/crash；PTY restore；race/secret scan。

**Milestone Status**: Complete

**Current checkpoint**: restricted editor/opener direct exec、完整 materialization/lease、Vim/Neovim/open/xdg-open、变化/冲突/恢复矩阵和 atomic edit-session→Stage 2 Job 均完成。expected-present sync-back 先 no-replace 保存 remote original，再 bounded 验证并 no-replace 发布 part；所有 error/event 显示实际 preservation effect，避免 silent overwrite。

### M3.4: 命令、shell 与平台收尾

**Goal**: 交付 `!` local/remote、`gs` local/remote、完整 TTY ownership/recovery，以及 Stage 4 Helper trust handoff。

**Success Criteria**: remote `!` 严格 fresh argv、byte-safe cwd、byte-0 marker-before-input、32 KiB command 与双 1 MiB ring；remote `gs` home/current-cwd probe、`-tt`、显式 fallback；所有退出恢复 termios/raw/alternate/cursor/pgrp/SIGWINCH；完成 ADR-0010 public key/custody/revoke/rotation 门禁但不实现 Helper。

**Tests**: exact argv/config conflict；cwd/Q/marker/banner/custom shell；dual-stream/cancel/detach；PTY/pgrp/SIGWINCH；background Job continuation；macOS/Linux native/oldstable；Helper key tabletop/fixture；full race。

**Milestone Status**: Complete

**Current checkpoint**: `!` local/remote single-flight、Esc cancel、15-minute timeout、marker/Q/32 KiB/dual 1 MiB rings 与 `gs` local/remote current/home/`-tt`/TTY ownership 全部通过 unit、race、real PTY/temporary-sshd 和 Hosted native gate。post-spawn cleanup 2 秒恢复上限且只允许一个 pending worker；真实 Helper custody 未建立，因此 production Helper distribution 按允许分支保持 **CLOSED**。

## Stage 4: Search & Optional Helper

**Goal**: 在零部署 SFTP 基线之上引入可选择、可降级、无常驻监听的远端 Helper，提供高速遍历、内容搜索、强校验和远端增强能力。

**Success Criteria**: Helper 安装、校验、握手、升级与移除流程安全可审计；`f`/`g/` 搜索可流式返回、取消并受资源预算约束；Helper 缺失、崩溃或版本不兼容时自动降级且不破坏 SFTP 会话；增强能力只在明确协商后启用。

**Tests**: Helper lifecycle/protocol；current-policy manifest/Ed25519/revoke/compat/floor/high-water；preliminary取消=0 probe、final取消=0 app-tree create/content、drift重probe/确认；Stage4 testdata fixture-only、production trust拒fixture且installable binary/curated dist无fixture assets（自动source archive除外）；shared/unknown/双节点mapping、fresh ssh GSS-delegation+CM off、root-owned utilities/PATH、uid0/cwd/RealPath/uname；safe-home/path/temp/ancestor、128MiB/expected+1/O_EXCL/首write前Chmod0600、client回读、no-replace；shell-c/唯一command/banner/chroot/byte0/stderr边界。失败Level0且不外推node/object/ACL/same-euid/root/server；百万节点/取消/预算与远端矩阵。

**Status**: Complete

### M4.1: SFTP 搜索基线

**Goal**: 冻结搜索 request/result/event/cancel/generation/budget 契约，并在标准 Provider 上交付无 Helper、无 probe、无 install 的 `f` 有界递归文件名搜索与 `g/` 受限慢速内容搜索。

**Success Criteria**: 搜索绑定 Endpoint/session/generation/request/scope/options/budget；结果分页流式返回并可跳转、预览、选择和生成现有 Operation Intent；取消、权限、symlink、超时以及深度/队列/结果/字节上限都返回诚实的 partial/stop reason；迟到 generation 被丢弃；百万节点 fixture 不全量进入内存。

**Tests**: 先写 RED 契约测试；Provider/Fake/LocalFS/SFTP contract、TUI reducer/render、temporary-sshd `f`/`g/`、权限/symlink/binary/encoding/long-line、generation/cancel/partial、百万节点首结果/取消/peak RSS/goroutine/process/FD/read-list 计数。

**Milestone Status**: Complete

**Current checkpoint**: Provider-only filename/content contracts, daemon cursors, TUI `f`/`g/`, explicit slow-content confirmation, cancellation/generation isolation, real temporary-sshd coverage, and a one-million-entry streaming fixture are green. The measured synthetic fixture returns the first result in about 65 µs, uses one 128-entry list page, adds about 6 MiB RSS, and records bounded goroutine/FD counts without retaining the full tree.

### M4.2: Helper 安装与握手

**Goal**: 在 production distribution 保持 CLOSED 的前提下，以显式 test-only non-release fixture 实现 Manifest v1、current-policy、双 consent、安全 probe/install/publish、每次执行 freshness 与受限握手。

**Success Criteria**: production verifier 拒绝 fixture key；preliminary 取消为 0 probe、final 取消为 0 app-tree create/content；safe-home/path/attrs、expected+1、exclusive temp、首 byte 前 chmod/stat、client readback、standard no-replace、current-policy/floor/high-water/revoke 与每次 exec 重验均 fail closed；任何失败保留 Level 0。

**Tests**: manifest/signature golden/fuzz/compat；两阶段 consent/drift；OpenSSH 8.9 shell `-c`、fresh argv、poisoned PATH、uid/cwd/uname、safe-home 255/256 与 path 1000/1001、stderr 65536/65537、tamper/replay/upgrade/disable/remove、fixture isolation 与 production artifact pollution scans。

**Milestone Status**: Complete

**Current checkpoint**: Canonical raw manifest/signature and enabled/high-water state persist atomically in owner-private bounded files; installation has two consent boundaries, current policy, fresh absolute-utility binding, SFTP attrs/readback/exclusive upload, hardlink target-exists-fails publication, exact enable-time revalidation, disable/remove, and protocol/version/capability handshake. Fresh OpenSSH processes use isolated process groups, GSS delegation/ControlMaster off, byte-zero preface, bounded stderr and automatic termination on heartbeat/hard-deadline failure. Exact removal is serialized against Helper-capable planning and rejects non-terminal exact artifact references. The complete hostile OpenSSH/native/pollution/Hosted matrix passed at `199e1012530b4d0112d0bbb1eef175c761db1567`. Production runtime exposes no fixture install source, production trust is empty, and raw-MKDIR packet implementation/distribution remain **CLOSED**.

### M4.3: Helper 搜索

**Goal**: 实现版本化、有界、可取消的 framed stdio helper protocol，将 fast walk 与安全 content search 接入 M4.1 的统一搜索契约/UI。

**Success Criteria**: handshake/capability/request/result/progress/partial/error/cancel/heartbeat 全部有硬上限；用户 path/pattern 只走 framed stdin；helper 按需运行且无 listener/daemon/提权；崩溃/超时/畸形协议保留已返回结果并允许新 request 降级 Level 0。

**Tests**: protocol golden/fuzz/compat/capability violation、cancel/timeout/invalid frame；million-node streaming/取消/资源证据；long line/binary/encoding/large-file/permission/mid-search change；process/port/service/secret scans 与 Helper failure 后 Stage 1–3 regression。

**Milestone Status**: Complete

**Current checkpoint**: The strict framed protocol, independent capability negotiation, concurrent request correlation, cancellation, hard resource limits, structured completion/errors, heartbeat, built-in filename/content scanner, and daemon Level 1 routing are verified. Rejected request IDs cannot be reused; terminal/error payloads fit inside the shared client/server budget. Negotiated Helper results retain the exact M4.1 identity and a closed Helper routes a new request through Level 0 while Provider snapshot remains available. The million-node Helper fixture and real SSH crash/hang/native resource gates passed in the accepted local and Hosted matrices.

### M4.4: 增强能力与退化闭环

**Goal**: 交付独立协商的 strong hash、disk stats、tail/watch、same-host copy，以及逐能力降级、会话熔断和可观察性。

**Success Criteria**: hash 中途变化失效；disk quota unknown 不推断无限；tail/watch 明示 truncate/rotate/lost/coalesced 并以 stat/list 为真相；same-host copy 仍经 Planner/Job/conflict/verify/commit/restart；反复崩溃不形成启动/安装循环。

**Tests**: capability removal table；hash mutation、quota unknown、tail truncate/rotate、watch lost/coalesced、same-host copy conflict/verify/restart；kill/crash/hang/version mismatch 后 browsing/search/transfer/Preview/Edit/Job 继续绿色；完整平台、资源、安全和 cold-start 门禁。

**Milestone Status**: Complete

**Current checkpoint**: Strong-hash mutation, statfs quota-unknown, tail truncate/rotation, watch loss/coalescing, independent capability removal, dynamic Helper status and same-host copy are verified. The durable `helper_same_host` Plan freezes Endpoint, artifact, source identity and capabilities; Job Store admission/removal coordination prevents exact-artifact races; the existing Worker owns checksum, conflict, commit, restart adoption, cancellation and Job state. Full-platform/fault/resource/security/cold-start gates and exact-SHA push [29557909197](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29557909197) and PR [29557911211](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29557911211) runs passed 24/24 jobs each at commit `199e1012530b4d0112d0bbb1eef175c761db1567`, tree `48e5ebddf136d2c59a144fc5f82dd14dd60e24dc`.

## Stage 5: Direct Transfer & Scale

**Goal**: 增加经安全预检的同端点快路径与跨主机直传，并将目录、树和超大文件处理提升到既定规模目标，同时保留语义一致的本地中继兜底。

**Success Criteria**: 路由计划可解释且冻结；直传不默认转发 Agent、委派 Kerberos 或复制密钥；预检失败或提交前故障可安全降级中继；直传与中继的冲突、校验、取消和结果语义一致；5 万项目录、百万节点树及 100GB 稀疏文件场景保持有界内存。

**Tests**: 路由决策表与能力组合测试；直传/中继等价性测试；认证、磁盘、网络和中途失败矩阵；50k 目录、百万树、100GB 稀疏文件基准；并发/限速测试；长时间运行、race 与资源泄漏测试。

**Status**: In Progress

### M5.1: 路由统一与同 Endpoint 快路径

**Goal**: 把 atomic rename、标准 bounded relay、声明式 SFTP server copy、Stage 4 `helper_same_host` 和 Level 2 direct 候选纳入同一个可解释、可持久、可恢复的 Planner/Plan 证据模型。

**Success Criteria**: Job 创建前冻结 route/evidence version、选中路线、备选拒绝原因、能力版本、完整性策略、风险、part/final、预检和降级边界；执行器不静默改路；所有快路径仍只写 Job-owned part，并复用现有 conflict、verify、commit、restart 和 source-delete 后置条件；关闭快路径时与 Stage 2–4 byte/state 等价。

**Tests**: 首个 RED 共享 route contract 覆盖同 Endpoint atomic rename、同 Endpoint relay/server-copy 候选、`helper_same_host`、跨 Endpoint bounded relay 和 production-closed Level 2 拒绝；route decision table、Plan JSON round-trip/restart、Worker route regression、共享 conflict/cancel/verify/commit/source-retention 套件，以及 Jobs/Log/TUI evidence snapshot。

**Milestone Status**: Complete

**Current checkpoint**: 首个 RED 共享 route contract 已按要求覆盖五类路线并转绿。Plan 持久化 v1 selected/candidate/reason/integrity/downgrade/risk/progress evidence，执行前反篡改校验；目录子 Plan 重新冻结自己的 part/final evidence；durable JobView、`job_created`/runtime downgrade Log 与 Jobs drawer 显示计划和真实路线。声明式 SFTP server-copy 仅在同一 SSH Endpoint、普通文件、显式 `server_copy` capability 与结构化 facet 同时存在时选中，冻结 capability revision/1 TiB hard ceiling，只写 Job-owned part，并由既有 Worker 独立计算 source/part SHA-256 后执行原有 conflict/commit。写前失败仅在 exact part 被证明不存在时持久降级 relay；未知 part 状态、context cancel/deadline 不降级；restart 复用已持久的真实路线且不重试 server-copy。共享 conflict/cancel/verify/commit 契约、执行中 cancel、响应丢失、损坏、能力/绑定/checkpoint 篡改与 complete route/Job/Log/TUI regressions 均绿色；CI-equivalent `make docs-check`、`make check`、`make lint` 和 focused race 通过。M5.1 退出门禁完成，M5.2 可以从版本化 Level 2 protocol/preflight RED 开始。

### M5.2: Level 2 预检与跨主机直传

**Goal**: 通过显式 non-release `testdata` fixture 交付版本化、有界、daemon-owned 的 Level 2 控制面和隔离双远端数据面，同时让普通 runtime 保持 production distribution CLOSED。

**Success Criteria**: protocol/capability/network/address/write/temp/space/quota/auth/host-key/user/workspace/data/hash 每项都必须通过；目标先写唯一 part，目标 durable acknowledgement 才计进度；strong hash/fingerprint 证明来源未变；无 Agent forwarding、GSS delegation、secret/key/ticket/known_hosts 复制或宽松 host-key；普通 runtime 稳定记录 `production_distribution_closed` 并走 relay。

**Tests**: bounded protocol/frame/deadline/heartbeat/cancel contract；逐项 unknown/fail 预检矩阵及零 direct mutation 断言；固定 OpenSSH argv/config 与 secret/pollution 扫描；真实隔离 test-only 双远端 direct，证明数据面直达、daemon 控制面可审计且本地不承载完整内容。

**Milestone Status**: In Progress

**Current checkpoint**: direct protocol v1 已冻结 request/Job/Endpoint/path/target-alias/source identity、14 项 ordered preflight、1 MiB/4-request/10-minute/heartbeat/cancel/progress/result limits；逐项 fail/unknown 均 0 direct stage 并 relay。仅同包 `_test.go` 可注入的 data facet 已证明 source→target staging、target durable checkpoint、remote part/final strong hash、shared Worker commit、daemon 0 Provider content read、expiry fresh preflight、in-flight cancel、lost-response exact restart adoption 与 absent-part safe relay downgrade。普通 `NewPlanner`/`NewWorker` 无注入入口，production 继续 `production_distribution_closed`。M5.2 尚需真实隔离双 sshd 与 native auth/host-key evidence 后关闭。

### M5.3: 降级、故障与语义等价

**Goal**: 在 direct 与所有快路径的每个安全边界注入故障，证明降级、结果恢复和精确清理不改变冻结保证。

**Success Criteria**: 写入前可安全 relay；已有 part 仅在 exact Job/source/target/checkpoint/hash/format 兼容时复用；commit response loss 先检查 final/part/strong fingerprint；未知结果不盲重放；move 不早删源；清理只作用于 exact verified Job-owned path；direct 与 relay 的 bytes、冲突、取消、事件、restart、integrity 和 source retention 等价。

**Tests**: 网络/认证/空间/能力/source/target 变化、short/corrupt write、Helper crash/hang、daemon restart、cancel、checkpoint mismatch、commit response loss 和 source-delete uncertainty 矩阵；random/sparse/large direct-relay golden equivalence；浏览/Search/Preview/Edit/Cache 与无关 Job 可用性回归。

**Milestone Status**: Not Started

### M5.4: 规模、资源预算与公平调度

**Goal**: 将 50k 目录、百万树和 100GB 稀疏文件纳入可重复门禁，并统一连接、进程、FD、goroutine、内存、队列、事件、日志、带宽和公平调度预算。

**Success Criteria**: UI/IPC/数据库不全量物化大目录或树；遍历、transfer、hash 和 restart 的内存不随总节点/字节线性增长；全局/Endpoint/Job 配额、idle recovery、deterministic token bucket 和交互优先公平性可解释且有硬上限；大结果/事件/日志分页或截断并保留摘要；长稳无单调资源增长。

**Tests**: 50,000-entry 首屏/滚动/过滤/render/RSS fixture；1,000,000-node browse/search/plan/copy/cancel/restart 资源曲线；100GB sparse local/SFTP relay/test-only direct pause/resume/restart/cancel/hash/limit；多 Endpoint/多大小 Job 的 fairness/backpressure/idle recovery；race、soak、benchmark 环境与趋势记录。

**Milestone Status**: Not Started

## Stage 6: Hardening & 1.0 Release

**Goal**: 完成配置与键位、安装包、迁移、兼容、安全、诊断和发布工程，使 macOS/Linux 1.0 可安装、可升级、可支持。

**Success Criteria**: 全功能矩阵均有明确状态与证据；干净安装、受支持版本升级和回滚路径验证通过；配置/协议/数据库兼容策略落地；安全评审与权限边界无未处置高风险项；发行物、man page、补全、诊断包和运维文档完整。

**Tests**: 全量unit/integration/contract/race/fuzz；macOS/Linux安装升级、配置/DB/protocol/security；darwin两架构Developer ID/runtime/timestamp/strict verify/notary Accepted与ZIP↔final tar byte identity，pre-sign/pre-Accepted production manifest拒绝、Accepted后才offline签manifest；linux manifest绑定final unsigned bytes；四平台manifest size/hash↔final tar binary，并交叉验证checksums/SBOM/attestation同一archives；quarantine macOS15 Gatekeeper/version、发行冒烟与长稳。

**Status**: Not Started
