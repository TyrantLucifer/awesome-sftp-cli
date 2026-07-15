# Stage 0 Foundation & Knowledge Implementation Plan

> **For agentic workers:** This plan is self-contained and does not require an external skill. Execute it task-by-task and keep the checkbox/evidence trail durable in the repository.

**Goal:** 建立可在 macOS 与 Linux 重复构建、测试和续接的 Go 工程基线，并冻结 Stage 1–6 共用的领域、IPC、Provider、Fake Provider、配置和文档验证契约。

**Architecture:** 单一 Go 程序通过角色分发承担 client、daemon、askpass 与 helper 入口；Stage 0 只实现入口和共享契约，不实现 SSH、TUI、SQLite 或传输。领域值对象不依赖传输，IPC 使用 4-byte big-endian 长度分帧 JSON，Provider 使用有界拉取式分页与可选写 Facet，Fake Provider 通过确定性控制面复现故障。

**Tech Stack:** Go 1.25 language baseline, Go 1.26.5 preferred toolchain, standard library production dependencies, GNU Make 3.81-compatible Makefile, GitHub Actions, golangci-lint v2.12.2, govulncheck v1.6.0, actionlint v1.7.12.

## Global Constraints

- Module path is `github.com/TyrantLucifer/awesome-mac-sftp`; the public command is `amsftp`.
- `go.mod` declares `go 1.25.0` and `toolchain go1.26.5`; compatibility CI uses Go 1.25.12 with `GOTOOLCHAIN=local`.
- Supported build targets are `darwin/arm64`, `darwin/amd64`, `linux/arm64`, and `linux/amd64`; release-style builds use `CGO_ENABLED=0`.
- Product code added in Stage 0 uses the Go standard library only.
- The same Go program exposes client, daemon, askpass, and helper roles; do not create separate daemon or helper codebases.
- Protocol v1.0 uses a 4-byte unsigned big-endian frame length and an 8 MiB maximum payload.
- Unix/SFTP path bytes, names, and symlink targets must round-trip through IPC without UTF-8 replacement.
- Endpoint capability support is session-scoped and generation-scoped; absence is `unsupported` only when the snapshot is complete.
- The core Provider is read-only. Mutating behavior is an optional `MutableProvider` Facet and must not become a Stage 1 dependency.
- Every production behavior follows red-green-refactor. The implementing Agent records the expected failing test before adding production code.
- Stage 0 must not connect to SSH/SFTP, implement a TUI, add SQLite, implement transfer jobs, or start Stage 1 behavior.
- Do not create Git commits unless the user explicitly authorizes implementation commits. Each task ends with a reviewed diff and test record instead.
- Only the controller updates `IMPLEMENTATION_PLAN.md`, `PROJECT_STATE.md`, the feature matrix, verification records, and shared module files.

---

## File and ownership map

```text
go.mod
.go-version
.gitignore
Makefile
.golangci.yml

cmd/amsftp/main.go

internal/app/
  invocation.go
  invocation_test.go
  run.go
  run_test.go

internal/buildinfo/
  info.go
  info_test.go

internal/config/
  schema.go
  schema_test.go

internal/domain/
  id.go
  id_test.go
  endpoint.go
  location.go
  location_test.go
  entry.go
  fingerprint.go
  fingerprint_test.go
  capability.go
  capability_test.go
  error.go
  error_test.go

internal/foundation/
  clock.go
  manual_clock.go
  manual_clock_test.go

internal/testkit/
  id.go
  id_test.go

internal/ipc/
  version.go
  wire_bytes.go
  wire_bytes_test.go
  envelope.go
  envelope_test.go
  handshake.go
  handshake_test.go
  frame.go
  frame_test.go
  fuzz_test.go

internal/provider/
  provider.go
  listing.go
  mutable.go
  validate.go
  validate_test.go
  contracttest/suite.go
  fake/provider.go
  fake/tree.go
  fake/controller.go
  fake/script.go
  fake/provider_contract_test.go
  fake/faults_test.go
  fake/fuzz_test.go

internal/docscheck/
  check.go
  links.go
  matrix.go
  state.go
  workflow.go
  check_test.go
  testdata/

internal/tools/docscheck/main.go

.github/dependabot.yml
.github/workflows/ci.yml
.github/workflows/nightly.yml

docs/architecture/adr/0005-foundation-contracts.md
docs/development/toolchain.md
docs/development/testing.md
docs/security/dependency-policy.md
docs/verification/stage-00.md
```

Ownership during parallel waves:

- Controller: root module files, `cmd/`, `internal/app/`, state documents.
- Domain Agent: `internal/domain/`, `internal/config/`, `internal/foundation/`, `internal/testkit/`.
- IPC Agent: `internal/ipc/` only.
- Provider Agent: `internal/provider/` only.
- Quality Agent: `internal/docscheck/`, `internal/tools/`, `Makefile`, `.golangci.yml`, `.github/`, development/security docs.

## Execution waves

1. Gate: Task 1.
2. Parallel Wave A: Tasks 2, 3, and 4.
3. Parallel Wave B after Task 3 review: Tasks 5, 6, and 9.
4. Sequential Provider Wave: Tasks 7 then 8.
5. Integration Wave: Task 10.
6. Evidence and handoff: Task 11.

No task may edit another task's owned package. Shared-file requests go to the controller.

## Stage 0 feature coverage

| Feature | Owning tasks | Required evidence |
|---|---|---|
| CORE-001 | 1, 2, 10 | four role dispatch tests and four target builds |
| CORE-002 | 5 | envelope, negotiation, frame, cursor, cancel, and fuzz tests |
| CORE-003 | 3, 6, 7 | Provider interfaces, reusable contract suite, Fake conformance |
| CORE-004 | 3, 5, 6, 8 | session/generation capability tests and safe loss behavior |
| CORE-005 | 4, 7, 8 | deterministic clock plus delay, short I/O, permission, disconnect, and stale-stat tests |
| CORE-006 | 4 | strict schema, defaults, version, unknown-field, and trailing-value tests |
| CORE-007 | 10, 11 | stable/oldstable macOS/Linux CI, race, lint, security, and four builds |
| CORE-008 | 9, 11 | docscheck fixtures, real-repository check, verification record, cold-start audit |

---

### Task 1: Freeze module, toolchain, and foundational ADR

**Files:**

- Create: `go.mod`
- Create: `.go-version`
- Modify: `.gitignore`
- Create: `docs/architecture/adr/0005-foundation-contracts.md`
- Modify: `docs/architecture/overview.md`
- Create: `docs/development/toolchain.md`

**Interfaces:**

- Produces: canonical module import prefix, Go support policy, protocol frame constants, path wire policy, Provider Facet decision.
- Consumes: approved Stage 0 specification and ADR-0001 through ADR-0004.

- [ ] **Step 1: Create the exact Go module and toolchain declarations**

```go
module github.com/TyrantLucifer/awesome-mac-sftp

go 1.25.0

toolchain go1.26.5
```

Write `.go-version` as exactly:

```text
1.26.5
```

- [ ] **Step 2: Extend ignored generated outputs**

Keep `.superpowers/` and add:

```gitignore
.superpowers/
.cache/
bin/
dist/
coverage/
```

- [ ] **Step 3: Record ADR-0005**

The ADR must mark `Accepted` and record these exact decisions:

```text
Protocol version: 1.0
Frame header: uint32 big-endian
Maximum payload: 8 MiB
Path wire encoding: base64 of original bytes
Event cursor: epoch string plus monotonically increasing sequence
Provider split: read-only Provider plus optional MutableProvider
Capability identity: SessionID plus generation and complete flag
Error recovery: stable code, retry advice, and effect status
```

It must explain why direct JSON strings cannot safely represent arbitrary Unix filenames and why a bare event sequence cannot distinguish daemon restart.

- [ ] **Step 4: Verify the selected toolchains**

Run:

```bash
GOTOOLCHAIN=go1.26.5 go version
GOTOOLCHAIN=go1.26.5 go env GOVERSION GOOS GOARCH
GOTOOLCHAIN=go1.25.12 go version
```

Expected:

```text
go version go1.26.5 darwin/arm64
go1.26.5
darwin
arm64
go version go1.25.12 darwin/arm64
```

- [ ] **Step 5: Validate the documentation change**

Run:

```bash
git diff --check
rg -n '0005-foundation-contracts' docs/architecture/overview.md
```

Expected: no diff errors and one ADR link.

- [ ] **Step 6: Record task checkpoint**

Record changed files and command outputs in the task report. Do not commit without user authorization.

---

### Task 2: Add the single-program role boundary and build information

**Files:**

- Create: `cmd/amsftp/main.go`
- Create: `internal/app/invocation.go`
- Create: `internal/app/invocation_test.go`
- Create: `internal/app/run.go`
- Create: `internal/app/run_test.go`
- Create: `internal/buildinfo/info.go`
- Create: `internal/buildinfo/info_test.go`

**Interfaces:**

- Produces: `app.ParseInvocation(args []string) (Invocation, error)`, `app.Run(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer, handlers Handlers) int`, `buildinfo.Current() Info`.
- Consumes: module path and Go versions from Task 1.

- [ ] **Step 1: Write failing invocation tests**

```go
func TestParseInvocation(t *testing.T) {
    tests := []struct {
        name string
        args []string
        want app.Invocation
    }{
        {name: "default client", want: app.Invocation{Role: app.RoleClient}},
        {name: "daemon", args: []string{"daemon"}, want: app.Invocation{Role: app.RoleDaemon}},
        {name: "askpass", args: []string{"askpass"}, want: app.Invocation{Role: app.RoleAskpass}},
        {name: "helper", args: []string{"helper"}, want: app.Invocation{Role: app.RoleHelper}},
        {name: "version", args: []string{"--version"}, want: app.Invocation{Role: app.RoleClient, ShowVersion: true}},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := app.ParseInvocation(tt.args)
            if err != nil {
                t.Fatal(err)
            }
            if got != tt.want {
                t.Fatalf("got %#v want %#v", got, tt.want)
            }
        })
    }
}
```

Add `Run`-level tests proving exact reserved role tokens select that role and are stripped, while every unmatched first token—including one Location, two Locations, `--workspace <name>`, and future client flags—defaults to `RoleClient` and is forwarded byte-for-byte in the argument slice. The public Location grammar makes role typos indistinguishable from valid client input, so do not add typo heuristics or an “unknown role” rejection.

- [ ] **Step 2: Verify RED**

Run:

```bash
GOTOOLCHAIN=go1.26.5 go test ./internal/app ./internal/buildinfo
```

Expected: package or symbol-not-defined failure caused by missing implementation.

- [ ] **Step 3: Implement the exact role API**

```go
type Role string

const (
    RoleClient  Role = "client"
    RoleDaemon  Role = "daemon"
    RoleAskpass Role = "askpass"
    RoleHelper  Role = "helper"
)

type Invocation struct {
    Role        Role
    ShowHelp    bool
    ShowVersion bool
}

func ParseInvocation(args []string) (Invocation, error)

type Handler func(context.Context, []string, io.Writer, io.Writer) error

type Handlers struct {
    Client  Handler
    Daemon  Handler
    Askpass Handler
    Helper  Handler
}

func Run(
    ctx context.Context,
    args []string,
    stdout io.Writer,
    stderr io.Writer,
    handlers Handlers,
) int
```

Exit codes are fixed as `0` success, `2` for a structurally malformed meta invocation if one is introduced, and `1` handler failure. An unmatched token is client input, not an invalid role. A missing selected-role handler returns a descriptive failure; it never silently falls back to another role.

- [ ] **Step 4: Implement build information**

```go
type Info struct {
    Version   string `json:"version"`
    Commit    string `json:"commit"`
    Dirty     bool   `json:"dirty"`
    GoVersion string `json:"go_version"`
    GOOS      string `json:"goos"`
    GOARCH    string `json:"goarch"`
}

func Current() Info
func (i Info) String() string
```

