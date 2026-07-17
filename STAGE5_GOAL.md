# Stage 5 Goal Objective — Direct Transfer & Scale

在仓库 `TyrantLucifer/awsome-sftp-cli` 的固定分支 `codex/stage5-direct-transfer-scale` 上完整交付 Stage 5 — Direct Transfer & Scale。

这是终局目标，不是只写设计、计划、benchmark 草图、单一路由原型或局部性能优化。持续实施、测试、审查、修复、提交和推送，直到 M5.1–M5.4 全部达到退出标准，22 个 Stage 5 Feature Matrix 行有可审计的 exact PASS 证据，并形成 Ready PR。不要自动合并 PR。

Stage 5 必须完整保留 Stage 1–4 的系统 OpenSSH 认证、标准 SFTP Level 0、双栏 TUI、durable Job、冲突/覆盖/取消/恢复、Preview/Edit/Cache、搜索与可选 Helper 的 fail-closed 语义。所有快路径只能改变数据路线和性能，不能创建第二套 mutation、commit、source-delete 或恢复语义。

生产 Helper 分发和生产 Level 2 直传继续 **CLOSED**：真实 production private-key custody、双钥轮换、最终 macOS 签名/公证 bytes 和 release manifest 尚未完成。Stage 5 可以使用显式注入的 non-release `testdata` fixture 实现、验证 Level 2 协议与真实隔离环境中的端到端路线，但普通 runtime/config 不得启用 fixture trust、fixture backend、production Helper 下载/安装或声称 production direct transfer 已开放。该门禁只能由 Stage 6 的真实发布与 custody 证据解除。

## 1. 工作目录、基线与固定交付面

- 工作目录：仓库根目录（以远程开发机上的实际绝对路径为准）
- 仓库：`TyrantLucifer/awsome-sftp-cli`
- Stage 4 PR：`https://github.com/TyrantLucifer/awsome-sftp-cli/pull/4`
- Stage 4 最终分支提交：`488d4166d40a2520600871050cbbaae0c3ffc055`
- Stage 4 final tree：`3bbf1a64dae964e92a6bcc65a06d87bc0f004a65`
- Stage 4 exact-head push CI：`https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29558466408`
- Stage 4 exact-head PR CI：`https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29558469031`（attempt 2 完整绿色）
- Stage 4 合入 `main` 的 merge commit：`e77da118da6f12b341fcccbb8b9046020dc69c05`
- 合并树：`3bbf1a64dae964e92a6bcc65a06d87bc0f004a65`
- 合并后 exact-main CI：`https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29559132294`（首次执行仅在原生 PTY race fixture 的已知时序波动处失败；Stage 4 相同最终 tree 的 exact-head push/PR CI 已完整绿色。按用户决定，不为该不稳定项修改代码；Stage 5 会话应记录该基线事实，并仅在 Stage 5 相关失败可稳定复现时修复）
- Stage 5 分支固定为：`codex/stage5-direct-transfer-scale`
- Stage 5 PR 标题固定为：`feat: deliver the Stage 5 direct transfer and scale`

开始前必须：

1. `git fetch --prune origin`，确认本地 `main`、`origin/main` 和远端 `refs/heads/main` 精确为 merge commit `e77da118da6f12b341fcccbb8b9046020dc69c05`，tree 为 `3bbf1a64dae964e92a6bcc65a06d87bc0f004a65`，工作树干净。
2. 核对 exact-main Hosted run `29559132294` 的实际结果与上述已知 PTY race 波动；不得把该已知不稳定项误归因于 Stage 5，也不得为了制造绿色基线而修改无关代码。Stage 4 exact-head push/PR 的绿色 run 保留为相同 tree 的基线证据。
3. 确认 `codex/stage5-direct-transfer-scale` 本地、远端和 PR 均不存在，然后从干净 exact-main 基线创建并切换一次；不在 `main` 直接开发。
4. 若固定分支实际已存在，先检查 HEAD、tracking、工作树、远端和 PR；不删除、重建、reset、rebase、强推或覆盖未知改动。
5. 运行基线 `make docs-check`、`make check` 并与 exact-main Hosted 结果核对；失败先诊断，不把已有失败归因于 Stage 5。
6. 保留 Stage 4 PR、merge commit、CI 和验证历史，不 squash/rebase/改写已绑定证据的提交。

