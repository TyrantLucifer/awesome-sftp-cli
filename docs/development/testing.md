# 本地测试与质量门禁

## 1. 常用入口

提交前首选运行 `make check`。完整本地门禁是 `make ci`；它会生成覆盖率和四个构建产物，因此应把带空格的外部目录以双引号传给 `BUILD_DIR` 与 `COVERAGE_DIR`：

```sh
EXTERNAL_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/amsftp-local.XXXXXX")"
make BUILD_DIR="$EXTERNAL_ROOT/build outputs" \
  COVERAGE_DIR="$EXTERNAL_ROOT/coverage outputs" ci
```

不要为了通过清洁树检查而删除已有的 `.cache`、`bin`、`dist` 或 `coverage`；这些路径可能是开发者的既有基线。

## 2. Make 目标契约

| 目标 | 行为 |
| --- | --- |
| `fmt-check` | 只读检查 `cmd` 与 `internal` 下的 Go 文件是否经过 `gofmt`；不改写文件。 |
| `vet` | 对产品 module 运行 `go vet ./...`。 |
| `lint` | 通过已锁定的 tools module 从仓库根目录运行 golangci-lint。 |
| `test` | 运行全部单元测试，并写入 `unit.out`。 |
| `test-contract` | 只运行 Provider contract，并写入 `provider-contract.out`。 |
| `test-race` | 在原生平台运行全部 race 测试；保留 cgo。 |
| `fuzz-smoke` | 分别运行四个 fuzz 目标，每个目标 1 秒。 |
| `docs-check` | 运行可执行的文档与仓库策略检查。 |
| `mod-check` | 对根 module 和 tools module 运行非变更式 tidy 检查及校验和验证。 |
| `supply-chain` | 运行 govulncheck 与 actionlint。 |
| `build-all` | 以 `CGO_ENABLED=0` 和 `-trimpath` 构建四个支持目标。 |
| `check` | 组合 Make 契约、格式、vet、单元/contract、文档与 module 门禁。 |
| `ci` | 在 `check` 上增加 lint、race、fuzz、供应链与四目标构建。 |
| `make-contract` | 内部门禁：扫描 canonical Makefile，并以假 Go 可执行文件运行全部实际 Go 目标，验证 `GO` 覆盖、分析器工作目录和禁止字面量 `go` 的契约。 |

`test` 的 profile 位于 `$(COVERAGE_DIR)/unit.out`；`test-contract` 的 profile 位于 `$(COVERAGE_DIR)/provider-contract.out`。

普通 `docs-check` 适用于开发中的仓库。公开最终候选还必须显式运行 `GOTOOLCHAIN=local go run ./internal/tools/docscheck --release .`；该模式在普通检查之上读取 `docs/release/RC-GATES.md`，任何未勾选门禁都会失败。内部预览不冒充公开候选，普通 docscheck 的成功也不能替代真实签名、平台和渠道证据。

Make 契约的信任边界是仓库根目录中已提交的 canonical `Makefile`。可接受的本地/CI 证据必须从产品仓库根目录直接执行 `make <target>`；workflow policy 同时要求 `MAKEFLAGS`、`GNUMAKEFLAGS`、`MAKEFILES` 的有效值为空，并只认可精确、安全的 Make 命令。通过 wrapper Makefile、额外 `-f`、`MAKEFILES` preload、include 或其他外部 Make source 得到的结果不属于这条契约，也不能作为门禁证据；Make 自身不被描述为任意外部 source 的完整性边界。

