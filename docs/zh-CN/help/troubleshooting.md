# 故障排查与恢复

[English](../../help/troubleshooting.md)

AMSFTP 采用保守恢复原则：只要无法证明一次写入或删除是否完成，就会保留现场并等待
人工确认，而不会盲目重试或靠猜。

## 先做只读排查

大多数问题都可以在不改文件、不重启进程的情况下先定位：

```sh
amsftp --version
amsftp daemon status --format json
amsftp doctor --format json
amsftp doctor --endpoint work --format json   # 涉及某台 SSH 主机时补充检查
```

`doctor` 是严格只读命令，不会修改 SSH 信任关系、修复数据库或上传任何信息。

**崩溃或传输失败后，在动手之前记住三条：**

1. 不要对同一批文件重复发起新的复制、移动、删除或编辑任务。
2. 记下错误码、request ID 与 Job ID。
3. **切勿**手动删除 SQLite 数据库、WAL 文件、控制 socket、`.part` 临时文件或工作区
   备份——这些文件是 AMSFTP 判断能否安全续传的依据。

## 连接、SSH 与认证问题

**远端主机打不开：** AMSFTP 直接复用系统 OpenSSH 别名，先在外部直接测试：

```sh
/usr/bin/ssh work
amsftp doctor --endpoint work
```

请核对 `~/.ssh/config` 中是否存在该别名、远端路径是否为绝对路径（`work:/srv/project`）、
服务端是否启用了 SFTP subsystem，以及跳板机、VPN、SSH Agent 或 Kerberos 票据是否正常。

**主机密钥告警：** 请通过独立可信渠道核对新指纹，不要为了消掉提示而删除
`known_hosts` 或关闭主机密钥校验。待 `/usr/bin/ssh work` 按正常策略能够连通后，再
重试 AMSFTP。

**反复要求认证或任务停在 `waiting_auth`：** 先在 `/usr/bin/ssh work` 中完成登录或
刷新 Agent / Kerberos 票据。后台任务需要交互回答而当前没有 TUI 连接时，它会暂停
等待，绝不保存或猜测密码——打开 TUI 完成认证后继续任务即可。

## 启动、权限与配置错误

**AMSFTP 拒绝启动：**

```sh
amsftp config validate
amsftp config print-effective
```

- **未知配置字段：** 修正或删掉报错指出的键名；严格 JSON 校验会主动拦截拼写错误。
- **路径、所有者、权限、ACL 或符号链接不安全：** 切勿通过 `chmod 777` 放宽目录权限来
  绕过检查。请重新运行官方安装脚本；若提示需要自定义私有数据目录，请让管理员严格按
  输出创建目录后再安装。
- **文件报 `permission_denied`：** 检查本地或远端文件权限，不要用 `root` 运行 AMSFTP
  （`root` 会使用另一套数据目录与 SSH 身份）。
- **`protocol_incompatible`：** 客户端、daemon 或本地数据来自不兼容版本。请完成升级
  或重装同一套版本。

## 中断的任务、冲突与编辑恢复

查看卡住或中断的任务及事件：

```sh
amsftp job list --limit 50
amsftp job events <job-id> --after 0 --limit 100
```

| 状态 / 报错 | 处理方式 |
| --- | --- |
| `paused` | 在 TUI 中按 `U`，或执行 `amsftp job resume <job-id>` 继续。 |
| `waiting_auth` | 恢复 OpenSSH 认证后继续任务。 |
| `waiting_conflict` | 刷新两栏、核对两边文件后选择覆盖/跳过/自动重命名（`w`/`x`/`a`）。 |
| 连接中断 / 超时 | 恢复网络或服务器后，查看事件并继续原任务（不要新建重复任务）。 |
| 资源耗尽 | 释放磁盘空间或缓存配额，或等待其他活跃任务完成后再继续。 |
| 服务端特性变化（`capability_lost`） | 重新连接，让 AMSFTP 重新检测服务器支持的 SFTP 扩展，必要时回退到基础 SFTP。 |

显式取消任务：`amsftp job cancel <job-id> --confirm <job-id>`。

**移动结果不确定或残留临时文件：** 不要仅凭修改时间手动删除源文件或覆盖目标。请直接
继续原任务，让 AMSFTP 核对源文件未改动、校验临时文件摘要，并确认目标是否已正式
重命名到位。如果移动已完成复制但无法证明源文件删除成功，两边文件会一并保留，请核对
后再自行处理。

**远端编辑中断：** 在 TUI 中按大写 `E` 进入编辑恢复界面，用 `K` 检查本地已修改的
副本，再决定上传、另存为还是放弃。

**预览或搜索提前结束：** 这是触发了耗时、字节数、深度或结果数保护上限。缩小目录或
关键词重试即可；部分结果并不意味着后续没有匹配项。

## Daemon、工作区、缓存与升级恢复

**Daemon 不可用：** 先运行 `amsftp daemon status`。如果只是停止了，执行
`amsftp daemon start` 和 `amsftp doctor` 即可。若提示 socket 不安全或归属其他用户，
请**立即停止**，不要手删 socket 或强杀未知进程。daemon 崩溃重启后会自动检查所有
未完成任务的两端状态。

**工作区损坏：**

1. 确认不加载该工作区时，其他目录可以正常打开。
2. 停止 daemon（`amsftp daemon stop --confirm stop`）。
3. 备份原工作区文件（以及可能存在的 `<name>.schema-v1.backup`），不要原地编辑。
   默认目录：
   - macOS：`~/Library/Application Support/io.github.tyrantlucifer.amsftp/state/workspaces/`
   - Linux：`${XDG_STATE_HOME:-$HOME/.local/state}/amsftp/workspaces/`
4. 重启 daemon，从正常的双栏位置重新保存一个工作区。

**缓存异常：** 先关闭占用缓存的编辑器、外部程序和预览面板，保住本地已编辑文件，再
运行 `amsftp doctor` 并使用 AMSFTP 内置的 `gc`/`gC` 清理，不要直接 `rm -rf` 整个
缓存目录。

**升级失败或被拒绝：**

- **替换程序前被拒绝：** 通常是存在活跃传输任务、另一个升级进程持有锁，或目录权限/
  校验未通过。等待或暂停任务后重试 `amsftp upgrade`。
- **程序已替换但 daemon 未启动：** 运行 `amsftp --version`、`amsftp daemon status`
  与 `amsftp doctor`，再重跑 Homebrew 升级或官方安装脚本。若可能已触发数据库迁移，
  不要直接启动旧版本程序。
- **数据库迁移失败：** 停止所有写操作，完整保留私有状态目录并留存 `doctor` 输出。
  切勿直接复制运行中的 SQLite 主文件或删除 WAL 文件。

## 提交问题报告与支持包

反馈问题时请附上 `amsftp --version`、失败命令或操作步骤、错误码与 request ID，以及
`/usr/bin/ssh <alias>` 是否能够正常登录。

如需导出脱敏诊断包，分两步操作：

```sh
amsftp support-bundle preview --format json
amsftp support-bundle create \
  --consent <sha256-from-preview> \
  --output /absolute/private/path/amsftp-support.tar.gz \
  --format json
```

输出路径必须尚不存在且位于当前用户私有目录下。如果预览后状态发生变化，SHA-256
确认摘要会失效，需要重新预览。AMSFTP 不会自动上传支持包；分享前请自行检查内容
（凭据和原始路径会被脱敏，但仍含版本与系统元数据）。
