# 按键和命令

[English](../../user/reference.md)

本页用于快速查询默认 TUI 按键、位置写法和公开命令。运行 `amsftp --help` 和
`man amsftp` 可以查看当前安装版本的准确命令表面。

## 位置写法

```text
/absolute/local/path
SSH-alias:/absolute/remote/path
```

示例：

```sh
amsftp
amsftp /Users/alice/Downloads
amsftp /home/alice work:/srv/project
amsftp host-a:/data host-b:/backup
amsftp --workspace project
```

本地和远端路径都必须是绝对路径。SSH 别名来自 `~/.ssh/config`。

## 文件栏按键

| 按键 | 作用 |
| --- | --- |
| `h` / `j` / `k` / `l` | 上级 / 下移 / 上移 / 进入或预览 |
| `←` / `↓` / `↑` / `→` | 相同导航的固定别名 |
| `Tab` | 切换活动栏 |
| `g` | 输入绝对路径 |
| `c` | 选择 `local` 或 OpenSSH 端点 |
| `/` | 在当前已加载目录中模糊跳转 |
| `f` | 递归搜索文件名 |
| `g/` | 递归搜索文件内容 |
| `s` | 切换排序方式 |
| `H` | 切换隐藏条目 |
| `R` | 刷新 |
| `Space` | 切换离散标记 |
| `v` / `V` | 开始或结束 Visual / Visual Line 选择 |
| `y` / `d` / `p` | 复制 / 剪切 / 粘贴 |
| `r` | 重命名一个条目 |
| `D` | 进入需要确认的删除流程 |
| `.` | 重复上一个符合条件且已冻结的操作 |
| `e` / `o` | 编辑 / 打开普通文件 |
| `E` | 打开编辑恢复 |
| `K` / `J` / `L` | 预览 / Job / 日志抽屉 |
| `S` | 保存工作区 |
| `!` | 在当前目录运行一条经过确认的命令 |
| `gs` / `gS` | 在当前目录打开 shell / 明确打开远端 home shell |
| `gp` | 切换工作区缓存策略 |
| `gc` / `gC` | 检查当前工作区 / 全局缓存清理 |
| `Esc` | 取消最内层提示或返回焦点 |
| `q` | 退出 TUI；Job 继续运行 |

数字是支持计数的安全动作的前缀。数字不会取消冲突或破坏性确认。即使字母键被重新
映射，方向键仍然是导航别名。

查看当前准确键位：

```sh
amsftp config print-effective-keymap
```

## 预览和 Job 按键

预览获得焦点时：

| 按键 | 作用 |
| --- | --- |
| `j` / `k` | 向下 / 向上滚动 |
| `h` / `l` | 读取头部 / 尾部范围 |
| `r` | 切换可用预览视图 |
| `Esc` | 返回文件栏 |
| `K` | 关闭预览 |

Job 抽屉获得焦点时：

| 按键 | 作用 |
| --- | --- |
| `j` / `k` | 选择 Job |
| `P` / `U` / `C` | 可用时暂停 / 恢复或重试 / 取消 |
| `w` / `x` / `a` | 覆盖 / 跳过 / 自动重命名单个冲突 |
| `W` / `X` / `A` | 在当前 Job 中应用对应的冲突选择 |

抽屉标题是最终依据：不适用于当前状态的动作不会显示。

## 启动和生命周期命令

```text
amsftp [<location> [<location>]]
amsftp --workspace <name>
amsftp daemon start [--format human|json]
amsftp daemon status [--format human|json]
amsftp daemon stop --confirm stop [--format human|json]
amsftp upgrade [--format human|json]
amsftp --help
amsftp --version
```

`daemon status` 只检查 daemon，不会启动它。明确停止时必须提供字面值
`--confirm stop`。

## Job 命令

```text
amsftp job list [--limit <1..100>] [--format human|json]
amsftp job events <job-id> [--after <sequence>] [--limit <1..100>] [--format human|json]
amsftp job pause <job-id> [--format human|json]
amsftp job resume <job-id> [--format human|json]
amsftp job cancel <job-id> --confirm <same-job-id> [--format human|json]
```

列表和事件默认上限是 50，最大为 100。`events --after N` 只返回序号大于 `N`
的事件。

## 配置和诊断

```text
amsftp config validate [<absolute-config-path>]
amsftp config print-effective [<absolute-config-path>]
amsftp config print-effective-keymap [<absolute-config-path>]
amsftp config reset-keymap --yes [<absolute-config-path>]
amsftp doctor [--endpoint <SSH-alias>] [--format human|json]
amsftp helper status <SSH-alias> [--format human|json]
```

`doctor` 是只读检查。`helper status` 会报告端点使用标准 SFTP 还是可用的增强能力；
公开构建目前没有受支持的 production Helper 安装通道。

## 支持包

先预览准确的本地诊断快照：

```text
amsftp support-bundle preview [--format human|json]
```

预览会返回一个同意摘要。检查内容后，可以创建新的私有归档：

```text
amsftp support-bundle create --consent <sha256> --output <absolute-path> [--format human|json]
```

输出目标不能已经存在，并且必须位于当前用户私有的目录下。AMSFTP 会重新生成快照；
如果预览后内容发生变化，就会拒绝创建。没有命令会自动上传归档。

## Shell 补全

```sh
amsftp completion bash
amsftp completion zsh
amsftp completion fish
```

生成的脚本会补全命令和已保存的工作区名称。手动安装升级后，请重新生成，确保它与
当前程序一致。

## Human 和 JSON 输出

默认输出适合人类阅读。支持 `--format json` 的命令会输出一个带版本的 JSON 文档。
脚本应该使用 JSON，不要解析人类表格。

成功时 JSON 写到 stdout；失败时，带版本的错误写到 stderr，并保留有意义的进程
退出码。

## 退出码

| 代码 | 含义 |
| ---: | --- |
| `0` | 成功 |
| `1` | 内部或未分类错误 |
| `2` | 命令用法错误 |
| `3` | 配置错误 |
| `4` | 认证错误 |
| `5` | 网络错误 |
| `6` | 需要选择的冲突 |
| `7` | 部分完成 |
| `8` | 用户取消 |

只有证据足够时，命令才会选择更具体的错误类型。无法可靠分类时会返回 `1`，而不是
猜测。

## 相关指南

- [配置](configuration.md)
- [传输和 Job](transfers.md)
- [故障排查](../help/troubleshooting.md)
- [`amsftp(1)`（英文）](../../man/amsftp.1)