Defaults are `dev` and `unknown`. `Current` may enrich commit and dirty state using `debug.ReadBuildInfo`; it must not include a wall-clock build time.

- [ ] **Step 5: Verify GREEN and command smoke**

Run:

```bash
GOTOOLCHAIN=go1.26.5 go test ./internal/app ./internal/buildinfo
GOTOOLCHAIN=go1.26.5 go build -o bin/amsftp ./cmd/amsftp
./bin/amsftp --version
```

Expected: tests pass, build succeeds, and version output contains `dev`, Go version, OS, and architecture.

- [ ] **Step 6: Record task checkpoint**

Run `git diff --check -- cmd internal/app internal/buildinfo` and write the test output into the task report. Do not commit without user authorization.

---

### Task 3: Implement stable domain values, capability revisions, and error semantics

**Files:**

- Create: all files listed under `internal/domain/` in the file map.
- Create: `internal/testkit/id.go`
- Create: `internal/testkit/id_test.go`

**Interfaces:**

- Produces: typed IDs, Endpoint, Location, Entry, Fingerprint, CapabilitySnapshot, OpError.
- Consumes: no production package other than the standard library.

- [ ] **Step 1: Write failing ID and Location tests**

Tests must prove:

```go
func TestRandomIDUsesExpectedPrefixAndLength(t *testing.T)
func TestParseIDRejectsWrongPrefix(t *testing.T)
func TestLocationRejectsEmptyEndpoint(t *testing.T)
func TestLocationRejectsEmptyOrNULPath(t *testing.T)
func TestLocationPreservesInvalidUTF8Bytes(t *testing.T)
```

ID wire form is a type prefix plus 26 lowercase base32 characters generated from 16 random bytes:

```text
ep_ followed by 26 lowercase base32 characters
sess_ followed by 26 lowercase base32 characters
req_ followed by 26 lowercase base32 characters
job_ followed by 26 lowercase base32 characters
evt_ followed by 26 lowercase base32 characters
```

- [ ] **Step 2: Verify RED**

Run `GOTOOLCHAIN=go1.26.5 go test ./internal/domain`.

Expected: missing symbols.

- [ ] **Step 3: Implement domain identity and endpoint types**

```go
type EndpointID string
type SessionID string
type RequestID string
type JobID string
type EventID string

type Generator interface {
    New(prefix string) (string, error)
}

type RandomGenerator struct {
    Reader io.Reader
}

type EndpointKind string

const (
    EndpointLocal EndpointKind = "local"
    EndpointSSH   EndpointKind = "ssh"
)

type Endpoint struct {
    ID           EndpointID
    Kind         EndpointKind
    DisplayName  string
    SSHHostAlias string
}

type ConnectionState string

const (
    StateDisconnected ConnectionState = "disconnected"
    StateConnecting   ConnectionState = "connecting"
    StateReady        ConnectionState = "ready"
    StateDegraded     ConnectionState = "degraded"
    StateAuthRequired ConnectionState = "auth_required"
    StateFailed       ConnectionState = "failed"
)

type EndpointSnapshot struct {
    EndpointID   EndpointID
    SessionID    SessionID
    State        ConnectionState
    Capabilities CapabilitySnapshot
    ObservedAt   time.Time
}
```

`RandomGenerator` defaults to `crypto/rand.Reader` and uses unpadded RFC 4648 base32 in lowercase.

Add a deterministic test generator:

```go
type SequenceGenerator struct {
    mu   sync.Mutex
    next uint64
}

func (g *SequenceGenerator) New(prefix string) (string, error)
```

It emits the requested prefix followed by a zero-padded, 26-character lowercase base32 sequence. It is concurrency-safe, returns a descriptive exhaustion error instead of wrapping and reusing an ID after `uint64` is exhausted, and lives in `internal/testkit`, never in a product runtime dependency.

- [ ] **Step 4: Implement Location, entries, and fingerprint strength**

```go
type CanonicalPath string

type Location struct {
    EndpointID EndpointID
    Path       CanonicalPath
}

func NewLocation(endpointID EndpointID, path CanonicalPath) (Location, error)

type NormalizeRequest struct {
    EndpointID EndpointID
    Base       *Location
    Input      string
}

type EntryKind string

const (
    EntryFile      EntryKind = "file"
    EntryDirectory EntryKind = "directory"
    EntrySymlink   EntryKind = "symlink"
    EntryOther     EntryKind = "other"
)

type TimePrecision string

type Metadata struct {
    Size              *uint64
    Mode              *uint32
    UID               *uint32
    GID               *uint32
    ModifiedAt        *time.Time
    ModifiedPrecision *TimePrecision
    FileID            *string
}

type SymlinkInfo struct {
    RawTarget    string
    ResolvedKind *EntryKind
}

type Entry struct {
    Location    Location
    Name        string
    Kind        EntryKind
    Metadata    Metadata
    Fingerprint Fingerprint
    Symlink     *SymlinkInfo
}

type Fingerprint struct {
    Size              *uint64
    ModifiedAt        *time.Time
    ModifiedPrecision *TimePrecision
    FileID            *string
    VersionID         *string
    HashAlgorithm     *string
    HashHex           *string
}

type FingerprintStrength string

func (f Fingerprint) Strength() FingerprintStrength
```

Strength order is `weak`, `stat`, `identity`, `strong`. Hash or VersionID is strong; FileID plus size plus precise mtime is identity; size plus precise mtime is stat.

- [ ] **Step 5: Implement capability revisions**

```go
type CapabilityName string

type CapabilityConstraint struct {
    Name  string
    Value string
}

type Capability struct {
    Name        CapabilityName
    Version     uint16
    Constraints []CapabilityConstraint
}

type CapabilityRevision struct {
    SessionID  SessionID
    Generation uint64
}

type CapabilitySnapshot struct {
    Revision CapabilityRevision
    Complete bool
    Items    []Capability
}

func NewCapabilitySnapshot(
    revision CapabilityRevision,
    complete bool,
    items []Capability,
) (CapabilitySnapshot, error)

func (s CapabilitySnapshot) Lookup(name CapabilityName) (Capability, bool)
```

The constructor sorts by name, rejects duplicate names, rejects empty SessionID, and requires generation greater than zero.

- [ ] **Step 6: Implement stable errors**

```go
type Code string
type RetryKind string
type EffectStatus string

const (
    RetryNever          RetryKind = "never"
    RetryImmediate      RetryKind = "immediate"
    RetryBackoff        RetryKind = "backoff"
    RetryAfterReconnect RetryKind = "after_reconnect"
    RetryAfterAuth      RetryKind = "after_auth"
    RetryAfterConflict  RetryKind = "after_conflict"
    RetryAfterReplan    RetryKind = "after_replan"
)

const (
    EffectNone    EffectStatus = "none"
    EffectApplied EffectStatus = "applied"
    EffectUnknown EffectStatus = "unknown"
)

type RetryAdvice struct {
    Kind  RetryKind
    After time.Duration
}

type OpError struct {
    Code       Code
    Message    string
    Operation  string
    EndpointID EndpointID
    Location   *Location
    Retry      RetryAdvice
    Effect     EffectStatus
    Cause      error
}

func (e *OpError) Error() string
func (e *OpError) Unwrap() error
func IsCode(err error, code Code) bool
func FromContext(operation string, endpointID EndpointID, loc *Location, err error) error
```

Canonical codes are `invalid_argument`, `not_found`, `already_exists`, `permission_denied`, `auth_required`, `transport_interrupted`, `timeout`, `unsupported`, `capability_lost`, `conflict`, `resource_exhausted`, `integrity_failed`, `canceled`, `protocol_incompatible`, and `internal`.

- [ ] **Step 7: Verify GREEN**

Run:

```bash
GOTOOLCHAIN=go1.26.5 go test -count=1 ./internal/domain
GOTOOLCHAIN=go1.26.5 go test -race -count=1 ./internal/domain ./internal/testkit
```

Expected: all tests pass without races.

- [ ] **Step 8: Record task checkpoint**

Run `git diff --check -- internal/domain` and record test output. Do not commit without user authorization.

---

### Task 4: Implement strict versioned configuration and deterministic clock

**Files:**

- Create: `internal/config/schema.go`
- Create: `internal/config/schema_test.go`
- Create: `internal/foundation/clock.go`
- Create: `internal/foundation/manual_clock.go`
- Create: `internal/foundation/manual_clock_test.go`

**Interfaces:**

- Produces: `config.Default() Config`, `config.Decode(io.Reader) (Config, error)`, `foundation.Clock`, `foundation.ManualClock`.
- Consumes: standard library only.

- [ ] **Step 1: Write failing strict-config tests**

```go
func TestDefaultConfigIsValid(t *testing.T)
func TestDecodeRejectsUnknownField(t *testing.T)
func TestDecodeRejectsTrailingJSONValue(t *testing.T)
func TestDecodeRejectsUnsupportedSchemaVersion(t *testing.T)
func TestDecodeAppliesNoImplicitZeroDefaults(t *testing.T)
```

- [ ] **Step 2: Verify RED**

Run `GOTOOLCHAIN=go1.26.5 go test ./internal/config ./internal/foundation`.

- [ ] **Step 3: Implement the exact schema**

```go
const SchemaVersion = 1

type Config struct {
    SchemaVersion int           `json:"schema_version"`
    IPC           IPCConfig     `json:"ipc"`
    Listing       ListingConfig `json:"listing"`
}

type IPCConfig struct {
    MaxFrameBytes uint32 `json:"max_frame_bytes"`
}

type ListingConfig struct {
    DefaultPageSize uint32 `json:"default_page_size"`
    MaxPageSize     uint32 `json:"max_page_size"`
}

func Default() Config
func Decode(r io.Reader) (Config, error)
func (c Config) Validate() error
```

Defaults are schema 1, 8 MiB maximum frame, page size 256, maximum page size 4096. `Decode` uses `DisallowUnknownFields` and rejects a second JSON value. The Stage 0 JSON decoder is the canonical machine schema; a future user-facing config adapter must map into this schema rather than weakening it.

- [ ] **Step 4: Implement deterministic time**

```go
type Timer interface {
    C() <-chan time.Time
    Stop() bool
}

type Clock interface {
    Now() time.Time
    NewTimer(time.Duration) Timer
}

type RealClock struct{}

type ManualClock struct {
    // private mutex, current time, and ordered timers
}

func NewManualClock(start time.Time) *ManualClock
func (c *ManualClock) Now() time.Time
func (c *ManualClock) NewTimer(d time.Duration) Timer
func (c *ManualClock) Advance(d time.Duration)
```

Tests must prove stable timer order, cancellation, and no real sleep.

- [ ] **Step 5: Verify GREEN**

Run:

```bash
GOTOOLCHAIN=go1.26.5 go test -count=1 ./internal/config ./internal/foundation
GOTOOLCHAIN=go1.26.5 go test -race -count=1 ./internal/config ./internal/foundation
```

- [ ] **Step 6: Record task checkpoint**

Record tests and diff check. Do not commit without user authorization.

---

### Task 5: Implement the versioned IPC wire contract

**Files:**

- Create: all files under `internal/ipc/` in the file map.

**Interfaces:**

- Produces: wire-safe bytes, Envelope validation, version negotiation, frame Reader/Writer, fuzz targets.
- Consumes: Task 3 domain IDs and errors; Task 4 frame-size default only through an injected constructor value.

- [ ] **Step 1: Write failing wire-bytes and envelope tests**

Tests include invalid UTF-8 round-trip:

```go
func TestWireBytesRoundTripInvalidUTF8(t *testing.T) {
    input := []byte{0xff, '/', 0x00, 0x80}
    encoded := ipc.EncodeWireBytes(input)
    got, err := encoded.Decode()
    if err != nil {
        t.Fatal(err)
    }
    if !bytes.Equal(got, input) {
        t.Fatalf("got %x want %x", got, input)
    }
}
```

