# Stage 0 — Foundation & Knowledge

- **状态**：Complete
- **阶段类型**：工程与知识基线
- **前置条件**：已批准的产品设计、架构决策和阶段路线
- **完成后进入**：仅在本阶段本地、双平台 CI 与 cold-start 门禁全部通过后进入 [Stage 1 — Read-only Explorer](01-read-only-explorer.md)

完成检查点：Tasks 1–11、独立审查、两轮 cold-start audit、本地 Go 1.26.5/1.25.12 门禁与污染检查均已完成。首轮 Hosted run `29394164471` 暴露的 GNU Make 4.x 安全 `-I` 路径误判已有回归测试和最小修复；修复提交 `cf8e6ef…`、树 `e70a8f0…` 的替代 Hosted run [`29394698864`](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29394698864) 23/23 作业通过。Stage 0 已完成，Stage 1 已解锁但尚未开始。详细证据见 [Stage 0 verification](../verification/stage-00.md)。

## 1. 阶段目标

建立后续六个阶段共同依赖的最小工程底座和长期知识契约。本阶段的产物必须让一个没有历史会话的新开发者，仅按固定阅读顺序就能理解产品边界、找到当前状态、运行验证并安全开始 Stage 1。

本阶段不追求用户可用功能；它追求的是所有后续功能都有稳定的契约、测试替身、质量门禁和事实来源。

## 2. 范围

### 2.1 包含

1. 建立 Go 工程骨架和 macOS/Linux 构建、格式化、静态检查、单元测试入口。
2. 定义一个二进制四角色运行的边界：交互 client/TUI、后台 daemon、短生命周期 askpass broker 入口、可选远端 helper；此阶段只建立 dispatch 边界，不实现完整角色行为。
3. 定义客户端与守护进程之间的版本化消息信封、请求标识、错误结构、事件顺序和能力协商规则。
4. 定义不绑定具体实现的文件系统 Provider/Endpoint 契约，包括：位置、条目、分页/流式枚举、能力、元数据、读取句柄、错误分类和取消。
5. 提供可脚本化的 Fake Provider，用于模拟慢响应、分页、陈旧元数据、短读、短写、权限错误、断线和能力差异。
6. 建立测试分层与夹具规范，明确哪些行为由单元、契约、集成、故障注入、规模和平台冒烟测试覆盖。
7. 建立可续接的文档真相链和自动一致性检查。

### 2.2 非目标

1. 不连接真实 SSH/SFTP 服务器。
2. 不提供可用的双栏 TUI、文件预览或传输。
3. 不实现 SQLite 作业数据库、远端 Helper 或直传。
4. 不提前决定所有内部包结构；只有跨阶段共享的行为契约需要稳定。
5. 不以一次性的脚本或口头约定代替可重复执行的验证入口。

## 3. 依赖与已冻结约束

### 3.1 外部依赖

- Go 源码/module 语义基线已冻结为 1.25.0；生产/稳定工具链为 1.26.5，oldstable 门禁为 1.25.12，均不得自动切换。变更必须新增 ADR。
- GitHub Actions 原生门禁已冻结为 `ubuntu-22.04`、`ubuntu-24.04`、`macos-15` 与 `macos-15-intel`；交叉构建另覆盖 darwin/linux × arm64/amd64，且不能替代原生证据。

### 3.2 已冻结的产品约束

- 目标平台是 macOS 与 Linux 终端用户。
- 认证真相源是系统 OpenSSH；项目不自行保存密码、私钥或 Kerberos 票据。
- 长任务归后台守护进程所有，TUI 是可离线、可重连的客户端。
- 标准 SFTP 是永远可用的基线；Helper 只能增强，不能成为浏览和基本传输的前置条件。
- 所有规模敏感接口都必须支持取消和有界消费，不能要求把整棵树一次性载入内存。

### 3.3 Stage 1 前的技术与兼容门禁

