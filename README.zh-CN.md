# AMSFTP

[English](README.md) | 简体中文

AMSFTP 是一个 Vim 风格的双栏文件管理器，可以同时管理本地文件和 SFTP
服务器。每一栏都可以打开本地目录或 `~/.ssh/config` 中的主机，因此浏览、预览、
编辑、搜索和传输都能在同一个终端工作区中完成。

> [!WARNING]
> AMSFTP 目前是公开预览版，还不是 1.0。macOS 构建尚未签名或公证。公开版本以
> 标准 SFTP 作为受支持的远端通道；远端加速和服务器之间的直传目前不可用。

## 可以用它做什么

- 并排浏览本地和远端目录。
- 直接使用系统 OpenSSH 的配置、密钥、Agent、主机密钥策略、
  ProxyJump/ProxyCommand 和 Kerberos/GSSAPI 环境。
- 把复制、移动、重命名和删除作为可恢复的后台 Job 执行。
- 验证传输内容后再提交目标文件。
- 预览文本和图片，在本地编辑或打开远端文件。
- 按文件名或内容搜索，并在搜索规模较大时明确提示限制。
- 使用 `doctor`、结构化诊断和需要明确同意的支持包排查问题。

AMSFTP 不会保存密码、私钥、Agent 内容、Kerberos 票据或认证回答。

## 系统要求

- macOS 15 或更高版本，或者 Ubuntu 22.04/24.04。
- 系统自带的 `/usr/bin/ssh`。
- 远端服务器提供 SFTP subsystem。
- 在 `~/.ssh/config` 中配置一个明确的主机别名。

其他 Linux 发行版可能可以运行，但目前只提供尽力而为的支持。

## 安装

使用 Homebrew：

```sh
brew install TyrantLucifer/tap/amsftp
```

或者把最新的独立版本安装到 `$HOME/.local`：

```sh
curl --proto '=https' --tlsv1.2 -fsSL \
  https://github.com/TyrantLucifer/awesome-sftp-cli/releases/latest/download/install.sh | sh
```

安装器会在替换程序前校验发行包 checksum。如果主目录的所有者或权限不符合安全
要求，它会打印创建私有托管安装目录所需的准确命令。

手动安装、升级、shell 补全、源码构建和卸载方法见
[安装指南](docs/zh-CN/user/installation.md)。

## 第一次连接

先添加一个普通的 OpenSSH 别名：

```sshconfig
Host work
    HostName dev.example.com
    User alice
    IdentityFile ~/.ssh/id_ed25519
```

让 OpenSSH 先完成第一次主机密钥确认和认证，再检查端点：

```sh
/usr/bin/ssh work
amsftp doctor --endpoint work
```

然后并排打开一个本地目录和一个远端目录：

```sh
amsftp /absolute/local/path work:/absolute/remote/path
```

位置可以是本地绝对路径，也可以是
`<SSH-别名>:<远端绝对路径>`。不带位置运行 `amsftp` 会进入启动选择器；
`amsftp --workspace <名称>` 可以重新打开保存过的工作区。

## 常用按键

| 按键 | 作用 |
| --- | --- |
| `h` / `j` / `k` / `l` 或方向键 | 上级 / 下移 / 上移 / 打开 |
| `gg`、`G` | 跳到首个 / 最后一个条目或预览范围 |
| `Tab` | 切换文件栏 |
| `c` | 切换当前栏的端点 |
| `/` | 在当前目录中查找条目 |
| `Space`、`v`、`V` | 选择文件 |
| `y`、`d`、`p` | 复制、剪切、粘贴 |
| `r`、`D` | 重命名、确认后删除 |
| `K`、`J`、`L` | 预览、Jobs、日志 |
| `e`、`o` | 在本地编辑或打开文件 |
| `f`、`g/` | 按文件名或内容搜索 |
| `S` | 保存当前工作区 |
| `q` | 退出 TUI；后台 Job 会继续运行 |

动作栏会根据当前选择显示真正可用的按键。运行
`amsftp config print-effective-keymap` 可以查看完整的当前键位。

复制和剪切只记录选择；粘贴会先展示目标位置和需要确认的选项，然后才创建 Job。
新内容在完成验证和提交之前不会以最终文件出现，移动也会在目标完整后才删除来源。
远端到远端复制通过本地 daemon 使用受限内存中继，不会把 SSH 凭据复制到服务器。

## 文档

- [完成第一次连接和传输](docs/zh-CN/user/getting-started.md)
- [浏览全部用户指南](docs/zh-CN/README.md)
- [排查问题](docs/zh-CN/help/troubleshooting.md)
- [了解架构](docs/zh-CN/architecture/overview.md)
- [`amsftp(1)` 手册（英文）](docs/man/amsftp.1)

AMSFTP 使用 [Apache License 2.0](LICENSE) 许可证。
