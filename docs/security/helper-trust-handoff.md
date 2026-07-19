# Helper Ed25519 信任交接

- **契约来源**：[ADR-0010](../architecture/adr/0010-helper-artifact-trust-and-distribution.md)
- **记录日期**：2026-07-16
- **记录状态**：仅完成非生产 public-only tabletop；production custody gate **CLOSED**
- **授权范围**：本记录不授权 Helper 下载、上传、安装、签名或分发实现

## 1. 当前结论

这个工作环境不能证明真实的离线设备、保险柜、具名 custodian、双人审批或恢复演练。因此仓库中没有 production Ed25519 public key，没有 production private key，也没有 production Helper 签名或产物。任何人都不得把下面的 RFC 8032 public test vectors 注册为 production trust set。

Helper 生产分发继续 fail closed：只有本文件第 7 节全部由真实责任人签署，并且 production public key/key ID 已经由第 3 节的离线 ceremony 导出并复核后，才允许任何 production Helper 下载、上传、安装入口或信任声明。测试可以用普通 runtime/config 无法开启的显式 non-release fixture 验证 lifecycle/protocol/fallback；production verifier 必须保持空 trust set，不能把 fixture 变成产品资产。

**精确剩余 blocker**：尚未指定并验证真实 offline signing station、至少三名独立 custodian/approver 及其保管位置；production key generation、backup recovery、紧急撤销批准和 current/next 双 key 实操均未发生，也没有对应的具名签字记录。

## 2. Public key 与确定性 key ID 契约

Production key 必须是离线 ceremony 新生成的 32-byte Ed25519 public key。公钥以 64 个小写十六进制字符表示，仅供交接、代码录入和人工复核；客户端内置值必须解码为恰好 32 bytes。

Key ID 的唯一派生规则是：

```text
digest = SHA-256(raw_32_byte_ed25519_public_key)
key_id = "ed25519-" + lowercase_hex(digest)[0:56]
```

结果恰好 64 ASCII bytes，满足 ADR-0010 的 `[a-z0-9][a-z0-9._-]{0,63}`。截断只用于稳定标识；安全判定仍对 trust set 中该 ID 对应的完整 32-byte public key 做 Ed25519 验签。发布者不得手工选择 key ID，也不得用 PEM 文本、base64 文本或其换行参与 hash。若派生 ID 已存在但 raw public key 不同，ceremony 必须终止并重新生成 key；不得覆盖既有映射。

仓库提供两个明确的 [非生产 public test vectors](testdata/helper-ed25519-public-keys.json) 和一个只读 [key ID verifier](verify-helper-public-keys.go)：

```sh
go run ./docs/security/verify-helper-public-keys.go \
  ./docs/security/testdata/helper-ed25519-public-keys.json
```