Envelope table tests must cover every legal kind and illegal field combination.

- [ ] **Step 2: Verify RED**

Run `GOTOOLCHAIN=go1.26.5 go test ./internal/ipc` and confirm missing symbols.

- [ ] **Step 3: Implement protocol and envelope types**

```go
const (
    ProtocolMajor uint16 = 1
    ProtocolMinor uint16 = 0
    MaxFrameBytes uint32 = 8 * 1024 * 1024
)

type ProtocolVersion struct {
    Major uint16 `json:"major"`
    Minor uint16 `json:"minor"`
}

type EventCursor struct {
    Epoch    string `json:"epoch"`
    Sequence uint64 `json:"sequence"`
}

type WireBytes struct {
    Base64 string `json:"base64"`
}

func EncodeWireBytes(value []byte) WireBytes
func (w WireBytes) Decode() ([]byte, error)

type WireLocation struct {
    EndpointID string    `json:"endpoint_id"`
    Path       WireBytes `json:"path"`
}

type MessageKind string

const (
    KindRequest  MessageKind = "request"
    KindResponse MessageKind = "response"
    KindEvent    MessageKind = "event"
)

type RPCError struct {
    Code       domain.Code         `json:"code"`
    Message    string              `json:"message"`
    Operation  string              `json:"operation,omitempty"`
    EndpointID domain.EndpointID   `json:"endpoint_id,omitempty"`
    Location   *WireLocation       `json:"location,omitempty"`
    Retry      domain.RetryAdvice  `json:"retry"`
    Effect     domain.EffectStatus `json:"effect"`
}

type Envelope struct {
    Protocol  ProtocolVersion `json:"protocol"`
    Kind      MessageKind     `json:"kind"`
    Name      string          `json:"name"`
    RequestID domain.RequestID `json:"request_id,omitempty"`
    Cursor    *EventCursor    `json:"cursor,omitempty"`
    Payload   json.RawMessage `json:"payload,omitempty"`
    Error     *RPCError       `json:"error,omitempty"`
}

func (e Envelope) Validate() error
func DecodeEnvelope(data []byte) (Envelope, error)
func EncodeEnvelope(e Envelope) ([]byte, error)
```

`DecodeEnvelope` ignores unknown object fields for same-major forward compatibility, rejects trailing JSON values, and validates invariants.

- [ ] **Step 4: Implement handshake**

```go
type VersionRange struct {
    Major    uint16 `json:"major"`
    MinMinor uint16 `json:"min_minor"`
    MaxMinor uint16 `json:"max_minor"`
}

type ProtocolFeature struct {
    Name    string `json:"name"`
    Version uint16 `json:"version"`
}

type HelloRequest struct {
    ClientVersion    string
    ClientInstanceID string
    Protocols        []VersionRange
    Features         []ProtocolFeature
    EventTypes       []string
}

type HelloResponse struct {
    DaemonVersion string
    Selected      ProtocolVersion
    Features      []ProtocolFeature
    EventTypes    []string
    CurrentCursor EventCursor
    OldestCursor  EventCursor
}

func Negotiate(
    client []VersionRange,
    server []VersionRange,
    clientFeatures []ProtocolFeature,
    serverFeatures []ProtocolFeature,
) (ProtocolVersion, []ProtocolFeature, error)
```

Select the highest shared minor under a common major; intersect features by name using the lower supported version. No common version returns `protocol_incompatible`.

- [ ] **Step 5: Implement bounded frames**

```go
type Reader struct {
    r   io.Reader
    max uint32
}

type Writer struct {
    w   io.Writer
    max uint32
}

func NewReader(r io.Reader, max uint32) (*Reader, error)
func NewWriter(w io.Writer, max uint32) (*Writer, error)
func (r *Reader) ReadFrame() ([]byte, error)
func (w *Writer) WriteFrame(payload []byte) error
```

Readers reject zero length, over-limit length, truncated header, and truncated body before allocation beyond the configured limit. A broken reader that repeatedly returns `(0, nil)` is bounded by a consecutive-empty-read limit and fails with `io.ErrNoProgress`; any actual progress resets the limit. Writers loop on short writes and apply the same no-progress safety rule.

- [ ] **Step 6: Add cancellation wire types and cursor validation**

```go
type CancelRequest struct {
    TargetRequestID domain.RequestID `json:"target_request_id"`
}

type CancelState string

const (
    CancelAccepted       CancelState = "accepted"
    CancelAlreadyFinished CancelState = "already_finished"
    CancelNotFound       CancelState = "not_found"
    CancelNotCancellable CancelState = "not_cancellable"
)

type CancelResult struct {
    State CancelState `json:"state"`
}

type CursorRelation string

const (
    CursorNext        CursorRelation = "next"
    CursorDuplicate   CursorRelation = "duplicate"
    CursorGap         CursorRelation = "gap"
    CursorEpochChange CursorRelation = "epoch_change"
)

func CompareCursor(previous, next EventCursor) CursorRelation
```

Cursor relations distinguish next, duplicate, gap, and epoch change.

- [ ] **Step 7: Add fuzz tests**

Required targets:

```go
func FuzzFrameDecoder(f *testing.F)
func FuzzEnvelopeDecode(f *testing.F)
func FuzzWireBytes(f *testing.F)
```

Each target seeds valid and invalid cases and asserts no panic, unbounded allocation, or acceptance of invalid envelope invariants.

- [ ] **Step 8: Verify GREEN**

Run:

```bash
GOTOOLCHAIN=go1.26.5 go test -count=1 ./internal/ipc
GOTOOLCHAIN=go1.26.5 go test -race -count=1 ./internal/ipc
GOTOOLCHAIN=go1.26.5 go test -run='^$' -fuzz=FuzzFrameDecoder -fuzztime=10s ./internal/ipc
```

- [ ] **Step 9: Record task checkpoint**

Record tests and diff check. Do not commit without user authorization.

---

### Task 6: Define Provider core, Mutable Facet, and reusable contract suite

**Files:**

- Create: `internal/provider/provider.go`
- Create: `internal/provider/listing.go`
- Create: `internal/provider/mutable.go`
- Create: `internal/provider/validate.go`
- Create: `internal/provider/validate_test.go`
- Create: `internal/provider/contracttest/suite.go`

**Interfaces:**

- Produces: stable Provider and MutableProvider contracts plus `contracttest.Run`.
- Consumes: Task 3 domain types.

- [ ] **Step 1: Write failing validation tests**

Test page limit 0, page limit 4097, endpoint mismatch, invalid offset, negative range limit, and invalid Done/NextCursor combinations.

- [ ] **Step 2: Verify RED**

Run `GOTOOLCHAIN=go1.26.5 go test ./internal/provider/...`.

- [ ] **Step 3: Implement the read-only core**

```go
type Provider interface {
    Descriptor() domain.Endpoint
    Snapshot(context.Context) (domain.EndpointSnapshot, error)
    Normalize(context.Context, domain.NormalizeRequest) (domain.Location, error)
    List(context.Context, ListRequest) (ListPage, error)
    Stat(context.Context, StatRequest) (domain.Entry, error)
    OpenRead(context.Context, OpenReadRequest) (ReadHandle, error)
}

type PageCursor string
type SortKey string
type SortDirection string

const (
    SortAscending  SortDirection = "ascending"
    SortDescending SortDirection = "descending"
)

type SortHint struct {
    Key              SortKey
    Direction        SortDirection
    DirectoriesFirst bool
}

type ListRequest struct {
    Location domain.Location
    Cursor   PageCursor
    Limit    uint32
    Sort     *SortHint
}

type ListPage struct {
    Entries              []domain.Entry
    NextCursor           PageCursor
    Done                 bool
    RequestedSortApplied bool
    Consistency          ListConsistency
    DirectoryFingerprint domain.Fingerprint
}

type ListConsistency string

const (
    ConsistencySnapshot   ListConsistency = "snapshot"
    ConsistencyBestEffort ListConsistency = "best_effort"
)

type StatRequest struct {
    Location       domain.Location
    FollowSymlinks bool
}

type OpenReadRequest struct {
    Location            domain.Location
    Offset              int64
    Limit               *int64
    ExpectedFingerprint *domain.Fingerprint
}

type ReadInfo struct {
    Entry       domain.Entry
    Fingerprint domain.Fingerprint
}

type ReadHandle interface {
    Info() ReadInfo
    Read(context.Context, []byte) (int, error)
    Close(context.Context) error
}
```

List limit is 1–4096. `Done=true` exactly when `NextCursor=""`. Cursor is opaque and bound to endpoint, path, sort, and listing generation.

- [ ] **Step 4: Implement the optional write Facet**

```go
type MutableProvider interface {
    OpenWrite(context.Context, OpenWriteRequest) (WriteHandle, error)
    Mkdir(context.Context, MkdirRequest) (domain.Entry, error)
    Rename(context.Context, RenameRequest) (RenameResult, error)
    Remove(context.Context, RemoveRequest) error
}

type WriteDisposition string

const (
    WriteCreateNew      WriteDisposition = "create_new"
    WriteResumeExisting WriteDisposition = "resume_existing"
    WriteTruncate       WriteDisposition = "truncate"
)

type OpenWriteRequest struct {
    Location            domain.Location
    Offset              int64
    Disposition         WriteDisposition
    ExpectedFingerprint *domain.Fingerprint
}

type WriteHandle interface {
    Write(context.Context, []byte) (int, error)
    Sync(context.Context) error
    Close(context.Context) error
}

type MkdirRequest struct {
    Location  domain.Location
    Exclusive bool
}

type RenameRequest struct {
    Source              domain.Location
    Destination         domain.Location
    Replace             bool
    ExpectedSource      *domain.Fingerprint
    ExpectedDestination *domain.Fingerprint
}

type RenameResult struct {
    Atomic   bool
    Replaced bool
}

type RemoveRequest struct {
    Location domain.Location
    Expected *domain.Fingerprint
}
```

`Remove` handles one file or one empty directory only. Recursion belongs to a later planner.

- [ ] **Step 5: Implement the reusable contract suite**

```go
type Fixture struct {
    Provider            provider.Provider
    InvalidateListing   func(context.Context, domain.Location) error
    ChangeCapabilities  func(context.Context) error
}

type Factory interface {
    New(t *testing.T) Fixture
}

func Run(t *testing.T, factory Factory)
```

Every fixture must provide a fresh Provider and both non-nil deterministic control hooks. The suite checks descriptor stability, normalization boundaries, bounded pages, actual listing-generation cursor invalidation, fixture isolation, short reads, EOF, context cancellation, idempotent close, symlink stat behavior, an explicit same-session capability change with a strictly increasing revision, and typed error context. These checks must not be conditional or skipped when a hook is absent.

- [ ] **Step 6: Verify GREEN**

Run:

```bash
GOTOOLCHAIN=go1.26.5 go test -count=1 ./internal/provider/...
GOTOOLCHAIN=go1.26.5 go vet ./internal/provider/...
```

The contract package may have no concrete factory until Task 7, but its own validation tests must pass.

- [ ] **Step 7: Record task checkpoint**

Record tests and diff check. Do not commit without user authorization.

---

### Task 7: Implement the deterministic in-memory Fake Provider

**Files:**

- Create: `internal/provider/fake/provider.go`
- Create: `internal/provider/fake/tree.go`
- Create: `internal/provider/fake/controller.go`
- Create: `internal/provider/fake/provider_contract_test.go`
- Create: `internal/provider/fake/fuzz_test.go`

**Interfaces:**

- Produces: `fake.New(Scenario) (*Provider, *Controller, error)` implementing both Provider contracts.
- Consumes: Task 4 Clock and Task 6 Provider interfaces.

- [ ] **Step 1: Write the failing contract invocation**

