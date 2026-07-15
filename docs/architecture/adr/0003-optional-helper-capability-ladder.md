# ADR-0003：可选 helper 与能力阶梯

- 状态：Accepted
- 日期：2026-07-14
- 影响范围：远端搜索、哈希、tail/watch、快速复制、跨主机 direct 与发布兼容

## 背景

标准 SFTP 足以完成浏览和字节流读写，却不提供高效的内容搜索、强哈希、文件系统 watch、磁盘空间探测或服务端复制等统一能力。百万级目录和超大文件场景需要这些能力，但把远端 agent 设为使用前提会破坏零部署体验，也会增加安全审查、版本漂移和运维负担。

## 决策

标准 SFTP 是永远可用的 Level 0 基线；用户可明确批准安装一个版本化、非驻留的 helper 来获得 Level 1 能力，并在额外预检通过后使用 Level 2 direct。

能力按操作逐项协商，不因安装 helper 就假定全部可用：

| 等级 | 能力集合 |
| --- | --- |
| Level 0 | 浏览、基础操作、流式读写、断点续传、本机有界中继、受限递归文件名搜索 |
| Level 1 | 快速 walk、内容搜索、强哈希、磁盘空间、tail/watch、同主机复制 |
| Level 2 | 跨主机网络与认证预检、受控 direct 传输 |

helper 每次按需通过系统 ssh 的 stdio 启动，不监听端口、不常驻、不提权。

## 安装与升级契约

安装必须是用户可见、可撤销的操作：

1. 先按当前客户端policy验证canonical raw manifest/signature、key/artifact revoke、protocol/`min_client`与release floor；此时不读artifact、不做Endpoint high-water、不probe。
2. preliminary consent只展示Endpoint、`shared-session-stable-home`声明、将运行read-only probe及可能的sshd/audit副作用，并明确actual uid/home/path仍未知；unknown/异构mapping保持Level0。
3. consent后以validated absolute fresh ssh、root-owned absolute utilities做binding，取得non-root uid/cwd/OS/arch；cwd/RealPath只是compatibility preflight，不证明node/mount/object。随后target match与Endpoint high-water。
4. high-water通过后流式验证local artifact expected+1/size/SHA-256，再派生safe ASCII home/path（component≤255、app absolute path≤1000）并read-only验证existing ancestor/final plan、列出待建exact`0700`目录。
5. final consent展示fresh observed non-root numeric uid（不伪称username）、target/home/path/create list、version/key/source/hash/floor/high-water/mode/capability与边界；绑定当前plan，任何变化重新probe/确认。确认前app install tree零create/零content。
6. 确认后在final sibling以`.amsftp.tmp-`+128-bit CSPRNG、`O_EXCL`打开temp；首byte前handle`Chmod(0600)`+handle/path复核，写signed size后client SFTP expected+1回读复算。通过才chmod0700，以standard no-replace发布并重验；不信existing helper/remote digest，不overwrite/delete-first。
7. 每次enable/exec仍按current policy→fresh binding/target/high-water→ancestor/final/hash→唯一restricted command→byte0 preface/stderr cap/handshake重验。失败回Level0，只精确清理本次temp；“零写”仅指final consent前app install tree，不虚构sshd/shell环境无副作用。

未经用户批准不得自动安装。自动下载的发布物必须来自项目受信发布渠道，并强制执行 ADR-0010 的内置 Ed25519 签名、撤销/denylist、版本 floor 与上传前后固定摘要全链校验；仅依赖传输链路或摘要不足以证明发布物身份。

## Helper 协议

- 使用长度分帧的请求、响应与流式事件，不依赖终端文本和 locale。
- 握手包含协议主次版本、helper build、OS/arch、逐项能力和限制。
- 请求包含取消标识、资源预算、路径参数和相关 Job/Search ID。
- 文件名和搜索参数作为结构化字段传递，不拼接 shell。
- stderr 与 stdout 并发独立 drain；stdout 只用于协议，probe/formal stderr 服从 ADR-0010 65536-byte cap并只保留有界脱敏摘要。
- 主版本不兼容时不执行操作；次版本通过能力交集继续。

## 能力使用规则

### 搜索

