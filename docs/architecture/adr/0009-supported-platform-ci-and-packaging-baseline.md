# ADR-0009：冻结支持平台、CI 与首发包装基线

- 状态：Accepted
- 日期：2026-07-15
- 影响范围：正式支持承诺、OpenSSH 基线、GitHub Actions、发行物与包标识

## 背景

“可以交叉编译”不等于“正式支持”。项目需要为最低 macOS/Linux、两种 CPU 架构、系统 OpenSSH 和包管理器定义可持续验证的承诺。runner label 会退役，`*-latest` 会漂移；如果最低系统没有原生门禁，Stage 6 不能诚实声明生产支持。

## 决策

### 1.0 正式支持矩阵

| 平台 | 正式支持下限 | 架构 | 说明 |
| --- | --- | --- | --- |
| macOS | macOS 15 | arm64、amd64 | Terminal/iTerm2、系统 OpenSSH 与外部 Vim 工作流均需原生证据 |
| Linux | Ubuntu 22.04 LTS | amd64、arm64 | Ubuntu 24.04 LTS 同为当前测试线 |

其他 glibc/musl Linux 发行版可以作为 best-effort 二进制兼容，不在没有原生安装、终端、OpenSSH 和 filesystem 证据时标为正式支持。Linux kernel 的 Go 技术下限也不能替代发行版支持承诺。

OpenSSH 8.9p1+ 是集成测试基线，因为 Ubuntu 22.04 提供该版本。运行时诊断记录脱敏的 `ssh -V`，但不因无法解析版本字符串而硬拒绝；真实子系统连接和能力结果优先。项目不自行开启 GSSAPI、转发 agent/凭据或改写用户 ssh_config。

macOS 14 虽高于 Go 1.25 的技术下限，但 GitHub 已宣布其 hosted runner 于 2026-07-06 开始退役并在 2026-11-02 完全停止支持。没有新增自托管真机门禁前，1.0 不承诺 macOS 14。

### CI provider 与固定矩阵

使用 GitHub Actions，所有 action 固定完整 commit SHA，所有 OS 使用具体 label：

- quality：`ubuntu-24.04`。
- native stable：`ubuntu-22.04`、`ubuntu-24.04`、`macos-15`、`macos-15-intel`。
- native oldstable：同一四 OS 矩阵，精确 Go 1.25.12 与 `GOTOOLCHAIN=local`。
- build：darwin/linux × amd64/arm64，`CGO_ENABLED=0`、`-trimpath`。
- 当前系统定期兼容：在 UI/daemon 可运行后增加 macOS 26；不得用 `macos-latest` 替代最低线。

`macos-15` 在当前 GitHub Actions 是 arm64，`macos-15-intel` 是 amd64；GitHub 当前公告 Intel label 供应到 2027-08，若发布门禁执行时已不可用，必须换成等价受控 Intel runner，不能删除 Intel 证据。Linux ARM hosted labels 的可用性与仓库可见性/计划有关；Stage 6 发布前必须用 `ubuntu-22.04-arm`/`ubuntu-24.04-arm` 或等价受控 arm64 runner 完成原生安装与冒烟。若没有可用 runner，该架构保持“构建成功、正式支持未验证”，并阻塞 1.0 对该架构的支持声明。

托管运行的 setup log 或 provenance 记录实际 runner image/version、commit、tree、Go version、GOOS/GOARCH 和 clean status。runner image 每周更新，因此 label 固定仍需在验证记录中保留 ImageVersion 证据。远端 workflow 只有在用户明确授权、且运行树与本地审阅 tree 精确相同时才可计入门禁。

### 包装与发行标识

首发渠道为不可变 GitHub Release，规范资产名为：

    amsftp_<version>_<os>_<arch>.tar.gz
    checksums.txt
    sbom.spdx.json

package/application ID 为 `io.github.tyrantlucifer.amsftp`；Homebrew formula 名为 `amsftp`，launchd label 为 `io.github.tyrantlucifer.amsftp.daemon`，systemd user unit 为 `amsftp-daemon.service`。首发不制作 `.app`、DMG 或 App Store 包。

每个 release 由受保护 tag/environment 生成，附 SHA-256、SBOM、GitHub artifact attestation/provenance。Homebrew 是第二分发渠道，formula 只引用不可变 release URL 和 SHA-256；Homebrew 不能成为 helper 的信任根。

macOS 两个架构的正式 release binary 必须在受保护 release environment 中使用 **Developer ID Application** identity、hardened runtime（`codesign --options runtime`）与可信 timestamp 签名，并通过 strict codesign verification。随后把每个已签名二进制的精确 bytes 放入仅用于提交的临时 ZIP，使用 Apple notary service 等待 `Accepted`；最终仍发布上述 tar.gz，但其中 binary 必须与已接受 submission 的 binary byte-identical。release provenance 记录 certificate Team ID/leaf fingerprint、binary CDHash、notary submission ID/status、签名后 artifact SHA-256 和验证 runner，不记录证书私钥或 notary 凭据。

