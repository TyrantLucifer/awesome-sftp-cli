# ADR-0019：安装前路径预检与受管安装根

- 状态：Accepted
- 日期：2026-07-22
- 影响范围：独立安装器、平台路径、daemon 启动、诊断与自升级

## 背景

独立安装器默认把 binary 放到 `$HOME/.local/bin`。部分托管 Linux 主机把 `/home/<user>` 作为指向数据盘的符号链接，且数据盘挂载根由另一个非 root UID 所有。即使最终 home、安装目录和 binary 属于当前用户，该第三方 UID 仍可通过可写父目录替换下面的目录项；当前基于路径的 daemon 启动、配置和 SQLite 打开因此必须继续拒绝这条祖先链。

旧安装器只在发布 binary 后启动 daemon，导致 checksum 与安装成功后才报告不可信祖先；首次安装还会错误声称已保留不存在的 `amsftp.previous`。简单放宽 owner 校验、自动 `chmod`/`chown`、信任同组 UID 或把持久数据放进 runtime 目录都会降低既有信任或恢复语义。

## 决策

1. 发布归档中的 binary 提供不公开展示的 `__install preflight` 角色。安装器在创建目标目录、停止 daemon、保存旧 binary 或发布新文件之前，用 checksum 已验证的 staged binary 对安装目标和 config/state/cache 路径执行只读预检。
2. 默认 `$HOME/.local` 只有在 executable 与持久路径的全部已有祖先通过 root/current-euid、mode、ACL、no-symlink 和文件系统策略时才可使用。预检不创建目录，也不把缺失路径当成已证明安全的最终对象；它验证到最近已有祖先。
3. Linux 默认路径不安全时，安装器只自动尝试已经存在的 `/var/lib/amsftp-users/<uid>`。该目录必须由当前 euid 所有、mode 精确 `0700`，其祖先必须由 root/current-euid 所有且不可被 group/other 写入。安装器不调用 `sudo`、不修改共享挂载点，也不扫描任意目录寻找候选。
4. 可信根不存在或校验失败时，安装器在目标零变更状态下停止，并输出两条有界的管理员初始化命令。用户也可通过 `--root /absolute/path` 显式选择已经预置且通过同一校验的根；`--prefix` 保留普通布局语义，不降低校验。
5. 受管根使用固定布局：`bin/amsftp`、`config/`、`state/`、`state/log/`、`cache/`。根目录中的 `.amsftp-root` 必须由当前 euid 所有、mode `0600`、内容精确为版本化标记且完整祖先链可信。binary 从自身规范路径发现并验证该标记后派生持久路径；受管布局不依赖不可信 HOME 或 XDG config/state/cache 值，runtime 仍使用既有平台策略。
6. standalone 自升级继续从规范 executable 推导 prefix；已有受管标记使发布 installer 自动保持同一布局。显式 CLI 路径 override 继续优先，但不能降低任何 owner/mode/ACL/no-symlink 校验。
7. 不可信 daemon executable 作为本地 configuration/integrity failure 输出，不再归类为 network；machine output 和 doctor 只暴露稳定 code/detail，不输出底层本机路径。
8. 自动切换到受管根不会修改原不可信 prefix，也不会编辑 shell 配置。安装器必须提示把新 `bin` 放在旧 prefix 之前；这是 shell 父进程环境不可由子安装器安全修改的边界。

## 后果

- 典型安全 home 继续使用原布局，无额外文件或迁移。
- 管理员只需一次性预置每用户可信根，后续首次安装、重装和升级均自动复用。
- 安装器可以在任何发布文件替换前拒绝确定性的路径问题；daemon 启动失败仍保留新 binary 与真实存在的 previous 状态，避免在可能发生数据库迁移后猜测回滚。
- 无管理员协作且不存在可信持久根时，产品不能承诺自动成功；该限制来自文件系统权限而非安装器交互能力。

## 验证

- 平台测试覆盖缺失后缀的只读预检、第三方 owner 拒绝、受管路径派生、标记 owner/mode/content 和零 mutation。
- 安装器测试覆盖默认路径拒绝、已预置根自动选择、无可信根时目标零变更、marker 发布、PATH 迁移提示、checksum、首次安装和原子升级。
- CLI/doctor 测试覆盖 configuration + `integrity_failed` 分类及原始路径不泄漏。
