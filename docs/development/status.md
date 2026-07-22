# 当前项目状态

最后更新：2026-07-22。

## 当前结果

- PR #6 已合并到 `main`，合并提交为 `1f8bef8c3055150c6e47d05eab30f79d02396e04`。
- 一键安装与发布自动化 PR #9 已合并到 `main`，合并提交为 `7e6f6986af45712ae81d826001f0dc2454804a15`。
- 内部预览版 [`v0.1.0-internal.20260719.1`](https://github.com/TyrantLucifer/awesome-sftp-cli/releases/tag/v0.1.0-internal.20260719.1) 已发布；候选提交为 `541c3c7434d05bc5366950c53c8b1f1774d72e38`。
- 公开预览版 [`v0.1.13`](https://github.com/TyrantLucifer/awesome-sftp-cli/releases/tag/v0.1.13) 为独立安装增加目标发布前路径预检和 owner-private managed root：默认 HOME 含 symlink、foreign-owner ancestor 或不安全权限时自动复用已预置 `/var/lib/amsftp-users/<uid>`，缺失时保持目标零变更并给出一次性管理员初始化命令；布局标记让后续 daemon、配置、状态、缓存和升级保持同一可信根。沿用 macOS/Linux 渠道感知升级、四平台归档、checksum、SBOM 与 Homebrew formula 渠道，历史严格版本 tag 保持不可变。
- Stage 0–6 的实现阶段已经结束；历史计划和验证流水从活动文档中移除，通过 Git 历史追溯。
- 仓库现在进入“内部预览反馈与下一阶段迭代准备”状态，不宣称公开 1.0 已完成。
- 内部预览反馈已修复 Preview drawer 的逐行滚动、小文件 EOF range 越界与空文件零长度读取问题。
- 内部预览反馈已将 TUI `c` Endpoint 切换改为 `local` 与 OpenSSH Host 的有界模糊选择；未匹配输入不再直接发起连接。
- 内部预览反馈已为登录后的 Normal/Visual 浏览补齐方向键；`←/↓/↑/→` 与 `h/j/k/l` 使用相同的返回上级、下移、上移和进入语义。
- 内部预览反馈已修复复制、移动、重命名和删除 Job 成功完成后可见文件列表不及时更新的问题；后台只在存在非终态 Job 时继续有界轮询，并对受影响目录去重刷新。
- 内部预览反馈已修复 `Space`、`v`、`V` 多选后快速 `y`/`p` 会复用上一次单文件剪贴板的问题；新捕获立即使旧引用失效，提前到达的粘贴按捕获代次排队，迟到的旧结果不会覆盖当前选择。
- 内部预览反馈已修复同一次多选粘贴并发创建多个 Job 时只有一个能进入 WAL 的问题；共享状态写批次现在覆盖事务和提交后 checkpoint 串行准入，每个已冻结剪贴板项都独立持久化为 Job。
- 内部预览反馈已将 TUI Jobs 改为响应式主从展示：每个任务行优先显示操作、文件名和状态化进度，宽终端在右侧独立展示选中任务详情，窄终端使用单行选中路径摘要；完成删除不再伪装成 0% 字节传输，终态任务不再显示无意义的 `No actions`。
- 内部预览反馈已为获得焦点的 TUI Jobs 抽屉补充选中任务的状态感知快捷键提示；取消、暂停、恢复/重试和冲突选择只在对应状态可操作时展示，终态任务保留选择导航但省略空操作提示。
- 内部预览反馈已为文件栏补充选中对象的上下文动作栏：普通文件、目录和 Visual 多选只显示各自可用操作，提示使用有效 keymap 并在抽屉/弹窗获得焦点时隐藏；动作栏或主状态栏持续显示当前 AMSFTP 构建版本。
- 内部预览反馈已重排 TUI 主状态栏：恢复入口和连接状态改为用户可读文案并前置，默认模式、capability generation、raw Helper 原因和默认 hidden 状态不再挤占主视图；缓存策略保留显示但使用 automatic/temporary/offline 语义。
- 内部预览反馈已让 Preview 抽屉按终端高度在 6–16 行内自适应，常见 24 行终端可显示 10 行内容；Jobs 与 Log 继续保持固定有界高度。
- 内部预览反馈已将 `/` 改为当前已加载目录项的模糊跳转：方向键选择，Enter 定位后恢复完整列表，Esc 恢复搜索前位置，不产生递归远端读取。
- TUI 已采用显式 Graphite/Cyan 语义主题：活动与非活动栏、光标、多选、连接状态、抽屉和弹窗拥有独立层级；文件元数据按栏宽渐进收缩，主状态栏优先保留恢复、失败和当前动作，同时继续维持大目录窗口化渲染。
- CI 已拆为普通 PR/main 使用的影响感知 `Fast CI`、release 分支/tag/手动触发的完整 `Release Gates`，以及定时 `Nightly`；普通改动不再重复运行发布矩阵。
- 严格 `X.Y.Z` 公开预览的一键安装/升级脚本、Homebrew formula 生成器与 tag 发布 workflow 已进入主线；脚本校验 checksum、原子替换 binary、保留上一版并按 daemon 契约完成启停验证。
- `amsftp upgrade` 已进入主线：macOS/Linux 的 Homebrew 与独立安装统一使用渠道感知升级；命令只在存在新版本时申请升级停机，活动 Job 会原子拒绝停机，原本停止的 daemon 不会被意外启动。
- `v0.1.1` 已修复 `v0.1.0` Homebrew 符号链接入口被认证 helper 完整性检查拒绝的问题；包管理器入口会冻结到 Cellar 真实二进制并保持最终路径的 owner、mode、ACL 与祖先目录校验。

## 可用边界

- Vim-first 双栏浏览、LocalFS/SFTP Provider、持久 daemon、Job、预览、编辑、缓存、搜索、诊断与内部发布路径已进入主线。
- 标准 Level 0 SFTP 是当前支持的数据路径。
- production Helper 分发与生命周期仍为 **CLOSED**。
- production Level 2 跨主机直传仍为 **CLOSED**。
- 当前内部归档与公开预览均未经 Apple 签名或公证；公开预览不等于公开 1.0。
- `TyrantLucifer/homebrew-tap`、`public-preview` environment、tag 规则与两个受保护写入 secret 已配置并用于严格 patch 发布。

## 下一步

`v0.1.13` 发布后继续从[产品路线图](../product/roadmap.md)的“内部预览反馈闭环”收集真实问题；每个严格 patch 使用新的不可变 tag，production Helper、Level 2 和公开 1.0 仍使用独立门禁。

开发前阅读根目录 [AGENTS.md](../../AGENTS.md)，并只运行与改动风险相称的定向测试。公开 release 仍必须单独通过 [RC 门禁](../release/RC-GATES.md)。
