# 故障排查

[English](../../help/troubleshooting.md)

大多数 AMSFTP 问题都可以在不修改文件、不重启进程的情况下缩小范围。先运行：

```sh
amsftp --version
amsftp daemon status --format json
amsftp doctor --format json
```

如果问题与某台 SSH 主机有关，再检查对应 Endpoint：

```sh
amsftp doctor --endpoint work --format json
```

`doctor` 只做读取和检查，不会修复状态、迁移数据、修改 SSH 信任关系或上传报告。
Endpoint 检查也不会弹出交互式认证，因此有时仍需先手动登录一次 SSH。

## AMSFTP 无法启动

先验证配置：

```sh
amsftp config validate
amsftp config print-effective
```

如果错误提到安装路径不可信、运行目录、所有者、权限、ACL 或符号链接，不要通过
放宽目录权限来绕过检查。请重新运行可信来源的安装器。如果安装器要求使用受管根
目录，请让管理员严格按输出创建对应的私有路径，再重新安装。

AMSFTP 会主动拒绝未知配置项。请只修正或删除错误中指出的字段，不必把整个配置
文件重置。支持的配置项和优先级见[配置说明](../user/configuration.md)。

## 无法打开远端位置

AMSFTP 使用系统 OpenSSH 能识别的主机别名。先在 AMSFTP 之外测试同一个别名：

```sh
/usr/bin/ssh work
```

然后运行：

```sh
amsftp doctor --endpoint work
```

常见原因包括：

- `~/.ssh/config` 中没有这个别名，或它展开后的配置和预期不同；
- 远端路径不是绝对路径（`work:/srv/project` 才是有效格式）；
- 远端账号不能使用 SFTP subsystem；
- 跳板机、VPN、DNS、SSH Agent 或 Kerberos 票据不可用；
- 服务器断开连接或拒绝访问。

请先让 OpenSSH 连接恢复正常。AMSFTP 不维护另一套 SSH 配置或凭据存储。

## OpenSSH 报告主机密钥异常

先停止连接，通过可信且独立的渠道核对新指纹。不要为了消除提示而删除
`known_hosts`、关闭主机密钥检查，或直接接受发生变化的密钥。

当 `/usr/bin/ssh work` 已能按你的日常安全策略正常连接后，再重试 AMSFTP。

## 缺少认证或反复要求认证

在普通 SSH 会话中完成首次认证或续期认证：

```sh
/usr/bin/ssh work
```

确认 SSH Agent 中有预期的密钥，或对应 Kerberos 票据仍然有效。如果 Job 需要
交互式回答、但当前没有可信客户端，AMSFTP 会进入等待，不会保存或猜测答案。
重新打开 TUI，完成认证，必要时恢复该 Job。

AMSFTP 不会把密码、私钥、Agent 内容、Kerberos 票据或认证回答写入配置、数据库、
Job、日志或支持包。

## daemon 不可用，或 TUI 反复重连

先查看 daemon 状态，不要急着替换它：

```sh
amsftp daemon status
amsftp doctor
```

如果 daemon 只是停止了，正常启动即可：

```sh
amsftp daemon start
```

不要手工删除控制 socket，也不要终止身份不明的占用进程。AMSFTP 在使用 socket
前会校验它的所有者和对端用户。升级期间，其他客户端可能会短暂等待旧 daemon
释放实例，再连接新 daemon。

如果客户端与 daemon 的版本不兼容，请完成升级，或重新安装同一个当前版本，确保
两者来自同一套安装。

## Job 没有进展

列出 Job，并查看目标 Job 最近的事件：

```sh
amsftp job list --limit 50
amsftp job events <job-id> --after 0 --limit 50
```

状态通常会指出下一步：