```go
func TestProviderContract(t *testing.T) {
    contracttest.Run(t, contractFactory{})
}
```

The factory creates a fresh local endpoint with root, nested directory, regular file, symlink, and an invalid-UTF-8 filename. Its returned `contracttest.Fixture` closes over that fixture's Controller so `InvalidateListing` changes only its listing generation and `ChangeCapabilities` changes the payload within the same session while incrementing generation.

- [ ] **Step 2: Verify RED**

Run `GOTOOLCHAIN=go1.26.5 go test ./internal/provider/fake`.

Expected: contract failures caused by unimplemented Fake behavior.

- [ ] **Step 3: Implement Scenario and tree ownership**

```go
type Scenario struct {
    Endpoint     domain.Endpoint
    Snapshot     domain.EndpointSnapshot
    Root         Node
    DefaultLimit uint32
    Clock        foundation.Clock
}

type Node struct {
    Name       string
    Kind       domain.EntryKind
    Data       []byte
    Metadata   domain.Metadata
    LinkTarget string
    Children   []Node
}

func New(s Scenario) (*Provider, *Controller, error)
```

Validate unique child names, exactly one root directory, no NUL bytes, matching endpoint/session IDs, a `DefaultLimit` in `1..4096`, and deep-copy all caller-owned slices. Treat `Node.Children` as a caller-owned graph while validating: reject a node that is revisited on the current recursion stack so a cyclic slice cannot recurse forever, but allow the same acyclic slice to be reused in separate branches and deep-copy each branch independently.

- [ ] **Step 4: Implement read behavior**

Implement Normalize, List, Stat/lstat, OpenRead, bounded Read, and idempotent Close. The Fake never starts a background prefetch goroutine. Every page and returned byte slice is a deep copy.

`ConsistencySnapshot` is a materialized listing contract, not only a generation label. The first page freezes the fully sorted `Entry` sequence, directory fingerprint, requested-sort result, resolved directory node identity, and listing generation in fixture-private state. Every continuation verifies the opaque cursor's fixture, canonical request location, complete sort hint, resolved directory node identity, and generation, then slices the frozen entries instead of rebuilding future pages from the live tree. A direct mutation of the listed directory returns stale conflict; a nested child-fingerprint change or external symlink-target change cannot leak newer `Entry` data into an older cursor chain. Returned entries and fingerprints remain deep copies of the private snapshot.

- [ ] **Step 5: Implement write Facet behavior for future contract tests**

Implement create/resume/truncate, short-write-compatible handles, mkdir, rename, and single-node remove. No transfer engine or recursive operation is added. Rename compares resolved dirent identity after both expected-fingerprint preconditions: two locations that address the same dirent through different symlink parents are an atomic no-op with `Replaced=false` and do not advance listing generation.

- [ ] **Step 6: Verify contract GREEN**

Add `FuzzNormalizePath` with seeds for root, repeated separators, parent traversal, NUL, invalid UTF-8, relative paths with and without a base, and endpoint mismatch. The invariant is that accepted output stays under the endpoint root and can be normalized again without change.

Run:

```bash
GOTOOLCHAIN=go1.26.5 go test -count=1 ./internal/provider/contracttest ./internal/provider/fake
GOTOOLCHAIN=go1.26.5 go test -race -count=1 ./internal/provider/fake
GOTOOLCHAIN=go1.26.5 go test -run='^$' -fuzz=FuzzNormalizePath -fuzztime=10s ./internal/provider/fake
```

- [ ] **Step 7: Record task checkpoint**

Record contract results and diff check. Do not commit without user authorization.

---

### Task 8: Add Fake fault scripting, call records, and deterministic concurrency

**Files:**

- Create: `internal/provider/fake/script.go`
- Create: `internal/provider/fake/faults_test.go`
- Modify: `internal/provider/fake/provider.go`
- Modify: `internal/provider/fake/controller.go`
- Modify: `internal/provider/fake/tree.go`

**Interfaces:**

- Produces: deterministic delays, gates, nth-call faults, short I/O, stale metadata, permission changes, disconnect, capability withdrawal, non-atomic rename reporting, and call records.
- Consumes: Task 7 Fake.

**Fault-script contract:**

- Extend `Scenario` with `Script []FaultStep`. `New` validates and deep-copies the slice, path pointers, stale-revision pointers, and injected `OpError` locations. Every Fake owns independent consumption, counters, calls, and gates.
- Export stable `Operation*` constants for snapshot, normalize, list, stat, open/read/close-read, open/write/sync/close-write, mkdir, rename, and remove. Descriptor, handle Info, and Controller calls are not recorded. Snapshot has no Call location; Normalize records its successfully computed canonical Location; handle calls retain their open Location; rename records its source because the frozen `Call` shape has one location.
- A valid call passes context and pure request/path/range validation before it is recorded, but is recorded before tree lookup, persistent connection/capability/permission checks, or Provider/handle locking. Structurally invalid or already-canceled requests neither record nor consume a step. A call canceled during a selected delay/gate remains recorded and its step remains consumed.
- `Nth` is one-based within the exact matcher key `(Operation, optional Path)`. Nth values are unique per key. To prevent one call from being due in two matcher domains, one Operation may use either a wildcard path or exact paths, not both; different exact paths have independent counters. Declaration order is stable and one call consumes at most one step.
- The independent script mutex is the call/fault linearization point: allocate a globally increasing sequence, append a deep-copied Call, advance the matcher count, select and consume at most one step, copy its effect, then unlock. It is never nested with Provider or handle mutexes.
- After releasing the script mutex, every selected effect runs deterministic Delay, sticky Gate, then a context recheck. A standalone injected Error returns immediately before persistent checks. Every other primary is carried into the operation's locked path. Provider-only operations apply Disconnect to the session current at execution; otherwise they check connection, capability, permission, and operation preconditions before stale/non-atomic/underlying behavior. Handle operations acquire `handle -> Provider`, check `closed -> captured sessionEpoch`, and apply Disconnect only after those two guards; a closed or old-session handle must not disconnect the current session. A valid current-session handle applies Disconnect before connection/capability/permission and returns transport interruption. Without Disconnect, it checks `connection -> capability -> permission -> operation-specific preconditions/commit`. Short I/O and non-atomic rename act only after those checks; a combined Error+short is returned after the real prefix. Delay and Gate hold no state/handle locks and start no goroutine.
- Lock order is frozen: script linearization holds no Provider/handle lock; Provider-only operations take only the Provider lock; handle operations take `handle -> Provider`, with closed/session/connection/capability/permission checks and I/O commit in that one critical section; Controller methods take only the Provider lock. No path acquires a handle lock while holding the Provider lock. `Clock.Now`, `Clock.NewTimer`, `Controller.Advance`, and gate waits/releases run outside all three lock classes.

`Delay` uses the injected Clock's timer and selects the timer against context cancellation; tests wait for an instrumented ManualClock timer-registration signal before calling `Controller.Advance`. A gate is a pre-created one-shot broadcast latch: release-before-wait is sticky and repeated release is idempotent. `Controller.Advance` keeps the required no-error signature, requires the injected clock to implement `Advance(time.Duration)`, and panics descriptively rather than silently doing nothing when it cannot.

At most one primary effect is allowed except that an explicit Error may accompany `MaxReadBytes` or `MaxWriteBytes` to model real `(n > 0, err != nil)` progress when bytes are available. Delay and/or Gate may decorate a primary effect or stand alone. Stale revision is Stat-only. `NonAtomicRename` is Rename-only, performs the normal mutation, and reports `Atomic:false`. Disconnect is valid only for List/Stat/OpenRead/Read/OpenWrite/Write/Sync/Mkdir/Rename/Remove. Disconnect, stale, and non-atomic rename exclude every other primary; short-read/write and stale values must be positive and operation-specific; `StaleNodeRevision == 0` is rejected by `New`; Delay is non-negative; and an effect with no positive Delay, non-empty Gate, or primary behavior is rejected. Short I/O applies only the real prefix and never invents progress for an empty caller buffer/source or exhausted read.

Injected errors are fixture-owned copies and require known non-empty Code, Retry.Kind, and Effect values plus non-negative Retry.After. Error-alone may declare only EffectNone or EffectUnknown because no operation is applied. At return time, fill missing operation, endpoint, and Location from the matched Call without mutating the script input. `Cause` deliberately preserves identity by shallow copy because arbitrary error implementations cannot be deep-copied. A combined short fault performs persistent checks first, applies only the real prefix, then returns the owned Error: a progressing short write forces EffectApplied, while short-read and every zero-progress result force EffectNone; code, retry, cause, and message otherwise remain declared. Persistent denial uses `permission_denied`, disconnect uses `transport_interrupted` with reconnect advice, and confirmed withdrawal uses `capability_lost` with replan advice.

Exact Match paths must be canonical absolute paths; Snapshot cannot have a path matcher. Empty/unknown operations, Nth zero, duplicate Nth, wildcard/exact overlap for one operation, empty gate names, negative Delay, and invalid effect combinations make `New` fail before the fixture becomes observable.

**Persistent controller semantics:**

- `SetPermission(path, allowed)` names the boolean contract: `false` denies and `true` restores. Controller lookup bypasses current denial so restoration remains possible, root is valid, and the final symlink is followed before storing permission by node identity. Denial of an actually traversed ancestor blocks descendant lookup; create/Mkdir check the resolved parent chain; resume/truncate check parent chain and target; Rename/Remove check lexical source/target dirents, relevant parent chains, and any replaced destination. A Rename permission error from the source chain or lexical source uses `Source` as its Location, while denial from the destination parent or a replaced lexical destination uses `Destination`; endpoint-wide state/capability errors continue to use `Source`. Ongoing Read/Write/Sync check their opened node so later denial/restoration is observed; a ReadHandle therefore retains opened node identity while keeping its immutable byte snapshot. Namespace operations on a final symlink do not inherit denial from its followed target.
- List hard-checks only the requested directory traversal. A child symlink whose target traversal is currently denied remains visible with `RawTarget` but has `ResolvedKind:nil`; `Stat(FollowSymlinks:false)` behaves the same, while followed Stat and OpenRead fail permission-denied. Authorization at return wins: every List-page clone and live/stale lstat response scrubs only the returned copy's `ResolvedKind` against current permission, including denial between cursor pages, without mutating frozen listing snapshots or history. Missing or changed targets otherwise retain the existing snapshot/history semantics.
- `ReplaceNode` accepts a canonical non-root path and a Node whose name equals the basename and whose root EntryKind equals the existing node's kind. Intermediate symlinks are followed but the final dirent is lexical, so replacing `/alias` replaces the symlink while `/alias/file` traverses the alias parent. It validates/deep-copies the replacement, preserves the logical root pointer/ID and permission, advances root version once, and gives fresh descendants new IDs, revision 1 history, and allowed permission. File/symlink replacement increments the lexical parent listing generation once; directory replacement also increments the preserved directory's own listing generation once; unrelated directory generations do not change. Existing ReadHandles retain old bytes and node identity; an existing file WriteHandle continues against the preserved file identity and offset. Root kind changes are rejected. This is revision replacement, not Task 7 remove/create ABA.
- Provider-private immutable stat history starts at revision 1 for every initial, create/Mkdir, and replacement-descendant node and records every version-changing revision by node ID/revision. `StaleNodeRevision=N` returns a deep copy of historical kind/metadata/fingerprint/symlink rebased to the current Stat request Location/name and then permission-scrubbed as above, without mutating history. An unknown positive revision returns `invalid_argument/never/none` after the valid call is recorded and its selected step consumed; zero never reaches runtime because `New` rejects it.
- `SetSnapshot` validates and owns endpoint/session/known state/capabilities. Within one session, a lower capability generation is rejected; an equal generation is allowed only with byte-for-byte equivalent Complete/items and supports ready reconnect; changed capability payload requires a strictly greater generation and updates loss history. The Provider owns a positive monotonic `sessionEpoch`; every accepted SessionID change increments it, even if a later session reuses an older ID. Handles capture the epoch, so A→B→A never revives A handles. A new session may restart at any positive generation and always clears cursors/loss history. Same-session entry into connecting, disconnected, auth_required, or failed clears cursors; ready↔degraded retains them. Rejected transitions change no snapshot, cursor, epoch, or loss history. `SetCapabilities` requires the current session and a strictly greater generation, owns the payload, and samples Clock outside Provider locks; the legacy `ChangeCapabilities` hook reuses the same loss bookkeeping. Calls return copied slices and Location pointers.
- Disconnect retains session/capability revision, changes state to disconnected, updates observation time, clears cursors, and makes the triggering/subsequent data, mutation, and Sync operations return transport-interrupted/reconnect. A gated old-session or closed-handle Disconnect must not affect the current snapshot; a Provider-only Disconnect targets the session current when it acquires the Provider lock. Snapshot, Normalize, Info, and Close stay usable. Same-session ready SetSnapshot reconnects and existing handles may resume; a new session makes old handles return conflict/replan without I/O. Ready and degraded are operational, auth_required returns auth_required/after_auth, and connecting/disconnected/failed return transport_interrupted/after_reconnect.
- Capability-loss checks apply only when a capability explicitly present in this session is absent from a newer complete snapshot. Initial absence, even in a complete snapshot, does not gate this Fake; callers route from Snapshot. Incomplete absence neither creates nor clears confirmed loss, explicit presence restores it, and a new session clears it. Minimal names are `read` for List/Stat/OpenRead/Read and `write` for OpenWrite/Write/Sync/Mkdir/Rename/Remove; Close, Normalize, and Snapshot require neither.