f 优先使用 helper 的流式 walk；g/ 优先使用安全调用的 rg 或内置内容扫描器。请求必须包含遍历边界、符号链接策略、并发与结果预算。helper 不可用时，f 可降级为有界 SFTP walk；g/ 只提供明确标记的受限慢速模式。

### 哈希与验证

helper 的强哈希用于传输验证和缓存身份。只有两端对相同算法与完整字节范围达成一致时才标为 strong。否则回退到流中哈希或大小/mtime 等较弱策略，并影响 move 是否允许删除来源。

### 同主机复制

helper 可以避免字节绕回本机，但必须报告目标临时文件、提交边界和错误类别。它仍遵守 ADR-0004 的 .part、验证和原子提交语义。

### 跨主机 direct

direct 不是“有 helper 就自动开启”。计划器必须预检：

- 来源到目标的网络可达性
- 两端 helper 协议与所需能力
- 目标可用空间和写权限
- 来源主机已有的非交互目标认证
- 用户或 workspace 策略明确允许
- 不需要应用替用户复制密钥、开启 agent forwarding 或委托 Kerberos

direct 属于高风险计划，执行前展示数据路径、认证前提和失败降级。提交前失败且 Plan 允许时可切回本机中继。

## 安全边界

- helper 只使用登录用户权限，禁止 sudo、setuid 和系统目录写入。
- helper 不监听网络；direct 的出站连接只为当前 Job 存活，并受冻结目标和端口限制。
- 不默认复制 SSH key，不默认转发 agent，不默认委托 Kerberos。
- 安装路径位于 fresh strict-safe canonical absolute home 的用户私有目录，服从 component/path ceiling、完整祖先验证与 exact `0700`；版本路径不可由不可信符号链接劫持。该保证依赖显式 shared-session-stable-home、受信 sshd/root/admin/shell/ACL/LSM/server 边界。
- helper 只访问请求明确指定且当前用户有权访问的路径；产品不宣称额外沙箱权限。
- 搜索模式、文件名和命令参数使用结构化协议；调用 rg 时使用参数数组和参数终止符。
- 能力、版本、安装摘要和降级原因进入脱敏日志。

## 降级与失败行为

- 未安装 helper：完整保留 Level 0。
- helper 不兼容或握手失败：禁用 helper 能力并回到 SFTP，浏览会话不中断。
- helper 在搜索中崩溃：结果标记 partial_results，可在预算内切换 SFTP 文件名搜索；不把部分结果标为完整。
- helper 在传输提交前崩溃：按检查点和 Plan 降级或重试；提交边界不明确时进入冲突审计。
- 强哈希不可用：降低验证等级；move 若不能满足来源删除策略，则以 completed_with_source_retained 结束。
- direct 预检或执行失败：提交前可按计划降级到本机中继；提交后只做后置条件审计，不盲目重跑。
- helper 卸载或版本清理：不能删除仍被运行 Job 引用的版本。

## 替代方案

### 强制安装常驻远端 agent

性能和能力最好，但引入端口、生命周期、升级、权限和企业审查成本，且破坏开箱即用，因此拒绝。

### 永远只用标准 SFTP

安全面最小，但百万目录搜索、强校验、watch 和服务端复制体验无法达到目标，因此拒绝。

### 每次临时上传脚本

看似轻量，但脚本依赖 shell、locale、远端工具版本和不安全转义，协议难以兼容，因此使用版本化二进制 helper。

### 默认 agent forwarding 实现 direct

实现方便，但扩大凭据暴露面并把认证授权隐式带到来源主机，因此拒绝。

## 后果

### 正面

- 基线无需部署，高级能力按需获得。
- helper 故障域与 SFTP 浏览隔离。
- 能力协商使版本差异和平台差异可观测、可降级。

### 代价

- 需要为 macOS/Linux 与远端 OS/arch 维护发布矩阵。
- 安装、校验、版本共存和清理需要独立测试。
- 同一功能可能有 helper 与 SFTP 两条实现路径，必须通过 provider contract 保持语义一致。

## 验证要求

- helper 各支持平台的握手和 capability contract。
- 版本主次不匹配、损坏二进制、错误架构和安装中断测试。
- helper kill -9 后 SFTP 会话与浏览不受影响。
- 百万级树的流式搜索、取消、预算和 partial_results 测试。
- direct 的预检矩阵、禁止隐式凭据转移和中继降级测试。
