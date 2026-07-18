# Stage 6 — Hardening & 1.0 Release

- **状态**：In Progress
- **阶段类型**：兼容、安全、发行与支持
- **前置条件**：[Stage 5 — Direct Transfer & Scale](05-direct-transfer-scale.md) 已通过退出门禁
- **完成后进入**：1.0 维护与后续路线评审
- **执行计划**：[Stage 6 execution plan](06-hardening-release-plan.md)
- **证据账本**：[Stage 6 verification](../verification/stage-06.md)

## 1. 阶段目标

把已通过端到端和规模验证的功能收敛为可安装、可升级、可配置、可诊断、可维护的 macOS/Linux 1.0。阶段完成的标准不是“打出一个二进制”，而是功能矩阵没有无法解释的缺口，安全边界经负向审查，支持矩阵和迁移承诺清楚，发行物能在干净机器上安装并从已支持预发布版本升级。

本阶段允许修正硬化中发现的问题，但不新增未经阶段设计的核心功能；任何范围扩张必须进入后续路线而不是挤入 1.0。

## 2. 范围

### 2.1 包含

1. 稳定配置 schema、加载优先级、校验、默认值、弃用和错误诊断。
2. 完整可重映射键位与 Vim-first 默认键位；冲突检查和恢复默认能力。
3. CLI 参数、工作区、非交互状态/诊断命令、shell 补全和 man page。
4. IPC、SQLite、工作区、缓存和 Helper 的版本/迁移/兼容策略。
5. macOS/Linux 发行物、校验和、版本信息和至少一个受支持安装渠道/路径。
6. 安全威胁模型、权限/认证/命令执行/Helper 供应链/临时文件/日志审查与修复。
7. OS、架构、终端、OpenSSH、SFTP Server、认证方式和 Helper 的兼容矩阵。
8. 诊断命令、支持包、健康检查、日志级别和隐私/脱敏规则。
9. 全量自动化、故障、规模、长稳、安装、升级、回滚和发布候选验证。
10. 用户、管理员、贡献者与发布文档。

### 2.2 非目标

1. 不新增 Windows、GUI、移动平台或嵌入式终端。
2. 不新增双向同步、云存储 Provider、插件市场或永久远端索引。
3. 不为赶发布而弱化、跳过、静默忽略失败测试。
4. 不承诺未进入兼容矩阵的 OS、架构、终端或服务端。
5. 不自动收集或上传遥测、文件名、内容、凭据或日志；若未来考虑必须独立设计和授权。

## 3. 1.0 发布原则

### 3.1 阶段依赖

- Stage 0–5 均已通过各自退出门禁，相关 Verification、ADR、功能矩阵证据和 `PROJECT_STATE.md` 可追溯。
- 现有 IPC、SQLite、工作区、缓存与 Helper 版本必须可枚举，真实历史夹具必须在修改迁移代码前冻结。
- 50k/1M/100GB 规模夹具、SSH/Kerberos 环境和 macOS/Linux 兼容环境可重复运行；缺失环境先作为发布阻塞处理。

### 3.2 发布原则

1. **默认安全**：无 Helper 仍可用；直传默认不扩权；覆盖/删除/编辑回传继续显式确认。
2. **兼容可解释**：协议、配置和存储版本不兼容时拒绝危险操作并给出升级/回滚路径。
3. **升级可恢复**：迁移前备份、迁移事务化、失败不覆盖最后可用状态。
4. **发行可验证**：每个二进制可显示版本/构建信息，发布清单和校验和可验证。
5. **支持可复现**：诊断包含环境和关联事件但默认脱敏，不需要用户暴露秘密或完整文件内容。
6. **文档即契约**：功能矩阵、配置参考、兼容矩阵和验证记录与实际 1.0 一致。

## 4. 详细交付物

### S6-D01 配置体系

- 明确系统级（若支持）、用户级、工作区级、环境变量和 CLI 参数的优先级。
- 配置 schema 有版本、类型、范围、默认值和未知字段策略；错误指出文件、字段和修复方法。
- 敏感值不鼓励写入配置；SSH 认证继续引用系统 OpenSSH 环境。
- 覆盖缓存配额、并发/带宽、重试、预览限制、外部程序、搜索预算、Helper 和直传策略。
- 配置热加载只作用于明确安全的项；影响 Job 语义的值在规划时冻结。
- 提供验证/打印有效配置命令，输出默认脱敏。
- 弃用项在至少一个有记录的过渡周期内给出替代方案，1.0 前清除无意遗留别名。

