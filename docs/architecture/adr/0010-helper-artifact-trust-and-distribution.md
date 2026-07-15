# ADR-0010：冻结 Helper 产物信任与分发机制

- 状态：Accepted
- 日期：2026-07-15
- 影响范围：Stage 4 helper 构建、签名、下载、安装、轮换与降级

## 背景

Helper 是同一 `amsftp` 代码库的受限远端角色，但它会被客户端上传并在用户开发机执行。只校验下载链路或 release 页面给出的 SHA-256，不能抵抗分发位置、manifest 或上传过程被替换。与此同时，helper 必须能在远端无 GitHub API、无 cosign/minisign 工具时离线验证，且任何失败都回退标准 SFTP。

该机制必须在任何 helper 下载、上传或安装实现进入仓库前确定。Stage 0 冻结协议与信任模型；实际 release key、演练和远端矩阵仍是 Stage 3 exit/Stage 4 entry 的实施门禁，不能冒充已经完成。

## 决策

### 产物与角色

- Helper 仍由同一 module、同一版本的 `amsftp` 构建，受限入口为 `amsftp helper serve`；不建立独立仓库、常驻服务或公开日常 CLI。
- 远端产物按 OS/arch/version/hash 不可变命名，只覆盖明确支持的远端矩阵；安装必须由用户显式批准。v1 唯一安装根是 SFTP `RealPath(".")` 所代表登录 home/cwd 下的 `.local/lib/amsftp/helpers/`，不接受用户自定义绝对安装路径、`TMPDIR` 或 remote sticky directory。
- 标准 SFTP 是永久 Level 0。缺失、拒绝、验签失败、版本不兼容或撤销都只禁用 Helper，不破坏浏览与基础传输。

### Manifest v1 与签名

每个产物伴随固定 ASCII/LF line manifest（ASCII 是 UTF-8 子集）。manifest 总长不得超过 512 bytes，读取时先施加该上限；它恰好包含以下 9 行、按此顺序排列，并以第 9 行后的单个 LF 结束：

    amsftp-helper-manifest-v1
    version=<release-version>
    protocol_major=<decimal>
    os=<goos>
    arch=<goarch>
    size=<decimal-bytes>
    sha256=<lowercase-hex>
    key_id=<restricted-ascii>
    min_client=<release-version>

Manifest v1 值域固定如下；parser 必须在任何数值比较或分配前执行长度/字节检查：

| 字段 | 规范值 |
| --- | --- |
| `version`、`min_client` | canonical release version `MAJOR.MINOR.PATCH`；每个分量是单个 `0`，或匹配 `[1-9][0-9]{0,9}`，且数值 ≤ 2147483647，总长 ≤ 32；禁止 prerelease、build、空分量与字符串/浮点比较 |
| `protocol_major` | `[1-9][0-9]{0,4}` 且数值 1..65535 |
| `os` | `darwin` 或 `linux` |
| `arch` | `amd64` 或 `arm64` |
| `size` | `[1-9][0-9]{0,8}`，数值 1..134217728；Manifest v1 的编译期/versioned `MaxHelperArtifactBytes` 固定为 128 MiB，提升上限必须新增 ADR/manifest 版本 |
| `sha256` | 恰好 64 个小写 `[0-9a-f]` |
| `key_id` | 1..64 bytes，匹配 `[a-z0-9][a-z0-9._-]{0,63}` |

版本解析为三个整数并按 major/minor/patch 逐项比较。禁止 BOM、NUL、CR、空格、空行、额外/重复/未知/乱序字段、额外末尾 bytes 或缺失末尾 LF。

Ed25519 对上述完整原始 manifest bytes 签名。detached signature 文件恰好是 88 个 RFC 4648 standard padded base64 ASCII bytes（`[A-Za-z0-9+/]{86}==`）后跟单个 LF，总长 89 bytes；拒绝 URL alphabet、无 padding、空白、CRLF、额外行和任何解码后重新以 `base64.StdEncoding` 编码不完全相同的表示。解码结果必须恰好 64 bytes。客户端内置 `key_id -> ed25519.PublicKey` 集合，使用 Go 标准库 `crypto/ed25519` 验证，不依赖远端工具或在线 API。

