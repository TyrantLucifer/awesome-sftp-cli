# ADR-0018: 渠道感知自升级与 daemon 静止契约

- **Status**: Accepted
- **Date**: 2026-07-22
- **Decision owners**: CLI、daemon 与 transfer lifecycle

## Context

AMSFTP 同时通过 Homebrew formula 与仓库独立安装器分发。Homebrew 没有适合应用用来可靠完成“升级前停止自有 daemon”的通用 formula hook；让 formula 或旧进程直接覆盖 Cellar 也会破坏包管理器所有权。独立安装器已有归档 checksum、原子替换、上一版保留和 completion/man 更新语义，但用户不应记忆两套升级步骤。

普通 `daemon stop --confirm stop` 是显式破坏性运维命令，允许按已记录的重启恢复语义停止 transfer manager。自动升级不能把“用户要升级”解释为允许静默中断正在执行的 Job，也不能先检查 Job、再留下新执行在检查与停机之间进入的竞态窗口。

## Decision

1. 公开入口为 `amsftp upgrade [--format human|json]`，仅支持严格 release build、macOS/Linux、AMD64/ARM64，以及可证明的 Homebrew Cellar 或精确 `<prefix>/bin/amsftp` 独立安装布局。自定义布局继续使用手工升级文档。
2. 命令先刷新/解析渠道元数据并比较严格 `X.Y.Z` 版本；already-current 不探测、不停止 daemon。目标低于当前版本时拒绝自动降级。
3. Homebrew 渠道由 AMSFTP 调用绝对 brew 路径的固定 argv：元数据刷新与读取发生在停机前，真正替换只委托给 `brew upgrade TyrantLucifer/tap/amsftp`。AMSFTP 不直接写 Cellar。
4. 独立渠道下载同一不可变 tag 的 `checksums.txt` 与 `install.sh`，要求 installer 恰有一个 lowercase SHA-256 条目并验证精确字节，然后以固定参数为原 prefix 执行 `--no-start-daemon`。归档验证、原子发布、上一版保留和 ancillary files 更新继续由已发布 installer 负责。
5. 若确有升级且 daemon 正在运行，旧 CLI 通过现有 owner-only、peer-UID 校验和版本协商后的 RPC 发送 `ShutdownRequest{ForUpgrade:true}`。普通人工 stop 的 literal confirmation 契约不改变。
6. transfer manager 在同一 `queueMu` admission 边界设置 upgrade-prepared 状态并检查 active execution 计数。已有执行则返回稳定 conflict，daemon 不退出；成功准备后新 Job creation/execution admission 关闭，queued/waiting/paused 状态保持 durable，供新 daemon 恢复。该检查与 admission 不能拆成两个 RPC。
7. 升级停机得到 acknowledgement 后，CLI 仍须证明 control socket 消失且 instance lock 可取得，再执行包替换。若 acknowledgement 写回失败但 daemon 已进入 prepared 状态，server 必须继续请求 shutdown，不能留下永久拒绝 Job 的半静止进程。
8. 升级后只恢复升级前正在运行的 daemon，并从渠道的当前 launch path 重新解析、验证和启动新 binary，不能依赖可能已被 Homebrew 清理的旧 Cellar 路径。CLI 验证新 binary 的精确版本和新 daemon 的 build/protocol 后才报告 success。
9. 包替换失败时尝试恢复 daemon 可用性，但保持升级失败；替换已发生而 daemon restart 或版本验证失败时返回 partial completion。人类和 JSON 错误只暴露稳定安全文案，不包含包管理器输出、下载 URL、本地路径或底层 cause。
10. 自动升级不执行 sudo、不改变 Gatekeeper、不处理凭据、不降级 persistent state，也不替代手工回滚、撤回、签名、公证和真实原生发布门禁。

## Consequences

- 用户获得一个跨两种正式渠道的一致升级入口，同时文件所有权仍留在对应包管理器/installer。
- 活动传输会阻止自动升级；用户必须等待完成或明确暂停，而不是让升级隐式把 running/verifying 变成重启后的 paused。
- queued、waiting 与 paused Job 不阻止版本替换；新 daemon 按已有 durable recovery 契约继续处理。
- 首个公开版本仍是 unsigned preview；checksum 证明字节一致性，不等于 Apple 签名、公证或独立供应链信任。

## Evidence

稳定依据由 [upgrade CLI tests](../../../internal/app/upgrade_command_test.go)、[channel/checksum tests](../../../internal/selfupdate/selfupdate_test.go)、[daemon shutdown tests](../../../internal/daemon/server_test.go)、[transfer admission tests](../../../internal/transfer/manager_test.go) 与[升级手册](../../release/UPGRADE.md)维护。
