# ADR-0008：选择 pure-Go SQLite 与前向校验迁移

- 状态：Accepted
- 日期：2026-07-15
- 影响范围：Stage 2 持久状态、schema 兼容、崩溃恢复、四目标发行构建

## 背景

daemon 将用 SQLite 保存 workspace、不可变 Job plan、状态/检查点、cache lease 和有界诊断事件。发行矩阵固定为 darwin/linux × amd64/arm64 且 `CGO_ENABLED=0`；驱动不能要求每目标 C 编译器。迁移必须在 daemon 开始监听前完成，崩溃后只能看到完整旧 schema 或完整新 schema，不能把失败迁移当成空库重建。

通用迁移框架通常同时支持多种数据库、CLI 和动态 SQL 文件。本项目只有一个内嵌 SQLite store、一个 writer daemon 和受控 schema；引入未使用的多数据库依赖会扩大供应链与升级面。

## 决策

### 驱动

Stage 2 精确使用：

    modernc.org/sqlite v1.53.0
    modernc.org/libc v1.73.4

`modernc.org/libc` 是该 sqlite 版本上游 module 的精确依赖，根 module 必须保留解析结果，不通过 replace 或松散升级改变它。第一次导入时提交完整 `go.sum`，重新执行许可证、漏洞、race、四平台交叉构建和原生 macOS/Linux数据库测试。

只注册这一种生产 driver；不提供运行时 driver 开关，也不让 modernc 与 CGO driver 长期并存。

### 状态文件系统资格与 WAL 门禁

SQLite 状态库只允许位于本地、支持同机 POSIX locking/shared-memory wal-index、atomic rename 和 file/directory fsync 的文件系统。Stage 2 首个正式支持 allowlist 固定为 macOS APFS，以及 Linux ext4/XFS；扩展 Btrfs、ZFS 或其他类型前必须新增原生崩溃/锁/WAL/fsync 证据并更新本 ADR/支持矩阵。Darwin 读取 `statfs` filesystem type 并要求 exact `apfs`。Linux 同时读取 `statfs` magic 与 `/proc/self/mountinfo` 中对 canonical state path 的 longest-component-prefix mount：XFS 要求 magic `0x58465342` 且 fs type exact `xfs`；ext4 要求 magic `0xEF53` 且 mount fs type exact `ext4`，因为该 magic 也被 ext2/ext3 使用。mountinfo escape/字段必须严格解析，magic/type/mountpoint 不一致、ext2/ext3、NFS、SMB/CIFS、FUSE network mount、未知或查询失败一律 fail closed。默认 state root 不合格时给出不含敏感路径的诊断，要求用户显式 `--state-dir` 指向通过 ADR-0007 权限检查的安全本地盘；不能静默换库、另建隐藏状态或降级到 rollback journal。

只有下述 raw identity preflight 已确认 final 不存在或属于本项目后，才可在同一 state root 创建 owner-only、不可预测的临时 probe database；wrong/foreign path 在此之前结束，不能用 capability probe 改变其目录 metadata。当前 daemon 与一个通过 inherited pipe 协调的 same-binary 短生命周期 internal child（不增加公开 command role、不经过 shell）执行跨进程 WAL/locking smoke：父/子两个 connection 都设置 `wal_autocheckpoint=0`，父保持打开；先建 probe schema并 `wal_checkpoint(TRUNCATE)`，断言 WAL 清空，再提交唯一 marker，按 page size 验证 `-wal` 至少含 header+一个完整 frame，从而证明 marker 仍只在 WAL 中；child 必须读到 marker。父持有 `BEGIN IMMEDIATE` 时 child writer 必须在有界 timeout 内得到 busy，释放后 child 必须能提交且父可读。`PRAGMA journal_mode=WAL` 必须返回 exact lowercase `wal`；file/directory fsync 必须成功。随后只按重验 identity 的精确 probe db/`-wal`/`-shm` 清理；启动时也只清理符合内部随机 grammar、owner/mode/type 的遗留 probe，不模糊删除。child crash、返回非 `wal`、缺少真实 WAL frame、共享内存/锁/可见性/fsync/清理检查失败都阻止真实数据库打开。statfs allowlist 与 smoke 必须同时通过。

### 项目内迁移 runner

不引入第三方 migration framework。迁移以 Go 数据结构编入二进制：

    Migration {
        Version              uint64
        Name                 string
        Statements           []string
        MaxMigrationWalBytes uint64
    }

checksum 输入冻结为 `amsftp-migration-checksum-v1` 二进制格式，按以下顺序无间隔拼接；所有整数都用 `encoding/binary.BigEndian`，长度均指原始 byte 数而非 rune 数：

| 顺序 | 编码 |
| --- | --- |
| 1 | 27 个固定 bytes：ASCII `amsftp-schema-migration-v1` 后跟单个 NUL (`00`) |
| 2 | migration `Version`：unsigned 64-bit big-endian |
| 3 | `Name` byte 长度：unsigned 32-bit big-endian |
| 4 | `Name` 原始 ASCII bytes |
| 5 | statement count：unsigned 32-bit big-endian |
| 6 | 对 slice 中每条 statement 按原顺序写入：unsigned 64-bit big-endian byte 长度，再写原始 UTF-8 bytes |

