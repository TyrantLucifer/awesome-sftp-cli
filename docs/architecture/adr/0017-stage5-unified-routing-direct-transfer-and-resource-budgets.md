# ADR-0017: Stage 5 统一路由、直传与资源预算契约

- **Status**: Accepted
- **Date**: 2026-07-17
- **Decision owners**: Stage 5 implementation

## Context

Stage 2–4 已分别交付 durable relay、同 Endpoint atomic rename 和 `helper_same_host` part staging，但它们的路线证据仍分散在 `Route`、`MoveStrategy` 和可选 binding 中。Stage 5 要加入声明式 server copy 与 test-only Level 2 direct，同时保持一个 Planner、一个 durable Job、一个 verify/commit/source-delete/restart 语义。若执行器在运行时按便利改路、快路径直接发布 final、或直传取得新的认证权限，已有崩溃安全和 OpenSSH 信任边界会失效。

生产 Helper custody、双钥轮换、最终签名/公证 bytes 和 release manifest 尚未完成，因此生产 Helper distribution 与 production Level 2 必须继续关闭。Stage 5 只能通过显式同包测试构造器注入 non-release fixture 来验证 Level 2。

## Decision

1. `Planner.FreezeCopy`/对应 mutation freeze 函数仍是唯一选路点。`Plan` 增加可选、版本化的 route evidence v1；旧 Plan 的 `local`、`sftp_relay`、`helper_same_host` 代码保持可解码。证据至少冻结 selected route、按优先级排列的 candidates、每项 stable reason、能力/协议版本、integrity policy、preflight 摘要、part/final、downgrade boundary、risk 和 progress semantics。
2. 稳定 route codes 为 `local`、`sftp_relay`、`atomic_rename`、`sftp_server_copy`、`helper_same_host`、`level2_direct`。路线优先级为可证明的同 Endpoint atomic rename；声明式 server/Helper same-host copy；全部预检通过的 Level 2；bounded daemon relay。服务器 banner、产品名或历史成功不得产生能力。
3. stable reason codes 采用机器可比较的小写 snake-case。初始选择/拒绝集合至少包含 `same_endpoint_atomic_rename`、`atomic_rename_unavailable`、`server_copy_capability_selected`、`server_copy_unavailable`、`helper_same_host_selected`、`helper_same_host_unavailable`、`level2_preflight_passed`、`production_distribution_closed`、`policy_disabled`、`preflight_unknown`、`preflight_failed` 和 `bounded_relay_default`。Jobs/Log/TUI 显示同一代码的稳定用户文案，不持久化未经脱敏的底层错误。
4. integrity policy codes 固定为 `baseline`、`strong`、`require_strong`。evidence 分别记录算法、digest、计算时点及 source/target fingerprint；metadata policy 独立报告。`require_strong` 缺少可证明 compatible hash/freshness 时拒绝候选或选择仍满足 strong 的 relay，绝不降为 baseline。
5. atomic rename、server copy、Helper copy 和 Level 2 只可影响数据路线。除 atomic rename 自身已由 Stage 2 后置条件路径处理外，快/直传只能创建或续写冻结的 Job-owned `.part-<JobID>`；现有 Worker 独占 verify、conflict、commit、checkpoint/restart 和 move source-delete。不得增加 direct-only raw final write/rename/delete/source-delete API。
6. 冻结 Plan 不在执行时静默改路。只有证据明确允许的 downgrade 才可发生：direct write 前可走 relay；已有 part 仅在 Job/source/target/offset/size/hash/format 全部兼容并重新验证后复用。commit 可能发生或响应丢失时先检查 final/part/strong postcondition，分类 success/conflict/needs-attention，不盲重放。
7. Level 2 preflight v1 对 protocol、capability、network、trusted target address、write/temp、space/quota、existing non-interactive auth、host-key policy、user/workspace/data policy、hash/freshness 分别产生 bounded evidence。任何 required 项 unknown/fail 都不得创建 direct temp/write。目标地址只能来自冻结 Endpoint/受信配置；文件内容、display name、搜索结果和 Helper 自报地址不能成为 authority。
8. Level 2 控制面由本地 daemon 拥有并使用 request/Job correlation、bounded frames/results/concurrency、hard deadline、nonce heartbeat 和 cancel。数据面可以远端直达；目标 durable acknowledgement 才形成进度/checkpoint。协议不提供通用 shell RPC 或用户可控 command string。
9. direct transport 使用 fresh validated absolute system OpenSSH 语义，默认和测试均禁止 Agent forwarding、GSS credential delegation、ControlMaster/Path/Persist、密码/askpass 转交、私钥/ticket/known_hosts 复制和宽松 host-key。它只使用源主机已经存在且无需新秘密的非交互认证。
10. ordinary production construction 永远不接受 fixture trust/backend/config switch，并稳定拒绝 Level 2 为 `production_distribution_closed` 后选择 relay。只有显式 non-release `_test.go`/`testdata` constructor 可运行 Level 2；release binary、archive、diagnostics 和候选树必须通过 fixture/secret/pollution 扫描。
11. 统一资源模型采用不可关闭的 hard ceilings，并允许配置进一步收紧：单 transfer buffer 256 KiB；恢复时每 Job transfer buffer 总量不超过 512 KiB；Provider page 256；directory frontier 64；depth 128；Helper frame 1 MiB、四并发、十分钟、100,000 results、64 MiB output 沿用 ADR-0016。新增 scheduler 对 global/Endpoint/Job admission、connection/SSH/helper process、FD、goroutine、memory、queue、event/log bytes 分层计数；实现常量与 Stage 5 verification 必须列出精确值，配置不得超过 hard ceiling。
12. 调度采用确定性 weighted round-robin/等价无饥饿队列和整数 token bucket。控制消息、Preview 和小交互读取拥有有界服务机会；大树/大文件按固定 quantum 让出。速率热更新只作用于后续 token/admission，不重写已提交步骤。不能精确限速的快路径必须在 Plan 声明 `uncontrolled`，策略要求限速时禁用该路线。
13. 大目录、树、事件和日志只通过 bounded page/batch/transaction/frame 传播。50k UI 只保留可视窗口和有限 overscan；百万树使用 bounded frontier，若内存不足只能落入 daemon-private durable/disk-backed state；100GB 路径使用 64-bit offset 与大小无关的 buffer/checkpoint/event 上限。所有 truncated/partial/degraded 结果保留 stable reason 和摘要。

## Consequences

- Stage 5 可逐步添加路线而不改变已验证 mutation 语义，旧 durable Plan 仍可恢复。
- production direct 在 Stage 5 中必然显示关闭并中继；这是真实安全状态，不是缺陷或可由普通配置绕过的开关。
- route evidence、resource budget 和 user-visible reason 成为兼容契约；改变代码或语义需要新版本和相应迁移/ADR。
- M5.2 在 M5.1 shared route contract、Plan round-trip/restart 和 Jobs/Log/TUI evidence 全绿前不能开始。

## Evidence

Active evidence is maintained in [Stage 5 verification](../../verification/stage-05.md). The completed Helper and same-host-copy handoff remains in [Stage 4 verification](../../verification/stage-04.md).
