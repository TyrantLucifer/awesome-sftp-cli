# ADR-0011：以不可变窄 fork 提供 SFTP 目录流式游标

- 状态：Accepted
- 日期：2026-07-15
- 影响范围：Stage 1 SFTP Provider、依赖供应链、远端目录分页与取消
- 修订：本 ADR 仅取代 ADR-0006 对 `github.com/pkg/sftp v1.13.10` 的精确 pin；其余身份、工具链、TUI 和日志决策不变

## 背景

Stage 1 要求远端目录项在源端增量进入 daemon 页面，首屏不能等待完整目录枚举。`github.com/pkg/sftp v1.13.10` 与 v1.13.11 的公开 `ReadDir`/`ReadDirContext` 都会循环读取全部 `SSH_FXP_NAME` 响应并累积完整切片后才返回；`Walk` 也委托同一路径。daemon 分页、TUI generation 和窗口化渲染无法补偿这个源端阻塞。

项目仍必须复用 ADR-0001 的系统 OpenSSH stdio 传输，不能增加第二套 SSH/SFTP 协议栈。上游 v1.13.11 同时包含扩展属性计数的恶意包边界修复，因此窄改动应以该发布为基线，而不是继续维护 v1.13.10。

## 决策

根 module 保留上游导入路径 `github.com/pkg/sftp`，要求 `v1.13.11`，并通过 Go module `replace` 固定到以下不可变 fork 版本：

```text
github.com/TyrantLucifer/sftp v1.13.12-0.20260715132526-f947b886400b
commit f947b886400be01ed663564525f8bacf1be6c74e
tree   32258bedd3d535d1da84d5ca9fc489533bce88c6
sum    h1:nTtLW2gYG18og3cJx3d8cxf1vEX4naYExzYQvdRtg6s=
```

fork 基于上游 `v1.13.11` commit `fc82c354c0d87349411e30a08bef297c9f132105`，只增加以下公开能力与配套测试：

- `Client.OpenReadDir(ctx, path)` 打开一个 `ReadDirCursor`；
- `ReadDirCursor.Read(ctx)` 每次返回一个协议 `SSH_FXP_NAME` 响应中的目录项，而不是完整目录；
- `ReadDirCursor.Close()` 幂等关闭远端句柄；读取、关闭、取消和错误继续沿用 pkg/sftp 的请求/状态错误语义；
- 新目录项解码器在按 server-reported count 分配前用包长度建立上限，并对截断字段返回 `errShortPacket`。

AMSFTP SFTP Provider 每个活动 cursor 只保存远端句柄和当前协议包尚未消费的有限条目。它只为满足当前 daemon page limit 读取必要的协议批次；取消、EOF、错误、fingerprint 冲突、显式丢弃和 Provider 关闭都关闭句柄。目录恰好在页面边界结束时，允许下一次 continuation 返回空的终止页，因为 SFTP v3 必须再读一次才能观察 EOF。

这不是新的产品内 SFTP 实现：认证、连接、packet 编解码、请求复用、状态映射和绝大多数协议代码仍来自 pkg/sftp，业务代码继续只导入 `github.com/pkg/sftp`。

## 依赖准入与维护

- 上游与 fork 均使用 BSD-2-Clause；fork 不增加依赖。v1.13.11 把传递图更新到 `golang.org/x/crypto v0.54.0`、`x/sys v0.47.0`、`x/term v0.45.0` 和 `x/text v0.40.0`，并声明 Go 1.25.0，符合项目兼容线。
- fork 的单元、真实 client/server、race 和 Go 1.25.12 测试必须通过；根项目仍执行双工具链、`govulncheck`、许可证、四目标 `CGO_ENABLED=0` 构建和完整 Hosted 门禁。
- 上游 v1.13.11 的 `go vet ./...` 本身存在两个 `ReadFrom` 签名提示和两个测试锁复制提示；fork 未新增 vet 提示。该上游基线例外只在 fork intake 中记录，不授权根项目忽略 lint/vet。
- 更新 fork 时先从新的不可变上游 tag 重放最小 cursor patch，审查上游安全变更与 API 差异，重新生成 pseudo-version/checksum，并通过新 ADR 或本 ADR 的明确修订更新 pin。禁止可变 branch、`latest` 或未记录提交。
- 若上游发布等价的 context-aware streaming cursor，可在完整兼容和资源语义验证后删除 `replace`，回到上游精确 tag。回滚到 v1.13.10/11 的完整切片 API 会重新打开 Stage 1 退出门禁，不能静默进行。

## 替代方案

### 保留完整 `ReadDirContext`

只能保证 daemon/UI 页面有界，不能保证源端首屏延迟和内存有界，因此不满足 PANE-004。

### 在产品内复制或重写 SFTP 目录协议

会形成第二套 packet/request/error 维护面，并扩大安全边界；相较四个窄 API/decoder 改动不可接受。

### 仅升级到上游 v1.13.11

获得安全修复但公开目录 API 仍完整缓冲，不能单独关闭本决策的功能缺口。

## 安全与可测试性检查

- 真实 257 项目录必须按协议响应边界 `128/128/1/EOF` 增量返回，关闭幂等且关闭后读取失败。
- 恶意 impossible count 和截断 name packet 必须返回 `errShortPacket`，不得按不可信 count 大分配或 panic。
- Provider 的确定性 request-server 测试让第二个 `READDIR` 阻塞；`Limit=1` 必须在请求第二批前返回首批页面。
- continuation token 冲突、context 取消、读取错误、显式 discard 和 Provider close 必须回收远端句柄；分页内存只随协议包与 page limit 增长。
- fork commit、tree、pseudo-version、module checksum、上游基线、许可证和传递依赖变更必须保留在 Stage 1 验证记录中。

## 后果

- Stage 1 获得真正的远端源端增量枚举，Provider 不再为每个 cursor 保存完整目录。
- 项目承担一个小型公开 fork 的安全更新与 rebase 责任；不可变 pin 和上游回归矩阵使该责任可审计、可回滚。
- SFTP v3 仍没有服务端总量提示；UI 必须继续把未读完的目录表示为进行中，而不是伪造百分比。

## 参考依据

- [`pkg/sftp` v1.13.11](https://github.com/pkg/sftp/releases/tag/v1.13.11)
- [AMSFTP `pkg/sftp` fork commit](https://github.com/TyrantLucifer/sftp/commit/f947b886400be01ed663564525f8bacf1be6c74e)
- [SFTP v3 draft，`SSH_FXP_READDIR`/`SSH_FXP_NAME`](https://datatracker.ietf.org/doc/html/draft-ietf-secsh-filexfer-02)
