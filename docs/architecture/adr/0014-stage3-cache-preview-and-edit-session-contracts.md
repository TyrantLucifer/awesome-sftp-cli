# ADR-0014：冻结 Stage 3 cache、Preview 与 edit-session 持久契约

- 状态：Accepted
- 日期：2026-07-16
- 影响范围：Stage 3 Preview/Jobs/Log drawer、内容缓存、租约、编辑会话、SQLite Version 2/3、崩溃恢复
- 依赖：ADR-0002、ADR-0005、ADR-0007、ADR-0008、ADR-0012、ADR-0013

## 背景

Stage 1 已有有界文本预览和 generation 丢弃，Stage 2 已冻结 Version 1 Job schema 与唯一 mutation 路径。Version 1 没有 cache blob、Location reference、materialization、lease 或 edit-session 表；把 Stage 3 状态塞进 Job event、request-dedup、workspace JSON 或不受 schema contract 保护的 sidecar，会绕开 ADR-0008 的 forward migration、single writer、whole-schema 与恢复门禁。

缓存内容与 SQLite 位于不同目录，不能用单个事务原子提交。编辑器还必须修改独立 materialization，绝不能原地修改 content-addressed blob。崩溃、索引损坏、quota 或恢复不确定时，唯一 dirty 本地副本的保留优先级高于自动清理。

## 决策

### 版本、根目录与命名

Stage 3 cache metadata format 固定为 `1`。SQLite Version `2` 新增 cache/lease/edit-session catalog，Version `3` 不改写 Version 2，只新增可完整重建编辑决策输入的 `edit_session_details`；Stage 3 完成时的 schema head 是 `3`，后续当前 head 由 compatibility inventory 记录。Version 1 与 Version 2 的 statements、checksum 和 whole-schema contract 保持字节不变。Version 3 checksum 是 `16ae664c033fb1fae7da937eae6c4b19c6b05430fa3499fa5f0da8daa58e1ab4`，head-3 whole-schema contract digest 是 `a523d6c4aeebb386780f7283b63aacb175cf0420027114edac51a032425615a2`。

cache 根使用 ADR-0007 的 `Paths.CacheDir`，应用布局为：

```text
<cache>/content-v1/
  blobs/sha256/<first-two-hex>/<64-lower-hex>.blob
  entries/<64-lower-hex-entry-id>/manifest-v1.json
  materializations/<32-lower-hex-id>/content
  materializations/<32-lower-hex-id>/manifest-v1.json
  staging/.amsftp-cache-<kind>-<32-lower-hex>.tmp
```

所有目录精确 `0700`，文件精确 `0600`；root/current-euid、regular/directory type、owner-private ACL、no-follow 和祖先规则服从 ADR-0007。已存在但 type/owner/mode/ACL 不符时不 chmod、不接管、不删除。Stage 3 原生 crash 语义只承诺 APFS、ext4、XFS；cache filesystem 不合格时 cache/edit/open fail closed，但 Stage 1 browsing 与既有 Stage 2 Jobs 继续可用。

entry ID 是 `SHA-256(amsftp-cache-entry-v1 NUL + EndpointID length/value + canonical raw path length/value + canonical fingerprint bytes)`。同 Location 的不同 fingerprint 产生不同 entry；路径相同从不等于内容相同。manifest 使用严格、bounded、unknown-field-rejecting canonical JSON，原始路径以 ADR-0005 base64 wire bytes 表示，且不得含 credential、preview body、command text 或完整 file content。

### 内容、materialization 与强身份

只有完整读取、大小一致并在本机复算 SHA-256 的对象才能发布到 `blobs/sha256` 并参与去重。partial preview、weak-only fingerprint 或未完成下载不得进入 blob namespace，也不得交给 editor/opener。相同 SHA-256/size 可共享 blob；每个 Location+fingerprint 仍有独立 entry/freshness。

发布顺序固定为：在 verified staging directory `O_EXCL` 创建 `0600` temp → bounded stream write并复算 size/hash → file full-sync → no-replace rename 到派生 final → sync parent → SQLite transaction 发布 blob/entry/reference。任一 DB 前崩溃只产生 unknown orphan；启动扫描保留并报告，不直接删除。删除顺序固定为 SQLite durable `deleting` marker → no-follow identity/hash/attrs/reachability recheck → unlink → sync parent → 删除 exact catalog row。未知、变化或无法证明不可达的对象保留。

Provider 报告的 expected size 是 staging publication 的硬前置：`LimitedReader` 只允许 expected+1 bytes，流式 SHA-256 与实际 size 在 temp 仍位于 staging 时就与 expected 比较。short read、oversize 或 hash/size 不匹配会删除 exact temp 并 sync staging，不发布 blob/manifest/catalog row。

