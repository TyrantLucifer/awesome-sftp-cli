# ADR-0014：冻结 Stage 3 cache、Preview 与 edit-session 持久契约

- 状态：Accepted
- 日期：2026-07-16
- 影响范围：Stage 3 Preview/Jobs/Log drawer、内容缓存、租约、编辑会话、SQLite Version 2、崩溃恢复
- 依赖：ADR-0002、ADR-0005、ADR-0007、ADR-0008、ADR-0012、ADR-0013

## 背景

Stage 1 已有 64 KiB 有界文本预览和 generation 丢弃，Stage 2 已冻结 Version 1 Job schema 与唯一 mutation 路径。Version 1 没有 cache blob、Location reference、materialization、lease 或 edit-session 表；把 Stage 3 状态塞进 Job event、request-dedup、workspace JSON 或不受 schema contract 保护的 sidecar，会绕开 ADR-0008 的 forward migration、single writer、whole-schema 与恢复门禁。

缓存内容与 SQLite 位于不同目录，不能用单个事务原子提交。编辑器还必须修改独立 materialization，绝不能原地修改 content-addressed blob。崩溃、索引损坏、quota 或恢复不确定时，唯一 dirty 本地副本的保留优先级高于自动清理。

## 决策

### 版本、根目录与命名

Stage 3 cache metadata format 固定为 `1`，SQLite schema head 固定新增为 Version `2`。Version 1 的 19 条 statements、checksum `281a5d34c0ebdd06de26fd1098fbf3efd7c8a7e283f5328ea218d1ca8dfb19f9` 与 whole-schema digest `659edd23b5bc332b488a171c920815daffef6223ef2d3859215ba177c3d55e64` 不修改。

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

发布顺序固定为：在 verified staging directory `O_EXCL` 创建 `0600` temp → bounded stream write并复算 size/hash → file full-sync → no-replace rename 到派生 final → sync parent → SQLite transaction 发布 blob/entry/reference。任一 DB 前崩溃只产生 unknown orphan；启动保留并诊断/重建，不直接删除。删除顺序固定为 SQLite durable `deleting` marker → no-follow identity/hash/attrs/reachability recheck → unlink → sync parent → 删除 exact catalog row。未知、变化或无法证明不可达的对象保留。

editor/opener/upload 只接收独立 `materializations/<id>/content`。从 verified blob 复制/生成 materialization 后，先 full-sync content 与 manifest，再在 SQLite 标记可交付并取得 lease；外部进程不得修改 blob。dirty 在外部 handoff 前先具备 durable session/materialization identity；检测到本地变化后状态必须先持久化为 dirty，才能释放进程 lease。dirty、pinned 或 recovery-unknown materialization 不因 quota、lease expiry、index rebuild 或普通 clear-cache 删除。

### SQLite Version 2 关系

Version 2 以 immutable DDL 和非零 `MaxMigrationWalBytes` 新增以下 typed tables/indexes；精确 SQL、checksum 与 per-head whole-schema bytes/digest由实现常量冻结：

- `cache_blobs`：verified SHA-256、size、派生 basename、published/deleting state、created/last-access。
- `cache_entries`：entry ID、EndpointID、raw canonical path bytes、typed fingerprint fields/strength、fresh/stale/unknown、policy/workspace、pinned、complete blob reference、last-access。
- `cache_materializations`：随机 ID、entry/baseline blob、派生 basename、current size/SHA-256、clean/dirty/orphaned/deleting state、pinned、timestamps。
- `cache_references`：exact owner kind/ID 与 blob 或 materialization 二选一的引用；用于 workspace share、preview/edit/open/upload reachability。
- `cache_leases`：lease ID、受保护 blob/materialization、owner kind/ID、daemon instance、可选 PID+process-birth identity、heartbeat/expiry/grace、active/released state。
- `edit_sessions`：session ID、source entry/baseline、materialization、local/remote identity状态、clean/dirty/conflict/upload/recovery lifecycle、optimistic version 与时间。
- `edit_session_events`：每 session 单调、bounded 的决策/恢复审计；不保存 file body、diff body 或 raw Provider cause。
- `edit_session_jobs`：edit session 与 Stage 2 Job 的一对一绑定。

edit-session→Job 绑定必须与 Plan/Job/steps/initial event/request-dedup 在同一个 runner-owned `BEGIN IMMEDIATE` 中提交。先创建 Job 再单独更新 cache store 会留下不可接受的 crash gap。operation kind继续复用 `copy`，但 conditioned edit使用向后不兼容、旧binary必须拒绝的 Plan Version 2，并显式标记 `sync_back` origin、冻结 edit baseline 的 expected-destination precondition；不能仅依赖 Job 执行前刚观察到的 destination fingerprint，否则编辑期间发生的远端修改可能被当作 overwrite 新基线。expected-present要求kind/fingerprint一致，expected-absent要求仍不存在；不匹配保留part并进入durable conflict。显式overwrite从同一edit session的已持久observed destination生成新的precondition；若目标再次变化则再次冲突。