release 私钥离线保管，不存入仓库、普通 GitHub secret、构建日志或客户端。受保护 release 流程把 canonical raw manifest bytes 与独立计算的 SHA-256 一起送到离线签名站；签名站先核对 digest，再对原始 bytes 调用 `ed25519.Sign`，客户端也对完全相同的原始 bytes 验签，禁止把 SHA-256 digest 当 Ed25519 message。签名送回受控发布 job，构建 job 无私钥。GitHub artifact attestation 作为公开供应链补充，不能替代客户端的 Ed25519 强制校验。

### 安装验证链

1. 只接受内置/受信release channel的canonical raw manifest、detached signature与artifact reference；先按**当前客户端policy**完成canonical parse、Ed25519验签、key/artifact撤销、protocol/`min_client`与release floor。raw manifest/signature原样持久于本机受保护状态；此时不按旧cache跳过policy、不读artifact bytes、不检查Endpoint high-water，也不触碰remote install tree。
2. 先做**preliminary mapping consent**：展示Endpoint、manifest声明target、`shared-session-stable-home`的含义、将执行的只读SFTP utility/binding probe以及ssh daemon/audit/atime/shell-startup可能有环境副作用；明确此刻尚不知道/不声称actual remote uid/home/final path。用户或受管profile必须显式assert SFTP、probe与formal exec跨session映射同一canonical home/filesystem；默认unknown、per-session namespace、load-balanced node-local home或异构证据保持Level0。未确认不得probe。
3. consent后，SFTP `lstat`验证`/usr`、`/usr/bin`为uid0真实且group/other不可写目录，`/usr/bin/printf|id|uname`为uid0真实、不可扩写且可执行regular；不经PATH或usrmerge`/bin` symlink。随后以helper fresh ssh argv执行固定probe：`/usr/bin/printf 'amsftp-helper-bind-v1\000' && /usr/bin/id -u && /usr/bin/printf '\000' && command -p pwd -P && /usr/bin/printf '\000' && /usr/bin/uname -s && /usr/bin/printf '\000' && /usr/bin/uname -m && /usr/bin/printf '\000'`。stdout须从byte0精确解析magic、decimal uid、physical cwd、OS、arch与NUL边界，总长≤4096且无tail；stderr并发drain，65536/65537边界。banner/timeout/nonzero/字段异常失败；uid须1..2147483647，OS仅Darwin/Linux，arch仅x86_64/arm64/aarch64映射。
4. probe cwd须byte-equal同Endpoint SFTP `RealPath(".")`；这只是shared-policy下compatibility preflight，不证明node/mount/inode/object。home须absolute ASCII，每component 1..255且匹配`[A-Za-z0-9._-]+`、非`.`/`..`；不安全字符不做通用quoting。observed OS/arch须匹配signed target，然后才检查该Endpoint+protocol+observed target的version/hash high-water；任何降级/同版本异hash拒绝。
5. high-water通过后才以signed `size+1`上限流式读取本地artifact并复算size/SHA-256；额外byte、0、>128MiB或不匹配拒绝，不能按size整块分配。Linux绑定最终unsigned bytes；Darwin production bytes还须服从ADR-0009/Stage6的sign/notary顺序。
6. 只用fresh typed fields派生`<safe-home>/.local/lib/amsftp/helpers/p<protocol>/<version>/<os>-<arch>-<sha256>/amsftp`。read-only逐级`lstat`现有ancestor：home owner=probe uid；其他目录仅root/probe uid、真实、group/other不可写；app tree/派生目录须probe uid真实exact`0700`。列出尚需创建目录。suffix≤320，所有app-owned absolute dir/final/temp≤1000且component≤255；temp basename固定`.amsftp.tmp-<32-lowerhex>`（44 bytes、128-bit CSPRNG）。SFTP attrs不冒充ACL证明。
7. 在任何remote app-tree create/content write前做**final install-plan consent**，展示fresh actual uid/OS/arch/home、全部create目录、final/temp grammar、mode、version/key/source、size/SHA-256、floor/high-water、能力与禁用/移除方式，并再次声明mapping/object/ACL边界。确认绑定当前manifest digest、probe observation与derived plan；任一字段/attrs在create前变化必须回到probe/plan并重新确认。取消保持install tree零create/零content write。
8. final consent后才创建/复核exact`0700`目录；在verified sibling用`O_WRONLY|O_CREATE|O_EXCL`打开temp。因pkg/sftp v1.13.10 OPEN不带permission attrs，首个content byte前须handle `Chmod(0600)`，再分别handle stat/path lstat确认owner、regular、mode0600、size0、非symlink；SFTP v3无stable file-id，不声称二者证明同一object。entropy/O_EXCL/attrs失败不写byte。
9. 通过verified handle写恰好signed size；close后客户端再以expected+1回读复算size/hash，不信existing helper/remote digest。通过后才chmod0700并lstat；仅standard SFTP target-exists-fails rename，禁止posix-rename/overwrite/delete-first。existing final仅attrs/hash完全相同才no-op；publish后再次lstat+回读。每次exec仍重跑current policy、fresh binding、high-water、ancestor/final/hash与wire handshake。
10. 未知/撤销/降级、manifest/artifact/binding/plan/launch/handshake失败均不执行。清理只针对本次exact unpredictable temp且先重验parent/path，不递归/模糊删除。probe可能产生sshd audit/lastlog、atime或shell-startup副作用；“零写”仅指final consent前application-managed install tree零create/零content write，不虚构整个remote system无状态变化。