filesystem manifest 可证明 blob/content hash、Location/fingerprint 和 materialization baseline，但不包含 workspace policy、pinned/dirty、reference、lease 与完整时间序列。因此当前安全合约不是“从 filesystem 自动重建 catalog”：如果 catalog 损坏或两侧不一致，startup lifecycle 返回 `cache lifecycle requires operator attention`，停止 quota/clear/materialize 等可能删除或扩大不一致的操作，保留全部未知字节并暴露 bounded inventory。在未有新的、能保存上述安全元数据的 manifest migration 前，不得伪造自动 rebuild 或把未知内容当成可清理 orphan。Stage 1 browsing 与已有 Stage 2 Job 仍继续工作。

editor/opener/upload 只接收独立 `materializations/<id>/content`。从 verified blob 复制/生成 materialization 后，先 full-sync content 与 manifest，再在 SQLite 标记可交付并取得 lease；外部进程不得修改 blob。dirty 在外部 handoff 前先具备 durable session/materialization identity；检测到本地变化后状态必须先持久化为 dirty，才能释放进程 lease。dirty、pinned 或 recovery-unknown materialization 不因 quota、lease expiry、index rebuild 或普通 clear-cache 删除。

### SQLite Version 2/3 关系

Version 2 以 immutable DDL 和非零 `MaxMigrationWalBytes` 新增以下 typed tables/indexes；精确 SQL、checksum 与 per-head whole-schema bytes/digest由实现常量冻结：

- `cache_blobs`：verified SHA-256、size、派生 basename、published/deleting state、created/last-access。
- `cache_entries`：entry ID、EndpointID、raw canonical path bytes、typed fingerprint fields/strength、fresh/stale/unknown、policy/workspace、pinned、complete blob reference、last-access。
- `cache_materializations`：随机 ID、entry/baseline blob、派生 basename、current size/SHA-256、clean/dirty/orphaned/deleting state、pinned、timestamps。
- `cache_references`：exact owner kind/ID 与 blob 或 materialization 二选一的引用；用于 workspace share、preview/edit/open/upload reachability。
- `cache_leases`：lease ID、受保护 blob/materialization、owner kind/ID、daemon instance、可选 PID+process-birth identity、heartbeat/expiry/grace、active/released state。
- `edit_sessions`：session ID、source entry/baseline、materialization、local/remote identity状态、clean/dirty/conflict/upload/recovery lifecycle、optimistic version 与时间。
- `edit_session_events`：每 session 单调、bounded 的决策/恢复审计；不保存 file body、diff body 或 raw Provider cause。
- `edit_session_jobs`：edit session 与 Stage 2 Job 的一对一绑定。

Version 3 的 `edit_session_details` 冻结 purpose、reference/lease、可验证状态机 JSON、decision/audit reason、target Location、baseline/current local SHA-256，original/expected remote presence/kind/fingerprint。它不改写 Version 2 表，也不存储 diff 或文件正文。

edit-session→Job 绑定必须与 Plan/Job/steps/initial event/request-dedup 在同一个 runner-owned `BEGIN IMMEDIATE` 中提交。先创建 Job 再单独更新 cache store 会留下不可接受的 crash gap。operation kind继续复用 `copy`，但 conditioned edit使用向后不兼容、旧binary必须拒绝的 Plan Version 2，并显式标记 `sync_back` origin、冻结 edit baseline 的 expected-destination precondition。baseline 不只冻结 stat fingerprint；daemon 通过带 expected fingerprint 和 2 GiB 硬上限的 Provider stream 计算完整 remote SHA-256/size，并要求与 verified materialization 一致。每次编辑后观察重复此 content identity，所以 size/mtime/file-ID 未变但 bytes 变化仍是 conflict。

expected-present sync-back 不能实现成 hash→replace 的 check-then-rename。Plan V2 冻结同目录 job-owned `.<name>.amsftp-original-<job-id>` preservation Location；LocalFS 用 kernel no-replace rename、SFTP 用标准 no-replace rename、Fake 在同一锁内，先把 final path 原子移到该 Location，随后才在最多 2 GiB、256 KiB buffer、exact-size、zero-progress 和 15 分钟 deadline 下验证完整 SHA-256。若移动瞬间的内容已改变，Provider 尽力 no-replace restore 并返回 conflict；restore 因并发新 final 失败时两份内容都保留。验证成功后 part 只以 no-replace 发布，因此移走后出现的新 final 也只能触发 conflict。旧 inode 上稍后的并发写仍落在 preservation path，不会被静默删除。成功或冲突的 Job event 都报告 preservation Location；AMSFTP 不自动删除这份安全副本。expected-absent要求仍不存在。显式overwrite从同一 edit session 的已持久 observed destination 生成新的 fingerprint+content-SHA precondition；若目标再次变化则再次冲突。

