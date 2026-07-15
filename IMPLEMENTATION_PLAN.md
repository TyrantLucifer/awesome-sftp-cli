# Implementation Plan

本计划是项目的阶段索引。它只描述阶段目标、可验证完成条件与测试入口；详细范围、里程碑、失败处理和交接要求见 `docs/stages/`。阶段必须按顺序通过退出门禁，不以“代码已写完”代替行为、测试与文档证据。

Stage 0 正在实施；Stage 1–6 保持 Not Started，且在 Stage 0 全部门禁完成前不得开启。

## Stage 0: Foundation & Knowledge

**Goal**: 建立可在 macOS 与 Linux 构建、测试和持续续接的 Go 工程基线，冻结跨阶段共享的协议、端点与文档契约。

**Success Criteria**: 两个平台均可构建；版本化 IPC 信封、Endpoint 能力契约和可控 Fake Provider 有契约测试；公开身份、精确依赖、TUI/日志、平台路径、SQLite/迁移、支持矩阵/包装和 Helper 信任机制均由 Accepted ADR 冻结；Vision、Architecture、Feature Matrix、ADR、Stage、Verification、Project State 的真相链与更新规则可执行。

**Tests**: `make check`、`make lint`、`make supply-chain`、`make ci`；Go 1.25.12 oldstable；最低/当前 macOS/Linux 原生 CI 与四目标构建；协议兼容与 Fake Provider 契约测试；ADR 决策的版本/安全/可测试性审阅；文档链接、功能矩阵字段和严格阶段顺序检查；完整候选树污染检测与 cold-start audit。

**Status**: In Progress

**Current checkpoint**: 本地实现、独立审查、两轮 cold-start audit 与最终本地 closeout 均已完成。首轮 Hosted implementation-candidate run `29394164471` 绑定提交 `1da7254…`/树 `5880f05…`；四个 native、两个 macOS oldstable、四目标 build 和独立缓存 reproducibility compare 通过，但 `quality` 与两个 Ubuntu oldstable 因 GNU Make 4.x 安全 `-I` 路径误判而失败，该提交已作废。合成 GNU Make 4.x 表示的回归测试先红后绿，最小分类修复已通过本地 Go 1.26.5 `make ci` 与精确 Go 1.25.12 `make check`。替代 implementation-candidate Hosted run 与其后的 evidence-only final run 尚未完成，Stage 0 保持 In Progress，Stage 1 保持关闭。

## Stage 1: Read-only Explorer

**Goal**: 交付可日常验证的只读双栏浏览器：本地或任意 SSH 远端可放在任一栏，并复用系统 OpenSSH 的 Kerberos、代理与 SSH 配置能力。

**Success Criteria**: 本地/本地、本地/远端、远端/远端均可浏览；守护进程自动启动并通过私有 Unix Socket 服务 TUI；SSH 主机选择、工作区恢复、Vim 导航和有界文本预览可用；认证交互、断线和重连不会卡死或泄露凭据。

**Tests**: 单元与 Provider 契约测试；ADR-0007 路径的 XDG/TMPDIR/override/symlink/owner/mode、Darwin系统别名、integrity-only与owner-private deny/allow-read/write/inherited ACL、Linux access/default ACL、sticky `/tmp` 回退和竞态；ADR-0001 默认`/usr/bin/ssh`/安全absolute override完整链、poisoned PATH、special-bits/ACL/替换与精确argv/冲突配置；临时sshd、ProxyCommand、MIT Kerberos/GSSAPI、重启/断网/认证等待和macOS/Linux TUI冒烟。

**Status**: Not Started

## Stage 2: Durable Transfers

**Goal**: 在持久化后台作业模型上交付崩溃安全的复制、移动和基础文件变更，覆盖本地、单远端和双远端中继路径。

**Success Criteria**: `y`/`d`/`p` 语义、冲突处理、临时目标、验证后提交、暂停/取消/续传和守护进程重启恢复可用；跨端点移动在验证完成前绝不删除源；`kill -9`、断网、短写和目标磁盘故障不会留下被误认成完整文件的目标。

