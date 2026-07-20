# This contract covers the committed canonical Makefile invoked from the
# repository root. External wrappers, alternate -f sources, and MAKEFILES
# preloads are outside its trust boundary and are not accepted CI evidence.

# GNU Make either normalizes execution-skipping options into the first short
# option word or preserves them as dash-prefixed words beside long options.
# GNU Make 4.x can propagate an include directory as I/path or -I/path; that
# safe option word is not a short-option cluster even when the path contains
# n/i/q/t. Arguments to other safe options such as "-C n" are not candidates.
override make_contract_value_changed = $(strip $(filter-out $(1),$(2)) $(filter-out $(2),$(1)))
override make_execution_skip_letters = $(strip $(foreach letter,n i q t,$(if $(findstring $(letter),$(1)),$(letter))))
override make_execution_skip_word = $(if $(filter --just-print --dry-run --recon --ignore-errors --question --touch,$(1)),yes,$(if $(filter --% I% -I%,$(1)),,$(call make_execution_skip_letters,$(patsubst -%,%,$(1)))))
override make_flag_option_prefix = $(strip $(if $(1),$(if $(filter --,$(firstword $(1))),,$(if $(findstring =,$(firstword $(1))),,$(firstword $(1)) $(call make_flag_option_prefix,$(wordlist 2,$(words $(1)),$(1)))))))
override make_execution_flag_candidates = $(firstword $(call make_flag_option_prefix,$(1))) $(filter -%,$(call make_flag_option_prefix,$(1)))
override MAKE_CONTRACT_MAKE_INPUT_CHANGED = $(if $(filter command,$(origin MAKEFLAGS) $(origin MFLAGS)),yes)
override MAKE_EXECUTION_SKIP_FLAGS = $(strip $(foreach flag,$(call make_execution_flag_candidates,$(MAKEFLAGS)) $(call make_execution_flag_candidates,$(MFLAGS)),$(call make_execution_skip_word,$(flag))))
override MAKE_CONTRACT_SHELL_CHANGED = $(call make_contract_value_changed,/bin/sh,$(SHELL))
override MAKE_CONTRACT_SHELLFLAGS_CHANGED = $(if $(filter undefined default,$(origin .SHELLFLAGS)),,yes)
override MAKE_CONTRACT_RECIPEPREFIX_CHANGED = $(if $(filter undefined default,$(origin .RECIPEPREFIX)),,yes)
override MAKE_CONTRACT_RECIPE_GUARD = $(if $(MAKE_CONTRACT_MAKE_INPUT_CHANGED),$(error command-line assignment of MAKEFLAGS or MFLAGS is forbidden))$(if $(MAKE_EXECUTION_SKIP_FLAGS),$(error execution-skipping Make flags n/i/q/t and their long forms are not permitted))$(if $(MAKE_CONTRACT_SHELL_CHANGED),$(error recipe execution control SHELL is forbidden in the canonical Makefile))$(if $(MAKE_CONTRACT_SHELLFLAGS_CHANGED),$(error recipe execution control .SHELLFLAGS is forbidden in the canonical Makefile))$(if $(MAKE_CONTRACT_RECIPEPREFIX_CHANGED),$(error recipe execution control .RECIPEPREFIX is forbidden in the canonical Makefile))
ifneq ($(MAKE_CONTRACT_MAKE_INPUT_CHANGED),)
$(error command-line assignment of MAKEFLAGS or MFLAGS is forbidden)
endif
ifneq ($(MAKE_EXECUTION_SKIP_FLAGS),)
$(error execution-skipping Make flags n/i/q/t and their long forms are not permitted)
endif

GO ?= go
BUILD_DIR ?= dist
COVERAGE_DIR ?= coverage
TOOL_MOD := -modfile=tools/go.mod

.PHONY: fmt-check vet lint test test-contract test-race test-scale bench-scale fuzz-smoke
.PHONY: docs-check notice-check mod-check supply-chain build-all
.PHONY: make-contract-scan make-contract make-contract-flags check ci

fmt-check:
	+@: $(MAKE_CONTRACT_RECIPE_GUARD)
	"$(GO)" run ./internal/tools/makecontract fmt cmd internal

vet:
	+@: $(MAKE_CONTRACT_RECIPE_GUARD)
	"$(GO)" vet ./...

lint:
	+@: $(MAKE_CONTRACT_RECIPE_GUARD)
	"$(GO)" tool $(TOOL_MOD) golangci-lint run ./...

test:
	+@: $(MAKE_CONTRACT_RECIPE_GUARD)
	mkdir -p "$(COVERAGE_DIR)"
	"$(GO)" test -count=1 -coverprofile="$(COVERAGE_DIR)/unit.out" ./...