`migration.Version1()`、V1 contract 与 bootstrap 不变。pristine install 先发布 frozen V1，再通过 ADR-0008 的普通 V1→V2→V3 attempt/backup/WAL/immutable path 升级。compiled set 为 `[Version1, Version2, Version3]`，production 使用匹配 `^[0-9a-f]{32}$` 的 CSPRNG attempt ID 和显式 resume/retry path；恢复复用已持久 attempt/backup，绝不创建第二份。

### policy、quota、LRU 与 lease

workspace cache policy 固定为 `lru`、`ephemeral`、`pinned_offline`；workspace schema 从 v1 前向读写到 v2，旧 v1 `ephemeral` 保持原语义。默认预算为：

| 预算 | 固定默认值 |
|---|---:|
| 全局 cache content + materialization bytes | 2 GiB |
| 全局 managed objects（entries + materializations） | 4096 |
| 单 workspace 非共享份额 | 1 GiB |
| 单次 quota reconciliation 候选 | 256 |

配置只能收紧/在有界范围内提高，不能关闭硬上限或绕过保护集合。配额同时统计 blob+materialization bytes，全局 object count 是 entries+materializations；零字节 materialization 也消耗一个 object slot。每次 blob/entry publication 或 materialization handoff 在可见前先进入一个 daemon-wide serial admission critical section，将 incoming object 视为受保护并在同一把锁内最多重读/重规划 4 个批次、每批最多 256 个候选，直到 catalog commit。这防止并发 admission 重复消耗同一份剩余配额；无可删除 LRU 或 4 批后仍不满足时，RPC 以 typed `resource_exhausted` 拒绝，并给出 bounded required/pinned/dirty/leased/referenced 摘要。

LRU 以 daemon 注入的单调 manual clock 更新；eviction 忽略 pinned、dirty、active/uncertain lease、Stage 2 part、active upload、仍被 reference 引用的对象。blob 只有在所有 entry/reference/materialization reachability 为零且 durable deleting 流程完成后才删除。全部候选受保护时不误删。

lease heartbeat 默认 30 秒、正常 expiry 2 分钟；立即返回 opener 默认 grace 15 分钟并要求显式“检查更改/结束观察”。PID 不是存活证明，必须同时匹配 Linux process start ticks 或 Darwin process start identity。daemon restart 后 live PID+birth identity 保持保护；一般 owner 只有在普通 expiry/grace 与 identity 契约允许时才回收。Preview 是窄例外：只要 exact PID+birth 已证明死亡，就可立即释放其 exact Preview lease/reference，不等待本来用于活进程心跳宽限的 expiry。PID identity 无法可靠查询时 lease 标记 uncertain 并保持保护/诊断，不能猜测已失效。client disconnect 不是 cancel 或 release 证明。

startup 在 reconciliation/quota 前只回收“active Preview lease 的 exact process identity 已证明死亡，且恰好有一条 owner/target 全等的 Preview reference”的崩溃 handoff；不等待 expiry。查询以 `lease_id` cursor 分页，每页和每次 exact-reference lookup 分别最多 256/2 条，默认最多 64 批，即单次 lifecycle 理论 ceiling 16,384；实际仍受 4,096 managed-object 上限约束。释放前再读 lease 并重验。ambiguous/missing reference、edit/open/upload owner 与任何 live/uncertain 情况保留，因此不会把持久 edit recovery 当成 stale Preview 清理。

`pinned_offline` 只在 exact Provider `OpenRead` 返回 transport-interrupted 时降级，并且请求必须同时为 pinned policy+flag。daemon 在 admission mutex 内按 Location+workspace 定位，要求只有一个历史 fingerprint，重算 entry ID，重验 published blob、canonical manifest/catalog binding、strong embedded source fingerprint 与 content SHA/size，并把该 catalog entry 的 freshness 持久改为 `unknown`。随后 handoff 在同一 admission 序列再次检查 pinned identity 唯一性；若并发 online publication 增加第二个 fingerprint，它必须等到 publication commit 后以 ambiguity 失败，不能 materialize 较早选择。没有匹配 pinned entry 时保留原 transport-interrupted 结果，多个历史 fingerprint 或已选记录的 weak/corrupt/missing binding 则 typed integrity failure，始终不猜测“最新”。重连后仍按在线 remote fingerprint+streamed content hash 重新发布 fresh entry。离线 editor/opener 可查看或修改受保护副本，但 remote freshness 未重验前不能授权 sync-back。

### Preview 与 drawer 接口

