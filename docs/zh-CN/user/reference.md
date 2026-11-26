# 配置与命令参考

[English](../../user/reference.md)

本页汇总 `config.json` 配置项、默认 TUI 快捷键与 CLI 命令。查看当前安装版本的
完整命令说明可运行 `amsftp --help` 或 `man amsftp`。

## 配置文件

AMSFTP 无需配置文件即可开箱使用；仅在需要覆盖默认行为时创建 `config.json`。

**默认路径：**

- **macOS：** `~/Library/Application Support/io.github.tyrantlucifer.amsftp/config.json`
- **Linux：** `${XDG_CONFIG_HOME:-$HOME/.config}/amsftp/config.json`
- **自定义私有数据目录：** `<private-dir>/config/config.json`

配置文件必须是当前用户独享的普通文件（不能是符号链接），权限为 `0600`，采用严格
JSON 格式且包含 `"schema_version": 1`。未知字段或越界数值会被直接拒绝，不会静默
忽略。

修改后建议先校验并查看生效配置：

```sh
amsftp config validate
amsftp config print-effective
```

两个命令都支持追加 `/absolute/path/to/config.json` 在不覆盖正式配置的情况下单独
测试。外部命令的参数在输出中会自动脱敏。修改生效需要重启受影响的 TUI；如果修改了
daemon 侧配置（`transfer`、`cache`、`retry`、`diagnostic`、`ipc`），还需重启
daemon：

```sh
amsftp daemon stop --confirm stop
amsftp daemon start
```

已创建的任务会继续沿用创建时的配置。切勿把密码、私钥或 SSH 选项写入 `config.json`，
连接参数统一放在 `~/.ssh/config`。

## 常用配置示例

### 传输限速、校验与落盘同步

```json
{
  "schema_version": 1,
  "transfer": {
    "max_concurrent": 2,
    "max_queued": 64,
    "global_bytes_per_second": 10485760,
    "endpoint_bytes_per_second": 0,
    "job_bytes_per_second": 0,
    "verification": "protocol",
    "durability": "none"
  }
}
```

- **限速字段**单位为字节/秒（`0` 表示不限速），按文件数据字节统计（不含 SSH 加密与
  协议帧开销）；远端到远端中继时，源端读取与目标端写入分别计入全局与任务限速。
- **`verification`：** `"protocol"`（普通复制默认）或 `"sha256"`（全量回读摘要校验）。
  旧配置 `integrity.transfer_policy: "require_strong"` 同样会强制开启 SHA-256。
- **`durability`：** `"none"`（默认）、`"completion"`（重命名前 `fsync` 一次）或
  `"checkpoint"`（每个检查点 `fsync`）。移动与编辑回写始终保留严格校验与同步检查点。

### 编辑器、打开程序与外部预览器

```json
{
  "schema_version": 1,
  "external": {
    "editor": { "executable": "nvim", "argv": ["-f"] },
    "opener": { "executable": "/usr/bin/open", "argv": [] },
    "previewers": [
      {
        "name": "pdf-viewer",
        "media_types": ["application/pdf"],
        "extensions": [".pdf"],
        "command": { "executable": "/absolute/path/to/previewer", "argv": [] },
        "timeout_ms": 5000,
        "max_input_bytes": 1048576,
        "require_complete": true
      }
    ]
  }
}
```

程序通过 `exec` 直接启动，绝不经过 Shell 拼接。不含 `/` 的名称通过 `PATH` 解析，
包含 `/` 的路径必须为绝对路径。AMSFTP 会自动把缓存文件路径作为最后一个参数传入，
不需要也不应添加占位符或 Shell 引号。外部预览器会在内置预览之后按顺序尝试，失败
则回退到内置预览。

### 自定义快捷键

```json
{
  "schema_version": 1,
  "keymap": {
    "bindings": [
      { "context": "visual", "input": "n", "action": "down" }
    ]
  }
}
```

支持在 `normal` 与 `visual` 两个上下文中将单个可重映射动作改绑到单个可打印字符。
数字、控制键、多键序列（如 `gg`、`gs`）与危险动作均为保留键。查看生效键位或重置覆盖：

```sh
amsftp config print-effective-keymap
amsftp config reset-keymap --yes
```

### 全部配置项分组

