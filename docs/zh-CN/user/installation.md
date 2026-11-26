# 安装、升级与卸载 AMSFTP

[English](https://github.com/TyrantLucifer/awesome-sftp-cli/blob/main/docs/user/installation.md)

AMSFTP 支持 macOS（Apple Silicon 与 Intel）以及 Linux（AMD64 与 ARM64）。
希望由包管理器维护时请用 Homebrew；希望独立安装到用户目录 `$HOME/.local`
时请使用官方安装脚本。

> [!WARNING]
> macOS 公开构建目前未签名、未公证。请只从官方渠道下载，不要为了运行未经校验的
> 二进制而关闭系统级安全保护。

## 使用 Homebrew 安装

```sh
brew install TyrantLucifer/tap/amsftp
amsftp --version
amsftp daemon start
amsftp daemon status
```

Homebrew 会托管可执行文件并同时安装 `amsftp(1)` 手册页。

## 使用独立安装脚本

官方脚本默认安装到 `$HOME/.local`，不调用 `sudo`：

```sh
curl --proto '=https' --tlsv1.2 -fsSL \
  https://github.com/TyrantLucifer/awesome-sftp-cli/releases/latest/download/install.sh | sh
```

脚本会自动下载匹配系统和 CPU 架构的压缩包、校验 SHA-256、检查安装目录与数据
目录的所有者和权限、安全替换可执行文件、安装手册页并生成 Shell 补全。

请确认 `$HOME/.local/bin` 已加入 `PATH`：

```sh
export PATH="$HOME/.local/bin:$PATH"
```

### 当安装脚本提示需要自定义私有数据目录

AMSFTP 拒绝将私有数据（配置、数据库、socket、缓存）存放在所有者异常、其他用户
可写、带有意外 ACL 或包含符号链接的目录下。如果你的主目录未通过检查，安装脚本
会改用 `/var/lib/amsftp-users/<你的-uid>`。

若该目录尚不存在，安装脚本会直接退出且不改动系统，并打印一次性管理员建目录命令。
让管理员执行后重新运行脚本即可，也可以直接指定已经建好的自定义私有数据目录：

```sh
sh install.sh --root /absolute/private/path
```

运行 `install.sh --help` 可查看全部 `--prefix`、`--root`、`--version` 与 daemon
选项。

## 手动下载压缩包安装

1. 前往 [GitHub Releases](https://github.com/TyrantLucifer/awesome-sftp-cli/releases)
   下载对应平台压缩包及同一版本的 `checksums.txt`。
2. 解压前校验 SHA-256（Linux 用 `sha256sum <压缩包>`，macOS 用
   `shasum -a 256 <压缩包>`），确保与 `checksums.txt` 中对应行完全一致。
3. 解压到干净目录，将 `amsftp` 放入当前用户独享的 `PATH` 目录（如
   `$HOME/.local/bin`），并将 `share/man/man1/amsftp.1` 复制到用户 man 目录。
4. 生成 Shell 补全（`amsftp completion bash|zsh|fish`）并验证：

   ```sh
   amsftp --version
   amsftp config validate
   amsftp doctor
   ```

请勿使用 `root` 运行 AMSFTP、设置 setuid，或将其安装到其他用户可写的目录。

## 从源码构建

需要 Go 1.26.6：

```sh
git clone https://github.com/TyrantLucifer/awesome-sftp-cli.git
cd awesome-sftp-cli
GOTOOLCHAIN=local go build -trimpath -o ./amsftp ./cmd/amsftp
./amsftp --version
```

本地源码构建仅供开发测试，不受 `amsftp upgrade` 升级渠道管理。

## 升级与回滚

Homebrew 与独立安装均支持以下命令升级：

```sh
amsftp upgrade
```

AMSFTP 会在停止任何进程前先检查新版本；如果存在正在传输数据的任务，会直接拒绝
升级以免中断传输。升级完成后，仅当升级前 daemon 正在运行时才会重启它，并校验新
程序与 daemon 版本一致。

如果升级在**尚未修改数据库格式**之前失败，可以安全回滚：

1. 停止当前用户的 daemon：`amsftp daemon stop --confirm stop`。
2. 恢复上一个校验过的程序（独立安装默认保留在 `<prefix>/bin/amsftp.previous`）、
   手册页与补全文件。
3. 用 `amsftp --version`、`amsftp daemon start`、`amsftp daemon status` 与
   `amsftp doctor` 确认恢复正常。

如果数据库迁移已经开始，或无法确认数据库格式是否已变化，**切勿降级运行旧版本**，
也不要删除 socket 或 SQLite 文件。请按[故障排查与恢复](https://github.com/TyrantLucifer/awesome-sftp-cli/blob/main/docs/zh-CN/help/troubleshooting.md)处理。

## 卸载

1. 先停止自己的后台进程：

   ```sh
   amsftp daemon stop --confirm stop
   amsftp daemon status
   ```

2. 删除程序文件：
   - **Homebrew：** `brew uninstall amsftp`
   - **独立安装：** 删除 `amsftp`、`amsftp.1` 及自行生成的 Shell 补全文件。

用户配置、工作区、任务历史、日志与缓存默认保留。如需彻底清理，请先做好备份：

- **macOS：** `~/Library/Application Support/io.github.tyrantlucifer.amsftp`、
  `~/Library/Caches/io.github.tyrantlucifer.amsftp`、
  `~/Library/Logs/io.github.tyrantlucifer.amsftp`
- **Linux：** 当前有效 XDG config、state、cache、runtime 目录下的 `amsftp` 子目录
  （日志位于 state 目录内）
- **自定义私有数据目录：** 该目录下的 `config`、`state` 与 `cache` 目录

下一步：[使用指南](https://github.com/TyrantLucifer/awesome-sftp-cli/blob/main/docs/zh-CN/user/guide.md)。