Version 必须在 `1..9223372036854775807`（SQLite signed INTEGER 正范围）且整个 migration 集合从 1 严格连续。Name 必须是 1..64 bytes 并匹配 `[a-z][a-z0-9_]{0,63}`。statement count 必须是 1..1024；每条 statement 必须是 1..1048576 bytes、有效 UTF-8、不含 NUL，且至少有一个 byte 不是 ASCII SP/HT/CR/LF。完整编码不得超过 8388608 bytes；所有加法和长度转换先做 overflow/上限检查，再以流式 writer 同时编码并计算 SHA-256，不能按不受信长度预分配。空 statement 和空 migration 均拒绝。Version 1 的 `MaxMigrationWalBytes` 必须为 0（只在 unpublished rollback-journal temp bootstrap）；每个后续 version 必须声明 `1..8657043456` bytes（8 GiB + 64 MiB）的独立 WAL space budget并通过最大历史 fixture/fault test。该 runtime budget 不进入 checksum-v1 历史 digest，但进入下述 attempt migration-set digest，变更时不能复用进行中的 attempt。

statement 的空格、注释、大小写、CR/LF 和末尾分号一律作为原始 bytes 进入 checksum，不做 Unicode、换行或 SQL 规范化；slice 顺序也是兼容格式的一部分。`Statements` 的 slice 边界本身**不是**执行边界：modernc.org/sqlite v1.53.0 的 `Exec` 会把 tail 当作同一脚本继续执行，因此 runner 必须在调用 driver 前用下述受测 lexer 证明每个元素恰好是一条允许的 SQL statement。迁移按严格连续 version 排序，不允许重复、缺口、out-of-order、环境替换或生产 Down。

lexer 只做确定性的语句边界与首部分类，不重写 SQL。它逐 byte 识别 SQLite 单引号字符串（`''` 转义）、双引号 identifier（`""` 转义）、反引号 identifier（成对反引号转义）、方括号 identifier、`--` 到行尾注释以及不嵌套的 `/* ... */` 注释；quote/comment 未闭合立即拒绝。只有 quote/comment 之外的分号才是 separator；每个元素允许没有分号，或仅有一个末尾分号且其后只能是 ASCII whitespace/comment。第二个 separator、末尾第二条 token、空/comment-only 输入均拒绝。分号位于 string/quoted identifier 内时不得被误分割，注释也不能拼接或隐藏第二条语句。lexer 输出的 ASCII keyword token 只按 ASCII case-fold 分类，原始 bytes 仍原样送入 checksum 与执行。

v1 generic migration statement allowlist 固定为 `CREATE TABLE`、`CREATE [UNIQUE] INDEX`、`CREATE VIEW`、`ALTER TABLE`、`DROP TABLE|INDEX|VIEW`、`INSERT`、`UPDATE`、`DELETE`；`WITH`/`SELECT`/`EXPLAIN`/`REPLACE` 等未列首部默认拒绝，未来扩展必须先更新 ADR 与恶意语法测试。`BEGIN`、`COMMIT`、`END`、`ROLLBACK`、`SAVEPOINT`、`RELEASE`、`ATTACH`、`DETACH`、`VACUUM`、generic `PRAGMA`、`CREATE TRIGGER`、`CREATE TEMP|TEMPORARY ...` 与 `CREATE VIRTUAL TABLE` 一律拒绝。admission parser 还必须按每种 allowlisted grammar 提取被创建/修改/删除的目标 schema/object；只接受 unqualified target 或未引用的 ASCII token `main.`，拒绝 `temp.`、任何 attached/其他 schema 以及 quoted schema qualifier。migration connection 的 `PRAGMA database_list` 只能有 `main`/`temp`，且 `temp.sqlite_schema` 在每条 statement 前后都必须为空，防止 unqualified name 被 temp object 遮蔽；任一跨 migration temp/attached 依赖都 fail closed。真实 Version 1 的精确第二条 `PRAGMA application_id = 1095586630` 是唯一例外：它必须位于固定 index、raw bytes 精确匹配，并由 runner 的常量专用操作执行，绝不进入 generic `Exec` 路径；任何其他位置、大小写、空格、注释或值都拒绝。

外层 transaction 及其 `COMMIT`/`ROLLBACK` 只由 runner 的固定常量操作控制。每条 generic statement 执行前后由 runner 创建并释放固定名内部 SAVEPOINT；`Exec` 或 `RELEASE` 任一失败都使外层 transaction 失败并进入 rollback/fail-closed 路径。该 guard 不依赖 modernc 未公开的 autocommit/transaction-state；契约测试用第二个 connection 的 writer lock 证明从 `BEGIN IMMEDIATE`、每条 statement、history/attempt 更新直到唯一 runner-owned `COMMIT` 期间事务始终未被 statement 提前结束。

固定 golden vector 是 Version `1`、Name `init`、唯一 statement `CREATE TABLE jobs(id INTEGER PRIMARY KEY);`。它**仅用于 checksum encoder 的 synthetic conformance test，不是可提交或执行的项目 Migration Version 1**；后述 bootstrap validator 必须拒绝把它当成真实 v1。其 97-byte 编码 hex 为：

```text
616d736674702d736368656d612d6d6967726174696f6e2d763100000000000000000100000004696e697400000001000000000000002a435245415445205441424c45206a6f627328696420494e5445474552205052494d415259204b4559293b
```

对应 SHA-256 必须恰好为 `e5e82c0a4bc1d54a3a4091ce62177b04d3cce9d82925c1ef9a3902f3b99bd122`。更改 magic、字段顺序、整数宽度/端序、限制或规范化规则必须新增 checksum format/ADR；不能静默重算既有历史。