Developer ID 私钥和 App Store Connect/notary 凭据只授予受保护 tag/environment 的最小 release job；PR、普通 branch、fork 和 artifact producer job均无权读取。签名、timestamp、notarization、byte-identity 或验证任一失败都阻止 macOS release，禁止回退为未签名/adhoc 产物后仍声明正式支持。

同一个 platform binary bytes 同时承载 client/daemon/askpass/helper roles，因此 Helper Manifest v1 的 `size`/`sha256` 与 offline Ed25519 signature 必须针对**最终发布 bytes**生成。darwin 顺序固定为 build → Developer ID+hardened runtime+timestamp signing → strict verify → byte-identical ZIP notarization `Accepted` → freeze accepted signed binary → 生成/离线签署 Helper manifest → 把同一 binary bytes 放入最终 tar；Linux 则对最终 unsigned release binary freeze 后生成/签署 manifest。四个平台的 manifest hash、最终 tar 内 binary hash、`checksums.txt`、SBOM 与 attestation/provenance 必须交叉一致；禁止对 pre-sign macOS binary 生成 manifest 后继续发布。tar.gz、checksums 与 attestation 只能在上述平台 bytes 和 helper manifest/signature 冻结后生成。

## 替代方案

### macOS 14 或只按 Go 技术下限支持

会得到更宽名义范围，却无法用持续 hosted runner 证明，故不采用。未来若引入受控 macOS 14 真机，可用新 ADR 下调最低版本。

### 只测试 Ubuntu 24.04/macOS arm64

无法覆盖 Ubuntu 最低线和仍承诺的 Intel macOS，交叉构建又不能证明终端、socket、filesystem 或 OpenSSH 行为，故不采用。

### 多 CI provider、DMG/App Store 首发

增加凭据、签名和维护面，对命令行产品没有首发收益。GitHub Actions + tarball + 后续 Homebrew 足以建立可审计安装链。

## 安全与可测试性检查

- PR workflow 无 secrets、最小 permissions、action SHA 固定；发布 job 只在受保护 tag/environment 获得 attest 权限，macOS signing/notary job 才获得 Developer ID/notary 凭据且不得把凭据传给构建 producer。
- 四个正式 OS/CPU 组合最终都要运行原生 help/version、TUI 恢复、socket peer、OpenSSH 和安装/卸载冒烟；交叉编译只计 build evidence。
- 支持矩阵覆盖 password/key/agent/keyboard-interactive、GSSAPI、ProxyJump/ProxyCommand 和 ControlMaster；这些真实环境验证未完成前不得写成已支持证据。
- Stage 6 验证 clean install、升级、数据库迁移、package checksum/SBOM/attestation 和回退策略；macOS 15 arm64/amd64 在干净机上给下载归档施加真实/等价 quarantine，再解压并运行 `codesign --verify --strict`、`spctl --assess --type execute` 与 `amsftp --version`，证明 Gatekeeper 可接受且 binary CDHash/bytes 对应已接受 notarization。
- release-order 测试必须证明 darwin pre-sign bytes 不匹配且绝不进入 Helper manifest，Accepted submission ZIP、manifest `size`/`sha256` 与最终 tar binary byte-identical；Linux manifest 同样只绑定最终 tar binary。任一平台重签、重建或重打 binary 都使 manifest/signature/checksum/attestation 重新生成并重跑门禁。

## 后果

- CI 时间增加，但最低系统与 Intel/ARM 差异成为显式门禁。
- Ubuntu 以外用户仍可使用静态二进制，但问题需要先在可复现环境确认后才进入正式支持范围。
- 当前 workflow 尚未执行远端授权运行，且 Linux arm64 原生 runner 尚未绑定；Stage 0/Stage 6 文档必须继续把这些列为缺失证据。

## 参考依据

- [GitHub hosted runner labels](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)
- [GitHub runner image support and labels](https://github.com/actions/runner-images)
- [Go minimum requirements](https://go.dev/wiki/MinimumRequirements)
- [Ubuntu 22.04 OpenSSH source package](https://packages.ubuntu.com/source/jammy/openssh)
- [OpenBSD `ssh` manual](https://man.openbsd.org/ssh) 与 [`ssh_config`](https://man.openbsd.org/ssh_config)
- [GitHub artifact attestations](https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/use-artifact-attestations)
- [Homebrew Formula Cookbook](https://docs.brew.sh/Formula-Cookbook)
- [Apple: Notarizing macOS software before distribution](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution)
- [Apple: Resolving common notarization issues](https://developer.apple.com/documentation/security/resolving-common-notarization-issues)