批准设计中原先有意延迟到 Stage 0 的选择现已由以下 Accepted ADR 冻结；它们是兼容约束，不表示 owning stage 的运行时功能已经实现：

- [ADR-0006](../architecture/adr/0006-public-identity-toolchain-and-runtime-libraries.md)：`AMSFTP`/`amsftp`、module/Go 支持线、tcell v3.4.0、最初的 pkg/sftp v1.13.10 与 `log/slog`；SFTP pin 后由 ADR-0011 修订。
- [ADR-0007](../architecture/adr/0007-platform-paths-runtime-socket-and-ipc.md)：macOS/XDG config/state/cache/log/runtime 路径、owner-only socket 与 IPC endpoint major。
- [ADR-0008](../architecture/adr/0008-modernc-sqlite-and-forward-migrations.md)：modernc SQLite v1.53.0/libc v1.73.4、项目内前向校验 migration runner 与 WAL-safe online backup。
- [ADR-0009](../architecture/adr/0009-supported-platform-ci-and-packaging-baseline.md)：GitHub Actions、macOS 15/Ubuntu 22.04 最低支持、OpenSSH 8.9p1 测试线、发行/包标识。
- [ADR-0010](../architecture/adr/0010-helper-artifact-trust-and-distribution.md)：Helper raw-manifest Ed25519、key/artifact 撤销、freshness floor/high-water、不可变 release 与 key 轮换边界。

Stage 0 根 module 仍按本阶段范围保持标准库-only；ADR 中的第三方库在 owning stage 第一次导入时以规范精确版本进入 `go.mod`/`go.sum`，不得重新选型或使用 `latest`。任何变更需要新增 ADR 和完整依赖 intake。Helper 第一组实际 key 与 custody/恢复演练是 Stage 3 exit 门禁，在此之前禁止 helper 下载、上传或安装实现进入仓库。

## 4. 详细交付物

### S0-D01 工程与命令基线

- Go module、最小可构建入口以及角色选择机制的最小契约。
- 统一的格式化、静态检查、单元测试、race 测试和构建命令。
- macOS/Linux CI 矩阵或等价自动化定义。
- 开发环境说明，包括所需工具版本和首次验证步骤。

验收重点是“任何人可重复运行”，而不是目录数量或框架复杂度。

### S0-D02 领域词汇和标识规则

必须定义并在文档与代码中统一以下术语：

- **Endpoint**：一个可访问的文件系统边界，例如本机或某个 SSH 主机别名。
- **Location**：Endpoint 标识与该端点内规范化路径的组合。
- **Capability**：端点在当前会话中明确声明的操作能力。
- **Pane State**：单栏当前 Endpoint、Location、选择、排序和视图状态。
- **Workspace**：两个 Pane State 与工作区级策略的可恢复集合。
- **Operation Intent**：用户动作形成、尚未执行的操作意图。
- **Job**：由守护进程持久化并执行的操作计划。

同时确定稳定 ID、请求 ID、作业 ID 和文档功能 ID 的生成与显示要求。具体编码格式可保持实现可替换，但必须具备唯一性、可日志关联和不可由路径直接推断等性质。

### S0-D03 版本化 IPC 信封

定义客户端—守护进程协议的最小行为：

- 协议版本与能力集合如何协商。
- 请求、响应、异步事件和取消如何关联。
- 错误如何保留机器可判定的类别和面向用户的上下文。
- 客户端断开后，哪些请求取消、哪些后台作业继续。
- 事件序列号、重连后的快照/补偿和重复事件处理规则。
- 未知字段、未知事件和版本不兼容时的安全行为。

协议序列化已由 ADR-0005 冻结并实现为 4-byte uint32 big-endian 长度前缀、最大 8 MiB 的 JSON payload；envelope/control fields 与原始 JSON payload strict UTF-8，原始路径 bytes 使用 base64，事件游标使用 epoch+sequence，Code/Retry/Effect 是 fail-closed canonical 枚举。任何 wire-format 改动必须先经版本化 ADR 与兼容/失败契约测试，不得在后续阶段重新选型。

