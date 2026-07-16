# ADR-0012：冻结 Stage 2 Job schema、事件序列与保守重启状态机

- 状态：Accepted
- 日期：2026-07-16
- 影响范围：Stage 2 Version 1 schema、Job/step 状态机、事务事件、daemon restart recovery
- 依赖：ADR-0004、ADR-0008

## 背景

ADR-0008 冻结了 migration runner 表、checksum、whole-schema contract 和数据库生命周期，但没有列出 Stage 2 领域表的精确 Version 1 形状。若 Job 表、状态转换和事件序列在实现中隐式演化，崩溃恢复无法区分可安全重试的阶段与可能已有外部副作用的阶段，历史 fixture 也无法证明完整 schema 未漂移。

## 决策

Version 1 名称固定为 `init`，包含 ADR-0008 的精确前六条 runner statements，随后按固定顺序创建：

1. `operation_plans`：保存 request ID、操作种类、来源/目标冻结 JSON、route、验证、冲突策略、风险等级和冻结时间；`request_id` 唯一。
2. `jobs`：一对一引用 plan，保存状态、乐观 `state_version`、下一个事件序号、pause/cancel 请求、retry 时间和 terminal summary。
3. `job_steps`：以 `(job_id, step_index)` 为无 rowid 主键，保存不可变 step 种类/位置快照和可变执行状态/attempt。
4. `job_checkpoints`：一对一引用 step，保存 phase、verified offset、source fingerprint、part location 和有界 checksum state。
5. `job_conflicts`：保存逐项 conflict、waiting/resolved 状态、冻结双方快照、resolution 和 Job-local apply scope。
6. `job_events`：以 `(job_id, sequence)` 为无 rowid 主键，`event_id` 全局唯一；每次状态事务读取 `next_event_sequence`、写入唯一事件并单调递增。
7. `job_results`：保存逐 step 的不可变 outcome/detail manifest。
8. `request_dedup`：保存 mutation request 的操作种类、Job 和 canonical response，实现跨进程 request 重放。

精确单行 SQL 由 `internal/state/migration.Version1` 作为 checksum-v1 输入编入二进制，任何 byte 变更都必须新增前向 migration，不得原地改写共享历史。当前真实 Version 1 migration checksum 是 `281a5d34c0ebdd06de26fd1098fbf3efd7c8a7e283f5328ea218d1ca8dfb19f9`。

Version 1 的完整 main schema（包括 exact SQL、列、STRICT/WITHOUT ROWID、FK、index 和 `sqlite_autoindex_*`）编码为 24,495-byte `SchemaContract`。编码在 ADR-0008 magic 后使用：`00=null`、`01=text + BE uint64 length + raw bytes`、`02=signed integer two's-complement bits as BE uint64`；collection/record tags `10..29` 分隔 sqlite_schema、table_list、table_xinfo、foreign_key_list、index_list 和 index_xinfo。排序分别使用 ADR-0008 要求的 raw-byte key、cid、id/seq、name 和 seqno。冻结 digest 是 `659edd23b5bc332b488a171c920815daffef6223ef2d3859215ba177c3d55e64`；压缩常量只缩小二进制源表示，运行时比较的是解压后的完整 canonical bytes。

## Job 状态机

主路径为：

```text
draft -> awaiting_confirmation -> queued -> running -> verifying -> completed
```

允许的附加转换固定如下：

| From | To |
|---|---|
| `draft` | `awaiting_confirmation`, `queued`, `canceled` |
| `awaiting_confirmation` | `queued`, `canceled` |
| `queued` | `running`, `paused`, `canceled` |
| `running` | `verifying`, `paused`, `waiting_auth`, `waiting_conflict`, `retry_wait`, `failed`, `canceled` |
| `verifying` | `completed`, `completed_with_source_retained`, `paused`, `waiting_auth`, `waiting_conflict`, `retry_wait`, `failed`, `canceled` |
| `paused`, `waiting_auth`, `waiting_conflict`, `retry_wait` | `queued`, `canceled` |
| terminal states | no transition |

`completed`、`completed_with_source_retained`、`failed`、`canceled` 是 terminal。terminal 必须有 summary，`retry_wait` 必须有未来 retry time，其他状态拒绝对应字段。非法边、stale `state_version`、重复但内容不同的 event ID 均在写入前拒绝。

daemon 在 bind/领取 Job 前用单个 `BEGIN IMMEDIATE` 扫描 interrupted Jobs。`running` 和 `verifying` 可能已有未确认外部副作用，因此 Job 及其相同状态的 step 一律转为 `paused`，追加唯一 `job_recovered` 事件；`queued`、显式 wait、paused、draft/confirmation 和 terminal 状态保持不变。第二次 recovery 不再产生事件，因而 restart 是幂等且可解释的。后续 worker 只能通过显式 resume 和 checkpoint/postcondition reconciliation 回到 `queued`。

## 写入与资源约束

- 所有 Job 状态事务使用保留 connection 的 runner-owned `BEGIN IMMEDIATE`/`COMMIT`，不把 `database/sql.BeginTx` 当成 writer lock。
- plan、step、event 和 summary 的单 row encoded values 总和最多 256 KiB；SQL 参数化，UI/IPC 不获得 raw DB 写入口。
- plan 创建、steps、初始事件和 request-dedup response 同事务提交；状态、版本、event sequence 和 event 同事务提交。
- schema 不含认证秘密、OpenSSH prompt、ticket、key 或环境；Location/FileRef 只保存非秘密操作快照。

## 后果

- Version 1 schema 与状态转换成为可重放、可比较的兼容格式；修改需要新的 migration 和 SchemaContract。
- daemon crash 不会把可能已产生外部副作用的 step 自动重跑；可用性让位于显式、安全恢复。
- Stage 2 后续里程碑必须在这些表上增加行为，不得另建 UI/CLI/RPC 直写旁路。

## 验证

- checksum golden、真实 Version 1 checksum、SQL lexer/admission 和 runner rollback。
- 每个历史 head 从 compiled migrations 重放并与冻结 canonical bytes/digest 双向比较。
- 全状态转换矩阵、非法转换、stale version、重复 request/event、事务回滚和单调 sequence。
- `running`/`verifying` restart pause、step 同步、唯一 recovery event 和第二次 recovery no-op。
- corruption/newer/tampered schema 只读降级，不能重建或继续 mutation。