数据库内维护项目自己的 `schema_migrations(version INTEGER PRIMARY KEY, name TEXT NOT NULL, sha256 TEXT NOT NULL, applied_at TEXT NOT NULL)`；`sha256` 只接受上述 digest 的 64-byte lowercase hex。项目的 SQLite `application_id` 冻结为 unsigned hex `0x414d5346`（ASCII `AMSF`，decimal `1095586630`）；项目不使用 header `user_version`，其值必须始终为 `0`。

每个受支持 head 还必须在二进制中带一份不可变 `SchemaContract`，覆盖 `main` 的全部持久 schema，而不只验证 `schema_migrations`。canonical bytes 以 `amsftp-schema-contract-v1\000` 开头，随后编码 application ID、user version、head，以及按 `(type,name,tbl_name)` raw-byte 排序的全部 `sqlite_schema` rows；每个可空/字符串/整数字段都用显式 tag 与 big-endian length/value 编码，排除不稳定的 `rootpage`，但保留 exact `sql` text。每张表再按稳定字段顺序编码 `table_list`、按 cid 编码 `table_xinfo`、按 id/seq 编码 `foreign_key_list`、按 name 编码 `index_list` 及其 `index_xinfo`（含 origin/partial/unique/collation/desc/key），并明确列出允许的 `sqlite_*` internal objects；任何未列 object、index、trigger、view、column、constraint、FK 或 internal object 都改变 SHA-256 并拒绝。`temp` 必须为空，attached schema 禁止。

committed contract 同时保存 canonical bytes 的 SHA-256；CI 从每个历史 fixture 重放同一编译期 migrations 生成实际 manifest，与 committed bytes/digest 双向比较，防止 validator 与 golden 同时漂移。运行时在无 sidecar 的 immutable view 或完成合法 recovery 后的同一 consistent view 重新编码 whole-main manifest，并同时要求 history head 对应的 contract digest；不能只凭 migration rows 推断 schema 正确。runner-owned `schema_migrations`、`migration_control`、`migration_attempts` 与 `migration_backups` 也没有豁免，必须匹配下述 exact DDL/row invariants。

在任何 SQLite read-write open、recovery 或 `journal_mode` 修改前，runner 必须完成 non-mutating identity preflight：

1. 取得 daemon 单实例 lock，验证 state root 真实路径/owner/mode/`owner-private` ACL，并只读完成上述 statfs/mount allowlist 判定；此时尚不得创建 WAL capability probe。pristine 的定义收紧为 final main path **不存在**且同 basename `-wal`/`-shm`/`-journal` 全部不存在；预置 zero-length、合法但空的 SQLite、`application_id=0` 或任意其他 existing final 都不自动接管，用户必须移走它或选择新的显式 state dir。
2. existing final 以 no-follow handle 读取 header；必须以 `SQLite format 3\000` 开头，offset 16..17 的 big-endian page size 必须为 4096，offset 60..63 的 big-endian `user_version` 必须为 0，offset 68..71 的 big-endian `application_id` 必须为 `1095586630`。截断、错误 magic/page size、其他 application ID 或 nonzero user version 立即判为 foreign/wrong，不能 SQLite open、recovery、WAL pragma或 capability probe。
3. project header 且没有 sidecar 时，才使用经支持矩阵证明不写原文件的 `immutable=1` main view 严格验证完整 schema/history。若存在 exact `-wal`、`-shm` 或 `-journal`，先要求每个 sidecar 是 current-euid-owned、真实 `0600` regular file、`owner-private` ACL且无 symlink，但**不得**根据忽略 sidecar 的 main-only pages判断 history prefix：checkpoint/rollback crash 可能使 main 只有在 WAL/hot-journal recovery 后才构成合法数据库。header 已确认项目归属后，sidecar recovery 与完整视图验证推迟到 capability probe通过后的 read-write open。
4. wrong/foreign path 必须在任何目录写入前结束；测试逐 byte 比较 main、比较 size/mode/owner/mtime/ctime 等 attrs，并比较父目录 listing/mtime/ctime，证明拒绝未创建/修改 sidecar或 probe。root/当前 euid 对文件的主动并发替换仍属本机信任边界，但实现必须在 preflight 后、probe/RW open 前重验 handle/path identity 与 attrs。

确认归属后启动顺序固定为：