### S0-D04 Endpoint/Provider 契约

契约至少覆盖：

- Endpoint 身份、显示名称、连接状态和能力查询。
- 路径规范化、根边界、符号链接元数据与路径展示。
- 增量目录枚举、排序提示、分页/背压、取消和部分结果。
- 有界范围读取及后续写入阶段所需的扩展边界。
- 可比较的文件指纹字段；缺失字段必须显式表达，不能伪造精度。
- 统一错误类别：不存在、已存在、无权限、认证、连接、超时、不支持、冲突、资源不足、取消和内部错误。
- 能力在连接重建后可能变化，调用方不得永久缓存未版本化能力。

本阶段不要求接口一次覆盖所有未来能力；要求扩展方式不破坏现有调用方。

### S0-D05 Fake Provider 与确定性时钟

Fake Provider 必须允许测试控制：

- 任意目录树、文件内容、元数据和能力组合。
- 分页大小、返回顺序、延迟、取消点和背压。
- 第 N 次调用失败、短读/短写、陈旧 `stat`、权限变化和断线。
- 可注入时钟与 ID 生成器，避免测试依赖真实时间与随机顺序。
- 调用记录，便于断言行为而非内部实现。

Fake 的行为必须由同一套 Provider 契约测试约束，防止测试替身与真实 Provider 语义漂移。

### S0-D06 文档真相链

建立并写明以下固定阅读顺序：

1. `PROJECT_STATE.md`：当前阶段、最近验证、阻塞和下一动作。
2. `IMPLEMENTATION_PLAN.md`：所有阶段状态与门禁索引。
3. `docs/product/feature-matrix.md`：功能 ID、归属阶段、状态与证据。
4. 当前 `docs/stages/NN-*.md`：本阶段范围、里程碑和退出标准。
5. 当前 `docs/verification/stage-NN.md`：候选标识、最近绿色验证、缺失证据和下一门禁。
6. 当前阶段引用的 ADR、接口与安全文档。
7. 完整工作树状态/清单与最近一次绿色验证命令。

完成定义：代码、测试、功能矩阵、验证记录和 `PROJECT_STATE.md` 五者一致，功能才可标为完成。

### S0-D07 ADR 与验证记录规范

- ADR 记录问题、选择、备选方案、后果和变更条件，不把讨论过程当结论。
- Verification 记录环境、提交/工作树标识、命令、结果、已知未覆盖项和证据位置。
- 阶段内的实现选择若改变进程边界、持久化格式、安全模型或兼容性，必须先新增或更新 ADR。
- 文档不得留下无归属的待办标记；未做能力通过功能矩阵状态和阶段计划表达。

## 5. 里程碑顺序

### M0.1 — 建立可重复工程入口

1. 固定 Go 与工具链支持策略。
2. 建立最小构建和测试入口。
3. 让 macOS/Linux 自动验证均能执行。

门禁：空功能工程也能在两平台完成构建、测试、race 和静态检查。

### M0.2 — 冻结共享领域契约

1. 写领域词汇和标识规则。
2. 定义 IPC 信封与错误语义。
3. 定义 Provider/Endpoint 和能力模型。

门禁：协议与 Provider 契约测试可在没有真实 SSH 的情况下执行。

### M0.3 — 建立测试替身和故障模型

1. 实现 Fake Provider 和确定性依赖。
2. 为慢、断、错、取消和部分结果建立表驱动夹具。
3. 证明客户端侧概念代码不会假设一次性完整结果。

门禁：Fake 与契约测试可复用于 Stage 1–5，不需要复制测试逻辑。

### M0.4 — 固化知识续接流程

1. 建立真相链文档。
2. 建立文档链接、状态和功能矩阵一致性检查。
3. 用一次“冷启动续接演练”验证新会话可找到下一动作。

门禁：仅阅读规定文件即可复述产品边界、当前阶段、未完成项和验证方式。

## 6. 可验证退出标准