### S6-D02 键位与交互可访问性

- Vim-first 默认完整记录：导航、Normal/Visual、标记、`y/d/p`、实际删除、重命名、重复、搜索、抽屉、编辑/打开、命令和 shell。
- 支持按上下文重映射，启动时检测不可达动作、重复冲突和危险动作误绑。
- 用户可查看有效键位、恢复默认和导出配置。
- 数字计数、`.` 重复和 Visual 选择的适用/禁止范围明确；1.0 不承诺宏和命名寄存器。
- 颜色、无颜色、窄终端、低能力终端和基本屏幕阅读输出有退化策略。
- 所有确认都能显示对象范围与默认安全选项，不只依赖颜色区分风险。

### S6-D03 CLI、补全与 man page

- 稳定 Location/Workspace 启动语义、daemon 管理、Job 查询/控制、Helper 管理、配置验证和诊断入口。
- 退出码按成功、用户取消、配置、认证、网络、冲突、部分完成和内部错误可区分。
- Shell 补全至少覆盖项目正式支持的主流 shell，且不会为了补全触发远端写入或认证洪泛。
- man page 与 `--help` 来自同一行为事实源或有自动一致性检查。
- 非交互输出可选择人类/机器可读格式；机器格式版本化且错误仍走确定通道。
- 破坏性 CLI 操作需要与 TUI 等价的计划/确认，自动化场景只能通过显式策略选择而不是隐式 yes。

### S6-D04 持久化与迁移

覆盖：

- Job SQLite schema 与事件历史。
- 工作区和配置 schema。
- 缓存索引、内容目录和编辑租约。
- Helper manifest、已安装版本和能力缓存。

要求：

- 每种格式有版本和支持范围；迁移只能从文档列出的来源版本执行。
- 迁移前生成可识别备份或恢复点，检查空间并记录步骤。
- 迁移事务化或分阶段可恢复；中断后能继续或回到最后安全状态。
- 新版不应让旧版误写未知格式；必要时使用不可逆版本门禁并在升级前提示。
- 提供只读检查、备份、修复/导出入口；损坏时不静默清空。
- 使用真实历史夹具测试，而不是测试运行时即时生成“旧格式”。

### S6-D05 发行物与安装渠道

- 产出支持矩阵中 macOS/Linux、amd64/arm64 的发行二进制或明确排除并说明依据。
- 每个归档包含许可证/通知、版本信息、安装/卸载说明；release 同时发布精确命名的 `checksums.txt`、`sbom.spdx.json` 和 GitHub artifact attestation/provenance。
- 首发渠道已由 ADR-0009 固定为不可变 GitHub Release，资产名为 `amsftp_<version>_{darwin,linux}_{arm64,amd64}.tar.gz`；第二渠道是名为 `amsftp` 的 Homebrew formula，只引用不可变 release URL/hash。两条路径都纳入干净机测试。
- darwin arm64/amd64 binary 在打包前使用 Developer ID Application、hardened runtime 与 timestamp 签名，并以 byte-identical 临时 ZIP 获得 Apple notarization `Accepted`；Accepted 后才冻结同一 platform binary、生成并离线签署 Helper manifest，再生成包含 byte-identical binary 的 tar/checksum/SBOM/attestation。Linux Helper manifest 同样只绑定最终 unsigned release binary。Developer ID/notary 凭据仅存在于受保护 release environment，PR/普通构建无权限，任何失败都阻止 macOS 正式 release。
- 安装不要求 root 才能运行核心功能；系统级包可使用平台标准权限但不创建常驻远端服务。
- 用户级守护进程标识固定为 launchd `io.github.tyrantlucifer.amsftp.daemon` 与 systemd `amsftp-daemon.service`；它们是同一产品的服务标识而非额外产品名，启动、升级停机、Socket 清理和多版本冲突遵守安装器无关规则。
- Helper 产物与主客户端发布同源、版本可追溯；仍需用户逐 Endpoint 明确批准安装。
- 发布流程生成变更说明、校验和、SPDX SBOM 和构建 provenance/attestation；应用/包标识固定为 `io.github.tyrantlucifer.amsftp`，签名/公证按分发渠道要求执行。

当前 M6.2 执行证据只完成了确定性 formula renderer：它要求显式审核过的单一 SPDX license 和 exact four canonical archives，按 macOS/Linux × ARM/Intel 绑定不可变 URL/hash，安装 binary/man/completion 并冻结 version smoke；不发布 formula、不创建 service，也不把 Homebrew 作为 Helper 信任根。真实项目 LICENSE、最终受保护 bytes、公开 Homebrew clean install/upgrade/uninstall 仍是发布门禁，不能由 renderer 测试替代。