1. identity 通过后才运行上述 WAL/locking/full-durability capability probe；probe 失败不打开 existing project，不发布 pristine final。
2. pristine flow 先以 `O_EXCL` 创建固定 singleton `.amsftp-bootstrap-v1.intent`，写入 128-bit CSPRNG generation ID，按平台 full-sync intent file并 sync parent directory；只有该目录项已持久后才能创建 temp。temp basename 由 ID 唯一派生为 `.amsftp-bootstrap-v1-<32-lowerhex>.sqlite3`。随后在 state root 以 `O_EXCL` 创建 current-euid-owned、真实 `0600` temp DB，在默认 rollback journal、`synchronous=FULL` 下执行原子 Version 1。Darwin connection 的第一批、不得触发 page read/recovery 的常量操作必须先设置并读回 `fullfsync=1`/`checkpoint_fullfsync=1`，再执行任何 schema/transaction SQL。复核 application ID/header、精确真实-v1 history/whole-schema contract、quick/FK，full-sync并关闭，确认无 hot sidecar后，才以 Linux `RENAME_NOREPLACE`/Darwin `RENAME_EXCL` no-replace 发布 final、sync parent并移除 intent、再次 sync parent。final 在 bootstrap 期间从不存在；任一步失败或重启都只在持有 singleton lock 后按 intent 指向的 exact generation、owner/mode/`owner-private` ACL/no-follow attrs清理 temp及 sidecar。invalid intent/诱饵/attrs不符 fail closed；因此连续 crash 最多遗留一个受认领 generation，不会模糊删除或无界堆积。
3. existing project 在 identity preflight 与 capability probe 后才允许 read-write open/recovery。Darwin 的 ordinary source/probe/bootstrap connection initializer 必须把 `PRAGMA fullfsync=ON` 与 `PRAGMA checkpoint_fullfsync=ON` 作为最先执行且读回 1 的 non-page-touch 操作；随后才设置并读回 `synchronous=FULL`、`foreign_keys=ON`、`trusted_schema=OFF`、有界 busy timeout等 policy，之后才允许触发 hot-journal recovery、schema read或请求 `PRAGMA journal_mode=WAL`。modernc v1.53.0 的 `NewBackup` destination 是唯一公开 API 例外：`Backup.Commit` 前拿不到其 hidden connection，故不能伪称 Step 前可读回；它必须使用 fixed、正确 percent-encoded URI，并精确带 repeated query `_pragma=checkpoint_fullfsync(1)&_pragma=fullfsync(1)&_pragma=synchronous(FULL)`。driver-version source-contract test 必须证明 `newConn/applyQueryParams` 在 `backup_init/Step` 前按序成功执行，平台 capability probe先证明这些 pragma可用；Commit返回 destination connection后立即读回 `checkpoint_fullfsync=1`、`fullfsync=1`、`synchronous=2/FULL`，任一不符就丢弃temp、绝不发布。writer journal pragma必须返回 exact lowercase `wal`。不能对unknown source发写操作，也不能在driver顺序契约、fullfsync、recovery或WAL不满足时降级。
4. 在 recovered WAL/hot-journal 的同一完整视图中严格验证 `application_id/user_version`、所有已应用 version/name/checksum、runner table invariants及对应 head 的 whole-main `SchemaContract`；历史被改写、schema 未知或数据库新于二进制时进入只读诊断。若 existing project 有 pending migrations，先按后述 attempt/backup规则准备并复用一次升级前备份，再逐条迁移。
5. 对每个待应用 migration 在同一保留 connection 上显式执行 `BEGIN IMMEDIATE`，按顺序执行已通过 lexer/admission 的 statements、写入 migration row并同步更新 durable attempt current head，然后仅由 runner `COMMIT`；任何显式错误先 ROLLBACK，再单独 durable 标记 failed并保留有界 cause。若 failed 标记本身因 disk-full/I/O 无法落盘，进程立即 fatal/read-only。重启时只有 `ready`（verified backup 已发布、尚未开始 migration）可自动进入migration；`preparing`可能代表backup中断或failed-marker未落盘，和`running|interrupted|failed`一样一律只读，必须由显式operator resume/retry重验source、temp/final backup、frozen set digest与current合法prefix，且不得新建第二份backup。
6. 达到 target head 后，先在一个 runner transaction 中验证 attempt ID/current/target/set digest 并清除 active attempt，COMMIT；接着在仍无业务连接时完成/恢复下述 retention 的 `deleting` 状态与超额 catalog/file清理，所有 catalog transaction/file delete/dir-sync 都必须在最终检查前结束。随后才依次要求 `quick_check=ok`、`foreign_key_check` 零行、`wal_checkpoint(TRUNCATE)` 返回 `busy=0, log=0, checkpointed=0`。quiesce并关闭全部 DB connections 后要求 exact `-wal`/`-shm`/`-journal` 不存在，再以 immutable main view复核 application ID、history、whole-schema contract、`migration_attempts` 为空、backup catalog一致及control无hold。immutable验证之后、daemon bind前禁止任何DB write；bind后按同一initializer重开runtime pool并再次只读核对identity/head，只有pool ready才开始accept/领取Job。attempt与retention写入都先于最终checkpoint/immutable，最终证明不会被后续catalog写作废。

`schema_migrations` 的 bootstrap 不是 checksum 历史之外的隐式 DDL。

受 checksum v1 约束的 Migration Version 1 必须把下列**精确单行 SQL**作为前六条 statements；runner 在同一个显式 `BEGIN IMMEDIATE` 中创建 runner tables、设置 application ID、执行 Version 1 的其余 domain statements、插入 Version 1 history row 并 COMMIT：

