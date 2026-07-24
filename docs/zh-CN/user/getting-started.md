# 开始使用

[English](../../user/getting-started.md)

这篇指南从已有的 SSH 账号开始，带你完成第一次经过验证的文件传输。AMSFTP 会把
认证和主机密钥决定交给系统 OpenSSH，不另外维护一套凭据。

## 1. 准备 SSH 别名

在 `~/.ssh/config` 中添加一个明确的主机条目：

```sshconfig
Host work
    HostName dev.example.com
    User alice
    IdentityFile ~/.ssh/id_ed25519
```

别名可以继续使用你熟悉的 OpenSSH 能力，包括 Agent、ProxyJump、
ProxyCommand、密码或 MFA 提示，以及 Kerberos/GSSAPI。

打开 AMSFTP 前，先使用系统客户端连接一次：

```sh
/usr/bin/ssh work
```

新的或发生变化的主机密钥应该在这里核对，交互认证也在这里完成。AMSFTP 不会静默
接受主机密钥，也不会把凭据复制到自己的配置中。

## 2. 检查安装和端点

```sh
amsftp --version
amsftp config validate
amsftp daemon start
amsftp doctor --endpoint work
```

`doctor` 是只读检查。它会检查本地安装、运行目录、daemon、OpenSSH 策略和所选
端点，但不会改动远端文件。如果使用代理，端点可能显示为没有直接探测；这不等同于
认证失败。

如果 SSH 别名使用 Kerberos，请先通过平时使用的系统工具检查或续期票据。

## 3. 打开两个位置

建议先在两栏中各打开一个本地目录：

```sh
mkdir -p /tmp/amsftp-source /tmp/amsftp-destination
printf 'hello from AMSFTP\n' > /tmp/amsftp-source/hello.txt
amsftp /tmp/amsftp-source /tmp/amsftp-destination
```

位置有两种写法：

```text
/absolute/local/path
SSH-alias:/absolute/remote/path
```

相对路径会被拒绝。远端位置中 `:` 前面是 OpenSSH 主机别名，而不是 AMSFTP 另外
保存的主机名。

其他启动方式：

```sh
amsftp
amsftp /absolute/local/path
amsftp work:/absolute/remote/path /absolute/local/path
amsftp --workspace <name>
```

不带位置启动时，选择器会显示保存过的工作区、`local` 和从 OpenSSH 配置中发现的
明确别名。带通配符的 `Host` 模板不会作为具体服务器列出。

## 4. 先做一次本地复制

在左栏中：

1. 使用 `j` 或 `k` 把光标移动到 `hello.txt`。
2. 按 `y` 复制它的引用。
3. 按 `Tab` 切换到目标栏。
4. 按 `p`，检查目标位置后确认。
5. 按 `J` 查看 Job，直到它完成。

`y` 不会读取或上传文件。只有按下 `p` 并创建 Job 后，传输才会开始。内容写入、
校验并提交成功后，目标文件才会以最终名称出现。

按 `q` 可以退出 TUI。后台 Job 会继续运行；重新打开 AMSFTP，或运行
`amsftp job list` 可以查看它。

## 5. 尝试远端端点

把本地测试目录和一个你有写权限的远端目录并排打开：

```sh
amsftp /tmp/amsftp-source work:/absolute/remote/path
```

重复复制步骤。如果认证需要交互，AMSFTP 会使用 OpenSSH 流程，而不是让你保存
密码。如果服务器断开，受影响的文件栏会独立重连，另一栏仍然可用。

远端到远端复制需要打开两个 SSH 位置。公开版本会通过本地 daemon 的受限内存中继
数据；它不会在服务器之间复制凭据，也不会把整个文件保存到预览缓存。

## 6. 保存工作区

按 `S`，输入名称并确认。工作区会记住：

- 两个端点别名和路径；
- 当前活动栏；
- 排序、过滤和隐藏文件选项；
- 该工作区的缓存策略。

工作区不会保存密码、私钥、展开后的 SSH 配置、Agent 内容或 Kerberos 票据。

以后可以这样重新打开：

```sh
amsftp --workspace <name>
```

如果保存的远端目录已经不存在，AMSFTP 会尝试最近的可访问上级目录，并告诉你实际
打开了哪里。

## 接下来读什么

- [日常使用](everyday-use.md)介绍导航、选择、工作区和抽屉。
- [传输和 Job](transfers.md)介绍移动、冲突、任务控制和恢复。
- [预览、编辑和搜索](preview-edit-search.md)介绍文件查看与本地编辑。
- [故障排查](../help/troubleshooting.md)从常见现象开始处理问题。
