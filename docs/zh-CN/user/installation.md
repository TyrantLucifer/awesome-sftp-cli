# 安装和维护 AMSFTP

[English](https://github.com/TyrantLucifer/awesome-sftp-cli/blob/main/docs/user/installation.md)

AMSFTP 支持 Intel 和 Apple 芯片的 macOS，以及 AMD64、ARM64 的 Linux。
Homebrew 是最省事的安装方式；如果不想使用包管理器，可以选择安装到用户目录的
独立版本。

> [!WARNING]
> macOS 公开构建尚未签名或公证。请确认下载来自项目 Release 页面，不要为了运行
> 无法验证来源的程序而关闭系统级安全保护。

## 使用 Homebrew 安装

```sh
brew install TyrantLucifer/tap/amsftp
```

检查程序并启动当前用户的 daemon：

```sh
amsftp --version
amsftp daemon start
amsftp daemon status
```

Homebrew 会同时安装 man 手册，并在自己的前缀中管理可执行文件。

## 安装独立版本

官方安装器默认安装到 `$HOME/.local`，不会调用 `sudo`：

```sh
curl --proto '=https' --tlsv1.2 -fsSL \
  https://github.com/TyrantLucifer/awesome-sftp-cli/releases/latest/download/install.sh | sh
```

安装器会下载适合当前平台的归档，校验 SHA-256，检查安装路径和数据路径的所有者与
权限，再原子替换程序。它也会安装 man 手册并生成 shell 补全。

请确认 `$HOME/.local/bin` 已经在 `PATH` 中：

```sh
export PATH="$HOME/.local/bin:$PATH"
```

如有需要，把这行加入当前 shell 的启动文件。

### 如果安装器要求使用托管根目录

如果路径的所有者、权限、ACL 或符号链接不安全，AMSFTP 会拒绝把私有状态放在它
下面。当普通主目录布局无法通过检查时，安装器会寻找
`/var/lib/amsftp-users/<你的-uid>` 这个私有根目录。

如果目录不存在，安装器会在改变目标之前退出，并打印准确的一次性管理员命令。请让
管理员执行这些命令，再重新运行安装器。不要通过放宽主目录或 AMSFTP 数据目录权限来
绕过检查。

也可以显式选择一个已经准备好的私有根目录：

```sh
sh install.sh --root /absolute/private/path
```

运行 `install.sh --help` 可以查看当前的 `--prefix`、`--root`、`--version`
以及 daemon 启动选项。

## 手动安装归档

1. 打开项目的 [GitHub Releases](https://github.com/TyrantLucifer/awesome-sftp-cli/releases)
   页面。
2. 从同一个 Release 下载适合操作系统和 CPU 的归档，以及
   `checksums.txt`。
3. 解压之前先计算归档摘要：

   ```sh
   sha256sum <downloaded-archive>
   ```

   在 macOS 上运行 `shasum -a 256 <downloaded-archive>`。结果必须与
   `checksums.txt` 中该文件名对应的一行完全一致。
4. 解压到一个新目录，把 `amsftp` 安装到当前用户控制的 `PATH` 目录，例如
   `$HOME/.local/bin`。
5. 把 `share/man/man1/amsftp.1` 安装到对应的用户 man 目录。
6. 使用已经安装的程序生成补全：

   ```sh
   amsftp completion bash
   amsftp completion zsh
   amsftp completion fish
   ```
7. 验证安装：

   ```sh
   amsftp --version
   amsftp config validate
   amsftp doctor
   ```

不要用 root 身份运行 AMSFTP，不要设置 setuid，也不要把它安装到其他用户可以修改
的目录。

## 从源码构建

源码构建需要 Go 1.26.5：

```sh
git clone https://github.com/TyrantLucifer/awesome-sftp-cli.git
cd awesome-sftp-cli
GOTOOLCHAIN=local go build -trimpath -o ./amsftp ./cmd/amsftp
./amsftp --version
```

开发构建适合本地测试，但不属于自动发行升级渠道。

## 升级

无论通过 Homebrew 还是官方独立安装器安装，日常升级都可以运行：

```sh
amsftp upgrade
```

AMSFTP 会先检查是否真的有新版本，再停止任何东西。如果某个 Job 正在传输数据，
升级会被拒绝，以免任务在没有提示的情况下中断。请先完成或暂停重要工作，再重试。

升级命令只会恢复升级前原本就在运行的 daemon，并检查新程序与 daemon 的版本是否
一致。如果它报告升级只完成了一部分，不要删除 socket 或状态数据库。先运行：

```sh
amsftp --version
amsftp daemon status
amsftp doctor
```

Homebrew 用户可以使用下面的命令恢复：

```sh
brew update
brew upgrade TyrantLucifer/tap/amsftp
```

独立安装用户可以重新运行已经验证的安装器。只要原先确实存在一个旧版本，它通常会
把旧程序保留为 `amsftp.previous`。

## 升级失败后的回滚

如果新程序在改变持久状态之前就失败了：

1. 运行 `amsftp daemon stop --confirm stop`，停止已经确认属于当前用户的
   daemon。
2. 恢复上一个已经验证的程序、man 手册和与它匹配的补全文件。独立安装通常会把旧
   程序保留为 `<prefix>/bin/amsftp.previous`。
3. 运行 `amsftp --version`，启动 daemon，再用
   `amsftp daemon status` 和 `amsftp doctor` 检查。
4. 先只读打开一个常用工作区，确认正常后再恢复传输。

如果数据库迁移已经开始、状态格式已经更新，或者无法证明具体改变了什么，不要让旧
程序操作这份状态。先停止通过 AMSFTP 改动文件，完整保留私有状态目录和所有备份，
使用当前程序的只读 `doctor` 结果确定安全恢复路径。不要通过删除控制 socket，或
复制、替换正在使用的 SQLite 数据库来强行回滚。

## 卸载

先只停止自己的 daemon：

```sh
amsftp daemon stop --confirm stop
amsftp daemon status
```

如果使用 Homebrew：

```sh
brew uninstall amsftp
```

如果使用独立安装，只删除安装的 `amsftp` 程序、对应的 `amsftp.1` 手册，以及你
生成的补全文件。用户配置、工作区、Job 历史、恢复记录、日志和缓存默认都会保留。

删除这些数据是另一个破坏性决定。默认位置是：

- macOS：`~/Library/Application Support/io.github.tyrantlucifer.amsftp`、
  `~/Library/Caches/io.github.tyrantlucifer.amsftp` 和
  `~/Library/Logs/io.github.tyrantlucifer.amsftp`。
- Linux：当前有效 XDG config、state、cache 和 runtime 目录下的 `amsftp`
  子目录。日志位于 AMSFTP state 目录中。
- 托管安装：指定托管根目录中的 `config`、`state` 和 `cache` 目录。

删除前请备份仍可能需要的内容。不要删除整个 home、XDG、`/tmp` 或
`/var/lib/amsftp-users` 目录。

下一步：[完成第一次连接](https://github.com/TyrantLucifer/awesome-sftp-cli/blob/main/docs/zh-CN/user/getting-started.md)。