- [ ] **Step 1: Write failing fault tests**

Required tests:

```go
func TestNthCallFaultIsConsumedOnce(t *testing.T)
func TestDelayUsesManualClock(t *testing.T)
func TestGateHonorsContextCancellation(t *testing.T)
func TestShortReadAndShortWriteReportProgress(t *testing.T)
func TestStaleStatUsesRequestedRevision(t *testing.T)
func TestDisconnectChangesSessionSnapshot(t *testing.T)
func TestCapabilityWithdrawalReturnsCapabilityLost(t *testing.T)
func TestCallsReturnsDeepCopy(t *testing.T)
func TestRenameCanReportNonAtomic(t *testing.T)
func TestDiskFullErrorIsTypedAndDoesNotApply(t *testing.T)
```

Also cover invalid/overlapping scripts, invalid calls not consuming faults, release-before-wait, unknown/empty `ReleaseGate` descriptive panic, delay registration before Advance, no locks held while waiting, globally ordered call sequences, non-atomic Rename, disk-full typed Error, controller input ownership, persistent permissions, incomplete capabilities, same/equal/lower-generation SetSnapshot, same/new-session handles, state-specific errors, List delay before snapshot materialization, non-advanceable clocks, and all Controller validation boundaries. `faults_test.go` must not use real-time sleeps, deadlines, or timers as synchronization and must not manufacture a simultaneous timer/context-ready tie.

Freeze the remaining TDD waves so a later session can continue without reconstructing cross-feature semantics:

- **D1 history/stale:** seed initial/create/Mkdir/replacement descendants; record write/truncate/directory bumps; request old/current and unknown-positive revisions; prove unknown consumes its step, rebase/deep-copy, rename-by-node-ID history, historical symlink data, and constructor-only zero rejection.
- **D2 ReplaceNode:** canonical/root/name/kind/cycle/metadata-size validation and failure atomicity; input ownership; lexical-final versus intermediate symlink; old read bytes and continuing write pointer/offset; exact root/parent/unrelated cursor invalidation; permission preservation and fresh-descendant identity/history.
- **D3 snapshot/capabilities bookkeeping:** known state and endpoint/session/generation validation; same-session lower/equal/higher rules; new-session generation restart; deep-copy and total rejection atomicity; SetCapabilities and legacy ChangeCapabilities shared loss logic; reentrant Clock.Now outside locks; the frozen cursor-clearing matrix.
- **E1 state/session/capability/disconnect:** all state error mappings; initial absence versus confirmed complete withdrawal, incomplete absence, restore, and new-session reset; Provider and handle operation map; same-session reconnect; A→B→A epoch safety; gated old-handle and closed-handle Disconnect not affecting a new/current session; Snapshot/Normalize/Info/Close availability.
- **E2 permission/precedence:** root/ancestor/final-symlink deny and restore; full table for List, both Stat modes, OpenRead, create/resume/truncate, Mkdir, Rename, Remove, and ongoing handles; lexical namespace behavior; List-page and stale-lstat ResolvedKind scrub; state before capability before permission before lookup/precondition; stale/short/non-atomic zero progress/mutation on persistent failure.

- [ ] **Step 2: Verify RED**

Run `GOTOOLCHAIN=go1.26.5 go test ./internal/provider/fake`.

- [ ] **Step 3: Implement fault script types**

```go
type Operation string

type FaultMatch struct {
    Operation Operation
    Nth       uint64
    Path      *domain.CanonicalPath
}

type FaultEffect struct {
    Delay             time.Duration
    WaitGate          string
    Error              *domain.OpError
    MaxReadBytes      int
    MaxWriteBytes     int
	StaleNodeRevision *uint64
	Disconnect        bool
	NonAtomicRename   bool
}

type FaultStep struct {
    Match  FaultMatch
    Effect FaultEffect
}

type Call struct {
    Operation Operation
    Location  *domain.Location
    Sequence  uint64
}
```

Each step is consumed at most once and each call matches at most one step under the matcher rules above.

- [ ] **Step 4: Implement Controller controls**

```go
func (c *Controller) Advance(time.Duration)
func (c *Controller) ReleaseGate(string)
func (c *Controller) ReplaceNode(domain.CanonicalPath, Node) error
func (c *Controller) SetPermission(domain.CanonicalPath, bool) error
func (c *Controller) SetSnapshot(domain.EndpointSnapshot) error
func (c *Controller) SetCapabilities(domain.CapabilitySnapshot) error
func (c *Controller) Calls() []Call
```

All state and call records are mutex-protected under the lock-order rules above. Gate waits select on context cancellation. No fault test uses real sleep or a wall-clock deadline as synchronization.

- [ ] **Step 5: Verify GREEN and race safety**

Run:

```bash
GOTOOLCHAIN=go1.26.5 go test -count=1 ./internal/provider/fake
GOTOOLCHAIN=go1.26.5 go test -count=100 -run='^(TestNthCallFaultIsConsumedOnce|TestDelayUsesManualClock|TestGateHonorsContextCancellation|TestFaultCallSequencesAreLinearized)$' ./internal/provider/fake
GOTOOLCHAIN=go1.26.5 go test -race -count=20 ./internal/provider/fake
GOTOOLCHAIN=go1.26.5 go test -count=1 ./internal/provider/contracttest ./internal/provider/fake
GOTOOLCHAIN=go1.26.5 go test -count=1 ./...
GOTOOLCHAIN=go1.26.5 go vet ./...
if rg -n 'time\.(Sleep|After|AfterFunc|NewTimer)|context\.With(Timeout|Deadline)' internal/provider/fake/faults_test.go; then
  echo "prohibited real-time synchronization found" >&2
  exit 1
else
  rc=$?
  test "$rc" -eq 1
fi
if rg -n 'time\.(Sleep|After|AfterFunc|NewTimer)' internal/provider/fake/{script,provider,controller}.go; then
  echo "prohibited production real-time primitive found" >&2
  exit 1
else
  rc=$?
  test "$rc" -eq 1
fi
if rg -n '\bgo\s+(func|[[:alnum:]_])' internal/provider/fake/{script,provider,controller}.go; then
  echo "prohibited production goroutine found" >&2
  exit 1
else
  rc=$?
  test "$rc" -eq 1
fi
```

- [ ] **Step 6: Record task checkpoint**

Record tests and diff check. Do not commit without user authorization.

---

### Task 9: Implement executable documentation and workflow checks

**Files:**

- Create: `internal/docscheck/check.go`
- Create: `internal/docscheck/links.go`
- Create: `internal/docscheck/matrix.go`
- Create: `internal/docscheck/state.go`
- Create: `internal/docscheck/workflow.go`
- Create: `internal/docscheck/check_test.go`
- Create: focused fixtures below `internal/docscheck/testdata/`
- Create: `internal/tools/docscheck/main.go`

**Interfaces:**

- Produces: `docscheck.Check(root string) []Finding` and a nonzero-exit command.
- Consumes: repository documents only.

- [ ] **Step 1: Create failing fixture tests**

```go
type Finding struct {
    Path    string
    Line    int
    Rule    string
    Message string
}

func TestValidFixtureHasNoFindings(t *testing.T)
func TestBrokenRelativeLink(t *testing.T)
func TestDuplicateFeatureID(t *testing.T)
func TestInvalidStageOrStatus(t *testing.T)
func TestMissingFeatureEvidence(t *testing.T)
func TestStatePlanMismatch(t *testing.T)
func TestUnpinnedWorkflowAction(t *testing.T)
func TestDangerousWorkflowPermissions(t *testing.T)
```

Every failing fixture asserts exact rule, path, and line number.

- [ ] **Step 2: Verify RED**

Run `GOTOOLCHAIN=go1.26.5 go test ./internal/docscheck`.

- [ ] **Step 3: Implement deterministic checks**

```go
func Check(root string) []Finding
```

`docscheck.Check` must detect:

- relative links that do not resolve or escape the repository;
- malformed or duplicate stable feature IDs;
- Stage outside 0–6 or illegal status;
- missing evidence and stale “not implemented” evidence on non-Planned rows;
- Verified rows without a verification record;
- missing Stage 0–6 documents;
- mismatched active/current Stage between project state and plan;
- unresolved marker tokens in durable documents;
- workflow actions not pinned to a 40-character lowercase SHA;
- write permissions, `pull_request_target`, or `persist-credentials: true` in CI.

Relative-link containment is checked again after resolving symlinks for both the repository root and existing target. The feature matrix may contain multiple section-scoped feature tables: every feature-bearing section has exactly one canonical header, every canonical table has at least one parsed feature row, IDs remain globally unique, and missing, malformed, duplicate, or empty table schemas fail closed with `matrix.schema`. Workflow checks normalize valid single- or double-quoted YAML keys/scalars and supported flow-style forms while retaining the indentation/path context: trigger rules apply only to top-level `on`, permissions only to top-level or `jobs.<job>.permissions`, action pins only to job/step `uses`, and credential persistence only to the checkout step's `with.persist-credentials`. Action inputs that merely share those key names must not be misclassified. Findings are sorted by path, line, then rule.

- [ ] **Step 4: Implement the command**

```go
func main() {
    root := "."
    if len(os.Args) == 2 {
        root = os.Args[1]
    }
    findings := docscheck.Check(root)
    for _, finding := range findings {
        fmt.Printf("%s:%d: %s: %s\n", finding.Path, finding.Line, finding.Rule, finding.Message)
    }
    if len(findings) > 0 {
        os.Exit(1)
    }
}
```

Reject more than one positional argument with exit code 2.

- [ ] **Step 5: Verify GREEN against fixtures and the real repository**

Run:

```bash
GOTOOLCHAIN=go1.26.5 go test -count=1 ./internal/docscheck
GOTOOLCHAIN=go1.26.5 go run ./internal/tools/docscheck .
```

Expected: fixture tests pass and the current repository produces no findings.

- [ ] **Step 6: Record task checkpoint**

Record tests and diff check. Do not commit without user authorization.

---

### Task 10: Add repeatable quality, supply-chain, build, and CI entrypoints

**Files:**