SFTP v3 的 server-reported uid/mode 不是 ACL、NFSv4 policy、macOS ACL、LSM 或导出策略的独立证明。本契约只在受信 sshd/SFTP server、remote root/admin、登录 shell/startup configuration、跨 session 的 shared-home mapping 和授权策略如实执行普通 POSIX DAC 的前提下，降低非特权其他 UID、poisoned `PATH` 与非恶意替换风险；same-euid、root/admin、被授权 ACL principal、用户可控 shell/startup 和恶意 server 都在远端信任边界内。产品 UI/日志不得把 mode/probe/hash 字符串检查表述为“证明相同对象或其他用户绝不可访问”；若托管环境要声明用户隔离，必须额外用第二 principal 端到端验证实际拒绝。关闭后 path 回读 hash、发布后的全校验和每次 exec 前复核仍是强制故障/篡改检测，但不能被描述为消除了同 euid/root/server 在“校验后、使用前”的主动竞态或跨节点映射差异。扩大威胁边界必须新增稳定 file-id/handle-publish 与可验证 authorization 能力的 ADR，不能冒充 Level 0 SFTP 保证。

### Helper 启动 command-string 契约

OpenSSH exec channel 不提供远端 argv；它把 remote command string 交给服务端，通常由用户登录 shell `-c` 解释。helper 的本地进程 argv 因此固定为以下数组，其中最后一个元素是**唯一一个** remote command argument；不得再追加第二个 command argument：

```text
[
  "<validated-absolute-ssh-path>",
  "-T",
  "-oEscapeChar=none",
  "-oForwardAgent=no",
  "-oForwardX11=no",
  "-oPermitLocalCommand=no",
  "-oClearAllForwardings=yes",
  "-oRemoteCommand=none",
  "-oStdinNull=no",
  "-oForkAfterAuthentication=no",
  "-oTunnel=no",
  "-oGSSAPIDelegateCredentials=no",
  "-oControlMaster=no",
  "-oControlPath=none",
  "-oControlPersist=no",
  "-oSessionType=default",
  "<validated-host-alias>",
  "<restricted-remote-command>"
]
```