- [x] macOS 与 Linux 的支持环境均完成构建和基础测试。
- [x] `go test ./...`、`go test -race ./...`、`go vet ./...` 通过。
- [x] IPC 信封具备成功、结构化错误、取消、未知字段和版本不兼容测试。
- [x] Provider 契约覆盖增量枚举、取消、部分结果、错误分类和能力变化。
- [x] Fake Provider 能确定性复现至少慢响应、短读/写、断线、权限和陈旧元数据。
- [x] 文档检查能发现断链、非法阶段状态、缺少功能 ID 证据等问题。
- [x] Stage 0 有意延迟的身份、依赖、TUI/日志、路径、SQLite、CI/包装、最低系统与 Helper 信任选择均有 Accepted ADR，且文档检查强制 Stage 0–6 唯一有序状态链。
- [x] 完成一次由未参与设计的人或新会话执行的续接演练，并记录发现的问题。
- [x] `PROJECT_STATE.md` 明确将当前阶段切换为 Stage 1，且 Stage 0 验证记录可追溯。

## 7. 测试矩阵

| 层级 | 场景 | 必须证明 | 证据 |
|---|---|---|---|
| 构建 | macOS、Linux | 同一源码可构建，平台差异被隔离 | CI/本地验证记录 |
| 单元 | 路径、标识、错误映射 | 边界条件确定、错误保留上下文 | 测试报告 |
| 契约 | IPC 信封 | 兼容、取消、重连语义明确 | 协议契约测试 |
| 契约 | Provider/Fake Provider | 所有 Provider 遵循同一行为 | 共享契约套件 |
| Race | 并发取消、事件订阅 | 无数据竞争或死锁 | race 报告 |
| 文档 | 链接、状态、矩阵字段 | 真相链可自动检查 | docs-check 输出 |
| 人工演练 | 新会话冷启动 | 无聊天历史也能继续 | Verification 记录 |

## 8. 失败与回滚策略

- 已冻结工具链或精确依赖在任一平台不可用时，先保留失败证据和最小接口；替换版本/依赖必须新增 ADR、完成双 Go/四目标/许可证/漏洞 intake，不能临时换库或把单平台特例泄漏进领域契约。
- 契约设计被真实实现证明不充分时，以新增向后兼容字段或能力为先；若必须破坏，Stage 1 开始前可提升协议主版本并记录 ADR。
- CI 不稳定时，不能把失败测试静默跳过；隔离环境依赖，保留本地可重复命令和失败证据。
- 文档自动检查误报时，只调整规则或文档结构，不删除真相链门禁。
- 本阶段没有用户数据迁移；回滚应能恢复到上一个绿色工程骨架，不保留半生效生成物。

## 9. 完成时必须更新的文档

- `PROJECT_STATE.md`：阶段结果、最后绿色命令、已知限制、Stage 1 第一动作。
- `IMPLEMENTATION_PLAN.md`：Stage 0 状态与实际测试命令。
- `docs/product/feature-matrix.md`：Foundation 类功能状态和证据。
- `docs/architecture/overview.md`：实际工程边界与术语链接。
- 相关 ADR：工具链、IPC、Provider 契约的重要选择。
- `docs/verification/stage-00.md`：平台、命令、结果、未覆盖项。

## 10. 下一阶段交接信息

Stage 1 开始前，交接记录必须明确：

1. 当前支持的 Go、macOS、Linux 版本。
2. Stage 1 必须机械采用的 tcell/SFTP 精确版本、`log/slog` 约束与平台路径 ADR。
3. 如何启动 Fake Provider 场景和运行共享契约测试。
4. IPC 与 Provider 契约的稳定部分、允许扩展点和禁止假设。
5. 尚未实现但 Stage 1 必须填充的角色入口。
6. SSH/Kerberos 集成测试所需的环境、凭据边界与可用夹具。
7. 最后一次绿色验证的命令和证据路径。

若以上任一项缺失，Stage 1 不得标记为 In Progress。
