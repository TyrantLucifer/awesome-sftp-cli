# 配置

[English](../../user/configuration.md)

没有配置文件也可以直接使用 AMSFTP。只有在需要调整资源限制、重试时间、完整性
策略、外部程序或可重映射按键时，才需要创建配置。

配置是严格的 JSON，schema 版本为 `1`。未知字段和不安全的值会被拒绝，不会
悄悄忽略。

## 找到配置文件

默认路径：

- macOS：
  `~/Library/Application Support/io.github.tyrantlucifer.amsftp/config.json`
- Linux：
  `${XDG_CONFIG_HOME:-$HOME/.config}/amsftp/config.json`
- 托管的独立安装：安装器记录的托管根目录下的 `config/config.json`

配置必须是当前用户私有的普通文件，不能是符号链接。权限应为 `0600`：

```sh
chmod 600 /absolute/path/to/config.json
```

最小有效文件是：

```json
{
  "schema_version": 1
}
```

没有写出的选项会使用内置默认值。

## 重启前先验证

验证默认用户配置：

```sh
amsftp config validate
```

只验证另一个文件，不安装它：

```sh
amsftp config validate /absolute/path/to/config.json
```

查看经过脱敏的最终配置：

```sh
amsftp config print-effective
amsftp config print-effective /absolute/path/to/config.json
```

外部命令参数会在输出中隐藏。该命令不会连接端点，也不会启动传输。

客户端或 daemon 启动时会读取配置。修改后，请关闭受影响的 TUI；如果修改项由
daemon 使用，再重启 daemon：

```sh
amsftp daemon stop --confirm stop
amsftp daemon start
```

已有 Job 会继续使用创建时记录的选项。

## 常用设置

### 限制并发传输或带宽

```json
{
  "schema_version": 1,
  "transfer": {
    "max_concurrent": 2,
    "max_queued": 64,
    "global_bytes_per_second": 10485760,
    "endpoint_bytes_per_second": 0,
    "job_bytes_per_second": 0
  }
}
```

速率单位是字节/秒，`0` 表示不限速。配置可以收紧内置安全上限，但不能把它扩大。

### 要求强传输完整性

```json
{
  "schema_version": 1,
  "integrity": {
    "transfer_policy": "require_strong"
  }
}
```

默认的 `strong` 策略会使用 SHA-256 验证传输内容。`require_strong` 还会拒绝
无法满足强完整性要求的通道。不接受更弱的策略。

### 选择编辑器和 opener

```json
{
  "schema_version": 1,
  "external": {
    "editor": {
      "executable": "nvim",
      "argv": ["-f"]
    },
    "opener": {
      "executable": "/absolute/path/to/opener",
      "argv": []
    }
  }
}
```

这些程序会被直接执行，不会拼接成 shell 命令。没有 `/` 的程序名通过 `PATH`
解析；包含 `/` 的值必须是绝对路径。AMSFTP 会把准备好的文件作为最后一个参数
添加。

### 添加外部预览器

```json
{
  "schema_version": 1,
  "external": {
    "previewers": [
      {
        "name": "pdf-viewer",
        "media_types": ["application/pdf"],
        "extensions": [".pdf"],
        "command": {
          "executable": "/absolute/path/to/previewer",
          "argv": []
        },
        "timeout_ms": 5000,
        "max_input_bytes": 1048576,
        "require_complete": true
      }
    ]
  }
}
```

外部预览器会在内置预览之后按顺序尝试。`require_complete` 决定某条规则是否必须
拿到完整的本地 materialization；无论如何，输入大小和运行时间都有上限。预览器
输出不会直接复制到终端，失败时会退回内置结果。

### 重映射按键

```json
{
  "schema_version": 1,
  "keymap": {
    "bindings": [
      {
        "context": "visual",
        "input": "n",
        "action": "down"
      }
    ]
  }
}
```

键位包含 `normal` 和 `visual` 两个上下文。一条配置会把一个可重映射动作移动到
一个单字符输入，不会创建第二个绑定。数字、危险动作、多键序列、控制字符和冲突输入
都保留给系统。

查看完整结果：

```sh
amsftp config print-effective-keymap
```

只重置键位覆盖：

```sh
amsftp config reset-keymap --yes
```

重置不会改变其他配置。

## 配置分组

当前 schema 按用途分组：

| 分组 | 控制内容 |
| --- | --- |
| `listing` | 目录分页大小 |
| `cache` | 全局和单工作区缓存配额 |
| `transfer` | 并发/排队 Job 和带宽速率 |
| `preview` | 输入、渲染输出、JSON 和图片限制 |
| `search.filename` | 递归文件名搜索限制 |
| `search.content` | 递归内容搜索限制 |
| `retry` | 安全重连间隔和 Job 重试间隔 |
| `integrity` | 传输完整性要求 |
| `diagnostic` | 私有日志轮转和内存历史大小 |
| `external` | 编辑器、opener 和外部预览器 |
| `keymap` | Normal 和 Visual 的可重映射按键 |
| `ipc` | 高级本地控制消息大小限制 |

运行 `amsftp config print-effective` 可以查看当前全部默认值。多数数值选项只能在
默认值基础上调小；超出允许范围时，validator 会给出说明。

最终输出中可能还会看到 `helper.enabled` 和 `direct_transfer.enabled`。公开构建
要求二者保持 `false`。配置无法打开尚未发布的生产加速或直传通道。

## 优先级和隐私

启动位置或 `--workspace` 决定打开什么。工作区提供文件栏位置和视图状态，用户配置
提供应用设置，最后由内置默认值补全没有填写的内容。

AMSFTP 专用环境变量不是通用配置层。`VISUAL`、`EDITOR`、`PATH` 和传给经过验证
的系统 OpenSSH 的环境，只会用于各自明确的用途。

不要把密码、私钥、认证回答或任意 SSH 选项写进 `config.json`。远端连接行为应该
放在 `~/.ssh/config` 中，由系统 OpenSSH 解释。