test-contract:
	+@: $(MAKE_CONTRACT_RECIPE_GUARD)
	mkdir -p "$(COVERAGE_DIR)"
	"$(GO)" test -count=1 -run='^TestProviderContract$$' -coverprofile="$(COVERAGE_DIR)/provider-contract.out" ./internal/provider/fake

test-race:
	+@: $(MAKE_CONTRACT_RECIPE_GUARD)
	"$(GO)" test -race -count=1 ./...

test-scale:
	+@: $(MAKE_CONTRACT_RECIPE_GUARD)
	"$(GO)" test -count=1 -run='(FiftyThousand|MillionNode|MillionEntries|HundredGiB|HardResource|ResourceLedger|TransferScheduler|DiscoverDirectoryDoesNotFollow|LocalOperationCancellation|ManagerClassifiesPermissionAndDiskFull|ProviderSessionsBoundDynamicConnections|JobEventPayloads|LargeJobEventHistory|RingRetainsOnlyBounded|RingQueryCapsPages|DaemonLogConcurrentWrites|Level0FilenameSearchStreamsBounded|Level2PolicyAndProductionClosure|Level2FrozenControlPlaneContainsNoCredential|OrchestrateHundredGiB)' ./internal/tui ./internal/search ./internal/helper ./internal/transfer ./internal/daemon ./internal/state/jobstore ./internal/diagnostic ./internal/externalpreviewer

bench-scale:
	+@: $(MAKE_CONTRACT_RECIPE_GUARD)
	GOMAXPROCS=1 "$(GO)" test -run='^$$' -bench='^Benchmark(Render|Move)FiftyThousandEntries$$' -benchtime=100x -count=3 -benchmem ./internal/tui

fuzz-smoke:
	+@: $(MAKE_CONTRACT_RECIPE_GUARD)
	"$(GO)" test -run='^$$' -fuzz='^FuzzFrameDecoder$$' -fuzztime=1s ./internal/ipc
	"$(GO)" test -run='^$$' -fuzz='^FuzzEnvelopeDecode$$' -fuzztime=1s ./internal/ipc
	"$(GO)" test -run='^$$' -fuzz='^FuzzWireBytes$$' -fuzztime=1s ./internal/ipc
	"$(GO)" test -run='^$$' -fuzz='^FuzzNormalizePath$$' -fuzztime=1s ./internal/provider/fake

docs-check:
	+@: $(MAKE_CONTRACT_RECIPE_GUARD)
	"$(GO)" run ./internal/tools/docscheck .

notice-check:
	+@: $(MAKE_CONTRACT_RECIPE_GUARD)
	"$(GO)" run ./internal/tools/releasenotice --check docs/release/runtime-dependencies.json docs/release/license-materials.json docs/release/NOTICE

mod-check:
	+@: $(MAKE_CONTRACT_RECIPE_GUARD)
	"$(GO)" mod tidy -diff
	"$(GO)" mod verify
	"$(GO)" -C tools mod tidy -diff
	"$(GO)" -C tools mod verify

supply-chain:
	+@: $(MAKE_CONTRACT_RECIPE_GUARD)
	"$(GO)" tool $(TOOL_MOD) govulncheck ./...
	"$(GO)" tool $(TOOL_MOD) actionlint .github/workflows/ci.yml .github/workflows/fast-ci.yml .github/workflows/nightly.yml

build-all:
	+@: $(MAKE_CONTRACT_RECIPE_GUARD)
	mkdir -p "$(BUILD_DIR)"
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 "$(GO)" build -trimpath -o "$(BUILD_DIR)/amsftp-darwin-arm64" ./cmd/amsftp
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 "$(GO)" build -trimpath -o "$(BUILD_DIR)/amsftp-darwin-amd64" ./cmd/amsftp
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 "$(GO)" build -trimpath -o "$(BUILD_DIR)/amsftp-linux-arm64" ./cmd/amsftp
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "$(GO)" build -trimpath -o "$(BUILD_DIR)/amsftp-linux-amd64" ./cmd/amsftp

make-contract-scan:
	+@: $(MAKE_CONTRACT_RECIPE_GUARD)
	"$(GO)" run ./internal/tools/makecontract scan Makefile

make-contract: make-contract-scan
	+@: $(MAKE_CONTRACT_RECIPE_GUARD)
	"$(GO)" run ./internal/tools/makecontract verify --go "$(GO)"

make-contract-flags:
	+@: $(MAKE_CONTRACT_RECIPE_GUARD)

check: make-contract fmt-check vet test test-contract docs-check notice-check mod-check

ci: check lint test-race fuzz-smoke supply-chain build-all

ifneq ($(MAKE_CONTRACT_MAKE_INPUT_CHANGED),)
$(error command-line assignment of MAKEFLAGS or MFLAGS is forbidden)
endif
ifneq ($(MAKE_EXECUTION_SKIP_FLAGS),)
$(error execution-skipping Make flags n/i/q/t and their long forms are not permitted)
endif