### S6-D06 安全审查

威胁模型至少覆盖：

- 恶意文件名、控制字符、Unicode 混淆、符号链接和路径穿越。
- TOCTOU：规划后来源/目标变化、临时文件替换、提交响应丢失。
- Unix Socket 劫持、跨用户连接、IPC 畸形/超大帧和客户端重放。
- Askpass 欺骗、提示归属、日志/诊断泄密和环境变量继承。
- 外部编辑器/预览器/打开器、`!` 和 `gs` 的参数/命令边界。
- Helper 产物篡改、版本降级、远端路径替换、协议恶意输入和能力谎报。
- 跨主机直传的认证扩权、主机身份、网络边界和目标伪装。
- 缓存/数据库/临时文件权限、备份泄密和清理范围。
- 资源耗尽：目录/帧/日志/图片/搜索/重试风暴。

所有发现有严重度、影响、修复/接受理由和验证证据。未处置高风险项阻止 1.0。

### S6-D07 兼容矩阵

矩阵至少记录：

- macOS/Linux 支持版本与架构。
- 终端：基础 ANSI、Kitty、iTerm2、Sixel 支持/降级。
- 系统 OpenSSH 版本范围和关键配置：Host、ProxyJump/Command、Agent、硬件密钥、Kerberos/GSSAPI。
- SFTP Server/扩展差异和标准基线。
- 本地/远端文件系统差异：大小写、Unicode、权限、符号链接、原子 rename。
- 主客户端/daemon/Helper 协议版本组合。
- 编辑器、系统打开器和 shell 的平台验证。

“未测试”与“不支持”必须区分；问题附可复现记录和用户可见降级。

### S6-D08 诊断与支持包

- `doctor`/等价检查验证配置、目录权限、Socket、daemon、OpenSSH、已知 Host、数据库、缓存、Helper 和磁盘空间。
- 支持包默认只含版本、平台、配置形状、脱敏日志、作业状态摘要、能力/协议和验证结果。
- 文件内容、Askpass 输入、私钥、Kerberos ticket、完整命令输出和未经同意的绝对路径默认排除。
- 用户能预览将导出的每个文件和敏感级别；支持包只写本地，不自动上传。
- 日志轮转、大小和保留期有上限；临时诊断级别自动恢复。
- 常见故障文档从诊断代码链接到认证、连接、冲突、恢复、Helper 和直传处理步骤。

### S6-D09 发布门禁与文档集

发布门禁：

- 功能矩阵每项为 `Verified`、`Deferred` 或 `Removed`，并有证据或理由；不得有无主状态。
- 所有 Stage Verification 与当前候选版本对应。
- 全量单元、契约、集成、race、fuzz、故障、规模、兼容、安装/升级和长稳满足阈值。
- 安全评审无未处置高风险，许可证/依赖审查完成。
- 干净安装、从支持版本升级、备份恢复和卸载在矩阵平台验证。
- 用户快速开始、键位、配置、认证、传输安全、缓存/编辑、Helper、直传、故障排查和隐私文档齐全。
- 发布候选冻结期间只接受阻塞缺陷修复；每个修复重跑受影响门禁。
- 两种 macOS 架构在干净 macOS 15 上以带 quarantine 的实际 release tar.gz 完成解压、strict codesign、Gatekeeper `spctl --assess --type execute` 和版本 smoke；记录 CDHash、notary submission 与二进制 byte identity。
- 四个平台逐一证明 Helper manifest `size`/`sha256` 等于最终 tar 中同一 `amsftp` binary；darwin 还必须证明 pre-sign bytes 不进入 manifest，notary Accepted ZIP、manifest hash 与最终 tar binary byte-identical。

## 5. 里程碑顺序

### M6.1 — 配置、键位与公共接口冻结

1. 盘点实际功能和所有用户可见默认。
2. 固定配置 schema、键位、CLI、退出码和机器输出。
3. 建立文档/补全/man 一致性测试。

门禁：公共行为有版本与兼容策略，功能矩阵无未知归属。

### M6.2 — 迁移、打包与干净机

1. 固定存储支持范围和真实历史夹具。
2. 建立发行构建、校验、渠道包和 provenance。
3. 在干净 macOS/Linux 环境验证安装、daemon、SSH、升级、恢复和卸载。

