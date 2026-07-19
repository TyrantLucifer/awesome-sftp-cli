# 依赖与供应链策略

## 1. 产品依赖边界

当前产品依赖由根 `go.mod`/`go.sum` 锁定。新增或升级依赖必须有明确必要性说明，并完成许可证、安全性与 Go 1.25.12/1.26.5 兼容影响审查；若依赖改变架构边界、数据流或兼容策略，还必须新增 ADR。依赖更新不得作为无关任务中的顺手重构。

当前关键产品 pin 为：

| 领域 | Module | 精确版本 | 约束来源 |
| --- | --- | --- | --- |
| TUI | `github.com/gdamore/tcell/v3` | `v3.4.0` | [ADR-0006](../architecture/adr/0006-public-identity-toolchain-and-runtime-libraries.md) |
| SFTP | `github.com/pkg/sftp` | 上游 `v1.13.11`；replace `github.com/TyrantLucifer/sftp v1.13.12-0.20260715132526-f947b886400b` | [ADR-0011](../architecture/adr/0011-pkg-sftp-streaming-directory-cursor.md) |
| SQLite | `modernc.org/sqlite` | `v1.53.0` | [ADR-0008](../architecture/adr/0008-modernc-sqlite-and-forward-migrations.md) |
| SQLite runtime | `modernc.org/libc` | `v1.73.4`（上游解析 pin） | [ADR-0008](../architecture/adr/0008-modernc-sqlite-and-forward-migrations.md) |

变更必须使用精确版本并提交 `go.sum`；审查完整 module graph、许可证、上游 changelog、撤回版本、已知漏洞、Go 1.25.12/1.26.5 兼容和四目标 `CGO_ENABLED=0` 构建。ADR pin 是已解决的选择，不能被 `latest` 或自动升级替换。

## 2. 独立工具 module

开发与 CI 工具位于独立的 `tools` module。它只有以下三个直接依赖：

| 工具 | module | 固定版本 |
| --- | --- | --- |
| golangci-lint | `github.com/golangci/golangci-lint/v2` | `v2.12.2` |
| actionlint | `github.com/rhysd/actionlint` | `v1.7.12` |
| govulncheck | `golang.org/x/vuln` | `v1.6.0` |

`tools/go.sum` 是工具执行图的锁文件。工具必须通过仓库根目录下的 `go tool -modfile=tools/go.mod` 执行，使分析器检查产品 module；禁止使用绕过该锁文件的 `go run package@version`。

根 module 与 tools module 的门禁都运行非变更式 `mod tidy -diff` 和 `mod verify`。只有有意维护依赖图时才可使用精确工具链执行会变更文件的 `go mod tidy`，并必须审查完整 diff。

## 3. GitHub Actions 固定引用

工作流代码固定不可变 commit SHA，tag 只作为审查注释：

| Action | 固定 commit | 审查 tag |
| --- | --- | --- |
| `actions/checkout` | `9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0` | `v7.0.0` |
| `actions/setup-go` | `924ae3a1cded613372ab5595356fb5720e22ba16` | `v6.5.0` |
| `actions/upload-artifact` | `043fb46d1a93c77aae656e7c1c64a875d1fc6a0a` | `v7.0.1` |
| `actions/download-artifact` | `3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c` | `v8.0.1` |

接受工作流变更前须从每个 action 的官方仓库重新核对 tag 指向；workflow 始终保留 SHA，不能改用可变 tag。

## 4. 自动更新与审查

Dependabot 每周检查根 gomod、`/tools` gomod 和 GitHub Actions；每个 ecosystem 最多同时打开五个 PR。依赖更新永不自动合并。

每次更新审查必须运行 lint、全部测试、race、四个独立 fuzz smoke、govulncheck、actionlint、docscheck、Go 1.25.12 oldstable、四目标构建和独立缓存的可复现构建字节比较。审查者还要检查传递依赖图、许可证、上游 changelog 与兼容影响，并记录已发现漏洞的适用性、处置或接受原因。

CI 的 Go 缓存依赖路径必须同时、精确使用已提交的根 `go.sum` 与 `tools/go.sum`；前者锁定产品依赖图，后者锁定独立工具图。不得用只覆盖其中一份或会吸收未审查 module 的宽泛 glob 代替。