若实际状态不一致，先诊断并恢复到安全、可解释状态；禁止 destructive reset、`--no-verify`、绕过测试或覆盖用户文件。

## 2. 开始前严格阅读

按顺序完整阅读并遵循：

1. 当前会话提供的 `AGENTS.md` / repository instructions
2. `docs/README.md`
3. `PROJECT_STATE.md`
4. `IMPLEMENTATION_PLAN.md` 的 Stage 5
5. `docs/product/feature-matrix.md` 中全部 22 个 `Stage = 5` 行：`CONN-010`、`JOB-009`、`DIRECT-002`、`DIRECT-004`–`DIRECT-010`、`SCALE-001`–`SCALE-003`、`SCALE-005`–`SCALE-012`、`SEC-011`；同时审计 Stage 5 共同拥有的跨阶段 `In Progress` 行
6. `docs/stages/05-direct-transfer-scale.md`
7. `docs/verification/stage-04.md`，作为 Search/Helper/Level 0–1 和 same-host copy 的已完成交接
8. `docs/architecture/overview.md`
9. ADR-0001：系统 OpenSSH、认证/host-key/ProxyCommand/GSSAPI、fresh transport 与 remote-command 边界
10. ADR-0002：client/daemon/session/Job 所有权
11. ADR-0004：崩溃安全传输、part、验证、commit 和 source retention
12. ADR-0005：Provider、能力、错误与 IPC 契约
13. ADR-0007/0008/0009：私有路径、SQLite/WAL/migration、平台/发布基线
14. ADR-0010：Helper trust/distribution、每次 freshness、production custody CLOSED
15. ADR-0011：bounded streaming cursor / SFTP intake
16. ADR-0012：durable Job/event/restart state machine
17. ADR-0013：唯一 mutation path 与 operation postconditions
18. ADR-0014/0015：cache/Preview/Edit/external process/TTY 资源和恢复边界
19. ADR-0016：Stage 4 search/helper runtime contracts、raw-MKDIR 与 no-replace CLOSED 边界
20. `docs/security/helper-trust-handoff.md`
21. `docs/testing/strategy.md`、`docs/development/testing.md`、`docs/security/dependency-policy.md`
22. 已批准产品设计中 direct transfer、routing、scale、resource budget 与 Stage 5 章节

不要把 `docs/superpowers/` 当作运行依赖，也不要恢复已删除的项目级 Superpowers skill；它只是历史持久设计文档路径。

## 3. 唯一起手式：先完成 M5.1 路由统一

第一个实现动作必须是写 RED 共享路由契约测试，把所有现有路线纳入一个可解释、可持久、可恢复的 Planner/Plan 证据模型：

1. 同 Endpoint 原子 rename；
2. 同 Endpoint SFTP relay / server copy 候选；
3. Stage 4 `helper_same_host`；
4. 不同 Endpoint 的有界本地 relay；
5. 尚未可用的 Level 2 direct 候选及其拒绝原因。

关闭所有快路径时，测试必须证明行为 byte-for-byte/状态等价于已验证 Stage 2–4 路径。路线、能力版本、完整性级别、预检证据、临时目标、降级条件和风险必须在 Job 创建前冻结；执行器不得根据运行时便利静默换路线或放宽保证。

在 M5.1 的共享 route contract、已有路线回归、durable restart 和 UI/Job 可解释证据绿色之前，不得开始 M5.2 跨主机数据面。

## 4. 终局交付目标

### M5.1 — 路由统一与同 Endpoint 快路径

