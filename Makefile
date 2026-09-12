GO ?= go
# Always use the explicitly selected local toolchain.
override GOTOOLCHAIN := local
export GOTOOLCHAIN
BUILD_DIR ?= dist
COVERAGE_DIR ?= coverage
TOOL_MOD := -modfile=tools/go.mod

.PHONY: fmt-check vet lint test test-contract test-race test-scale bench-scale fuzz-smoke
.PHONY: docs-check notice-check mod-check supply-chain build-all
.PHONY: check ci

fmt-check:
	"$(GO)" run ./internal/tools/fmtcheck

vet:
	"$(GO)" vet ./...

lint:
	"$(GO)" tool $(TOOL_MOD) golangci-lint run ./...

test:
	mkdir -p "$(COVERAGE_DIR)"
	"$(GO)" test -count=1 -coverprofile="$(COVERAGE_DIR)/unit.out" ./...

test-contract:
	mkdir -p "$(COVERAGE_DIR)"
	"$(GO)" test -count=1 -run='^TestProviderContract$$' -coverprofile="$(COVERAGE_DIR)/provider-contract.out" ./internal/provider/fake

test-race:
	"$(GO)" test -race -count=1 ./...

test-scale:
	"$(GO)" test -count=1 -run='(FiftyThousand|MillionNode|MillionEntries|HundredGiB|HardResource|ResourceLedger|TransferScheduler|DiscoverDirectoryDoesNotFollow|LocalOperationCancellation|ManagerClassifiesPermissionAndDiskFull|ProviderSessionsBoundDynamicConnections|JobEventPayloads|LargeJobEventHistory|RingRetainsOnlyBounded|RingQueryCapsPages|DaemonLogConcurrentWrites|Level0FilenameSearchStreamsBounded|Level2PolicyAndProductionClosure|Level2FrozenControlPlaneContainsNoCredential|OrchestrateHundredGiB)' ./internal/tui ./internal/search ./internal/helper ./internal/transfer ./internal/daemon ./internal/state/jobstore ./internal/diagnostic ./internal/externalpreviewer

bench-scale:
	GOMAXPROCS=1 "$(GO)" test -run='^$$' -bench='^Benchmark(Render|Move)FiftyThousandEntries$$' -benchtime=100x -count=3 -benchmem ./internal/tui

fuzz-smoke:
	"$(GO)" test -run='^$$' -fuzz='^FuzzFrameDecoder$$' -fuzztime=10000x ./internal/ipc
	"$(GO)" test -run='^$$' -fuzz='^FuzzEnvelopeDecode$$' -fuzztime=10000x ./internal/ipc
	"$(GO)" test -run='^$$' -fuzz='^FuzzWireBytes$$' -fuzztime=10000x ./internal/ipc
	"$(GO)" test -run='^$$' -fuzz='^FuzzNormalizePath$$' -fuzztime=10000x ./internal/provider/fake

docs-check:
	"$(GO)" run ./internal/tools/docscheck .

notice-check:
	"$(GO)" run ./internal/tools/releasenotice --check internal/release/metadata/runtime-dependencies.json internal/release/metadata/license-materials.json NOTICE

mod-check:
	"$(GO)" mod tidy -diff
	"$(GO)" mod verify
	"$(GO)" -C tools mod tidy -diff
	"$(GO)" -C tools mod verify

supply-chain:
	"$(GO)" tool $(TOOL_MOD) govulncheck ./...
	"$(GO)" tool $(TOOL_MOD) actionlint .github/workflows/fast-ci.yml .github/workflows/nightly.yml .github/workflows/release.yml

build-all:
	mkdir -p "$(BUILD_DIR)"
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 "$(GO)" build -trimpath -o "$(BUILD_DIR)/amsftp-darwin-arm64" ./cmd/amsftp
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 "$(GO)" build -trimpath -o "$(BUILD_DIR)/amsftp-darwin-amd64" ./cmd/amsftp
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 "$(GO)" build -trimpath -o "$(BUILD_DIR)/amsftp-linux-arm64" ./cmd/amsftp
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "$(GO)" build -trimpath -o "$(BUILD_DIR)/amsftp-linux-amd64" ./cmd/amsftp

check: fmt-check vet test docs-check notice-check mod-check

ci: check lint test-race fuzz-smoke supply-chain build-all
