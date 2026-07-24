# 恢复

[English](../../help/recovery.md)

AMSFTP 的恢复策略有意偏保守：无法证明一次写操作是否完成时，它会保留现场并等待
用户决定，而不是冒险重复破坏性步骤。

daemon 崩溃、传输中断、工作区损坏或升级失败后，请使用本指南。普通连接和配置
问题可先查看[故障排查](troubleshooting.md)。

## 修改任何内容之前

1. 暂停创建会接触同一批文件的新复制、移动、删除或编辑操作。
2. 记录当前安装版本、daemon 状态和只读诊断结果：

   ```sh
   amsftp --version
   amsftp daemon status --format json
   amsftp doctor --format json
   ```

3. 保留错误中显示的 Job ID、稳定错误码和 request ID。
4. 不要删除或重命名数据库、WAL、控制 socket、缓存、传输 part、工作区备份或最终
   目标。

这些文件可能是 AMSFTP 判断能否安全继续的唯一依据。

## 恢复 daemon

先查看状态：

```sh
amsftp daemon status
```

如果 daemon 只是停止了，正常启动：

```sh
amsftp daemon start
```

然后运行：

```sh
amsftp doctor
```

如果状态显示 socket 存在，但不安全或属于其他用户，请停止操作。不要删除它，也
不要修改权限。AMSFTP 使用私有 Unix socket，并校验对端用户；绕过检查可能会把
文件操作发送给错误的进程。

崩溃后，新 daemon 会读取持久 Job 状态，并检查每个未完成操作两端的实际情况。
它不会因为旧连接消失，就认定原请求一定失败。

如果 daemon 反复退出，请保留当前状态并预览支持包。不要用无关的数据库工具尝试
修复 SQLite。

## 恢复 Job

列出持久 Job：

```sh
amsftp job list --limit 50
```

查看目标 Job：

```sh
amsftp job events <job-id> --after 0 --limit 100
```

解决最新事件指出的外部条件：

- 恢复网络或远端服务器；
- 通过系统 OpenSSH 完成认证；
- 释放本地或远端磁盘空间；
- 处理冲突；
- 等待占用同一资源的其他操作结束。

然后继续原来的 Job：

```sh
amsftp job resume <job-id>
```

如果只是暂时不想继续，可以暂停并保留可恢复进度：

```sh
amsftp job pause <job-id>
```

取消必须明确提供同一个 Job ID：

```sh
amsftp job cancel <job-id> --confirm <job-id>
```

取消并不保证删除所有临时字节。AMSFTP 可能保留可用于安全恢复的匹配 part，也不会
递归删除不认识的文件。

### 结果不确定时

不要手工完成移动、覆盖目标或删除来源。继续已记录的 Job，让 AMSFTP 检查：

- 制定 Job 计划时记录的来源身份；
- 已持久化的偏移量和临时目标；
- 最终目标是否已经提交；
- 移动操作的来源是否仍然存在。

移动操作只有在目标经过验证并提交后才会删除来源。如果目标已经完成、但无法证明
来源删除也已完成，AMSFTP 会保留来源并报告实际结果，不会猜测。

如果来源和目标都存在、Job 又无法继续，请保留两份文件。报告稳定状态，不要只凭
时间戳决定保留哪一份。

## 恢复工作区

保存的工作区只包含两栏位置和视图偏好，不包含凭据。如果某个工作区无法打开：

1. 确认不使用这个工作区时，其他位置仍能正常打开。
2. 复制工作区状态前停止 daemon：

   ```sh
   amsftp daemon stop --confirm stop
   ```

3. 把原工作区文档复制到私有备份位置，不要直接编辑原文件。
4. 如果存在 `<name>.schema-v1.backup`，也要保留。它是在格式迁移成功时保存的
   原始内容，不应被覆盖。
5. 启动 daemon，从确认有效的位置创建一个新的工作区名称。

常见工作区目录是：

- macOS：
  `~/Library/Application Support/io.github.tyrantlucifer.amsftp/state/workspaces/`
- Linux：
  `${XDG_STATE_HOME:-$HOME/.local/state}/amsftp/workspaces/`

使用受管根目录的安装可能把状态放在其他位置，请以 AMSFTP 报告的路径为准，不要
假定一定使用默认目录。

工作区文件仅供所有者访问，并使用严格的数据结构。如果原文件与迁移备份不一致，
或错误提到备份冲突，请保留两者并停止操作。不要用手工编辑后的文件覆盖其中任何
一个同名文件。

## 恢复升级

先判断升级进行到了哪一步：

```sh
amsftp --version
amsftp daemon status --format json
amsftp doctor --format json
```

### 替换软件包之前就被拒绝

出现以下情况时，AMSFTP 不会修改软件包：

- 有正在执行的 Job，升级会导致其中断；
- 另一个升级持有升级锁；
- 安装或状态路径未通过所有者和权限检查；
- 无法验证发行版本或安装器。

只解决错误中指出的问题，再重试。不要放宽目录权限、替换控制 socket，或关闭系统
安全策略。

### 软件包已更新，但 daemon 没有重新启动

先运行新二进制的 `--version`，再查看 daemon 状态和 `doctor`。如果二进制缺失或
前后不一致，请重新运行可信安装器或包管理器升级。

不要立刻启动保存下来的旧二进制。持久状态可能已经采用新格式。只有发行说明明确
声明旧版本可以使用当前状态，或者文档化恢复流程同时恢复了匹配的状态时，才可以
安全降级二进制。

### 持久状态迁移失败

停止所有写操作，保留完整的用户私有状态目录。不要脱离配套恢复流程单独复制运行中
的 SQLite 主文件，不要删除 WAL、编辑迁移记录，或用新数据库覆盖旧数据库。

迁移中断或失败后，AMSFTP 可能让状态保持只读。请使用当前版本二进制和稳定诊断，
定位已记录的备份或恢复暂停状态。如果该版本没有针对这一情况提供恢复命令，请保留
状态并报告问题，不要自行拼凑恢复步骤。

可信安装渠道和正常升级、卸载方式见[安装与维护](../user/installation.md)。

## 恢复预览、编辑或缓存状态

如果预览或外部程序仍在使用某个条目，或者编辑器中有未保存内容，请先正常关闭，
再清理缓存。正在使用的条目受租约保护；编辑失败后，缓存中的本地副本可能是唯一
包含修改的版本。

当 `doctor` 报告缓存完整性或权限问题时：

1. 停止发起新的预览和编辑；
2. 保留所有已编辑的本地副本；
3. 关闭 AMSFTP 及关联的编辑器或打开程序；
4. 再次运行 `doctor`；
5. 只有在当前工作已经安全后，才使用 AMSFTP 自己的缓存操作。

不要为了修复单个条目而递归删除整个缓存目录。

## 创建经过检查的支持包

先预览准确内容：

```sh
amsftp support-bundle preview --format json
```

预览结果会给出 consent digest。检查文件清单和敏感级别后，在已有的用户私有目录
中创建新归档：

```sh
amsftp support-bundle create \
  --consent <sha256-from-preview> \
  --output /absolute/private/path/amsftp-support.tar.gz \
  --format json
```

如果预览后诊断状态发生变化，digest 会失效；请重新预览，不要复用旧授权。AMSFTP
不会上传支持包。即使敏感字段会被脱敏、归档以私有且不覆盖已有文件的方式发布，
分享之前仍应自行检查内容。