```sql
CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY CHECK(version > 0), name TEXT NOT NULL, sha256 TEXT NOT NULL CHECK(length(sha256) = 64 AND sha256 NOT GLOB '*[^0-9a-f]*'), applied_at TEXT NOT NULL) STRICT
PRAGMA application_id = 1095586630
CREATE TABLE migration_control(singleton INTEGER PRIMARY KEY CHECK(singleton = 1), upgrade_hold INTEGER NOT NULL CHECK(upgrade_hold IN (0, 1)), hold_reason TEXT, hold_attempt_id TEXT, CHECK((upgrade_hold = 0 AND hold_reason IS NULL AND hold_attempt_id IS NULL) OR (upgrade_hold = 1 AND hold_reason = 'restored_backup' AND hold_attempt_id IS NOT NULL AND length(hold_attempt_id) = 32 AND hold_attempt_id NOT GLOB '*[^0-9a-f]*'))) STRICT
INSERT INTO migration_control(singleton, upgrade_hold, hold_reason, hold_attempt_id) VALUES(1, 0, NULL, NULL)
CREATE TABLE migration_attempts(singleton INTEGER PRIMARY KEY CHECK(singleton = 1), attempt_id TEXT NOT NULL CHECK(length(attempt_id) = 32 AND attempt_id NOT GLOB '*[^0-9a-f]*'), original_head INTEGER NOT NULL CHECK(original_head > 0), current_head INTEGER NOT NULL, target_head INTEGER NOT NULL, migration_set_sha256 TEXT NOT NULL CHECK(length(migration_set_sha256) = 64 AND migration_set_sha256 NOT GLOB '*[^0-9a-f]*'), reserved_backup_basename TEXT NOT NULL, backup_sha256 TEXT, status TEXT NOT NULL CHECK(status IN ('preparing', 'ready', 'running', 'interrupted', 'failed')), error_kind TEXT, CHECK(original_head <= current_head AND current_head <= target_head AND original_head < target_head), CHECK(reserved_backup_basename = '.amsftp-backup-v1-' || attempt_id || '.sqlite3'), CHECK(backup_sha256 IS NULL OR (length(backup_sha256) = 64 AND backup_sha256 NOT GLOB '*[^0-9a-f]*')), CHECK((status = 'preparing' AND backup_sha256 IS NULL) OR (status = 'failed' AND backup_sha256 IS NULL AND current_head = original_head) OR (status IN ('ready', 'running', 'interrupted', 'failed') AND backup_sha256 IS NOT NULL)), CHECK((status IN ('interrupted', 'failed') AND error_kind IS NOT NULL AND length(error_kind) BETWEEN 1 AND 64) OR (status NOT IN ('interrupted', 'failed') AND error_kind IS NULL))) STRICT
CREATE TABLE migration_backups(attempt_id TEXT PRIMARY KEY CHECK(length(attempt_id) = 32 AND attempt_id NOT GLOB '*[^0-9a-f]*'), original_head INTEGER NOT NULL CHECK(original_head > 0), target_head INTEGER NOT NULL CHECK(target_head > original_head), migration_set_sha256 TEXT NOT NULL CHECK(length(migration_set_sha256) = 64 AND migration_set_sha256 NOT GLOB '*[^0-9a-f]*'), backup_basename TEXT NOT NULL UNIQUE, backup_sha256 TEXT NOT NULL CHECK(length(backup_sha256) = 64 AND backup_sha256 NOT GLOB '*[^0-9a-f]*'), created_at_unix INTEGER NOT NULL CHECK(created_at_unix > 0), status TEXT NOT NULL CHECK(status IN ('verified', 'deleting')), CHECK(backup_basename = '.amsftp-backup-v1-' || attempt_id || '.sqlite3')) STRICT
```

任一步失败都整体 ROLLBACK，因此不会留下“有表无 v1 row”、缺 control singleton、project ID 已变或半个 runner schema。COMMIT 后先复核 `application_id=1095586630`、`user_version=0`；以后每次打开既有库也必须在读取 migration rows 前满足这两个值。runner table 缺失但 main schema 存在任意对象必须判定为错误/未知数据库并 fail closed，不能自动建表后接管；表已存在时必须由 whole-schema contract与 row invariant 同时验证上述 exact SQL、STRICT 标志、列顺序/type/nullability/key/check、`migration_control` 恰一行且合法、`migration_attempts` 至多一行、`migration_backups` 的路径/hash/status一致性。`schema_migrations` 已存在但为空也不是全新库，不能重跑 Version 1。`temp`/attached schema 不参与持久历史且不得遮蔽 main。

不得假设 `database/sql.BeginTx` 等同 `BEGIN IMMEDIATE`；runner 使用保留 connection 的显式事务边界并通过锁竞争测试证明语义。单 writer connection 不排除有界 read connections，但所有写状态迁移都经过同一 store 层。

### WAL 生命周期与背压

Version 1 bootstrap 在任何 schema DDL 前通过 runner-owned constant operation 设置 `page_size=4096`，并把 versioned `StateMaxMainBytes=8 GiB` 换算为 `max_page_count=2097152`；COMMIT/发布后按 header与 pragma双重读回，后续每个 writer connection 都重设/读回该上限。existing project 的 raw identity 只接受 4096-byte page，已超过上限即只读诊断。所有 read transaction 都必须可取消、分页并在 2 秒内结束；长目录、历史或诊断读取不得把一个 SQLite snapshot 跨网络/终端消费生命周期保持打开。每个 connection 显式设置 `wal_autocheckpoint=1000` pages，writer 至少每 30 秒或完成一个有界写批次后尝试 non-blocking `wal_checkpoint(PASSIVE)`；无活跃 reader 的 idle 窗口尝试 `wal_checkpoint(TRUNCATE)`。

