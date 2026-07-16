# Go 工具链

## 1. 目的

本文冻结 Stage 0 的 Go module、语言基线、首选工具链和兼容验证方式，使 macOS 与 Linux 的本地开发、CI 和续接会话使用同一组版本事实。

规范 module import 前缀为：

    github.com/TyrantLucifer/awesome-mac-sftp

## 2. 版本策略

| 用途 | 版本 | 约束 |
| --- | --- | --- |
| 语言与 module 基线 | Go 1.25.0 | 源码不得依赖 Go 1.26 才引入的语言特性 |
| 首选开发与 CI 工具链 | Go 1.26.5 | 由 go.mod 的 toolchain 指令和 .go-version 选择 |
| 兼容工具链 | Go 1.25.12 | 使用精确二进制并设置 GOTOOLCHAIN=local 执行兼容门禁 |

go.mod 中的 go 指令定义最低语言与 module 语义；toolchain 指令建议日常命令使用 Go 1.26.5。.go-version 同样固定为 1.26.5，供支持该文件的版本管理器选择工具链。这三个声明必须保持一致，不使用未固定的 latest、stable 或系统默认别名。

Go 1.25.12 是 1.25 基线的受支持补丁版本。仅运行 go version 能证明该工具链可获得，不能代替兼容测试。兼容测试必须直接调用下载后的 Go 1.25.12 二进制，并用 GOTOOLCHAIN=local 禁止它根据 go.mod 自动切换到首选工具链。

Stage 0 的生产代码只使用标准库。后续阶段的规范依赖 pin 已由 [ADR-0006](../architecture/adr/0006-public-identity-toolchain-and-runtime-libraries.md)、修订其 SFTP pin 的 [ADR-0011](../architecture/adr/0011-pkg-sftp-streaming-directory-cursor.md) 与 [ADR-0008](../architecture/adr/0008-modernc-sqlite-and-forward-migrations.md) 冻结：Stage 1 使用 tcell v3.4.0，以及 upstream pkg/sftp v1.13.11 加精确 immutable cursor fork；Stage 2 使用 modernc SQLite v1.53.0 并保留 libc v1.73.4。它们在 owning stage 第一次导入时进入根 `go.mod`/`go.sum`，不改变 Stage 0 标准库-only 的实现边界。

## 3. 获取与选择

安装一个支持 Go 工具链自动选择的 Go 发行版后，可通过 GOTOOLCHAIN 获取精确版本。首次执行可能需要网络下载：

    GOTOOLCHAIN=go1.26.5 go version
    GOTOOLCHAIN=go1.25.12 go version

使用版本管理器时，让它读取仓库根目录的 .go-version。不要在仓库中提交本机 GOROOT、GOPATH 或工具链缓存路径。

## 4. 支持的构建目标

| GOOS | GOARCH |
| --- | --- |
| darwin | arm64 |
| darwin | amd64 |
| linux | arm64 |
| linux | amd64 |

发布式构建使用 CGO_ENABLED=0。跨平台构建只能证明源码可编译；它不能替代 macOS 与 Linux 上的原生单元、race 和系统集成证据。

## 5. 基线验证

在当前 macOS arm64 开发环境运行：

    GOTOOLCHAIN=go1.26.5 go version
    GOTOOLCHAIN=go1.26.5 go env GOVERSION GOOS GOARCH
    GOTOOLCHAIN=go1.25.12 go version

预期版本与平台字段为：

    go version go1.26.5 darwin/arm64
    go1.26.5
    darwin
    arm64
    go version go1.25.12 darwin/arm64

在其他受支持主机上，版本必须相同，GOOS 和 GOARCH 应与实际主机匹配。

日常提交前门禁使用 `make check`；包含 lint、race、四个独立 fuzz smoke、供应链检查和四目标构建的完整本地门禁使用 `make ci`。生成物目录使用外部绝对路径，并保持双引号：

    EXTERNAL_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/amsftp-toolchain.XXXXXX")"
    GO126="$(GOTOOLCHAIN=go1.26.5 go env GOROOT)/bin/go"
    GOTOOLCHAIN=local make GO="$GO126" \
      BUILD_DIR="$EXTERNAL_ROOT/build outputs" \
      COVERAGE_DIR="$EXTERNAL_ROOT/coverage outputs" check
    GOTOOLCHAIN=local make GO="$GO126" \
      BUILD_DIR="$EXTERNAL_ROOT/build outputs" \
      COVERAGE_DIR="$EXTERNAL_ROOT/coverage outputs" ci

`GO` 覆盖值是精确 Go 可执行文件路径，不是含 flags 的命令字符串。完整的 Go 1.25.12 兼容门禁同样使用精确工具链路径：

    GO125="$(GOTOOLCHAIN=go1.25.12 go env GOROOT)/bin/go"
    GOTOOLCHAIN=local "$GO125" version
    GOTOOLCHAIN=local make GO="$GO125" \
      BUILD_DIR="$EXTERNAL_ROOT/oldstable build outputs" \
      COVERAGE_DIR="$EXTERNAL_ROOT/oldstable coverage outputs" check

独立的 tools module 固定 golangci-lint `v2.12.2`、actionlint `v1.7.12` 和 govulncheck 所属 `golang.org/x/vuln` `v1.6.0`；执行图由 `tools/go.sum` 锁定。目标用途、输出与污染安全细节见[本地测试与质量门禁](testing.md)。

## 6. 版本变更

提升语言基线、首选工具链或兼容版本时，必须在同一可审查变更中更新 go.mod、.go-version、CI 配置和本文，并重新验证两个 Go 版本及四个构建目标。改变最低支持版本或兼容策略属于架构兼容性变更，需要新增 ADR 说明原因和迁移影响。