- 建立唯一 Planner route model 和稳定 route/reason/integrity/error codes，复用现有 Operation Intent、Plan、Job、Worker、conflict、checkpoint、verify、commit 与 source-delete postconditions。
- 同 Endpoint 原子 rename 只在显式能力和双后置条件可证明时选择；跨挂载/能力消失/响应丢失先检查后置条件再回退。
- 支持声明语义可靠的 SFTP `copy-data`/服务端复制候选；不得根据 server banner/品牌猜测扩展语义，不得把 replace/partial/unknown response 当成功。
- 服务端/Helper copy 只写标准 Job-owned `.part-<JobID>` 或同一冻结临时目标；最终发布仍由现有 Worker 的验证、冲突和 no-replace/conditional commit 负责。
- 快路径失败只能在目标仍可证明未提交时降级；既有 part 的复用必须验证 Job、来源、目标、offset/size/hash 和格式兼容。
- Jobs/Log/TUI 展示选中路线、未选路线首要原因、完整性等级、可降级边界和真实进度语义；服务端 copy 无可靠 byte progress 时不得伪造平滑进度。

### M5.2 — Level 2 预检与跨主机直传

- 扩展版本化、有界的 Helper/control protocol，冻结 direct capability、request/Job correlation、deadline、heartbeat、frame/result/concurrency 和取消语义；不得增加通用 shell RPC 或用户可控 remote command string。
- 直传只有在源/目标协议与能力、网络、目标写权限/临时提交、空间/配额、强校验、用户/工作区策略、host-key policy 和源主机已有非交互认证全部通过时才可选。
- 目标地址/host alias 必须来自受信 Endpoint/配置，不得来自文件内容、显示名、搜索结果或未经验证的 Helper payload。
- 不自动开启 Agent forwarding、Kerberos/GSS credential delegation、ControlMaster 复用、密码转交、私钥/ticket/known_hosts 复制或宽松 host-key 策略；预检不得弹出/代理新秘密。
- 数据面可以远端到远端，但本地 daemon 永远是 Job/控制面所有者。目标先创建唯一临时文件，源端只读；目标确认的 durable checkpoint 才能计入进度。
- 传输完成后比较计划要求的 strong hash、size 和 fingerprint，来源中途变化使结果无效；目标 commit 成功前绝不删除源。
- production Helper distribution/Level 2 开关继续 CLOSED。普通 runtime 必须显示 `production_distribution_closed` 或等价稳定原因并安全中继；只有显式 test-only fixture constructor 可执行 Level 2 集成路径。

### M5.3 — 降级、故障与语义等价

- 逐阶段注入预检后网络/认证/空间/能力变化、源变化、目标冲突、短写/损坏、Helper crash/hang、daemon restart、cancel、commit response loss 和 source-delete uncertainty。
- 预检失败必须 0 direct temp/write；写入前失败可直接中继；已有 direct part 只有在 exact checkpoint/identity/format 兼容时才可验证后复用，否则隔离或精确清理后从零中继。
- 提交响应丢失必须检查 final/part/强指纹，分类 success/conflict/needs-attention；禁止盲目重跑 commit。
- 降级不得改变用户冻结的完整性、覆盖、带宽、网络边界或认证策略；无法维持保证时进入明确等待/失败，不静默降级。
- direct 与 relay 必须通过同一共享契约，证明最终 bytes、冲突决策、取消、move source retention、事件、restart 和验证证据等价。
- 清理只针对 exact Job-owned verified path，不 glob、不递归、不删除漂移或无法证明的对象。

### M5.4 — 规模、资源预算与公平调度

- 50,000 项目录：首屏/可视窗口/有限预取/增量过滤与排序有界；不创建全量 UI 节点，不在单帧/单消息/单事务携带全部条目。
- 1,000,000 节点树：browse/search/plan/transfer 使用分页、有界队列和必要时 daemon-private disk-backed frontier；可暂停、取消、restart，不全量常驻内存。
- 100GB 稀疏文件：local/SFTP relay/test-only direct 支持持续进度、暂停、取消、verified checkpoint restart 和 strong hash；内存、buffer 和事件数量不随文件大小线性增长。
- 建立全局、Endpoint、Job 的连接/SSH/helper/FD/goroutine/内存并发预算；同 Endpoint 受控复用必须保持 auth/session 隔离并有 idle 回收。
- 实现确定性、可热应用于新调度的 token-bucket/等价限速和公平调度。大树/大文件不得永久饿死 Preview、Search、小 Job 或控制消息。
- 快路径无法精确限速时必须在 Plan 中声明，并按策略禁用或经明确接受；不能假装已限速。
- 大结果/事件/诊断分页、采样、轮转或截断，保留摘要和 truncation 标记，不让日志本身成为无界负载。
- 记录固定 benchmark 环境、命令、首结果/完成/取消延迟、吞吐、peak RSS/CPU、goroutine、FD、SSH/helper process、queue depth 和已知瓶颈；比较趋势而非宣称跨硬件绝对数字。

