# ADR-0005：冻结 Stage 0 基础合约

- 状态：Accepted
- 日期：2026-07-14
- 影响范围：本机 RPC、路径与事件线格式、Provider 扩展边界、能力快照和结构化错误

## 背景

Stage 0 将并行建立领域值、IPC 和 Provider 契约。若各包自行选择协议常量、路径编码、能力身份或错误恢复字段，后续实现会在最基础的边界上产生不兼容，且无法由契约测试可靠收敛。因此，这些跨阶段共享且难以事后更改的选择必须先被冻结。

Unix 文件名本质上是除 NUL 和路径分隔符外的字节序列，并不保证是有效 UTF-8。JSON 字符串表达 Unicode 文本；Go 的 JSON 编码在遇到无效 UTF-8 时会用替换字符代替原字节。直接把路径写成 JSON 字符串会丢失身份信息，甚至让两个不同的原始文件名变成同一个解码结果。

事件序号只在一个事件流中有意义。守护进程重启后序号可能从头开始；若游标只有序号，客户端无法判断它指向重启前的旧事件流还是重启后的新事件流，可能错误跳过或重复应用事件。

## 决策

以下摘要是 Stage 0 共享合约的规范值：

    Protocol version: 1.0
    Frame header: uint32 big-endian
    Maximum payload: 8 MiB
    Path wire encoding: base64 of original bytes
    Event cursor: epoch string plus monotonically increasing sequence
    Provider split: read-only Provider plus optional MutableProvider
    Capability identity: SessionID plus generation and complete flag
    Error recovery: stable code, retry advice, and effect status

### IPC 分帧与路径字节

协议版本固定为 1.0。每个消息使用 4 字节无符号大端整数声明后续 JSON payload 的长度；payload 上限为 8 MiB。接收方必须在按声明长度分配内存或解码前拒绝超过上限的帧。

路径、文件名和符号链接目标在跨进程线格式中以原始字节的 base64 表示。展示文本与路径身份分离；展示层可以转义或替换不可显示字节，但不能把展示结果写回为路径身份。base64 解码失败是协议错误，不能以替换字符继续。

JSON envelope 外壳及其中承担协议控制语义的字符串必须是严格有效的 UTF-8；decode 在 JSON 解析前拒绝非法字节，encode/Validate 也不能依赖 Go JSON 的 U+FFFD 替换。`Payload` 原始 JSON bytes 同样必须同时满足 strict UTF-8 与 JSON 语法。只有路径、文件名、符号链接目标等 Unix 原始字节字段通过 base64 保真；错误对象中的 Location 可以保留失败请求的原始诊断路径（包括业务上非法的 NUL），但不能因此成为可执行路径。

### 事件游标

事件游标由 epoch 字符串和在该 epoch 内单调递增的序号组成。任何会重置序号的事件流重建都必须更换 epoch。客户端只有在 epoch 相同时才能按序号续接；epoch 不同时先获取快照，再订阅新事件流。

### Provider 与能力

核心 Provider 只包含只读发现、规范化、分页枚举、stat 和有界读取语义。写入、创建目录、重命名和删除属于可选 MutableProvider Facet。只读调用方不得依赖或伪造写能力；需要变更状态的调用方必须同时检查 Facet 和当前能力快照。

能力快照由 SessionID、该会话内递增的 generation 和 complete 标志共同标识。SessionID 或 generation 变化会使旧判断失效。只有 complete 为 true 时，能力列表中缺少某项才能解释为明确不支持；不完整快照中的缺失表示未知。

### 错误恢复

跨 IPC 与 Provider 边界的错误必须同时提供稳定机器代码、重试建议和 effect status。稳定代码用于控制流与兼容；重试建议表达立即、退避、重连、认证、冲突处理或重新计划等前置动作；effect status 表达操作未生效、已生效或结果未知。结果未知的非幂等操作必须先检查后置条件，不能直接重放。

Code、RetryKind 与 EffectStatus 只接受本 ADR/领域合约冻结的 canonical 枚举；未知值不能作为同一协议主版本下的有效控制流继续执行。新增语义必须先走协议/能力演进。重试延迟不得为负数。

## 替代方案

### 直接使用 JSON 字符串传路径

实现最简单，但无法无损表示任意 Unix 文件名字节，因而拒绝。限制产品只支持有效 UTF-8 文件名会破坏 Provider 对现有文件系统的忠实表示，也不能作为隐式兼容策略。

### 只使用事件序号

字段更少，但 daemon 重启或事件日志重建后无法识别序号所属的事件流，因而拒绝。

### 单一可变 Provider 接口

调用形式统一，但会让 Stage 1 的只读浏览依赖写能力，并迫使只读后端实现无意义的变更方法。将变更操作放入可选 Facet 能保持最小依赖并让能力缺失显式可测。

## 后果

### 正面

- IPC 能无损传递 Unix/SFTP 路径身份，并在分配前限制不可信帧大小。
- 客户端可以识别事件流代次，不把 daemon 重启后的序号误认为旧流续接。
- 只读 Provider 保持最小，后续写能力可以独立演进。
- 能力撤回和非幂等错误可以通过明确身份与 effect status 安全处理。

### 代价

- 路径字段需要显式 base64 包装，调试输出不如直接文本直观。
- 所有事件续接点都必须携带并持久化 epoch。
- 需要对 Provider 与 MutableProvider 分别编写契约测试。
- 调用方必须处理不完整能力快照和 effect unknown，而不能依赖布尔能力或通用 retry 标志。

## 验证要求

- IPC 测试覆盖 8 MiB 边界、大端长度、截断帧和超限帧在分配前拒绝。
- 路径线格式测试覆盖有效 UTF-8、无效 UTF-8、控制字节和 base64 解码失败。
- Envelope 测试覆盖 decode/encode 两侧非法 UTF-8、不同非法字节不得归一碰撞、strict-UTF-8 `Payload`、未知 Code/Retry/Effect 与负重试延迟拒绝，同时证明错误 Location 的原始诊断路径仍可 base64 无损往返。
- 事件测试覆盖同 epoch 续接、epoch 变化后重新取快照和序号回绕拒绝。
- Provider 契约证明只读实现无需 MutableProvider，并验证运行时 Facet/能力丢失。
- 错误测试覆盖稳定代码、每类重试建议以及 none、applied、unknown 的 effect status。