当前增量：Helper state v2、公共四目标 deterministic package/provenance 与 target-aware dependency closure 已有 exact-SHA Hosted。installed lifecycle evidence `d625a677f59e6e21d55516d2533db34c43d516fa` push 24/24 并完整通过受信根 v3→v4/rollback/uninstall；PR 仅初始 Stage 5 readiness 超出 bounded poll，同 SHA push 伴随证明其为 runner timing instability，未 rerun。公共 Helper 已冻结 status/install/upgrade/disable/remove 管理语法：install/upgrade/remove 必须显式接受 shared-session/stable-home，disable 禁止无关 consent，任意 manifest/artifact/path 输入在 runtime 前拒绝；restricted serve 保持独立私有 stdio 角色且不进入补全。evidence SHA `9c2c19185ede624d5d7905614bb0bc6112b90a19` 的双 quality 与完整 installed lifecycle 通过；不同 workflow 的 macOS ARM/Intel heartbeat/stderr/PTY 可见性失败均由同 SHA 对侧成功 companion 分类为既有时序不稳定。release-source commit `53fceb0bf672607ba52d47550a65b095f3e0c76e` 进一步固定四目标 archive/Helper manifest/detached signature 名称和不可变 GitHub Release URL：只先读取有界 metadata，验签/current-policy/请求 identity 通过后仍不读取 archive；Installer preliminary consent、fresh probe、high-water 通过后才惰性读取。归档只接受 exact USTAR 内容，compressed/expanded 均硬限制，binary 流式进入立即 unlink 的 0600 file，每次 reopen 都重做 size/hash，bind/Installer 重做 current policy。新的 daemon/state/SFTP owner composition 增量建立了私有 Host alias→opaque EndpointID 注册表、受限 lifecycle RPC、独立 fresh OpenSSH/SFTP owner、exact 0700 SFTP MKDIR、同一认证尝试内的 fresh formal handshake，以及基于 JobStore removal lease 的重启恢复；本地 current/oldstable 全门禁已通过，exact-SHA Hosted 尚待本批提交。production verifier/policy 仍为空，公共 mutation 命令仍在 runtime 前 exit 3，daemon 也没有最终计划确认渠道，因此生产 Helper/Level 2 继续关闭。项目 LICENSE、完整第三方通知、native/channel、signing/notary 与 final release 仍未完成，不能提前打开 Helper distribution。

门禁：支持平台不依赖开发机残留，失败升级可恢复。

### M6.3 — 安全、兼容与诊断

1. 完成威胁建模、代码/配置审查和负向测试。
2. 跑完 OS/终端/OpenSSH/SFTP/Helper 兼容矩阵。
3. 完成 doctor、支持包、脱敏和故障文档。

门禁：无未处置高风险，已知兼容问题有降级或明确不支持说明。

### M6.4 — Release Candidate 与 1.0

1. 冻结 RC，执行全量、规模和长稳测试。
2. 从冷启动用户路径进行独立验收，修正文档或阻塞缺陷。
3. 生成最终发行物、校验和、变更说明与验证记录。
4. 发布后执行安装/更新冒烟并准备可逆撤回方案。

门禁：所有发布原则、退出标准和功能矩阵证据一致，才标记 1.0。

## 6. 可验证退出标准

- [ ] 配置、键位、CLI、退出码、IPC 和机器输出的 1.0 兼容边界有文档和测试。
- [ ] `--help`、man、补全、配置参考和实际命令一致。
- [ ] SQLite、工作区、缓存和 Helper 元数据从每个支持来源版本升级通过；中断与失败可恢复。
- [ ] 支持矩阵内每个平台/架构都有可校验发行物和干净安装证据。
- [ ] 守护进程升级、并发旧客户端、Socket 残留和版本不兼容均有安全行为。
- [ ] 安全威胁模型完成，无未处置高风险项；中低风险有归属和理由。
- [ ] OpenSSH/Kerberos、ProxyCommand、SFTP Server、终端和 Helper 兼容矩阵完成。
- [ ] doctor/支持包可在不暴露秘密和完整内容的情况下定位主要故障类别。
- [ ] 50k/1M/100GB、故障注入、race、fuzz 和发布候选长稳重新在最终候选版本通过。
- [ ] 功能矩阵、所有 Stage Verification、`IMPLEMENTATION_PLAN.md` 和 `PROJECT_STATE.md` 与实际发行一致。
- [ ] 新用户只依赖正式文档即可完成安装、SSH 连接、双栏浏览、一次安全传输、远端编辑和故障恢复。
- [ ] 撤回/回滚流程在发布前演练，并明确哪些存储迁移不可直接降级。