## 5. 必须冻结的安全与正确性边界

1. 所有路线共享唯一 mutation path；不得添加 direct-only raw write/rename/delete/source-delete API。
2. 标准 SFTP relay 永久可用；任何 fast/direct/Helper 缺失、关闭、未知、失败或 production CLOSED 都必须回到已验证 relay，而不是阻塞普通传输。
3. route、integrity、credential/network policy、capability version、source/target identity、temp/final path 和 downgrade boundary 在 durable Job 中冻结。
4. direct 数据面不能扩大认证权限：无 Agent forwarding、无 GSS delegation、无 secret/ticket/key/answer 复制、无 `StrictHostKeyChecking=no` 或临时宽松 known_hosts。
5. remote outbound SSH 仍由系统 OpenSSH/受信配置决定；不得自实现 SSH/Kerberos/host-key 验证或把 Helper 变成凭据代理。
6. 用户路径、文件名、目标地址和选项只走有界 framed data；remote command string 仍只允许 ADR-0016 的 fixed template + fresh validated typed fields。
7. Helper/直传不监听、不常驻、不提权、不安装服务；每个进程有正式 process-group cleanup、heartbeat 和 hard deadline。
8. source delete 永远晚于已证明的 target commit 和 source revalidation；任何不确定保留源并产生稳定 reason。
9. 所有 frame、queue、page、batch、transaction、buffer、event、log、retry、connection、process、FD、goroutine、time 和 byte 数量有不可被配置关闭的硬上限。
10. production Helper/Level 2 distribution 保持 **CLOSED**；不得伪造 private key、custodian、offline station、rotation/notary/release artifact 或 production success 证据。

## 6. 完整性等级与用户可见契约

至少冻结三种稳定、可解释的完整性策略：

- `baseline`：长度、I/O 完成和可用元数据；明确不能抵抗全部静默损坏。
- `strong`：来源和目标均有协商的强 hash，且 fingerprint 证明 hash 期间来源未变化。
- `require_strong`：缺少兼容 hash 或 freshness 证据时拒绝该路线或改走能满足强校验的 relay，绝不静默降低。

算法、digest、计算时点、source/target fingerprint、route 和 outcome 必须写入 Job 证据。权限、时间、链接等 metadata policy 与内容 hash 分开报告，不能用 hash 冒充 metadata 保留。

## 7. 持久计划与文档真相链

Stage 5 是大型高风险功能，必须在第一个实现提交前：

1. 扩展 `IMPLEMENTATION_PLAN.md`，将 Stage 5 标记 In Progress，并按 M5.1–M5.4 写 Goal/Success Criteria/Tests/Status；Stage 6 保持 Not Started。
2. 创建 `docs/verification/stage-05.md`，从 exact-main baseline 记录 commit/tree/CI、命令、route/fault/security/scale/resource 数据和每次失败/修复。
3. 更新 `PROJECT_STATE.md`：当前 M5.x、冻结 route/integrity/protocol/budget、最后绿色命令、production CLOSED、风险和唯一下一动作。
4. 22 个 Stage 5 Feature Matrix 行只随真实实现从 Planned/In Progress→Implemented→Verified；每个 Verified 行必须链接 `docs/verification/stage-05.md` 中 exact ID + 独立 PASS cell。
5. 新增 Accepted ADR，冻结统一 route/Level 2 认证与控制面、integrity/downgrade、scheduler/resource budgets；不修改已 Accepted ADR 来隐式重写历史。
6. M5.1 首个可审查 MVP 推送后尽早创建 Draft PR，并持续更新 exact SHA/tree、route table、测试、资源、安全、production CLOSED 和 pending gates。
7. 聊天、临时目录、Agent 内存和未上传日志不能是唯一证据。

## 8. 开发、审查与失败方法