- Create: `Makefile`
- Create: `.golangci.yml`
- Create: `.github/workflows/ci.yml`
- Create: `.github/workflows/nightly.yml`
- Create: `.github/dependabot.yml`
- Create: `tools/go.mod`
- Create: `tools/go.sum`
- Create: `docs/development/testing.md`
- Create: `docs/security/dependency-policy.md`
- Modify: `internal/docscheck/workflow.go`
- Modify: `internal/docscheck/state.go`
- Modify: `internal/docscheck/matrix.go`
- Modify: `internal/docscheck/check_test.go`
- Create: focused workflow-policy fixtures below `internal/docscheck/testdata/`
- Modify: `docs/README.md`
- Modify: `docs/development/toolchain.md`

**Interfaces:**

- Produces: `make check`, `make ci`, cross-build matrix, pinned remote CI.
- Consumes: all implemented packages and docscheck.

- [ ] **Step 1: Write the Make target contract**

The Makefile must expose:

```text
fmt-check
vet
lint
test
test-contract
test-race
fuzz-smoke
docs-check
mod-check
supply-chain
build-all
check
ci
```

Third-party tools live in an independent `tools` module using Go `tool` directives and exact requirements: golangci-lint v2.12.2, govulncheck from `golang.org/x/vuln` v1.6.0, and actionlint v1.7.12. `tools/go.sum` is the execution lock file. Run tools from the repository root so analyzers see the product module, while selecting the alternate module only for the tool binary:

```make
GO ?= go
TOOL_MOD := -modfile=tools/go.mod
GOLANGCI_LINT := $(GO) tool $(TOOL_MOD) golangci-lint
GOVULNCHECK := $(GO) tool $(TOOL_MOD) govulncheck
ACTIONLINT := $(GO) tool $(TOOL_MOD) actionlint
```

Do not use `go run package@version`: that bypasses the committed tools module graph. Root and tools module checks use non-mutating `mod tidy -diff` plus `mod verify`; only tools-module maintenance uses `$(GO) -C tools`, while golangci-lint, govulncheck, and actionlint execute from the product repository root through `$(GO) tool $(TOOL_MOD)` so analyzers inspect the product module. Every Go invocation, including tool execution and builds, honors the Make `GO` override. Add an executable Make contract check that fails on a literal Go executable in recipes or a tool command rooted in `tools`; it must prove a sentinel `GO=` reaches lint, supply-chain, module, fuzz, and build command expansion. `fmt-check` is read-only. `fuzz-smoke` invokes every named fuzz target separately. Define overrideable `BUILD_DIR ?= dist` and `COVERAGE_DIR ?= coverage`, safely quote them in POSIX-shell recipes even when an external path contains spaces, and route every generated binary/profile there. `build-all` emits the exact files `$(BUILD_DIR)/amsftp-darwin-arm64`, `$(BUILD_DIR)/amsftp-darwin-amd64`, `$(BUILD_DIR)/amsftp-linux-arm64`, and `$(BUILD_DIR)/amsftp-linux-amd64` with `CGO_ENABLED=0`, `-trimpath`, and explicit GOOS/GOARCH. Keep all recipes compatible with macOS GNU Make 3.81; do not rely on newer Make functions or Bash-only default-shell behavior.

`check` runs format, vet, unit/contract, docs, and module checks. `ci` additionally runs lint, race, fuzz smoke, supply chain, and all builds.

- [ ] **Step 2: Add lint configuration**

Use golangci-lint configuration version 2, a five-minute timeout, and enable at least `errorlint`, `gosec`, `govet`, `ineffassign`, `misspell`, `nilerr`, `staticcheck`, `unconvert`, and `unparam`. Generated files are not globally excluded.

```yaml
version: "2"
run:
  timeout: 5m
linters:
  default: none
  enable:
    - errorlint
    - gosec
    - govet
    - ineffassign
    - misspell
    - nilerr
    - staticcheck
    - unconvert
    - unparam
formatters:
  enable:
    - gofmt
issues:
  max-issues-per-linter: 0
  max-same-issues: 0
```

- [ ] **Step 3: Add pinned CI**

Use exact immutable references. Revalidate each SHA against its official repository/tag immediately before accepting Task 10 and record the date and exact `git ls-remote` commands in the implementation report:

```yaml
permissions:
  contents: read

concurrency:
  group: ci-${{ github.workflow }}-${{ github.ref }}
  cancel-in-progress: true

steps:
  - uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0
    with:
      persist-credentials: false
  - uses: actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16 # v6.5.0
    with:
      go-version-file: go.mod
      cache: true
```

Artifact handoff uses `actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a` (`v7.0.1`) and `actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c` (`v8.0.1`). On 2026-07-14, `git ls-remote` against each official `actions/*` repository confirmed all four pinned commits at the named tags; retain the immutable SHAs in workflow code.

`ci.yml` has:

- quality on `ubuntu-24.04` with `make check` and supply-chain checks;
- native tests on `ubuntu-22.04`, `ubuntu-24.04`, `macos-15`, and `macos-15-intel`; every native leg runs read-only format checking, vet, unit, the explicit Provider contract target, race, a native `cmd/amsftp` build, and a role smoke test so CORE-007 covers the supported minimum/current OS and both macOS architectures rather than borrowing Ubuntu quality results;
- oldstable compatibility on both platforms using Go 1.25.12 and `GOTOOLCHAIN=local`;
- build matrix for all four target pairs;
- job timeouts, `fail-fast: false`, and no `continue-on-error`.

`setup-go` cache configuration points to the committed `tools/go.sum`; it must not list a nonexistent root `go.sum`. Do not set `CGO_ENABLED=0` globally because race jobs need cgo. CI routes generated outputs below the runner temporary directory and requires a clean non-ignored checkout afterward. Every expected job that checks out or evaluates repository content—quality, every native and oldstable leg, every build and reproducibility leg, and the final comparison job—records actual `HEAD`, `HEAD^{tree}`, Go version, and post-job porcelain status in a provenance artifact; build jobs also record SHA-256 and `go version -m` for each artifact. The comparison job also records workflow SHA/context and fails if any expected job provenance is absent, any tree OID differs, or any checkout is dirty. Reproducible-build jobs use independent build caches, disable VCS stamping, upload/download pinned artifacts, and compare bytes in a separate job.

`nightly.yml` runs active fuzzing, `-count=100` concurrency tests, arm64/amd64 native runners, and reproducible-build comparison.

Before accepting the workflows, extend docscheck with executable repository policy tests. `.github/workflows/ci.yml` and `nightly.yml` are required instead of treating a missing workflow directory as clean. Every checkout step must explicitly set `persist-credentials: false`; omission fails because checkout otherwise persists credentials by default. Workflows require top-level `permissions: contents: read`, every job requires a timeout, matrix jobs require `strategy.fail-fast: false`, and `continue-on-error` is forbidden. CI-specific fixture tests also prove the required native, oldstable, and four-target build jobs/matrices exist. A native fixture must fail if either OS leg omits format, vet, unit, explicit contract, race, native build, or role-smoke execution; merely naming a native matrix is insufficient. Preserve the Task 9 semantic YAML scoping so action inputs with similar names remain benign.

Strengthen durable-truth checks in the same TDD wave: Stage status must agree across `IMPLEMENTATION_PLAN.md`, `PROJECT_STATE.md`, and the active `docs/stages/*.md`; a matrix row marked Verified must link a verification record containing the same feature ID and an explicit passing result, not merely any existing file. Focused failing fixtures precede implementation. These checks enforce repository-document coherence, not chat-only or ignored-ledger progress.

- [ ] **Step 4: Add Dependabot**

Configure weekly updates for the root gomod, independent tools gomod, and GitHub Actions with at most five open pull requests per ecosystem.

```yaml
version: 2
updates:
  - package-ecosystem: gomod
    directory: /
    schedule:
      interval: weekly
    open-pull-requests-limit: 5
  - package-ecosystem: github-actions
    directory: /
    schedule:
      interval: weekly
    open-pull-requests-limit: 5
  - package-ecosystem: gomod
    directory: /tools
    schedule:
      interval: weekly
    open-pull-requests-limit: 5
```

Link the new testing and dependency-policy documents from `docs/README.md`, and update `docs/development/toolchain.md` so it no longer claims Make/CI entrypoints are absent.

- [ ] **Step 5: Verify locally**

Run this as one macOS-compatible `/bin/bash` 3.2 fail-closed block. Every generated output is external to the repository; do not run a generating target with default in-repository directories:

```bash
set -euo pipefail
ROOT="$(git rev-parse --show-toplevel)"
test "$(pwd -P)" = "$ROOT"
TMP_BASE="$(cd "${TMPDIR:-/tmp}" && pwd -P)"
case "$TMP_BASE" in "$ROOT"|"$ROOT"/*) exit 1 ;; esac
EXTERNAL_ROOT="$(mktemp -d "$TMP_BASE/amsftp-task10.XXXXXX")"
EXTERNAL_ROOT="$(cd "$EXTERNAL_ROOT" && pwd -P)"
case "$EXTERNAL_ROOT/" in "$ROOT"/*) exit 1 ;; esac

capture_candidate() (
  set -euo pipefail
  label="$1"
  common_objects="$(git rev-parse --path-format=absolute --git-common-dir)/objects"
  object_dir="$EXTERNAL_ROOT/$label.candidate-objects"
  mkdir "$object_dir"
  export GIT_INDEX_FILE="$EXTERNAL_ROOT/$label.candidate-index"
  export GIT_OBJECT_DIRECTORY="$object_dir"
  export GIT_ALTERNATE_OBJECT_DIRECTORIES="$common_objects"
  git read-tree HEAD
  git add -A -- .
  git write-tree >"$EXTERNAL_ROOT/$label.candidate-tree"
)

capture_ignored() (
  set -euo pipefail
  label="$1"
  roots_file="$EXTERNAL_ROOT/$label.ignored-roots"
  object_dir="$EXTERNAL_ROOT/$label.ignored-objects"
  : >"$roots_file"
  for output_path in .cache bin dist coverage; do
    if test -e "$output_path" || test -L "$output_path"; then
      printf '%s\n' "$output_path" >>"$roots_file"
    fi
  done
  common_objects="$(git rev-parse --path-format=absolute --git-common-dir)/objects"
  mkdir "$object_dir"
  export GIT_INDEX_FILE="$EXTERNAL_ROOT/$label.ignored-index"
  export GIT_OBJECT_DIRECTORY="$object_dir"
  export GIT_ALTERNATE_OBJECT_DIRECTORIES="$common_objects"
  git read-tree --empty
  while IFS= read -r output_path; do
    git add -f -- "$output_path"
  done <"$roots_file"
  git write-tree >"$EXTERNAL_ROOT/$label.ignored-tree"
  LC_ALL=C COPYFILE_DISABLE=1 /usr/bin/tar -cf \
    "$EXTERNAL_ROOT/$label.ignored.tar" -C "$ROOT" -T "$roots_file"
)

STAGED="$(git diff --cached --name-only)"
test -z "$STAGED"
capture_candidate pre
capture_ignored pre
BUILD_DIR="$EXTERNAL_ROOT/build outputs"
COVERAGE_DIR="$EXTERNAL_ROOT/coverage outputs"
GO126="$(GOTOOLCHAIN=go1.26.5 go env GOROOT)/bin/go"
MAKE_VERSION="$(/usr/bin/make --version)"
printf '%s\n' "$MAKE_VERSION"
printf '%s\n' "$MAKE_VERSION" | grep -F 'GNU Make 3.81' >/dev/null
GOTOOLCHAIN=local /usr/bin/make GO="$GO126" BUILD_DIR="$BUILD_DIR" COVERAGE_DIR="$COVERAGE_DIR" check
GOTOOLCHAIN=local /usr/bin/make GO="$GO126" BUILD_DIR="$BUILD_DIR" COVERAGE_DIR="$COVERAGE_DIR" lint
GOTOOLCHAIN=local /usr/bin/make GO="$GO126" BUILD_DIR="$BUILD_DIR" COVERAGE_DIR="$COVERAGE_DIR" supply-chain
GOTOOLCHAIN=local /usr/bin/make GO="$GO126" BUILD_DIR="$BUILD_DIR" COVERAGE_DIR="$COVERAGE_DIR" build-all
GOTOOLCHAIN=local /usr/bin/make GO="$GO126" BUILD_DIR="$BUILD_DIR" COVERAGE_DIR="$COVERAGE_DIR" ci
entry_count="$(find "$BUILD_DIR" -mindepth 1 -maxdepth 1 -print | wc -l | tr -d '[:space:]')"
test "$entry_count" -eq 4
while read -r name want_os want_arch; do
  artifact="$BUILD_DIR/$name"
  test -f "$artifact"
  test ! -L "$artifact"
  shasum -a 256 "$artifact"
  metadata="$("$GO126" version -m "$artifact")"
  printf '%s\n' "$metadata"
  printf '%s\n' "$metadata" | grep -F "GOOS=$want_os" >/dev/null
  printf '%s\n' "$metadata" | grep -F "GOARCH=$want_arch" >/dev/null
  printf '%s\n' "$metadata" | grep -F 'CGO_ENABLED=0' >/dev/null
  printf '%s\n' "$metadata" | grep -F -- '-trimpath=true' >/dev/null
done <<'ARTIFACTS'
amsftp-darwin-arm64 darwin arm64
amsftp-darwin-amd64 darwin amd64
amsftp-linux-arm64 linux arm64
amsftp-linux-amd64 linux amd64
ARTIFACTS
"$BUILD_DIR/amsftp-darwin-arm64" --help
"$BUILD_DIR/amsftp-darwin-arm64" --version

# Keep oldstable in this same validated shell and disable further switching.
GO125="$(GOTOOLCHAIN=go1.25.12 go env GOROOT)/bin/go"
GOTOOLCHAIN=local "$GO125" version
GOTOOLCHAIN=local /usr/bin/make GO="$GO125" \
  BUILD_DIR="$EXTERNAL_ROOT/oldstable build outputs" \
  COVERAGE_DIR="$EXTERNAL_ROOT/oldstable coverage outputs" check
capture_candidate post
capture_ignored post
cmp -s "$EXTERNAL_ROOT/pre.candidate-tree" "$EXTERNAL_ROOT/post.candidate-tree"
cmp -s "$EXTERNAL_ROOT/pre.ignored-tree" "$EXTERNAL_ROOT/post.ignored-tree"
cmp -s "$EXTERNAL_ROOT/pre.ignored.tar" "$EXTERNAL_ROOT/post.ignored.tar"
STAGED="$(git diff --cached --name-only)"
test -z "$STAGED"
```