binding probe 的 remote command 恰好是第 3 步固定 string。正式启动 string 只能由 fixed literals、**本次 fresh binding probe 得到并通过 strict-safe grammar 的 canonical absolute home**，以及**本次 exec 前刚按当前 policy 重新验签并解析**的 typed manifest 确定性生成，格式恰好为：

```text
exec <safe-canonical-home>/.local/lib/amsftp/helpers/p<protocol_major>/<version>/<os>-<arch>-<sha256>/amsftp helper serve
```

string 必须 ≤ 1536 ASCII bytes，且其中的 executable absolute path 仍服从 `MaxHelperRemotePathBytes=1000`；除 fixed literals、fresh binding probe 得到并通过 safe-home grammar 的 canonical absolute home，以及通过 Manifest v1 grammar 的 protocol/version/os/arch/sha256 外，不允许任何可变内容。不能从数据库读取任意 pathname/command，不能包含 key_id、用户选择路径、文件名、搜索词、endpoint 显示名或未解析 manifest value。所有业务 path/pattern/options 只在 helper 启动后通过有界 framed stdin 发送。绝对 path 使下一 SSH session 即使 profile `cd /tmp` 也不会改选 `/tmp/.local/...`；remote shell 必须兼容绝对 `/usr/bin/printf`/`id`/`uname`、`command -p pwd -P` 与 shell builtin `exec`。登录 shell、它读取的 startup configuration、`sshrc` 与 remote-user same-euid 修改属于明确受信边界；custom/non-POSIX shell、ForceCommand、banner、SFTP `-d`/chroot namespace 不一致、command 被改写、超时/超限/非零都只禁用 helper并保留 Level 0，不尝试通用 quoting fallback。

helper stdout 的 byte 0 必须开始精确协议 preface `amsftp-helper-wire-v1\n`，随后才是版本化长度帧；客户端禁止扫描 banner 后重新同步 magic。formal launch 的 stderr 同样并发独立 drain并服从 `MaxHelperStderrBytes=65536`，第 65537 byte 取消会话，raw bytes 不持久化且诊断只保留有界脱敏摘要，避免无限 stderr 占用内存或堵塞 pipe。binding/formal helper每次都使用上述fresh transport flags，不复用可能已有delegation/forward的ControlMaster，并保留GSSAPI认证但不请求credential delegation。系统ssh仍处理host key/auth/proxy；remote same-euid在环境中原本可访问的cache、登录shell/startup与server/admin是信任边界，产品只能说“本次调用不传入/持久化认证回答且不新增委派”，不能声称远端绝无任何credential。stdout前置banner、截断preface、畸形首帧或stderr超限都使本次helper失败。

### 每次启用与执行的 freshness

每次把已安装 helper 标为 enabled、每次 exec 前，都必须从本机受保护持久层重新读取 canonical raw manifest bytes 与 detached signature。先按**当前客户端**内置 policy 执行 canonical parse、Ed25519 验签、key revoke、artifact denylist、客户端支持的 protocol/`min_client` 与 release floor；再运行 fresh binding probe，严格映射/比较 observed OS/arch、取得 strict-safe canonical home，并对对应 Endpoint+protocol+observed target 检查 high-water。随后只由 safe home+typed fields 重新派生 absolute final path/command，再重跑完整 ancestor attrs、final regular/owner/`0700`/size/hash/no-symlink 校验，最后启动并握手。raw metadata/signature 缺失、客户端升级后 key 被撤销/产物被 deny/最低 floor 提高、持久 DB path 与派生 absolute path 不一致或任一步失败都禁用 helper。旧的“已验证”布尔值和 version/hash row 只能作为索引/审计，不能跳过当前 policy。

验签只证明发布者身份，不单独证明产物仍然足够新。客户端 release 必须同时内置：