canonical Makefile 的初始/文件尾解析期 guard 拒绝 `-n`/`-i`/`-q`/`-t` 及其长形式；14 个 recipe 目标的第一行还必须精确为强制执行的 `+@: $(MAKE_CONTRACT_RECIPE_GUARD)`，因此即使 canonical source 在 footer 后晚置 `-t`，GNU Make 3.81 也必须展开 guard 并 fail closed，而不能把门禁假报为 “Nothing to be done”。guard 的 helper、派生结果和 recipe 入口使用 GNU Make `override` 保护，不能被命令行或 `-e` 环境变量降级；显式命令行赋值 `MAKEFLAGS`/`MFLAGS` 会因来源不可信而直接失败，checker 会重放内部变量、双 Make 输入、`SHELL`、`.SHELLFLAGS`、`.RECIPEPREFIX` 与环境覆盖攻击矩阵。flag 解析只检查 GNU Make 规范化后的选项前缀，遇到独立 `--` 或首个普通命令行变量赋值即停止；因此 `GO`、`BUILD_DIR`/`COVERAGE_DIR` 的预期覆盖及带空格路径（即使后续片段含 `-n`/`--touch` 文本）不会被误当成执行 flag。`-C <dir>`、`-I <dir>` 等带独立参数的安全选项也不会因参数文本被误判。静态 checker 会先折叠非 recipe 的 Make 逻辑续行，并按 shell 规则折叠 recipe 的反斜杠换行，再解析命令位置；命令只允许 `$(GO)` 或少量固定、无间接执行能力的基础工具，字面量 `go`、`*/go`、未知 Make/shell 引用、执行包装器和其他间接执行入口均 fail closed。canonical source 中的 global/target-specific `SHELL`、`.SHELLFLAGS`、`.RECIPEPREFIX`、Make 执行 flags/preload 变量以及 `.ONESHELL`、`.IGNORE`、`.POSIX` 同样被拒绝，不能改写 recipe 解释器或错误传播语义。

强制 recipe guard 精确覆盖全部 14 个目标；其中 fake-Go 运行时探针覆盖 `fmt-check`、`vet`、`lint`、`test`、`test-contract`、`test-race`、`fuzz-smoke`、`docs-check`、`mod-check`、`supply-chain`、`build-all` 这 11 个实际 Go 目标，checker 还会拒绝未纳入哨兵的新 Go 目标。每个探针目标的日志必须具有精确调用数量、允许的 Go 子命令前缀和产品根工作目录；以 fake Go 接收 `-c <recipe>` 的 shell 形态会明确失败。`lint`/`supply-chain` 必须通过根 module 的 `go tool -modfile=tools/go.mod` 从产品根目录运行。

## 3. Provider contract 与 fuzz

可复用 Provider contract 的唯一显式入口是 fake provider 中的 `TestProviderContract`：

```sh
go test -count=1 -run='^TestProviderContract$' \
  -coverprofile="$COVERAGE_DIR/provider-contract.out" ./internal/provider/fake
```

四个 fuzz 目标必须分别执行，不能用组合正则一次选择多个目标：

```sh
go test -run='^$' -fuzz='^FuzzFrameDecoder$' -fuzztime=1s ./internal/ipc
go test -run='^$' -fuzz='^FuzzEnvelopeDecode$' -fuzztime=1s ./internal/ipc
go test -run='^$' -fuzz='^FuzzWireBytes$' -fuzztime=1s ./internal/ipc
go test -run='^$' -fuzz='^FuzzNormalizePath$' -fuzztime=1s ./internal/provider/fake
```

本地 smoke 预算是每个目标 1 秒；nightly 在 amd64 与 arm64 原生 Linux runner 上为每个目标运行 10 分钟。

## 4. 可执行程序的角色边界

`cmd/amsftp` 是一个具有 `client`、`daemon`、`askpass`、`helper` 四种 dispatch 角色的二进制；无参数时默认角色为 `client`。只有完全匹配的内部 role token 与 `--help`/`--version` meta token 会被 dispatch 层消费；其他单/双 Location、`--workspace <name>` 或未来 client 参数均原样交给默认 client handler。production Helper 仍按发行门禁 fail closed；可执行 smoke、PTY、真实 OpenSSH、daemon recovery 与 `internal/app` 测试共同证明角色分派和参数保留，不能用空 handler 的进程调用替代。

## 5. 并发重复门禁

nightly 对下列四个 fake provider 测试精确重复 100 次：

```sh
go test -count=100 \
  -run='^(TestNthCallFaultIsConsumedOnce|TestDelayUsesManualClock|TestGateHonorsContextCancellation|TestFaultCallSequencesAreLinearized)$' \
  ./internal/provider/fake
```