以下4/8/264 MiB预算只约束全部startup migration完成、最终验证通过后的**在线 store/runtime worker**，不冒充migration本身的上限；migration使用每version独立的`MaxMigrationWalBytes`、space preflight与version间checkpoint。在线store写入不允许任意SQL：每条只按主键/唯一键影响一个row、无trigger/cascade扇出；一个row的全部encoded values **连同所有 indexed-key bytes 合计**最多256 KiB，schema contract冻结每表最多8个secondary index entry。基于4096-byte page、8 GiB最大main DB和最坏B-tree path/overflow/index split的保守公式，每个operation执行前计算declared page/frame budget，且不得超过`WalMaxStatementGrowthBytes=4 MiB`；一个transaction最多256 operations且所有declared budgets合计不得超过`WalMaxTxnGrowthBytes=8 MiB`。每条statement与COMMIT/ROLLBACK后都通过no-follow handle读取WAL size并核对actual delta；native property/fault tests必须证明公式覆盖全部schema/operation。无法证明、实际超declared budget或未来schema改变bound时立即fatal/read-only并要求更新ADR，不能继续沿用数值。

versioned `WalSoftLimitBytes` 固定 64 MiB，达到后暂停新 read snapshot/领取 Job并优先 checkpoint。`WalHardLimitBytes` 是固定 256 MiB 的**停止触发线**：开启每个 transaction/statement前以 `current WAL + 尚未消费的 declared/reserved budget` 重验，可能越线就先 checkpoint或拒绝，不能侥幸执行。任何外部 transfer chunk 开始前必须预留其随后 progress/pause commit 的 budget；所有在跑 worker 的全局未消费 reservation 合计也包含在 8 MiB transaction-growth上限内。达到触发线后拒绝新 DB write/外部副作用，已有 worker只完成已预留的当前 chunk与对应 COMMIT，然后停在可恢复边界；意外跨线或实际 delta 违约的 statement必须 ROLLBACK（若该 statement已用预留完成外部进度则只允许对应 COMMIT），不再执行下一 statement并进入 checkpoint/read-only路径。由 8 MiB 全局 reservation封顶，物理 WAL 冻结绝对上界为 `WalAbsoluteCeilingBytes = 264 MiB`，而不是把 256 MiB 冒充不可越过的 file cap。启动发现超过绝对上界、checkpoint仍不能降到 soft limit或无法兑现已预留 pause state时，daemon进入只读诊断。checkpoint 的 busy/部分完成结果、WAL bytes、最长 reader age、worker safe-boundary和背压状态必须有界且脱敏地记录；不得手工截断、复制或删除未 checkpoint 的 WAL/SHM。

### 备份、升级与回退