- 每个 `protocol_major/os/arch` 的最低可接受 Helper 版本；全新安装也拒绝低于该 floor 的旧但签名有效产物。
- 精确 artifact denylist，键至少包含 `version/os/arch/sha256`；它用于只撤销单个脆弱产物而不必撤销整把 key。
- 每个 Endpoint 与 `protocol_major/os/arch` 已成功安装/启用的 `(version, sha256)` 单调 high-water mark。低于记录 version 的普通安装拒绝；相同 version/相同 hash 且现有不可变文件仍通过 hash/mode 检查时返回 no-op，文件缺失/损坏时只能经显式批准重装同一 hash；相同 version/不同 hash 一律视为 same-version republish 并拒绝。更高 version 只有在安装、握手成功后才原子更新记录。显式 break-glass 只允许一次性运行不低于客户端 release floor、未在 denylist 且仍通过验签的旧版本，并且不得降低或改写持久 high-water mark。

安装顺序固定为：current-policy canonical parse/signature/revoke/denylist/protocol/`min_client`/floor → preliminary mapping+probe consent → read-only utility/fresh binding得到uid/home/OS/arch → target match+Endpoint high-water → local artifact expected+1/size/hash → derived path+read-only ancestor/final plan → final actual-plan consent → create/temp/upload/client-readback/no-replace/final/handshake。任何字段改变回到相应前置并重新final consent；创建前拒绝保持app install tree零create/零content。每次exec无需重复安装确认，但仍按“每次启用与执行”重跑current policy→fresh binding/target/high-water→ancestor/final/hash→handshake。恢复模式也不可绕过revoke/deny/floor。

### 轮换、撤销与分发

- 至少支持 current/next 两把公钥重叠；新客户端先发布 next key，再使用 next 签名，最后在覆盖升级窗口后撤销旧 key。
- key 撤销、artifact denylist、每平台 release floor 随客户端 release 发布并受同一代码签名/发布链保护；命中任一撤销规则时，即使数学验签成功也拒绝安装和执行。
- 版本比较同时检查 manifest version、协议 major、最低 client、客户端 release floor 和已安装 high-water mark；默认禁止降级，显式 break-glass 恢复也必须展示风险、验签且遵守不可绕过的撤销/floor 规则。
- Helper assets 由不可变 GitHub Release 分发，可被包管理器预取/缓存；运行时不把 Homebrew、GitHub API 或 TLS 单独当信任根。
- Helper 与 client/daemon/askpass 是同一 platform binary bytes。Manifest v1 只能在 ADR-0009 最终 artifact bytes 冻结后生成并离线签署：darwin 必须晚于 Developer ID signing/timestamp、strict verification 和 byte-identical notarization `Accepted`，Linux 必须针对最终 unsigned release binary；manifest `size`/`sha256`、最终 tar 内 binary、checksums/SBOM/attestation 必须一致，pre-sign macOS bytes 的 manifest 一律禁止发布。

在 Stage 3 标记 Complete 前，必须批准并演练 key custody/恢复 runbook、生成第一组 public key/key ID、定义撤销窗口并完成一次双 key 轮换演练。任一项缺失都阻塞 helper 分发代码和 Stage 4 M4.2，不阻塞 Level 0 SFTP 搜索基线。

## 替代方案

### 只有 SHA-256 或 GitHub attestation

若预期摘要与产物来自同一被替换位置，hash 不能提供独立身份。attestation 适合公开 provenance，但远端安装器不能假设在线 GitHub/Sigstore 验证，故只能作为补充。

### Sigstore/cosign keyless

供应链身份强，但客户端离线验证 bundle 会引入较大的依赖与证书/issuer policy，远端环境也不保证 cosign。未来可并行增加公开证明，不能替代 Ed25519 内置根。

### minisign 或自动安装未签名最新版本

minisign 格式可靠，但会新增外部格式/工具依赖；未签名 latest 明确违反不可变和显式信任边界，故不采用。

## 安全与可测试性检查