- 新行为和 bugfix 先写 RED 行为/契约测试；复用 Provider、Planner、Job、Worker、IPC、Helper protocol、drawer、OpenSSH 和 testkit 模式。
- 非平凡变更先寻找 2–3 个相似实现；优先最小、显式、可逆变更，不引入通用分布式框架、同步引擎、shell DSL、第二套 scheduler 或未经证明依赖。
- 若需要 schema migration，严格遵守 ADR-0008 的 checksum、backup、WAL、crash boundary、old-binary 和 exact Hosted native 证据；没有持久需求不要升级 schema。
- 任何新依赖必须精确版本并审查 license/retraction/changelog/module graph/`go.sum`/govulncheck/双工具链/四目标/原生平台；优先标准库和现有依赖。
- 每个资源契约同时需要单元硬上限、synthetic scale fixture 和实测资源证据；不得用无限预算或只增加 timeout 修复规模失败。
- 快路径/直传故障测试必须同时断言标准 relay、浏览、Search、Preview/Edit/Cache 和无关 Job 保持可用。
- 同一问题最多 3 次具体修复+验证尝试；仍失败则停止修改，记录 exact 命令/错误/假设/替代方案并请求方向。
- 最终 closeout 前必须启动至少两个独立子代理：一名做代码/安全/认证/恢复审查，一名只从 `docs/README.md` 冷启动验收。所有 High/Medium 必须修复并重新审查。

## 9. 最终必须完成的验证

在同一个干净 exact final candidate 上至少完成并记录：

1. `make docs-check`
2. `make check`
3. `make lint`
4. `make supply-chain`
5. `BUILD_DIR=/tmp/... COVERAGE_DIR=/tmp/... make ci`
6. `go test -race ./... -count=1`
7. exact oldstable：`GOTOOLCHAIN=go1.25.12 BUILD_DIR=/tmp/... COVERAGE_DIR=/tmp/... make check`
8. root 与 `tools/` 的 `go mod tidy -diff` / `go mod verify`
9. darwin/linux × arm64/amd64 builds 与两份独立 reproducibility/provenance comparison
10. macOS 15 Intel/ARM、Ubuntu 22.04/24.04 current/oldstable native，APFS/ext4/XFS、OpenSSH/ProxyCommand/Kerberos、Stage 1–4 PTY/auth/recovery 回归
11. unified route decision table：全部 capability/policy/integrity/Endpoint 组合、稳定 route/reason codes、Plan persistence/restart、UI/Job evidence
12. 同 Endpoint atomic rename、SFTP copy-data、Helper same-host、relay 的共享 conflict/cancel/verify/commit/source-retention contract；扩展谎报、partial、response loss 和 capability removal
13. Level 2 每项预检单独 unknown/fail：protocol/capability/network/address/write/temp/space/quota/auth/host-key/user/workspace/data policy/hash；失败 0 direct temp/write 且 relay 可用
14. 真实隔离双远端 test-only direct：数据面直达、控制面可审计，证明本地不承载完整内容；无 Agent/GSS delegation/secret/key/ticket/known_hosts copy
15. direct fault matrix：预检后变化、disconnect、short/corrupt write、ENOSPC/EACCES、Helper/daemon kill、cancel、checkpoint mismatch、commit response loss、source change/delete uncertainty
16. direct vs relay golden equivalence：随机/稀疏/大文件、冲突、overwrite、move、restart、cancel、strong/require-strong 与 metadata policy
17. 50k directory：首屏、滚动、过滤/排序、取消、render count、IPC/page/batch、peak RSS/CPU/goroutine/FD
18. million-node tree：browse/search/plan/copy/cancel/restart、disk-backed frontier（若使用）、peak RSS/CPU/queue/DB/WAL/event bounds
19. 100GB sparse：local/SFTP relay/test-only direct、pause/resume/restart/cancel/hash/limit，证明 buffer/RSS/event/checkpoint 与总 bytes 无线性增长
20. 多 Endpoint/多大小 Job 的 connection/FD/process/goroutine/内存上限、idle recovery、deterministic token bucket、动态限速、公平/背压与交互优先级
21. soak/long-run：累计 bytes/entries/jobs 不导致句柄、goroutine、process、subscription、cache、log 或 temp 单调增长
22. production closure/secret/pollution/tree/archive：普通 runtime 无 fixture trust/backend、production Level 2 disabled，无 private key/manifest/artifact/cache DB/blob/materialization/coverage/build/binary/temp 进入候选树
23. 独立代码/安全/认证/恢复审查和独立 cold-start audit；无未解决 High/Medium
24. exact final SHA 的 push 和 PR Hosted CI 完整绿色，然后才将 PR 从 Draft 改为 Ready