Before and after the external-directory smoke, compare a complete non-ignored candidate tree plus two ignored-output representations for `.cache`, `bin`, `dist`, and `coverage`: a temporary-index Git tree captures files, modes, and symlinks (including broken symlinks via `-e || -L`), while a same-run uncompressed bsdtar archive also captures empty directories. Existing ignored output is allowed only as an unchanged baseline; do not delete it merely to satisfy this gate. Tar bytes are compared only pre/post on the same runner and bsdtar version, never across platforms. Expected locally: GNU Make 3.81 accepts the Makefile, all available checks pass, exactly the four named binaries exist below the external path (including spaces), hashes and metadata assertions pass, and the native macOS arm64 binary passes help/version role-boundary smoke. Cross-build success is not Linux-native evidence. If Docker/VM/remote CI is unavailable, record Linux-native tests and GitHub artifact/reproducibility jobs as unverified; Stage 0 remains In Progress until remote macOS/Linux CI is actually green.

- [ ] **Step 6: Record task checkpoint**

Record exact versions, commands, and diff check. Do not commit without user authorization.

---

### Task 11: Integrate, review, verify Stage 0, and update durable state

**Files:**

- Modify only as required by review findings across Stage 0-owned files.
- Create: `docs/verification/stage-00.md`
- Modify: `IMPLEMENTATION_PLAN.md`
- Modify: `PROJECT_STATE.md`
- Modify: `docs/product/feature-matrix.md`
- Modify: `docs/architecture/overview.md`
- Modify: `docs/stages/00-foundation.md`
- Modify: `docs/stages/06-hardening-release.md`
- Modify: `docs/README.md`
- Create: `docs/architecture/adr/0006-public-identity-toolchain-and-runtime-libraries.md`
- Create: `docs/architecture/adr/0007-platform-paths-runtime-socket-and-ipc.md`
- Create: `docs/architecture/adr/0008-modernc-sqlite-and-forward-migrations.md`
- Create: `docs/architecture/adr/0009-supported-platform-ci-and-packaging-baseline.md`
- Create: `docs/architecture/adr/0010-helper-artifact-trust-and-distribution.md`

**Interfaces:**

- Produces: a complete-worktree review and truthful Stage 0 evidence; a Stage 1 handoff only when every external gate is present.
- Consumes: Tasks 1–10 and the approved design's Stage 0 gated-decision list.

- [ ] **Step 0: Resolve every Stage 0 gated architecture decision**

Before freezing a closeout candidate, use current primary-source evidence and record Accepted ADRs for the public identity/module, exact Go/dependency versions, TUI/rendering and structured logging, platform config/state/cache/runtime paths, IPC endpoint details, SQLite driver/migrations, CI/provider/package identifiers, minimum macOS/Linux/OpenSSH support, and Helper signing/distribution. Each ADR must compare alternatives and state security/testability gates without claiming its owning stage is implemented.

The normative result is ADR-0006 through ADR-0010. Stage 0 remains standard-library-only; future modules are compatibility pins written into the owning stage's `go.mod`/`go.sum` intake, not unused Stage 0 dependencies. Synchronize the architecture overview, toolchain/dependency policy, Stage 0–4 handoffs, feature/evidence truth chain and exact GitHub Actions platform matrix. Run docscheck and an independent architecture review; any unresolved High/Medium finding returns to this step before candidate freeze.

- [ ] **Step 1: Prove prerequisites and freeze the complete candidate identity**

Do not start closeout until Tasks 1–10 have implementation reports, independent PASS reviews, and no unresolved Critical/Important finding. Append a correction to Task 9 evidence: its earlier “real repository” check targeted the clean main worktree, so rerun it with `pwd -P` equal to `git rev-parse --show-toplevel` in this implementation worktree.

Run every evidence block with macOS-compatible `/bin/bash` 3.2 using `set -euo pipefail`; restore and validate `ROOT`, `BASE`, and `EVIDENCE_TMP` in every new shell. Capture the candidate through a temporary index and object directory outside the repository. This produces a full tree OID and patch including tracked changes, untracked files, deletions, symlinks, and executable modes without touching the shared index or repository object store. A plain `git diff` is invalid for this worktree.

```bash
set -euo pipefail
ROOT="$(git rev-parse --show-toplevel)"
BASE=e14159cef930ffa513ebf6a9a5319f8f0fab1f9f
test "$(pwd -P)" = "$ROOT"
TMP_BASE="$(cd "${TMPDIR:-/tmp}" && pwd -P)"
case "$TMP_BASE" in "$ROOT"|"$ROOT"/*) exit 1 ;; esac
EVIDENCE_TMP="$(mktemp -d "$TMP_BASE/amsftp-task11.XXXXXX")"
EVIDENCE_TMP="$(cd "$EVIDENCE_TMP" && pwd -P)"
case "$EVIDENCE_TMP/" in "$ROOT"/*) exit 1 ;; esac
test "$(git rev-parse "$BASE^{commit}")" = "$BASE"
STAGED="$(git diff --cached --name-only)"
test -z "$STAGED"
git status --porcelain=v1 -uall >"$EVIDENCE_TMP/candidate.status"
git ls-files -z --others --exclude-standard >"$EVIDENCE_TMP/candidate.untracked.zlist"
COMMON_OBJECTS="$(git rev-parse --path-format=absolute --git-common-dir)/objects"
mkdir "$EVIDENCE_TMP/candidate.objects"
export GIT_INDEX_FILE="$EVIDENCE_TMP/candidate.index"
export GIT_OBJECT_DIRECTORY="$EVIDENCE_TMP/candidate.objects"
export GIT_ALTERNATE_OBJECT_DIRECTORIES="$COMMON_OBJECTS"
git read-tree "$BASE"
git add -A -- .
git diff --cached --check "$BASE" -- .
git diff --cached --binary "$BASE" -- . >"$EVIDENCE_TMP/candidate.full.patch"
git write-tree >"$EVIDENCE_TMP/candidate.tree"
git cat-file -e "$(<"$EVIDENCE_TMP/candidate.tree")^{tree}"
unset GIT_INDEX_FILE GIT_OBJECT_DIRECTORY GIT_ALTERNATE_OBJECT_DIRECTORIES
STAGED="$(git diff --cached --name-only)"
test -z "$STAGED"

# Capture ignored output in the same validated shell. Git covers files, modes,
# and symlinks; same-run bsdtar comparison additionally covers empty directories.
capture_ignored() (
  set -euo pipefail
  label="$1"
  roots_file="$EVIDENCE_TMP/$label.ignored-roots"
  object_dir="$EVIDENCE_TMP/$label.ignored-objects"
  : >"$roots_file"
  for output_path in .cache bin dist coverage; do
    if test -e "$output_path" || test -L "$output_path"; then
      printf '%s\n' "$output_path" >>"$roots_file"
    fi
  done
  common_objects="$(git rev-parse --path-format=absolute --git-common-dir)/objects"
  mkdir "$object_dir"
  export GIT_INDEX_FILE="$EVIDENCE_TMP/$label.ignored-index"
  export GIT_OBJECT_DIRECTORY="$object_dir"
  export GIT_ALTERNATE_OBJECT_DIRECTORIES="$common_objects"
  git read-tree --empty
  while IFS= read -r output_path; do
    git add -f -- "$output_path"
  done <"$roots_file"
  git write-tree >"$EVIDENCE_TMP/$label.ignored-tree"
  LC_ALL=C COPYFILE_DISABLE=1 /usr/bin/tar -cf \
    "$EVIDENCE_TMP/$label.ignored.tar" -C "$ROOT" -T "$roots_file"
  shasum -a 256 "$EVIDENCE_TMP/$label.ignored.tar" \
    >"$EVIDENCE_TMP/$label.ignored.tar.sha256"
)
capture_ignored candidate
STAGED="$(git diff --cached --name-only)"
test -z "$STAGED"
```

Exclude `.superpowers` because it is coordination-only and copied durable verdicts are reviewed separately. Record branch/base/HEAD, full status, complete patch/tree OID, NUL-delimited untracked inventory, and both ignored-output tree OID and tar hash. Existing ignored output is an allowed but immutable same-run baseline; never delete it just to make the gate pass. Never use `git add -N`, the shared index, or the repository object directory. Any review fix or durable-state edit invalidates the candidate; recapture and re-review it before gates.

- [ ] **Step 2: Reindex, inspect architecture, and review the complete worktree**

Reindex codebase-memory for this worktree, then verify package/import direction:

```text
cmd -> app/buildinfo
app -> domain/config/ipc/provider
ipc -> domain
provider -> domain
fake -> provider/domain/foundation
docscheck -> standard library
domain -> standard library
```

No import cycle and no transport dependency inside domain. If graph transport is unavailable after the repository retry limit, record the exact failure and use compiler/import inspection instead of claiming graph evidence.

Every task must have spec-compliance and code-quality verdicts plus fix-and-re-review for every Critical/Important finding. The whole-worktree reviewer receives `candidate.full.patch`, the candidate tree OID, and the NUL-delimited untracked inventory; reviewing only `git diff e14159c` is invalid. Mandatory lenses are security, cancellation, untrusted lengths, invalid UTF-8 paths, context propagation, lock order, goroutine lifetime, races, dependency direction, and false completion claims.

