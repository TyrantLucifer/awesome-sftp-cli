# ADR-0013：冻结 Stage 2 mutation、提交、move 与 delete 后置条件

- 状态：Accepted
- 日期：2026-07-16
- 影响范围：Stage 2 Provider mutation、Plan JSON、Worker commit、move/rename/delete、恢复与事件摘要
- 依赖：ADR-0004、ADR-0005、ADR-0012

## 背景

ADR-0004 冻结了 part→verify→commit 和 move 最后删源的不变量，ADR-0012 冻结了 V1 Job 数据库。但 Stage 2 仍需把 Provider 写能力、冻结 capability、目录结果、有状态响应丢失和显式删除变成一个可执行契约。若 UI、RPC 或后端可以绕开这条路径，或者 rename/delete 收到未知响应后直接重放，目标完整性和源保留语义都无法证明。

## 决策

### 唯一 mutation 路径

只读 `Provider` 不增加写方法。写端点实现可选 `MutableProvider`，提供 bounded `OpenWrite`、`Mkdir`、conditional `Rename` 和 non-recursive conditional `Remove`。可靠回收站是另一可选 `TrashProvider` facet；只有同一个 frozen capability revision 同时明确声明 `trash` 时 Planner 才能使用。TUI/CLI/RPC 只暴露 capture、create/control/conflict 等高层 Job 操作，不暴露 raw provider mutation。

Planner 在创建 Job 前重新 stat，并冻结：source/destination descriptor、FileRef fingerprint、read/write/delete capability revision、route、verification、conflict policy、part/final、buffer 和目录 queue/page/depth。cut additionally requires a frozen source `write` capability and mutation facet. 能力在 freeze 的多次 snapshot 之间变化时拒绝 Plan。

### copy 与目录结果

文件只写同目录 `.<final>.part-<job-id>`。checkpoint 保存 verified offset、source/part fingerprint、SHA-256 state 和 phase；pause/cancel 在关闭 handle 后重新 stat part 再保存 identity。Worker 重读 part 计算 SHA-256、重查 final、按 frozen conflict 决定提交，再重读 final 证明内容。unknown commit response 必须通过 final/part/fingerprint 后置条件决定。

目录 discovery 使用 frozen queue/page/depth，逐项复用文件协议。symlink 可见但不跟随或复制。checkpoint/result manifest 最多保存 256 项，另存完整 aggregate 与 truncation count；resume 对已成功项做 content revalidation，只重试缺失失败项。

### move 与 rename

同 Endpoint 只有在 snapshot 明确声明 `atomic_rename`、Provider 返回 `Atomic=true`，并且 source absence 与 final frozen fingerprint 都可证明时走零 stream 快路径。其他 move 固定为：

```text
copy → verify part → commit final → verify final
     → revalidate frozen source + delete capability
     → conditional delete source → prove source absence
```

source changed、capability changed、目录任一内容未逐项匹配、symlink/unsupported child、delete error 或 unknown response 无法证明时，不回滚已验证目标，也不继续猜测；Job 终止为 `completed_with_source_retained`。terminal event 使用 `job_move_completed` 或 `job_completed_source_retained`，payload 记录 bounded `move_reason`。

旧 V1 plan JSON 中没有 `move_strategy`/source-delete binding 的已冻结 move 可以完成安全 copy，但不得据此删除 source；结果必须保留 source。这保持向后读取兼容，而不把新能力补猜进旧 Plan。

### 显式 delete 与 trash

删除 Plan 只接受非 root 的 frozen file/directory/symlink identity。`D` 先 capture，确认冻结范围；无可靠 trash 时必须再确认 irreversible risk。recursive delete 只用 bounded Provider pages，逐项验证 descendant，遇 symlink 只 conditional-remove link 本身。Provider `Remove` 永不递归。

delete/trash 的 unknown response 先 stat frozen Location：not-found 才证明成功；仍存在或 stat 无法完成则进入 conflict/retry/failure，不无条件重放。用户显式 resume 仍必须重新通过 frozen capability 与 fingerprint 检查。

### repeat、恢复与秘密

count/`.` 只复制已冻结 Intent。copy 可直接重复；cut/rename 重开 move confirmation；delete 重开两层确认。它们不能携带 overwrite/apply-all 到不同 Job、conflict class 或 source identity。

daemon restart 将 interrupted running/verifying Job 按 ADR-0012 暂停。resume 先检查 checkpoint、part、final 和 source 后置条件。kill 发生在 commit 与 delete 之间时源保持，重启不得假定 delete 已执行。

Plan、checkpoint、result 和 event 只保存 bounded non-secret identity/result。Provider raw errors在持久化前缩减为 stable code/operation；密码、私钥、Askpass 回答、agent 内容、ticket 和环境不得进入 SQLite/backup/log/workspace。

## 后果

- 标准 LocalFS/SFTP 即使没有 helper/trash/direct 也有完整保守基线；性能能力只能改变 route，不能改变结果语义。
- 当前 LocalFS 和 SFTP 不声明可靠 trash 或 atomic rename，因此使用已验证的 copy-delete/irreversible-delete 路线。测试 Provider 证明 capability facets 的选择与零-stream fast path。
- V1 schema 不因 M2.2–M2.4 改变；新增 Plan JSON 字段是向后可读的 optional data。任何未来必填语义或 schema 变更必须前向 migration。

## 验证

- Planner stale identity/revision、source delete capability、dangerous path 和 frozen-plan tests。
- LocalFS/SFTP/Fake mutation contract；short read/write、permission、ENOSPC、disconnect、commit response lost。
- file/directory move source-changed、per-item verify、delete response lost、commit→delete restart boundary、atomic zero-stream path。
- trash/no-trash、root/two confirmations、bounded recursive delete、symlink no-follow、count/repeat reconfirmation。
- local and real-sshd PTY plus dual-independent-sshd relay, race, secret/pollution, native and exact-SHA Hosted gates recorded in Stage 2 verification.