任何旧 checkpoint 或局部 benchmark 的绿色不能替代包含最终代码与文档的 exact final SHA。不得降低、skip、删除测试或绕过 hooks。

## 10. 最终文档与交付

完成时必须：

- `IMPLEMENTATION_PLAN.md`：Stage 5/M5.1–M5.4 Complete；Stage 6 Not Started。
- `PROJECT_STATE.md`：Stage 5 完成、exact final SHA/tree/Hosted runs、route/protocol/integrity versions、禁用开关、默认 budgets、50k/1M/100GB 数字、平台缺口、production Helper/Level 2 **CLOSED**、Stage 6 唯一第一动作。
- `docs/product/feature-matrix.md`：22 个 Stage 5 行全部有 exact PASS 证据；跨阶段行不冒充 Stage 6 完成。
- `docs/architecture/overview.md` 和 Accepted ADR：route planner、control/data plane、preflight、credential boundary、integrity、downgrade、scheduler/resource budget。
- `docs/verification/stage-05.md`：本地/Hosted/native/oldstable/route/fault/security/scale/resource/soak/cold-start 完整证据和失败修复历史。
- 用户文档：何时选 fast/direct、为何降级、如何禁用、完整性等级、认证前提、限速/进度限制、production CLOSED。
- PR body：exact SHA/tree、M5.1–M5.4、22 行、commands/platforms、route table、fault/security、50k/1M/100GB、资源/soak、production CLOSED、known limitations、push/PR CI URL。

最终工作树必须干净，本地 HEAD 与远端 Stage 5 分支一致，PR 标题精确、base=`main`、head=`codex/stage5-direct-transfer-scale`、状态 Ready、mergeable，exact final push/PR Hosted CI 绿色。不合并 PR，等待用户后续指令。

## 11. 成功定义

Stage 5 只在以下条件同时成立时完成：

1. 所有现有和新增路线进入唯一 durable Planner/Job/Worker 语义，关闭快路径时 Stage 2–4 行为完全不变。
2. 同 Endpoint copy-data/Helper 路线只在显式可靠能力下选择，失败/响应丢失不覆盖、不伪成功、不早删源。
3. Level 2 只有全部网络/空间/权限/认证/host-key/policy/hash 条件为真才执行；不转发/复制/委派凭据，不放宽 host-key。
4. direct 在提交前可安全降级或明确等待，提交不确定先查后置条件；direct/relay 的 bytes、冲突、取消、move、restart 和完整性证据等价。
5. 50k 目录、百万树和 100GB 稀疏文件保持流式、可取消、可恢复，内存/句柄/goroutine/process/event/log 由硬预算界定而非随总规模线性增长。
6. 连接复用、并发、带宽、背压和公平调度有确定性测试与长稳证据，交互操作有有界服务机会。
7. production Helper/Level 2 distribution 仍明确 **CLOSED**，普通 runtime 不可触达 fixture-only direct，标准 relay 永久可用。
8. 完整本地、双工具链、四平台/native/auth/reproducibility、fault/security/secret/pollution、独立审查和 cold-start 全绿。
9. 22 个 Stage 5 行、Stage spec、Verification、Architecture、User Guide、Project State 和 PR 对同一 exact final SHA 陈述一致。
10. exact final push/PR Hosted CI 全绿，PR Ready、clean、synchronized、unmerged。

不得因时间、token、工作量、真实 100GB/双远端环境复杂或 production custody 未开放而降低退出标准。production custody 未开放的正确结果是“完整交付 fixture-only Level 2、统一路由、故障/规模/资源证据与永久 relay 降级，同时 production distribution 明确 CLOSED”，不是伪造发布证据或跳过安全门禁。