## 7. 测试矩阵

| 层级 | 场景 | 必须证明 | 证据 |
|---|---|---|---|
| 全量 | 单元/契约/集成 | 所有阶段行为在候选版本保持 | CI 报告 |
| 并发 | race、长时间多 Job/TUI 重连 | 无竞态、死锁和资源增长 | race/soak 报告 |
| Fuzz | 路径、IPC、Helper、配置、搜索 | 畸形输入安全失败 | fuzz 语料与报告 |
| 迁移 | 每个支持旧格式、各中断点 | 数据可升级或恢复，不静默清空 | 历史夹具矩阵 |
| 安装 | 干净 macOS/Linux、升级、卸载 | 无开发机依赖，权限正确 | 平台自动化/记录 |
| 安全 | 威胁模型负向用例 | 不泄密、不扩权、不越界 | 安全验证报告 |
| 兼容 | OS/终端/SSH/SFTP/Helper | 支持、降级、不支持均准确 | 兼容矩阵 |
| 规模 | 50k/1M/100GB | 最终构建保持有界与可取消 | 发布基准 |
| 故障 | 断网、kill、磁盘满、权限、陈旧状态 | 无伪成功与数据安全回归 | 故障矩阵 |
| 文档 | help/man/config/matrix 链接与示例 | 文档可执行且无漂移 | docs-check |
| 用户验收 | 安装→连接→传输→编辑→恢复 | 新用户不依赖历史上下文 | 冷启动演练 |
| 发布 | 校验和、版本、provenance、渠道安装 | 发行物可追溯且一致 | Release Verification |

## 8. 失败与回滚策略

- 任一发布门禁失败都阻止 1.0；不通过跳过测试、放宽安全默认或删除兼容声明解决。
- 迁移失败保留当前原库/WAL、verified pre-upgrade backup 与诊断并进入只读模式；不得自动恢复或用空库继续写，恢复必须由用户显式选择并重新验证 identity/integrity/compatibility。
- RC 发现核心语义缺陷时回到对应阶段修复和验证，不在 Stage 6 另建旁路。
- 某平台/架构无法满足安全与质量门禁时，可从 1.0 支持矩阵明确移除并说明，而不是发布未验证包。
- 渠道包有问题时撤回该渠道并保留可校验归档；发布说明给出受影响版本和恢复步骤。
- 安全事件可通过禁用 Helper/直传/外部预览等独立能力进行紧急降级，核心 SFTP 只读/标准传输尽可能保持。
- 回滚二进制前检查配置/数据库/缓存版本；不兼容时使用备份或导出，不让旧版写新格式。
- 发布后若必须撤回，保留校验和、公告和诊断信息，不重用同一版本号覆盖不同二进制。

## 9. 完成时必须更新的文档

- `PROJECT_STATE.md`：1.0 版本、发布日期、支持矩阵、已知问题、维护入口和最后绿色证据。
- `IMPLEMENTATION_PLAN.md`：Stage 6 Complete；按用户要求保留本文件作为长期路线记录，而不是完成后删除。
- `docs/product/feature-matrix.md`：所有功能最终状态、版本和证据。
- `docs/architecture/overview.md` 与全部 ADR：实际 1.0 边界，无已失效设计结论。
- `docs/verification/stage-06.md` 与 Release Verification：候选环境、命令、结果和豁免。
- 配置参考、键位、CLI/man、安装/升级/卸载、认证、Helper、直传、安全、隐私和故障排查文档。
- 兼容矩阵、迁移支持表、许可证/第三方通知和发布说明。

## 10. 维护阶段交接信息

1. 1.0 支持的 OS/架构、终端、OpenSSH/SFTP 和 Helper 版本。
2. 配置、IPC、数据库、工作区、缓存和 Helper 格式版本及兼容承诺。
3. 发布与撤回操作手册、签名/校验材料和渠道所有权。
4. 安全开关、已接受风险、秘密处理与漏洞响应路径。
5. 性能基线、规模夹具、长稳时长和资源默认值。
6. 已知问题、用户可用降级、优先级和下一里程碑候选。
7. 全量绿色命令、平台人工验证项和下次发布必须重跑的矩阵。
8. 文档真相链维护人/规则：任何修复同时更新代码、测试、功能矩阵、Verification 和 Project State。

后续功能必须从新阶段设计开始，不能以“1.0 已完成”为由绕过阶段门禁和安全不变量。