这些 public keys 来自 [RFC 8032 §7.1](https://www.rfc-editor.org/rfc/rfc8032#section-7.1)，已公开且其测试 private material 也公开；因此它们绝不能成为 production trust root。仓库不保存对应 seed/private key，验证器也没有生成或签名能力。

## 3. Production 离线 custody ceremony（尚未执行）

### 3.1 角色与前置

每次 ceremony 需要六个互不兼任的具名角色：Security Owner、Release Owner、Security Custodian A、Security Custodian B、Recovery Custodian 与 Audit Witness。执行记录必须包含事件 ID、UTC 起止时间、参与者、设备资产编号、介质封条编号、离线工具及 OS image 的 SHA-256、每一步结果和签字。审计记录只保存 public key、key ID、公开 digest 和封条/资产编号，禁止保存 seed、private key、解密材料、屏幕照片或键盘录像。

离线签名站必须是项目批准的专用设备，断开网络并禁用无线硬件。工具从两人核验过 hash 的只读介质启动；Release Owner 只能携入 canonical raw manifest 与独立计算的 SHA-256，签名站不得接触 GitHub token、SSH key 或普通 CI secret。

### 3.2 首次生成与保管

1. Witness 验证房间、设备、只读启动介质和两个全新 backup media 的资产/封条编号，确认无网络连接。
2. A 与 B 分别复核 OS/tool image hash；任一不一致立即中止并记录事件。
3. 在离线站用 OS CSPRNG 生成 Ed25519 key；private key/seed 只存在于离线站的加密 signing volume，禁止打印、拍照、复制到 release/CI 机器。
4. 导出 32-byte public key，按第 2 节派生 key ID。A、B、Witness 分别从 raw public bytes 独立复算并逐字确认 public key、SHA-256 与 key ID。
5. 用项目批准、固定 hash 的离线 secret-sharing 工具把 backup 解锁材料制作成 2-of-3 shares；A、B 和独立 Recovery Custodian 各保管一份。加密 backup volume 制作两份，写后只读复核，分别装入有编号的防拆封介质，存放在两个不同的物理地点。任何单份 share 或单个 custodian 都不能恢复 private key。
6. 关闭并冷启动签名站，按正常签名流程对一份非发布测试 manifest 签名；另一台离线隔离验证机只用导出的 public key 验证 raw bytes。记录 manifest/signature 的公开 SHA-256，不保留测试 artifact。
7. A、B、Witness 签署首次 custody record；只有此后 production public key/key ID 才能提交给客户端 trust set 审查。private material 不进入仓库、GitHub secret、构建日志、客户端或普通备份。

在 secret-sharing 工具、设备和 custodian 尚未具名审批前，本流程只是 runbook，不能算 custody evidence。

### 3.3 每次离线签名

1. Release job 冻结 ADR-0009 要求的最终 bytes，只输出 canonical raw manifest、artifact digest 和 manifest digest。
2. Release Owner 与一名 Security Custodian 在离线站分别复算 manifest digest，并人工比较 release request；不一致即中止。
3. 签名站对完整 raw manifest bytes 调用 Ed25519 signing，禁止签 SHA-256 digest。
4. 只导出 detached signature；release job 使用已批准 public key 重新验签，并再次核对 final artifact size/hash。
5. 审计记录事件 ID、key ID、manifest/artifact/signature 的公开 digest、审批人和结果。不得记录 private bytes。

### 3.4 Recovery ceremony

1. 由 Release Owner 创建 recovery incident；Security Owner 批准目的（设备损坏、年度演练或轮换），冻结所有 Helper 发布。
2. 在新的已核验 offline station 上，由三名 share holder 中任意两名到场；Witness 核对人员、封条和资产编号。发现封条异常立即按 key compromise 处理，不尝试恢复。
3. 两名 custodian 只在离线站恢复 backup 解锁材料，挂载一份加密 backup；第三人不得把 share 复制到任何持久介质。
4. 从恢复的 private key 重新导出 public key并按第 2 节复算 key ID；必须与 custody record byte-equal，否则中止并按 compromise 处理。
5. 对全新非发布 challenge manifest 签名，在隔离验证机用记录中的 public key 验证。记录 challenge/signature 的公开 SHA-256 和 PASS/FAIL。
6. 销毁恢复产生的临时明文与解锁材料，复核 offline volume；重新封存 media/shares。任何无法证明清理的恢复站不再用于生产签名。
7. 即使演练成功，也在下一稳定窗口启动 current/next 轮换；真实灾难恢复后旧 key 不无限期继续作为唯一 current key。

Recovery 至少每 12 个月和 custodian/设备/工具变更后执行一次。失败立即关闭 Helper distribution，并启动第 4 节紧急撤销。

## 4. 撤销窗口与限制

下面是待真实 Security Owner 签署的运维 SLO；目前尚未成为可执行承诺：

| 事件 | 从确认事件起的最长窗口 | 必须动作 |
| --- | ---: | --- |
| suspected compromise | 1 小时 | 冻结 Helper 发布和签名，撤下受控渠道中的相关 assets，保留审计证据 |
| confirmed key compromise | 4 小时 | 双人批准 key revoke；确定受影响 key/artifact、denylist 与每平台 release floor；current key 不再签任何 bytes |
| revocation client policy release | 24 小时 | 发布携带 key revoke/artifact denylist/floor 的受签客户端安全版本；所有 Helper enable/exec 按当前 policy 重验 |
| normal current→next overlap | 至少 90 天且至少跨 2 个稳定 client release，取更长者 | 新客户端先信任 current+next，再用 next 签新 Helper；覆盖窗口后才从新客户端撤销 current |

ADR-0010 是离线 client policy：撤下 release asset 不能撤销已经缓存的有效签名，旧客户端也不会在线学习 revoke。因而 24 小时是“发布撤销 policy”的目标，不是所有安装实例已经生效的保证。无法确认客户端升级时，相关 Helper 必须在支持与分发层保持 disabled；不能把 TLS、GitHub 删除或 artifact attestation 描述为密码学撤销。

## 5. 双 key 轮换 tabletop

- **Exercise ID**：`HT-ROT-2026-07-16-01`
- **范围**：public-only 流程演练；没有签名、private key、Helper artifact、客户端 trust-set 代码或远端动作
- **输入**：test vector `current` 与 `next` public key/key ID；fixture SHA-256 `7e127cd473535922e17b5e8da6adbb1a8e2e6318403c503c3cb75dd3868ef6fc`
- **结论**：tabletop procedure PASS；production ceremony NOT RUN，不能关闭 custody gate

| 步骤 | 模拟动作 | 预期观察 | 结果 |
| ---: | --- | --- | --- |
| 1 | 验证 current/next raw public key 的确定性 ID | 两个 ID 均为 64 bytes、互异且匹配 ADR grammar | PASS（第 2 节命令） |
| 2 | 发布 client N，只内置 current+next，仍由 current 签 release manifest | N 接受 current 与 next；N-1 只接受 current | PROCEDURE PASS；未做签名/runtime测试 |
| 3 | 切换 Helper release N+1 使用 next 签名 | N 接受；N-1 fail closed 到 Level 0 | PROCEDURE PASS；未做签名/runtime测试 |
| 4 | 保持至少 90 天且 2 个稳定 client release 的 overlap | 覆盖记录可审计；current 仍可用于旧 manifest 的 current-policy判断 | PROCEDURE PASS；真实覆盖未开始 |
| 5 | 模拟 current compromise，4 小时内批准 revoke、24 小时内发布 client policy | 新 client 即使数学验签成功也拒绝 current；next 保持可用 | PROCEDURE PASS；真实批准/发布未发生 |
| 6 | 删除 current private material前验证 next recovery | next public ID byte-equal、challenge签名可验证、custody records完整 | NOT RUN：本环境没有 private material/custodian |
| 7 | retirement 后旧客户端尝试 current/next | 旧客户端风险明确；不声称远程撤销，unsupported客户端分发关闭 | PROCEDURE PASS |

Tabletop 证明的是步骤、顺序与 fail-closed 决策可审查；它不证明 production key 可恢复，也不替代 ADR-0010 要求的真实双 key 轮换演练。

## 6. 审计记录最小字段

每个 generation、signing、recovery、rotation 或 revocation 事件至少记录：event ID、UTC 时间、事件类型、角色与具名签字、批准的设备/介质/工具 hash、public key/key ID、manifest/artifact/signature 的公开 SHA-256（若适用）、步骤结果、异常和后续动作。记录必须可追加审计且不得包含 private key、seed、share、passphrase、解密材料或认证凭据。

## 7. Production gate checklist

- [ ] Security Owner、Release Owner、Custodian A/B、Recovery Custodian 与独立 Witness 已具名并签署职责。
- [ ] Offline signing station、两个异地 backup 位置、介质/封条与固定 hash 工具已批准并登记。
- [ ] Production Ed25519 key 已在真实离线 ceremony 生成；public key 与确定性 key ID 已由三人独立复核。
- [ ] 2-of-3 recovery shares 与两份加密 backup 已封存；仓库、CI、客户端和普通 secret store 均无 private material。
- [ ] 第 3.4 节真实 recovery exercise 已 PASS，审计记录可查。
- [ ] 第 4 节撤销窗口与旧客户端限制已由 Security Owner/Release Owner 批准，并有可执行 on-call/发布路径。
- [ ] 使用 production current/next keys 的完整双 key rotation 已演练：overlap、next signing、current revoke、旧客户端降级与 next recovery 均有记录。
- [ ] 审查者确认 production trust set 只包含批准 key，不包含本仓库 RFC test vectors。

只要任一 checkbox 未满足，Helper distribution gate 就保持 **CLOSED**。本文件当前所有 checkbox 均有意未勾选。
