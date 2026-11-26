# AMSFTP

[English](README.md) | 简体中文

AMSFTP 是一款 Vim 风格的双栏终端文件管理器，同时支持本地目录与 SFTP 服务器。
你可以把任意本地目录和 `~/.ssh/config` 主机并排打开，在终端里直接完成浏览、
预览、编辑、搜索和文件传输。

> [!WARNING]
> **公开预览版（尚未到 1.0）：** macOS 构建目前未签名、未公证。公开版本仅使用
> 标准 SFTP 传输；可选的远端加速组件和服务器间直传功能尚未开放。

## 核心特性

- **直接复用系统 OpenSSH：** 自动沿用 `~/.ssh/config` 中的配置（包括 `ProxyJump`、
  `ProxyCommand`、SSH Agent、主机密钥校验与 Kerberos/GSSAPI）。AMSFTP 不保存
  密码、私钥或票据。
- **断线不丢任务的后台传输：** 复制、移动、重命名和删除都交由当前用户的本地后台进程
  （daemon）作为可续传任务执行。退出 TUI 不会中断正在进行的传输。
- **安全的写入与移动：** 文件先写入临时文件，全部完成后再重命名为正式文件，不会出现
  “写了一半的损坏文件”。移动操作在目标校验完成前绝不删除源文件；需要更严格校验时
  可开启 SHA-256 校验与 `fsync` 落盘同步。
- **支持远端到远端复制：** 两台 SSH 服务器间复制时，数据通过本地后台进程按小块流式
  转发，既不会把整份文件落盘到本机，也不会把 SSH 密钥复制到任何一台服务器。
- **为日常终端工作打造：** 按需分段预览文本与图片、带冲突检测的远端安全编辑、带资源
  上限的文件名/内容搜索，以及只读诊断工具 `amsftp doctor`。

## 系统要求

- macOS 15+（Apple Silicon / Intel）或 Ubuntu 22.04 / 24.04（其他 Linux 发行版
  尽力兼容）
- 系统自带的 `/usr/bin/ssh`，以及远端服务器支持 SFTP（OpenSSH 默认开启 SFTP 子系统）
- 在 `~/.ssh/config` 中配置的命名主机别名（非 `Host *` 通配符）

## 安装

**Homebrew：**

```sh
brew install TyrantLucifer/tap/amsftp
```

**独立安装脚本（安装到 `$HOME/.local`，无需 `sudo`）：**

```sh
curl --proto '=https' --tlsv1.2 -fsSL \
  https://github.com/TyrantLucifer/awesome-sftp-cli/releases/latest/download/install.sh | sh
```

安装脚本在替换程序前会自动校验发布包的 SHA-256。手动下载、升级、Shell 补全、
源码构建与卸载请见[安装指南](docs/zh-CN/user/installation.md)。

## 快速上手

1. 在 `~/.ssh/config` 中配置一个常规主机别名：

   ```sshconfig
   Host work
       HostName dev.example.com
       User alice
       IdentityFile ~/.ssh/id_ed25519
   ```

2. 先用系统 OpenSSH 完成首次主机密钥确认和登录，再运行 `doctor` 检查：

   ```sh
   /usr/bin/ssh work
   amsftp doctor --endpoint work
   ```

3. 并排打开一个本地目录和一个远端目录：

   ```sh
   amsftp /absolute/local/path work:/absolute/remote/path
   ```

路径必须是绝对路径（`/local/path` 或 `<ssh-alias>:/remote/path`）。直接运行
`amsftp` 会打开启动选择器，运行 `amsftp --workspace <name>` 可重新打开已保存的
双栏工作区。

## 常用快捷键

| 快捷键 | 说明 |
| --- | --- |
| `h` / `j` / `k` / `l` 或方向键 | 上级目录 / 下移 / 上移 / 进入目录或预览 |
| `gg` / `G` | 跳到已加载列表的第一项 / 最后一项 |
| `Tab` / `c` | 切换左右栏 / 切换当前栏主机 |
| `/` / `f` / `g/` | 当前目录快速跳转 / 递归搜文件名 / 递归搜文件内容 |
| `Space`、`v`、`V` | 勾选或批量选中文件 |
| `y` / `d` / `p` | 复制 / 剪切 / 粘贴（粘贴前会弹窗确认） |
| `r` / `D` | 重命名 / 删除（需确认） |
| `K` / `J` / `L` | 打开或关闭 预览 / 任务 / 日志 面板 |
| `e` / `o` | 用终端编辑器编辑 / 用系统默认应用打开 |
| `S` | 保存当前双栏工作区 |
| `q` | 退出 TUI（后台任务继续运行） |

底部动作栏会实时提示当前可用快捷键。运行 `amsftp config print-effective-keymap`
可查看当前生效的完整键位。

## 文档导航

- [文档总览](docs/zh-CN/README.md)
- [安装、升级与卸载](docs/zh-CN/user/installation.md)
- [使用指南：浏览、传输、编辑与搜索](docs/zh-CN/user/guide.md)
- [配置、快捷键与命令参考](docs/zh-CN/user/reference.md)
- [故障排查与恢复](docs/zh-CN/help/troubleshooting.md)
- [整体技术设计与架构](docs/zh-CN/architecture/design.md)
- [`amsftp(1)` 手册页（英文）](docs/man/amsftp.1)

本项目采用 [Apache License 2.0](LICENSE) 开源协议。