`migration.Version1()`、V1 contract 与 bootstrap 不变。pristine install 先发布 frozen V1，再通过 ADR-0008 的普通 V1→V2 attempt/backup/WAL/immutable path 升级。compiled set 变为 `[Version1, Version2]` 前，production 必须生成匹配 `^[0-9a-f]{32}$` 的 CSPRNG attempt ID，并提供显式 resume/retry operator path；当前空 attempt ID 与无 production resume caller 都是阻断门禁。恢复复用已持久 attempt/backup，绝不创建第二份。

### policy、quota、LRU 与 lease

workspace cache policy 固定为 `lru`、`ephemeral`、`pinned_offline`；workspace schema 从 v1 前向读写到 v2，旧 v1 `ephemeral` 保持原语义。默认预算为：

| 预算 | 固定默认值 |
|---|---:|
| 全局 cache content + materialization bytes | 2 GiB |
| logical entries | 4096 |
| 单 workspace 非共享份额 | 1 GiB |
| 单次 quota reconciliation 候选 | 256 |

配置只能收紧/在有界范围内提高，不能关闭硬上限或绕过保护集合。LRU 以 daemon 注入的单调 manual clock 更新；eviction 忽略 pinned、dirty、active/uncertain lease、Stage 2 part、active upload、仍被 reference 引用的对象。blob 只有在所有 entry/reference/materialization reachability 为零且 durable deleting 流程完成后才删除。全部候选受保护时返回所需、可释放、pinned、dirty、leased 与 shared bytes/entries 的 bounded 摘要，不误删。

lease heartbeat 默认 30 秒、正常 expiry 2 分钟；立即返回 opener 默认 grace 15 分钟并要求显式“检查更改/结束观察”。PID 不是存活证明，必须同时匹配 Linux process start ticks 或 Darwin process start identity。daemon restart 后 live PID+birth identity 保持保护；明确 dead owner 且 grace 到期才回收。PID identity 无法可靠查询时 lease 标记 uncertain 并保持保护/诊断，不能猜测已失效。client disconnect 不是 cancel 或 release 证明。

### Preview 与 drawer 接口

drawer model 固定为 `closed|preview|jobs|log` 加独立 `pane|drawer` focus。`K/J/L`：closed 时打开对应 drawer 并聚焦；同 tab 在 pane focus 时聚焦 drawer；同 tab 已聚焦时关闭并回到 pane；另一 tab 直接切换并聚焦。`Esc` 在 drawer focus 时只回到 pane并保留可见 drawer。小写导航不改变 drawer mode，resize 只通过纯 layout reducer调整高度/降级，不丢失 active pane、drawer tab、Job cursor 或 frozen Preview source。

Preview request 必须带 128-bit request ID、pane、Endpoint session/generation、canonical Location、source fingerprint、mode、offset、requested limit 与 UI generation。result/cancel携带同一 identity；client只接受全部 identity匹配的最新 generation。Endpoint generation、refresh、cursor或mode变化取消旧 context；迟到结果按 identity丢弃。

初始硬预算冻结为：probe 4 KiB、initial/range read 64 KiB、continue chunk 64 KiB、单 Preview retained/render output 512 KiB、JSON parse input 256 KiB、JSON nesting 64、rendered lines 10,000、Jobs/Log snapshot 1,000 records、单次 replay/page 256 records。partial、offset/range、truncated/discarded、stale/unknown 与 source fingerprint必须显式返回。Preview正文、命令输出和 terminal bytes不持久化；事件仅保存 bounded type/status/budget/error code/IDs。

## 安全与恢复不变量

- cache 是可验证副本，不是真相源；每次 edit sync-back 按 baseline 重验 remote。
- index/manifest corruption 保留 bytes，进入 rebuild/diagnostic；未知文件不批量删除。
- quota、ENOSPC、crash、restore backup 或 schema rollback 不删除唯一 dirty materialization。
- cache failure 不能破坏 Stage 1 browsing、auth、workspace 或 Stage 2 Job recovery。
- Provider/RPC 不获得 raw cache mutation、external exec 或 shell API。
- log/DB/workspace/manifest 不保存 credential、完整 preview/file content、command output 或 terminal bytes。

## 验证要求

- V2 checksum、SQL admission、whole-schema contract、V1→V2/pristine→V1→V2、attempt ID/resume、backup/restore hold、WAL budget、old-binary/new-head fail-closed 与 process-death boundaries。
- APFS/ext4/XFS owner/mode/ACL/no-follow、publish/delete ordering、ENOSPC、index corruption、orphan rebuild、unknown-file preservation。
- manual-clock quota/LRU/share/reference/dedup、all-protected、lease PID-reuse/live/dead/uncertain、daemon kill-9 与 full race。
- reducer/layout snapshots覆盖 normal/narrow/resize/reattach、K/J/L/escape/focus、bounded Jobs/Log、Preview generation/cancel/out-of-order。
- edit no-change/local-only/remote-only/both/unknown matrix；sync-back Job atomic binding、expected-destination conflict、upload/restart/failure dirty recovery。

## 后果

- Stage 3 增加第一条 production forward migration 与 cache filesystem catalog；实现成本高，但恢复与兼容边界可审计。
- cache failure 可局部降级，持久 state schema failure仍按 ADR-0008 停止新 mutation。
- Stage 4 search/tail/watch只能复用此 Preview/cache接口，不能新建无预算内容路径。