- exact `migration_attempts` singleton 是 source DB 的 durable state。attempt ID 为 128-bit CSPRNG 的 32 lower-hex；`original_head`/`target_head` 创建后不可变，`current_head` 必须是冻结区间的合法 prefix。target digest 固定为 SHA-256(`amsftp-migration-set-v1\000`、BE uint64 original head、BE uint64 target head，再对 **original+1..target 的完整有序集合**逐条拼接 BE uint64 version、32-byte migration checksum、32-byte对应 `SchemaContract` digest、BE uint64 `MaxMigrationWalBytes`)。重启始终按 stored original/target 重算同一全集，不能按“当前剩余 pending”缩短集合；v2 commit 后 crash 时 digest仍与 `{v2..target}` 原集合相同，只单独把 current验证为合法 prefix。
- 除 final-absent pristine bootstrap 外，existing project 有 pending migrations时，先要求 `migration_control.upgrade_hold=0`，再在普通 store transaction 中创建或读取唯一 attempt并持久化派生 final basename；在任何 migration statement 前完成 backup。启动只有`ready`可自动推进；`preparing|running|interrupted|failed`均只读并要求显式operator resume/retry。resume必须重验exact source identity/whole-schema current head、完整set digest、temp/final backup或catalog/hash/hold marker和current prefix，且复用同一attempt/backup；active attempt与binary/set/DB不一致、backup缺失/损坏都fail closed。
- backup temp basename 唯一派生为 `.amsftp-backup-v1-<attempt-id>.sqlite3.tmp`，在同一受保护 `0700` backup dir以 `O_EXCL`/`0600` 创建。通过专用 `database/sql.Conn.Raw` 窄接口调用 modernc v1.53.0 `NewBackup`，Darwin destination使用上项 exact fixed-pragma URI，循环 `Backup.Step` 到完成并 Commit；Commit返回 hidden destination后必须先读回全部 durability pragma再做任何 sanitize/checkpoint，最后正确close。crash 后 `preparing` 只接受 exact attempt-derived temp/final：合法 partial temp因 Backup API不可跨进程续步而精确删除、dir-sync后以同名重建；合法 final则复核后接续；attrs/ACL/symlink/同时碰撞异常 fail closed。由 singleton attempt最多存在一个该 temp，不枚举删除诱饵，也不会连续 crash无界累积。
- online backup 初始会包含 source 的 active attempt，所以 destination **发布前必须净化**：在 destination 的受控 transaction 中删除且只删除 matching attempt singleton，并把 `migration_control` 精确设为 `(upgrade_hold=1, hold_reason='restored_backup', hold_attempt_id=<attempt-id>)`；随后 quick/FK、whole-schema/original-history、attempt-count=0、hold-row检查，TRUNCATE checkpoint、quiesce/close并确认无 sidecar。计算 final SHA-256 后执行 Linux fsync/Darwin `F_FULLFSYNC`，再以 Linux `RENAME_NOREPLACE`/Darwin `RENAME_EXCL` 发布、sync parent；目标存在永不覆盖/删除。source随后在一个transaction中写入exact `migration_backups` verified row、backup digest并把attempt置`ready`。任一显式backup/hash/fullsync/publish错误都不开始migration，并durable置`failed`；若尚无verified hash，只允许`current=original, backup_sha=NULL, error_kind=<bounded>`的pre-backup failed row，后续绝不自动重试。marker本身写失败则保守停止且下次不得把异常preparing当成功；不做raw DB/WAL/SHM copy。
- 每个 migration开始前把 attempt durable置 `running`，并在无业务连接时 checkpoint；free-space preflight要求可用bytes至少为`expected online-backup bytes + max(pending MaxMigrationWalBytes) + 64 MiB`，所有算术overflow fail closed。每条statement后、runner-owned COMMIT前重验实际WAL：超声明可ROLLBACK并只读；若只有COMMIT frame后才观察到超声明，则该version因history+attempt current同事务已完整提交，必须保留该合法prefix、立即只读且绝不进入下一version，不能声称rollback已提交结果。每个预算内完整version后TRUNCATE再进入下一version。显式statement错误rollback后另起transaction写`failed`；marker写失败只能留下`running`，running重启绝不自动执行。全部target commits后按“清attempt→retention→最终检查/checkpoint/immutable”收口。
- `BackupRetentionCount=2`：active attempt引用的 backup永不计入删除候选也永不删除；无 active attempt且新 head完整验证后，只保留 `migration_backups` 中最近两个 verified immutable backups。超额项先以 transaction置 `deleting`，再按 catalog exact basename+hash+owner/mode/ACL/no-follow重验后删除并 sync dir，最后删除 catalog row；crash按 `deleting` 精确续接。未知文件/attrs不符不碰，唯一/最新可回退副本不为腾空间静默删除。创建新 attempt前若在该 retention与space preflight下仍不足，阻止 migration并提示用户显式归档/删除，不牺牲唯一 rollback copy。
- SQLite Version 4 `job_history_retention` 冻结一个带 Job FK 的 singleton claim。1.0 Job-history policy 不是用户可弱化的配置：保留按 `(updated_at_unix,job_id)` 排序最新的 1,000 个 terminal Jobs，并保留 30 天内全部 terminal Jobs。每次 reconcile 最多 64 个 `BEGIN IMMEDIATE` transaction；每个 child table 每次最多删 8 行，单次 statement WAL budget 4 MiB、每个 transaction 总 budget不超过 6 MiB。claim先单独 durable，再按 checkpoint/conflict/event/result/step/dedup 分块，最后同一 transaction 精确删除 claim、terminal Job 和其唯一 Plan；进程死亡续接同一 claim，不枚举或积累删除队列。nonterminal、任意 `edit_session_jobs`、upload cache reference、active/uncertain upload lease 均阻止选择；每个删除 transaction 重验这些条件，claim 后 cache owner 也禁止新增 upload reference。daemon 在接受工作前先恢复 interrupted Jobs，再续接一次 retention，失败保持持久写入不可用而不删除被引用数据。
- 发布的 backup 自带 `upgrade_hold=1` 且无 active attempt；显式 restore 必须验证 catalog/hash/identity/original schema，使用 no-replace/全耐久发布并再次确认 hold。恢复后的普通启动保持只读，不会自动重入原 attempt或再次升级；只有单独的显式“解除恢复 hold 并开始新 attempt”操作才能继续，且会重新做 backup。migration失败同样只读，不自动删除、重命名或重建 source DB/WAL。
- 较新 schema 一律拒绝旧二进制写入。二进制回退只有在 release notes 声明兼容，或恢复上述已验证且带 hold 的升级前 backup时允许。生产版本不执行 Down migration；修复通过新的前向 version完成并保留完整历史。

## 替代方案

### `github.com/mattn/go-sqlite3`

项目成熟，但要求 CGO、C 编译器和四目标交叉工具链，会破坏当前 `CGO_ENABLED=0` 发行模型，故不采用。只有 modernc 在受控真实负载出现可复现的正确性/性能阻断时，才可通过新 ADR 整体切换；不能双 driver 掩盖差异。

### `github.com/pressly/goose/v3`

goose 功能成熟，但当前 module 同时带入多数据库/CLI 生态，且项目仍需自行加 checksum、`BEGIN IMMEDIATE`、备份和 newer-schema 策略。对单一内嵌 SQLite store 收益不足以抵消依赖面，故不采用。

### `golang-migrate` 或原始 SQL 文件分割

前者同样增加通用 driver 抽象；后者若自行按分号分割会错误处理 trigger、字符串和注释。显式 statements 保持实现小、确定、可测试。

## 安全与可测试性检查