## 6. 版本与平台矩阵

稳定门禁使用 Go 1.26.5；oldstable 门禁使用 Go 1.25.12，并通过 `GOTOOLCHAIN=local` 禁止自动切换。工作流要求两套工具链都在 `ubuntu-22.04`、`ubuntu-24.04`、`macos-15`（arm64）与 `macos-15-intel` 上验证；在授权 hosted run 真正绿色前，这仍是未满足门禁而不是当前证据。oldstable 运行 `make check`，稳定原生门禁分别运行格式、vet、单元、显式 Provider contract、race、原生构建及 `--help`/`--version` smoke。发布式交叉构建覆盖：

| GOOS | GOARCH | 输出文件 |
| --- | --- | --- |
| darwin | arm64 | `amsftp-darwin-arm64` |
| darwin | amd64 | `amsftp-darwin-amd64` |
| linux | arm64 | `amsftp-linux-arm64` |
| linux | amd64 | `amsftp-linux-amd64` |

交叉构建只证明相应目标可编译，不能作为 Linux 或 macOS 原生测试、race 或系统集成证据。

CI 的四目标 build 与 CI/nightly 的独立缓存 reproducibility build 都必须使用精确的 `go build -trimpath -buildvcs=false`；native 可执行 smoke 保持 `go build -trimpath`，且不能冒充可复现发行物证据。每个 build/repro producer 都从实际二进制重新计算 SHA-256、与 sidecar 逐字节核对，并记录目标 `GOOS`/`GOARCH`、artifact 名与 64 位小写十六进制 hash。comparison job 从两份独立 artifact 计算每个平台唯一 accepted hash；最终 collector 必须下载这份 comparison evidence，并将 CI 的 build + repro A/B（nightly 为 repro A/B）全部绑定到 accepted hash 和目标元组。Nightly fuzz/concurrency producer 还必须逐 step 精确绑定 checkout、setup-go 与真实 workload，空操作、条件成功或命令漂移不能只靠 clean provenance 伪装成已执行门禁。workflow policy 对 shell block 先仅去除 YAML 公共基础缩进，再逐内容行精确比较；heredoc 结束符、普通内容、反斜杠续行的任何前后空白漂移都必须 fail closed。

Linux arm64 在任何公开支持声明前必须通过 `ubuntu-22.04-arm`/`ubuntu-24.04-arm` 或等价受控原生 runner 的安装与冒烟。若仓库可见性或计划不提供 hosted ARM label，必须配置自托管/受控 runner；四目标交叉构建不能替代该证据。完整支持与包装承诺见 [ADR-0009](../architecture/adr/0009-supported-platform-ci-and-packaging-baseline.md)。

## 7. 精确工具链覆盖

所有 Make recipe 中的 Go 调用都尊重 `GO` 覆盖。需要复现固定工具链时，直接传入下载后精确二进制的路径：

```sh
GO126="$(GOTOOLCHAIN=go1.26.5 go env GOROOT)/bin/go"
GOTOOLCHAIN=local make GO="$GO126" \
  BUILD_DIR="$EXTERNAL_ROOT/build outputs" \
  COVERAGE_DIR="$EXTERNAL_ROOT/coverage outputs" check
```

兼容验证把 `go1.26.5` 换为 `go1.25.12`，仍使用 `GOTOOLCHAIN=local`。`GO` 是一个可执行文件路径，不是附带 flags 的命令字符串。

## 8. 污染安全

`test` 与 `test-contract` 的 profile 必须位于外部 `COVERAGE_DIR`；`build-all` 只能在外部 `BUILD_DIR` 顶层生成上表四个常规、非符号链接文件。完整接受门禁会在同一个 shell 中比较运行前后的非忽略 candidate tree、忽略输出 Git tree 与同次未压缩 tar 字节，并验证每个构建的 SHA-256 与 `go version -m` 元数据。

完整 pre/post tree 捕获以当前 `Makefile`、workflow policy 和 `internal/docscheck` 的可执行契约为准；历史计划不再是活动事实源。