- [ ] **Step 3: Run a pollution-safe complete local gate**

```bash
set -euo pipefail
: "${ROOT:?restore ROOT from the accepted candidate capture}"
: "${BASE:?restore BASE from the accepted candidate capture}"
: "${EVIDENCE_TMP:?restore EVIDENCE_TMP from the accepted candidate capture}"
ACTUAL_ROOT="$(git rev-parse --show-toplevel)"
test "$(pwd -P)" = "$ROOT"
test "$ACTUAL_ROOT" = "$ROOT"
test "$(git rev-parse "$BASE^{commit}")" = "$BASE"
EVIDENCE_TMP="$(cd "$EVIDENCE_TMP" && pwd -P)"
case "$EVIDENCE_TMP/" in "$ROOT"/*) exit 1 ;; esac
COMMON_OBJECTS="$(git rev-parse --path-format=absolute --git-common-dir)/objects"
GOTOOLCHAIN=go1.26.5 go mod tidy -diff
GOTOOLCHAIN=go1.26.5 /usr/bin/make \
  BUILD_DIR="$EVIDENCE_TMP/build outputs" \
  COVERAGE_DIR="$EVIDENCE_TMP/coverage outputs" ci
GOTOOLCHAIN=go1.26.5 go test -race -count=10 ./...
GOTOOLCHAIN=go1.26.5 go run ./internal/tools/docscheck "$ROOT"
GOFMT="$(GOTOOLCHAIN=go1.26.5 go env GOROOT)/bin/gofmt"
UNFORMATTED="$(find cmd internal -type f -name '*.go' -print0 | xargs -0 "$GOFMT" -l)"
test -z "$UNFORMATTED"
export GIT_INDEX_FILE="$EVIDENCE_TMP/candidate.index"
export GIT_OBJECT_DIRECTORY="$EVIDENCE_TMP/candidate.objects"
export GIT_ALTERNATE_OBJECT_DIRECTORIES="$COMMON_OBJECTS"
git diff --cached --check "$BASE" -- .
unset GIT_INDEX_FILE GIT_OBJECT_DIRECTORY GIT_ALTERNATE_OBJECT_DIRECTORIES
```

Expected: every command exits zero, the pinned `gofmt -l` result is empty, no required test is skipped, and output contains no warnings hidden as success. Hash all four exact binaries and record `go version -m`. Recapture status and candidate tree into distinct `post.*` temporary indexes/object directories using the Step 1 procedure; redeclare the exact `capture_ignored` function in that validated shell and run `capture_ignored post`. Require `cmp -s candidate.status post.status`, `cmp -s candidate.tree post.tree`, `cmp -s candidate.ignored-tree post.ignored-tree`, and `cmp -s candidate.ignored.tar post.ignored.tar`. The tar comparison is same-run/same-runner only. Generated outputs must exist only below the external BUILD_DIR/COVERAGE_DIR. Any mismatch fails closed; do not overwrite the pre-gate files before comparison.

- [ ] **Step 4: Acquire exact-tree platform evidence**

Required before Stage 0 is marked complete:

```text
ubuntu-24.04 / Go 1.26.5: native build + fmt + vet + unit + contract + race + role smoke
ubuntu-22.04 / Go 1.26.5: native build + fmt + vet + unit + contract + race + role smoke
macos-15 / Go 1.26.5: native build + fmt + vet + unit + contract + race + role smoke
macos-15-intel / Go 1.26.5: native build + fmt + vet + unit + contract + race + role smoke
ubuntu-24.04 / Go 1.25.12: compatibility test
ubuntu-22.04 / Go 1.25.12: compatibility test
macos-15 / Go 1.25.12: compatibility test
macos-15-intel / Go 1.25.12: compatibility test
darwin/arm64, darwin/amd64, linux/arm64, linux/amd64: build
independent reproducible-build comparison and artifact hashes
```

External CI can verify only a committed/ref-addressable tree and therefore requires explicit commit/push or equivalent authorization. After an authorized implementation-candidate commit and before any push or CI dispatch, mechanically bind it to the reviewed candidate:

```bash
set -euo pipefail
: "${ROOT:?restore ROOT from the accepted candidate capture}"
: "${BASE:?restore BASE from the accepted candidate capture}"
: "${EVIDENCE_TMP:?restore EVIDENCE_TMP from the accepted candidate capture}"
ACTUAL_ROOT="$(git rev-parse --show-toplevel)"
test "$(pwd -P)" = "$ROOT"
test "$ACTUAL_ROOT" = "$ROOT"
test "$(git rev-parse "$BASE^{commit}")" = "$BASE"
EVIDENCE_TMP="$(cd "$EVIDENCE_TMP" && pwd -P)"
case "$EVIDENCE_TMP/" in "$ROOT"/*) exit 1 ;; esac
EXPECTED_TREE="$(<"$EVIDENCE_TMP/candidate.tree")"
ACTUAL_COMMIT="$(git rev-parse HEAD)"
ACTUAL_TREE="$(git rev-parse 'HEAD^{tree}')"
COMMIT_STATUS="$(git status --porcelain=v1 -uall)"
test "$ACTUAL_TREE" = "$EXPECTED_TREE"
test -z "$COMMIT_STATUS"
```

Record `ACTUAL_COMMIT` and `ACTUAL_TREE` as the only permitted implementation-candidate CI identity. The implementation candidate run records workflow revision, actual commit and `HEAD^{tree}` from every expected job, clean status, job URL/result, and artifact URL/hash; all job tree OIDs must equal this reviewed tree and missing expected provenance fails the comparison job. After that run is green, the closeout delta is restricted mechanically to `docs/verification/stage-00.md`, the Stage 0 matrix/spec/plan/state/architecture/README status files, and ignored coordination reports—no Go, Make, workflow, dependency, or tool file may change. The closeout document records the implementation-candidate run links. CI runs again on the evidence-only revision; its final attestation is the external check attached to that commit/tree OID and is reported in the handoff, not a future URL that must self-appear inside the tree it verifies. Withhold the Complete claim until that final check is green. Any non-whitelisted change restarts implementation-candidate review and CI.

If no authorized remote/CI mechanism exists, leave Stage 0 `In Progress`, keep Stage 1 closed, and record the missing native Ubuntu 22.04/24.04, exact macos-15 ARM/Intel, oldstable, reproducibility, and run-link evidence. Cross-build or a local container does not replace native platform/race evidence. Linux arm64 remains a Stage 6 formal-support gate requiring an authorized public hosted label or equivalent controlled native runner; a cross-build alone cannot verify it.

- [ ] **Step 5: Update durable evidence and status truthfully**

`docs/verification/stage-00.md` records:

```text
repository root, branch/base/HEAD, complete temporary-index patch/tree OID, untracked inventory, and ignored-output Git-tree/tar baselines
OS and architecture
Go stable and oldstable versions
all commands and exit statuses
implementation-candidate CI workflow revision/run/job/artifact links when available; final closeout CI is located by the external checks attached to the final commit/tree
fuzz duration
race results
four build artifact hashes and `go version -m`
known missing evidence
CORE-001 through CORE-008 evidence mapping
all task review verdicts and superseded-evidence corrections
```

Create the verification record as `In Progress` first. Update each matrix row independently to In Progress, Implemented, or Verified from its actual evidence, then update Stage 0 spec/plan, architecture, and `PROJECT_STATE.md` last. Only rows with complete evidence may become Verified. If native CI evidence is absent, do not advance `PROJECT_STATE.md` to Stage 1. Add the active verification record to `docs/README.md` reading order and correct Stage 6's invalid matrix status word `Complete` to the defined vocabulary.

Use this scope statement verbatim in Project State, verification, and architecture:

> Stage 0 establishes and verifies foundation contracts and engineering gates only. It does not provide a usable TUI, daemon service, SSH/SFTP connection, SQLite persistence, transfer engine, or remote helper, and it is not production-ready. Production/release readiness is assessed only by the Stage 6 hardening and 1.0 release gates.

- [ ] **Step 6: Cold-start continuation audit**

Dispatch a fresh read-only Agent with only this prompt: “Open `docs/README.md` in the supplied repository root and follow its documented new-session reading order. Do not use `.superpowers`, chat history, Git history, or external network. Do not modify files. Report the current stage and reason, exact last-green commands and evidence path, unresolved evidence, immediate next action, conditional Stage 1 first action, frozen IPC/Provider rules, and whether the product is production-ready.”

PASS requires file/line evidence. If CI is missing, it must say Stage 0 In Progress, the immediate action is exact-tree CI, and Stage 1 is conditional. If all gates are present, it must identify Stage 1 M1.1. It must recover protocol 1.0, uint32 big-endian/8 MiB/base64-path framing, epoch+sequence cursors, stable error/retry/effect, read-only Provider plus optional mutable facet, bounded/bound cursors, and session/generation/complete capability identity. It must explicitly say Stage 0 is not a usable product. Fix gaps, rerun docscheck, then dispatch a second fresh Agent for re-audit.

- [ ] **Step 7: Apply the conditional transition**

Only with every local and external gate green may Stage 0 become Complete, eligible CORE rows become Verified, and `PROJECT_STATE.md` advance to Stage 1. Otherwise the durable outcome remains Stage 0 In Progress with the exact missing evidence and authorized next action. No commit, push, PR, CI dispatch, merge, or release is permitted without explicit user authorization.

- [ ] **Step 8: Final verification and checkpoint**

Run:

```bash
set -euo pipefail
: "${ROOT:?}"
: "${BASE:?}"
: "${EVIDENCE_TMP:?}"
ACTUAL_ROOT="$(git rev-parse --show-toplevel)"
test "$(pwd -P)" = "$ROOT"
test "$ACTUAL_ROOT" = "$ROOT"
test "$(git rev-parse "$BASE^{commit}")" = "$BASE"
EVIDENCE_TMP="$(cd "$EVIDENCE_TMP" && pwd -P)"
case "$EVIDENCE_TMP/" in "$ROOT"/*) exit 1 ;; esac
GOTOOLCHAIN=go1.26.5 go mod tidy -diff
GOTOOLCHAIN=go1.26.5 /usr/bin/make \
  BUILD_DIR="$EVIDENCE_TMP/final build outputs" \
  COVERAGE_DIR="$EVIDENCE_TMP/final coverage outputs" ci
GOTOOLCHAIN=go1.26.5 go run ./internal/tools/docscheck "$ROOT"
git status --porcelain=v1 -uall
STAGED="$(git diff --cached --name-only)"
test -z "$STAGED"
```

Freeze a fresh pre-gate candidate plus ignored Git-tree/tar baseline after the last deliberate document edit. After the commands, recapture into distinct post-gate files and compare status, candidate tree, ignored tree, and same-run tar bytes with `cmp -s`. Existing ignored output must remain byte/metadata-identical, not be deleted. If claiming Complete, also require the external final-closeout check attached to the exact final commit/tree to be green. Report implementation status, exact tests, missing platform evidence, staged/untracked/ignored state, and why the current milestone is or is not production-ready. Do not create a commit or merge without explicit user authorization.

---

## Plan self-review checklist

- [ ] CORE-001 through CORE-008 each map to at least one task and an evidence item.
- [ ] Exact type names are consistent across domain, IPC, Provider, Fake, and tests.
- [ ] No Stage 1 SSH/TUI work appears in any task.
- [ ] Every production-code task starts with a failing test and records RED and GREEN commands.
- [ ] Parallel tasks own disjoint files.
- [ ] All workflow actions use immutable full SHAs.
- [ ] The final stage state remains truthful when native CI evidence is unavailable.
- [ ] No unresolved marker text or ambiguous “handle errors later” step remains.