- 驱动 intake 必须在 darwin/linux × amd64/arm64 的 `CGO_ENABLED=0` 构建中通过，并在 macOS/Linux 原生运行数据库测试。
- 在每条 statement 前后、migration/history/attempt row 写入前后和 runner-owned commit 边界注入进程终止；单个 version只能整体旧/新，跨 version crash只能停在冻结集合的完整合法 prefix，`running` 启动必须只读等待显式 resume。
- checksum 单元测试必须逐 byte 断言上述 97-byte golden 编码与 digest，并覆盖 BE version/name length/count/statement length、slice order、raw whitespace/CRLF 变化、非法 UTF-8/NUL、空/超长 name、空/超长/超量 statement、总长与整数 overflow；不同实现只能以同一 golden 判定兼容。
- SQL admission测试覆盖 multi-statement/tail、第二分号、trailing comment、comment bypass/unclosed comment、single/double/backtick/bracket quote与 quoted semicolon、unclosed quote、COMMIT/ROLLBACK/SAVEPOINT、ATTACH/DETACH、VACUUM、危险/变形 PRAGMA、trigger/temp/virtual table、unknown首部；覆盖 `temp.x`/quoted temp/attached schema/unqualified temp shadow跨 migration。只有 exact v1 app-id special op可过，第二 connection证明每条 statement后 outer writer lock/transaction仍在，恶意 fixture不得改变schema/data/history/attempt。
- identity/bootstrap 测试覆盖 final+exact sidecars全不存在的 pristine、预置 zero-byte/valid-empty appid0/foreign DB拒绝、wrong appid/user-version/page-size/header；wrong path 的 main bytes+size/mode/owner/mtime/ctime 与父目录 listing/mtime/ctime全部不变。bootstrap intent需覆盖 file fullsync/dir-sync、temp建表/app-id/runner rows/v1 row/COMMIT/whole-schema/fullsync/close/no-replace/dir-sync各崩溃边界，final只能不存在或完整有效；连续 crash最多一个 generation，诱饵/attrs异常不删。existing project无 sidecar用 immutable strict validation，有 WAL/SHM/hot journal时先probe/recovery再严格验证，checkpoint/rollback crash不能以main-only mixed pages误拒。
- filesystem gate覆盖 APFS、ext4、XFS allowlist；Linux `0xEF53`+mountinfo exact ext4与ext2/ext3拒绝、XFS magic/type mismatch；NFS/SMB/CIFS/FUSE-network/unknown/statfs或mountinfo解析失败。probe必须在identity后，双 connection autocheckpoint=0、父保持打开、WAL清空后marker frame真实存在、child从WAL可见、锁busy/release、shared-memory/fsync/child crash。Darwin ordinary source/probe/bootstrap证明 initializer读回发生在page-touch/recovery/journal-mode前；backup destination另以modernc v1.53.0源码契约证明fixed URI pragmas在`backup_init/Step`前应用，并在Commit返回后读回，覆盖URI变形、driver版本漂移、pragma/F_FULLFSYNC失败。
- whole-schema测试逐head双向比较committed canonical bytes/digest与runtime `sqlite_schema`/table_xinfo/FK/index metadata，覆盖额外/缺失/改写table/index/view/trigger/internal object、runner exact DDL/control singleton/attempt/catalog invariant；history合法但domain schema tampered也必须拒绝。
- backup/attempt测试覆盖 pristine不备份、无 pending不建attempt、single/multi pending仅一个deterministic temp/final；target digest固定original+1..target全集，v2 commit crash后重算仍相同且current为prefix。仅ready可自动推进；preparing/running/failed及任一failed-marker写失败均0自动执行/0新backup，显式resume复用。覆盖binary/set/budget/schema-digest变化、marker/catalog/backup缺失损坏、backup destination删除attempt+设置hold、restore后不自动重入，以及success清attempt→retention→quick/FK/TRUNCATE/close/immutable、sidecar残留与no-replace/fullsync/dir-sync碰撞。
- backup retention/space测试覆盖多轮升级最多保留两个verified加active、第三份成功后的deleting crash续接、quota/disk-full/overflow、active/sole/latest/attrs异常/诱饵不删；backup temp连续crash有界。space不足必须在migration前阻断且不静默删除唯一rollback。
- 覆盖两个 daemon 竞态、busy timeout、磁盘满、只读目录、损坏 WAL、checksum 改写、version 缺口和 newer schema；备份测试必须在 WAL 中存在未 checkpoint 数据时证明 online backup 完整，并拒绝 raw-file copy、损坏备份和目录 fsync/rename 故障。
- WAL生命周期覆盖4096 page/8 GiB max、2秒reader deadline、1000-page autocheckpoint、30秒/批次PASSIVE、idle TRUNCATE、64 MiB soft、256 MiB停止线、4 MiB statement/8 MiB reservation/264 MiB绝对 ceiling；覆盖预算预留、多个worker安全chunk、commit/rollback实际delta、长期reader/checkpoint busy/磁盘满。任何分支不得手工截断/删除WAL、超绝对界继续写或hard后领取Job/启动外部chunk。
- 每次 migration fixture 从 1.0 最早支持 schema 逐版本升级，并执行 `foreign_key_check`/`quick_check` 与领域数据断言。
- SQL 参数必须参数化；migration statement 是受审查的编译时数据，不接收用户字符串或环境替换。

## 后果

- Stage 2 需要维护一个很小但关键的 migration runner；其边界比通用框架更窄，必须用崩溃矩阵补足。
- pure-Go driver 保持当前四目标交叉构建，不代表原生数据库行为可只靠交叉编译证明。
- schema 历史不可改写；开发期修正已进入共享候选的 migration 也必须新增 version。
- Stage 0 不添加 SQLite module，也不提供持久化；这里冻结的是 Stage 2 的兼容选择。

## 参考依据

- [`modernc.org/sqlite` canonical repository](https://gitlab.com/cznic/sqlite) 与 [v1.53.0 module](https://gitlab.com/cznic/sqlite/-/raw/v1.53.0/go.mod)
- [`modernc.org/sqlite` online `Backup` API](https://pkg.go.dev/modernc.org/sqlite@v1.53.0#Backup)
- [SQLite transaction semantics](https://sqlite.org/lang_transaction.html)
- [SQLite PRAGMA reference](https://sqlite.org/pragma.html)
- [SQLite write-ahead logging](https://sqlite.org/wal.html)