drawer model 固定为 `closed|preview|jobs|log` 加独立 `pane|drawer` focus。`K/J/L`：closed 时打开对应 drawer 并聚焦；同 tab 在 pane focus 时聚焦 drawer；同 tab 已聚焦时关闭并回到 pane；另一 tab 直接切换并聚焦。`Esc` 在 drawer focus 时只回到 pane并保留可见 drawer。小写导航不改变 drawer mode，resize 只通过纯 layout reducer调整高度/降级，不丢失 active pane、drawer tab、Job cursor 或 frozen Preview source。

Preview request 必须带 128-bit request ID、pane、Endpoint session/generation、canonical Location、source fingerprint、mode、offset、requested limit 与 UI generation。result/cancel携带同一 identity；client只接受全部 identity匹配的最新 generation。Endpoint generation、refresh、cursor或mode变化取消旧 context；迟到结果按 identity丢弃。

初始硬预算冻结为：initial/head/tail/range/continue 每次 provider read 64 KiB，单 Preview retained 与 render output 各 512 KiB，JSON parse input 256 KiB，JSON nesting 64，rendered lines 10,000，syntax spans 4,096，Jobs/Log snapshot 1,000 records，单次 replay/page 256 records。terminal image active-probe response 最多 256 bytes/200 ms；image metadata decode 拒绝超过 40,000,000 pixels，实际终端编码额外要求不超过 4 MiB payload、6 MiB output 和 1,000,000 pixels。partial、offset/range、truncated/discarded、stale/unknown 与 source fingerprint必须显式返回。Preview正文、命令输出和 terminal bytes不持久化；事件仅保存 bounded type/status/budget/error code/IDs。

metadata view 不需要为 directory/symlink 打开 content stream，且只输出 bounded Provider facts：Endpoint、canonical path、kind、size、mtime、mode、symlink target、fingerprint strength/hash/file ID/version ID，以及 media type/range/captured/status。每个外部字段先单行清洗并限制为最多 1,024 bytes（或更小的输出份额），组合后仍受 512 KiB output cap。

## 安全与恢复不变量

- cache 是可验证副本，不是真相源；每次 edit sync-back 按 baseline 重验 remote。
- index/manifest corruption 保留 bytes，进入 fail-closed diagnostic-preservation；因 manifest 缺少 safety-critical catalog metadata，当前不自动 rebuild，未知文件不批量删除。
- quota、ENOSPC、crash、restore backup 或 schema rollback 不删除唯一 dirty materialization。
- cache failure 不能破坏 Stage 1 browsing、auth、workspace 或 Stage 2 Job recovery。
- Provider/RPC 不获得 raw cache mutation、external exec 或 shell API。
- log/DB/workspace/manifest 不保存 credential、完整 preview/file content、command output 或 terminal bytes。

## 验证要求

- V2/V3 checksum、SQL admission、per-head whole-schema contract、V1→V2→V3/pristine→V1→V2→V3、attempt ID/resume、backup/restore hold、WAL budget、old-binary/new-head fail-closed 与 process-death boundaries。
- APFS/ext4/XFS owner/mode/ACL/no-follow、publish/delete ordering、ENOSPC、index corruption、uncataloged/orphan bounded inventory、unknown-file preservation与 unsafe rebuild refusal。
- manual-clock quota/LRU/share/reference/dedup、entry+materialization object admission、4-batch typed refusal、concurrent serialization、short-read staging cleanup、all-protected、lease PID-reuse/live/dead/uncertain、256/page × 64-batch（理论 16,384，实际受 4,096 managed-object 上限）keyset startup scan、exact dead-Preview immediate reclaim 且 edit/open/upload reference 保留、daemon kill-9 与 full race。
- reducer/layout snapshots覆盖 normal/narrow/resize/reattach、K/J/L/escape/focus、bounded Jobs/Log、Preview generation/cancel/out-of-order 与 file/directory/symlink full bounded metadata。
- edit no-change/local-only/remote-only/both/unknown matrix；Provider-stream SHA 侦测 same-stat byte change；sync-back Job atomic binding、no-replace original preservation/verify 与 no-replace part publication 的 before-move/new-final/old-inode/restore-failure 竞态、pinned-offline exact/ambiguous/reconnect、upload/restart/failure dirty recovery。

## 后果

- Stage 3 增加两条 production forward migration 与 cache filesystem catalog；实现成本高，但恢复与兼容边界可审计。
- 当前的 corruption recovery 优先保全 bytes 而不是可用性；安全自动 catalog rebuild 需要一个未来 manifest/schema 版本，不得从现有 manifest 猜测 dirty/pinned/reference/lease。
- cache failure 可局部降级，持久 state schema failure仍按 ADR-0008 停止新 mutation。
- Stage 4 search/tail/watch只能复用此 Preview/cache接口，不能新建无预算内容路径。