| 分组 | 控制范围 |
| --- | --- |
| `listing` | 目录分页大小 |
| `cache` | 全局与单工作区缓存配额 |
| `transfer` | 并发数、队列上限、限速、校验与落盘同步 |
| `preview` | 预览读取、渲染、JSON 与图片上限 |
| `search.filename` / `search.content` | 搜索深度、条目、字节、耗时与结果数上限 |
| `retry` | 重连与任务重试延迟 |
| `integrity` | 传输完整性策略 |
| `diagnostic` | 私有日志轮转与内存日志缓冲 |
| `external` | 编辑器、打开程序与外部预览器 |
| `keymap` | Normal 与 Visual 自定义快捷键 |
| `ipc` | 客户端与后台进程间的单条消息大小上限 |

运行 `amsftp config print-effective` 可查看全部默认值。大多数数值上限只允许在内置
安全上限之内收紧，不能放宽。公开版本中 `helper.enabled` 与 `direct_transfer.enabled`
必须保持 `false`。

## 默认快捷键

**文件栏：**

| 快捷键 | 作用 |
| --- | --- |
| `h` / `j` / `k` / `l`（或方向键） | 上级目录 / 下移 / 上移 / 进入目录或预览 |
| `gg` / `G` | 跳到已加载列表的第一项 / 最后一项 |
| `Tab` | 切换当前活动栏 |
| `g` | 输入绝对路径跳转 |
| `c` | 切换当前栏主机（`local` 或 SSH 别名） |
| `/` | 在当前目录内模糊跳转 |
| `f` / `g/` | 递归搜索文件名 / 递归搜索文件内容 |
| `s` / `H` / `R` | 切换排序 / 切换隐藏文件 / 刷新 |
| `Space` / `v` / `V` | 单项勾选 / Visual 框选 / Visual Line 行选 |
| `y` / `d` / `p` | 复制 / 剪切 / 粘贴 |
| `r` / `D` | 重命名 / 删除（需确认） |
| `.` | 重复上一个可重复的操作 |
| `e` / `o` / `E` | 编辑 / 外部打开 / 编辑恢复 |
| `K` / `J` / `L` | 预览 / 后台任务 / 日志面板 |
| `S` | 保存工作区 |
| `!` | 在当前目录执行一条经确认的命令 |
| `gs` / `gS` | 当前目录交互 Shell / 远端 home 目录 Shell |
| `gp` / `gc` / `gC` | 切换缓存策略 / 清理本工作区缓存 / 清理全部缓存 |
| `Esc` / `q` | 关闭弹窗或退回焦点 / 退出 TUI |

**预览面板（`K`）：** `j`/`k` 滚动，`h`/`l` 或 `gg`/`G` 读取头/尾范围，`r` 切换
视图，`Esc` 退回文件栏，`K` 关闭。

**任务面板（`J`）：** `j`/`k` 选择任务，`P`/`U`/`C` 暂停/继续/取消，`w`/`x`/`a`
处理单个冲突（覆盖/跳过/自动重命名），`W`/`X`/`A` 将选择应用到同一任务内的同类冲突。

## 命令行接口

```text
amsftp [<location> [<location>]]
amsftp --workspace <name>
amsftp --help | --version

amsftp daemon start | status [--format human|json]
amsftp daemon stop --confirm stop [--format human|json]
amsftp upgrade [--format human|json]

amsftp job list [--limit <1..100>] [--format human|json]
amsftp job events <job-id> [--after <seq>] [--limit <1..100>] [--format human|json]
amsftp job pause | resume <job-id> [--format human|json]
amsftp job cancel <job-id> --confirm <same-job-id> [--format human|json]

amsftp config validate | print-effective | print-effective-keymap [<abs-path>]
amsftp config reset-keymap --yes [<abs-path>]

amsftp doctor [--endpoint <ssh-alias>] [--format human|json]
amsftp helper status <ssh-alias> [--format human|json]
amsftp support-bundle preview [--format human|json]
amsftp support-bundle create --consent <sha256> --output <abs-path> [--format human|json]
amsftp completion bash | zsh | fish
```

支持 `--format json` 的命令在成功时向 stdout 输出单个带版本号的 JSON 文档，失败时
向 stderr 输出带版本号的 JSON 错误对象。

**退出码：**

| 退出码 | 含义 |
| ---: | --- |
| `0` | 成功 |
| `1` | 内部或未分类错误 |
| `2` | 命令行参数用法错误 |
| `3` | 配置错误 |
| `4` | 认证错误 |
| `5` | 网络错误 |
| `6` | 存在需要人工选择的冲突 |
| `7` | 部分完成 |
| `8` | 用户取消 |

另见：[使用指南](guide.md)、[故障排查与恢复](../help/troubleshooting.md)、
[`amsftp(1)`（英文）](../../man/amsftp.1)。