- raw-byte golden vectors 覆盖规范 manifest、固定公钥/签名、每个字段/总长边界、CRLF、缺/重/未知/乱序字段、尾随 bytes、错误 key、错误地签 digest；version/min_client 覆盖前导零、prerelease/build、溢出和数值三元组比较。
- signature vectors 覆盖 RFC 4648 standard padded canonical form，以及 URL alphabet、无 padding、whitespace/CRLF、非 canonical trailing bits、错误长度与额外行拒绝。
- 负向集成覆盖产物篡改、manifest 替换、截断/追加、size=0/超 128 MiB/expected+1、错误 OS/arch/protocol major/`min_client`、远端回读不一致、remote euid 0、ancestor other-owner/group-or-other-write/missing attrs/symlink、远端 umask `000`/`022`、handle `Chmod`/stat 失败、预置 0644 文件/symlink、`O_EXCL` 竞态、standard-vs-posix rename、既有 final 同/异 hash、各校验阶段间可检测的意外 path 替换、未知/撤销 key、artifact denylist 和降级；测试必须证明 `Chmod(0600)` 与 handle/path 复核都先于首个 write，所有上传前拒绝分支断言 application-managed install tree 零创建/零内容写入，并验证不覆盖 final、不递归/模糊删除、不执行校验失败对象。fake server “报 0700 但第二 principal 可访问”必须证明产品不会误报强隔离；受管支持矩阵只有在真实第二 principal 被拒后才可声明隔离。测试与报告不得把结果外推为 same-euid/root/admin/server 主动竞态防护。
- helper command集成覆盖真实OpenSSH 8.9 shell`-c`、逐项精确本地argv、最后恰一remote-command arg，并用冲突config/预建master证明GSS delegation off及ControlMaster/Path/Persist off确实fresh且GSSAPI认证仍可用。其余覆盖`Darwin/Linux`与支持arch映射、unknown probe字段、`/usr/bin`attrs、poisonedPATH 0-hit、usrmerge、safe-home/component/path/temp/entropy边界、用户path/pattern 0进入command、profile/SessionType/RemoteCommand/banner/ForceCommand/custom shell/chroot、uid0、stdout byte0与stderr 65536/65537；失败后Level0立即可用且不误称remote same-euid环境无credential。
- session-mapping 集成必须覆盖默认 `unknown` policy、明确 shared-session-stable mapping 和负载均衡双节点“相同 `pwd`/RealPath 字符串但 helper bytes 不同”；后者必须禁用 Helper或证明只在明确受信 policy 下运行，报告不得把字符串/hash preflight 误称为 node/mount/object binding。
- release 演练覆盖 current/next 重叠、丢失私钥、key 与单产物撤销、全新客户端重放旧但签名有效产物、已安装 high-water mark、同版本同 hash no-op/修复、同版本异 hash 拒绝和无网络离线验证；另逐平台证明最终 tar binary 与 manifest size/hash 一致，darwin pre-sign bytes 被拒且 Accepted ZIP/最终 tar/manifest 三者 byte-identical。另以旧客户端安装后升级到撤销 key、artifact denylist 或更高 release floor 的新客户端，证明每次 exec 重跑 raw manifest/signature 当前 policy 并拒绝旧 helper；删除 raw metadata/signature 同样禁用。
- installer状态机测试逐步断言preliminary consent前0 probe，final actual-plan consent前0 app-tree create/content，且展示字段不把unknown uid/path伪装为actual；probe/manifest/attrs变化强制重新确认，取消无remote install write。installer不把数据库任意path、用户路径或raw manifest值拼shell；Helper command只由fixed literals/fresh safe home/typed fields生成，不执行未校验bytes、不按size无界分配、不模糊清理。

## 后果

- 项目承担离线 Ed25519 key custody 和轮换运维，但运行时信任边界小、无需外部验证器。
- 每个 Helper release 增加 manifest/signature 资产和一次离线签名步骤。
- Stage 0 只冻结机制；目前没有 key、签名产物、installer 或已验证远端 Helper，功能矩阵不得提前升级。

## 参考依据

- [Go `crypto/ed25519`](https://pkg.go.dev/crypto/ed25519)
- [GitHub artifact attestations](https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/use-artifact-attestations)
- [OpenBSD `ssh(1)` remote command semantics](https://man.openbsd.org/ssh)