**Tests**: 作业状态机与幂等性；SQLite filesystem/probe、final-absent intent bootstrap、wrong DB零目录写与sidecar recovery、checksum+SQL lexer、per-head whole-schema/exact runner tables、无/单/多pending original..target attempt、backup sanitize/restore hold/非ready显式resume、catalog retention/space、Darwin source/hidden-destination fullsync顺序、no-replace及清attempt→retention→checkpoint→immutable，2秒reader与4/8/264MiB online WAL预算；四类传输、kill/断网/短写/权限/磁盘满、内存与恢复安全。

**Status**: Not Started

## Stage 3: Preview, Edit & Cache

**Goal**: 补齐 Vim-first 日常工作流：预览、任务/日志抽屉、外部编辑与打开、缓存租约、单次命令和临时 shell。

**Success Criteria**: 文本/代码/元数据及受支持终端图片预览可用；编辑或本机打开远端文件时可检测本地与远端并发变化；上传必须显式确认且不静默覆盖；缓存配额、租约、清理和异常退出恢复正确；`!` 与 `gs` 有清晰的本地/远端上下文。

**Tests**: 预览截断与流式读取测试；缓存配额/租约/逐出测试；编辑上传并发冲突测试；异常退出恢复测试；macOS/Linux 编辑器、默认打开器和 shell 冒烟测试；命令参数与路径安全测试。

**Status**: Not Started

## Stage 4: Search & Optional Helper

**Goal**: 在零部署 SFTP 基线之上引入可选择、可降级、无常驻监听的远端 Helper，提供高速遍历、内容搜索、强校验和远端增强能力。

**Success Criteria**: Helper 安装、校验、握手、升级与移除流程安全可审计；`f`/`g/` 搜索可流式返回、取消并受资源预算约束；Helper 缺失、崩溃或版本不兼容时自动降级且不破坏 SFTP 会话；增强能力只在明确协商后启用。

**Tests**: Helper lifecycle/protocol；current-policy manifest/Ed25519/revoke/compat/floor/high-water；preliminary取消=0 probe、final取消=0 app-tree create/content、drift重probe/确认；Stage4 testdata fixture-only、production trust拒fixture且installable binary/curated dist无fixture assets（自动source archive除外）；shared/unknown/双节点mapping、fresh ssh GSS-delegation+CM off、root-owned utilities/PATH、uid0/cwd/RealPath/uname；safe-home/path/temp/ancestor、128MiB/expected+1/O_EXCL/首write前Chmod0600、client回读、no-replace；shell-c/唯一command/banner/chroot/byte0/stderr边界。失败Level0且不外推node/object/ACL/same-euid/root/server；百万节点/取消/预算与远端矩阵。

**Status**: Not Started

## Stage 5: Direct Transfer & Scale

**Goal**: 增加经安全预检的同端点快路径与跨主机直传，并将目录、树和超大文件处理提升到既定规模目标，同时保留语义一致的本地中继兜底。

**Success Criteria**: 路由计划可解释且冻结；直传不默认转发 Agent、委派 Kerberos 或复制密钥；预检失败或提交前故障可安全降级中继；直传与中继的冲突、校验、取消和结果语义一致；5 万项目录、百万节点树及 100GB 稀疏文件场景保持有界内存。

**Tests**: 路由决策表与能力组合测试；直传/中继等价性测试；认证、磁盘、网络和中途失败矩阵；50k 目录、百万树、100GB 稀疏文件基准；并发/限速测试；长时间运行、race 与资源泄漏测试。

**Status**: Not Started

## Stage 6: Hardening & 1.0 Release

**Goal**: 完成配置与键位、安装包、迁移、兼容、安全、诊断和发布工程，使 macOS/Linux 1.0 可安装、可升级、可支持。

**Success Criteria**: 全功能矩阵均有明确状态与证据；干净安装、受支持版本升级和回滚路径验证通过；配置/协议/数据库兼容策略落地；安全评审与权限边界无未处置高风险项；发行物、man page、补全、诊断包和运维文档完整。

**Tests**: 全量unit/integration/contract/race/fuzz；macOS/Linux安装升级、配置/DB/protocol/security；darwin两架构Developer ID/runtime/timestamp/strict verify/notary Accepted与ZIP↔final tar byte identity，pre-sign/pre-Accepted production manifest拒绝、Accepted后才offline签manifest；linux manifest绑定final unsigned bytes；四平台manifest size/hash↔final tar binary，并交叉验证checksums/SBOM/attestation同一archives；quarantine macOS15 Gatekeeper/version、发行冒烟与长稳。

**Status**: Not Started