| 状态或提示 | 处理方式 |
| --- | --- |
| `paused` | 准备好后执行 `amsftp job resume <job-id>`。 |
| 需要认证 | 恢复 OpenSSH 认证会话，然后继续 Job。 |
| 冲突 | 刷新两端，并明确选择保留、替换、重命名或跳过。 |
| 连接中断或超时 | 恢复网络或服务器，查看事件后继续原来的 Job。 |
| 资源不足 | 释放提示中的磁盘、配额或并发资源，再继续。 |
| 能力丢失或不支持 | 重新连接，让 AMSFTP 改用标准 SFTP 或其他受支持路径。 |

不要因为第一次传输停住就再创建一份相同任务。Job 已记录安全进度，继续前会检查
来源、临时目标和最终目标。

如果结果不确定或涉及破坏性操作，请先阅读[恢复指南](recovery.md)，不要直接处理
临时文件。

## 复制或编辑出现冲突

冲突表示来源或目标已经不同于 AMSFTP 制定操作计划时看到的状态。另一个进程编辑、
重命名、替换或删除同一个文件时，都可能发生这种情况。

刷新两栏，查看两个版本，再明确选择。AMSFTP 不会静默覆盖并发修改。远端编辑发生
冲突时，请先保留本地编辑结果，再决定以新名称上传、替换远端版本，还是放弃修改。

## 找不到文件或没有权限

刷新上级目录，并确认当前查看的是预期的本地或 SSH Endpoint。`not_found` 也可能
表示文件在列出后被其他进程移动了。

遇到 `permission_denied` 时，请检查本地文件的所有者、mode 和 ACL，或远端账号的
访问权限。不要用 root 运行 AMSFTP 来绕过限制；root 会使用另一套用户状态和 SSH
身份。

## 预览或搜索提前结束

目录加载、预览和搜索都限制了时间、字节数、结果数和并发量。达到限制时，AMSFTP
会显示部分结果，而不是无上限占用内存或读取整棵远端目录树。

请缩小目录范围或搜索条件后再试。通过标准 SFTP 做大范围远端内容搜索可能较慢，
因为被检查的内容都需要经过 SSH 连接传回本机。出现部分结果并不表示后面一定没有
其他匹配项。

## 本地编辑器或默认打开程序失败

检查配置的命令，并先用一个本地文件单独测试。AMSFTP 会把参数以结构化形式交给
程序，不会把文件名解释为 shell 命令。

编辑器退出后，AMSFTP 会比较缓存的初始版本、本地修改和远端当前版本。如果远端
也发生了变化，上传会等待用户明确处理冲突。

## 升级无法完成

有 Job 正在修改数据时，AMSFTP 会拒绝升级。请等待这些 Job 完成，或先暂停；只有
在确实希望取消任务时才取消，然后重试：

```sh
amsftp upgrade
```

同一时间只能运行一个升级。如果软件包已经替换、但最终验证失败，请记录：

```sh
amsftp --version
amsftp daemon status --format json
amsftp doctor --format json
```

不要删除数据库，也不要让旧版本二进制写入较新的持久状态。接下来按
[升级恢复](recovery.md#恢复升级)处理。

## 出现 internal 或协议错误

`protocol_incompatible` 通常表示客户端、daemon 或持久状态来自无法安全协作的
版本。请只保留一套当前版本，并使用文档支持的升级路径。

`internal` 表示底层原因包含不适合直接显示的信息，AMSFTP 已将其隐藏。运行
`doctor`，记录产品版本和稳定错误码；如果问题持续，再准备一份经过检查的支持包。

## 为问题报告准备信息

建议提供：

- `amsftp --version`；
- 失败的命令或 TUI 操作；
- 界面显示的稳定错误码和 request ID；
- `/usr/bin/ssh <alias>` 是否能够连接；
- 仅在适合公开时提供 Job ID。

可以只预览诊断包，而不创建文件：

```sh
amsftp support-bundle preview --format json
```

创建或分享支持包前，请检查文件清单和敏感级别。支持包默认只保存在本地私有位置，
但仍可能包含系统元数据。不要在公开报告中附加私钥、票据、密码、未经检查的日志、
文件内容或原始命令行。
