//go:build darwin || linux

// Package externalpreviewer selects and runs configured external previewers.
// It passes a cache materialization to a frozen direct-exec command and keeps
// only bounded, redacted status diagnostics in memory.
package externalpreviewer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/externalprocess"
)

const (
	// MaxDiagnosticBytes is the maximum retained stderr diagnostic per run.
	MaxDiagnosticBytes = 4096
	maxRuleNameBytes   = 128
	maxRedactions      = 64
)

// Match is a structured set of media-type and extension alternatives. A rule
// matches when any configured condition matches. Media types may use a subtype
// wildcard such as image/*; extensions include their leading dot.
type Match struct {
	MediaTypes []string
	Extensions []string
}

// Rule is one ordered external previewer rule. Command must already have been
// resolved by externalprocess so its absolute executable identity is frozen.
type Rule struct {
	Name            string
	Match           Match
	Command         externalprocess.ResolvedCommand
	Timeout         time.Duration
	MaxInputBytes   int64
	RequireComplete bool
	Redact          []string
}

// Request describes a cache materialization considered for external preview.
// Path is the logical source path used only for matching. The process receives
// the canonical MaterializationPath as its final argument.
type Request struct {
	Path                string
	MediaType           string
	Complete            bool
	MaterializationPath string
}

// Status describes an isolated external preview attempt.
type Status string

const (
	StatusNoMatch     Status = "no_match"
	StatusRejected    Status = "rejected"
	StatusStartFailed Status = "start_failed"
	StatusSucceeded   Status = "succeeded"
	StatusNonZero     Status = "nonzero"
	StatusSignaled    Status = "signaled"
	StatusTimedOut    Status = "timed_out"
	StatusCanceled    Status = "canceled"
)

// Code is a bounded, non-sensitive reason suitable for application fallback.
type Code string

const (
	CodeNoMatch                Code = "no_match"
	CodeInvalidRequest         Code = "invalid_request"
	CodeIncompleteInput        Code = "incomplete_input"
	CodeInputTooLarge          Code = "input_too_large"
	CodeInvalidMaterialization Code = "invalid_materialization"
	CodeExecutableChanged      Code = "executable_changed"
	CodeIdentityChanged        Code = "identity_changed"
	CodeStartFailed            Code = "start_failed"
	CodeNonZero                Code = "nonzero"
	CodeSignaled               Code = "signaled"
	CodeTimeout                Code = "timeout"
	CodeCanceled               Code = "canceled"
)

// Result contains status metadata only. Diagnostic is a bounded, redacted,
// single-line stderr excerpt for immediate display and must not be persisted.
// Stdout and materialized file content are always discarded.
type Result struct {
	Matched    bool
	Rule       string
	Status     Status
	Code       Code
	Executable string
	ExitCode   int
	Signal     string
	Duration   time.Duration
	Diagnostic string
}

type frozenRule struct {
	name            string
	match           Match
	command         externalprocess.ResolvedCommand
	timeout         time.Duration
	maxInputBytes   int64
	requireComplete bool
	redact          []string
}

// Runner is an immutable, concurrency-safe ordered external previewer set.
type Runner struct {
	rules       []frozenRule
	environment []string
}

// New validates and freezes ordered rules. environment is scrubbed using the
// shared external-process allowlist before it can reach a child process.
func New(rules []Rule, environment []string) (*Runner, error) {
	if len(rules) == 0 {
		return nil, errors.New("create external previewer: no rules configured")
	}
	frozen := make([]frozenRule, 0, len(rules))
	names := make(map[string]struct{}, len(rules))
	for index, rule := range rules {
		validated, err := freezeRule(rule)
		if err != nil {
			return nil, fmt.Errorf("create external previewer rule %d: %w", index, err)
		}
		if _, duplicate := names[validated.name]; duplicate {
			return nil, fmt.Errorf("create external previewer rule %d: duplicate name", index)
		}
		names[validated.name] = struct{}{}
		frozen = append(frozen, validated)
	}
	return &Runner{
		rules:       frozen,
		environment: externalprocess.ScrubEnvironment(environment),
	}, nil
}

func freezeRule(rule Rule) (frozenRule, error) {
	if rule.Name == "" || len(rule.Name) > maxRuleNameBytes || !utf8.ValidString(rule.Name) || containsUnsafeText(rule.Name) {
		return frozenRule{}, errors.New("name must be non-empty bounded UTF-8 without control bytes")
	}
	match, err := normalizeMatch(rule.Match)
	if err != nil {
		return frozenRule{}, err
	}
	if rule.Timeout <= 0 {
		return frozenRule{}, errors.New("timeout must be positive")
	}
	if rule.MaxInputBytes <= 0 {
		return frozenRule{}, errors.New("maximum input bytes must be positive")
	}
	command := rule.Command
	command.Args = append([]string(nil), rule.Command.Args...)
	if err := command.Revalidate(); err != nil {
		return frozenRule{}, fmt.Errorf("command is not a frozen executable: %w", err)
	}
	if len(rule.Redact) > maxRedactions {
		return frozenRule{}, fmt.Errorf("redaction count exceeds %d", maxRedactions)
	}
	redactions := make([]string, 0, len(rule.Redact))
	for _, value := range rule.Redact {
		if value == "" {
			continue
		}
		if len(value) > externalprocess.MaxArgumentBytes {
			return frozenRule{}, fmt.Errorf("redaction exceeds %d bytes", externalprocess.MaxArgumentBytes)
		}
		redactions = append(redactions, strings.Clone(value))
	}
	return frozenRule{
		name:            strings.Clone(rule.Name),
		match:           match,
		command:         command,
		timeout:         rule.Timeout,
		maxInputBytes:   rule.MaxInputBytes,
		requireComplete: rule.RequireComplete,
		redact:          redactions,
	}, nil
}

func normalizeMatch(input Match) (Match, error) {
	if len(input.MediaTypes) == 0 && len(input.Extensions) == 0 {
		return Match{}, errors.New("match conditions are empty")
	}
	result := Match{
		MediaTypes: make([]string, 0, len(input.MediaTypes)),
		Extensions: make([]string, 0, len(input.Extensions)),
	}
	for _, value := range input.MediaTypes {
		value = strings.ToLower(strings.TrimSpace(value))
		if !validMediaPattern(value) {
			return Match{}, fmt.Errorf("invalid media type pattern %q", value)
		}
		result.MediaTypes = append(result.MediaTypes, value)
	}
	for _, value := range input.Extensions {
		value = strings.ToLower(strings.TrimSpace(value))
		if len(value) < 2 || value[0] != '.' || strings.ContainsAny(value, `/\\`) || containsUnsafeText(value) {
			return Match{}, fmt.Errorf("invalid extension %q", value)
		}
		result.Extensions = append(result.Extensions, value)
	}
	return result, nil
}

func validMediaPattern(value string) bool {
	if strings.HasSuffix(value, "/*") && strings.Count(value, "*") == 1 {
		major := strings.TrimSuffix(value, "/*")
		parsed, _, err := mime.ParseMediaType(major + "/plain")
		return err == nil && parsed == major+"/plain"
	}
	if strings.Contains(value, "*") {
		return false
	}
	parsed, _, err := mime.ParseMediaType(value)
	return err == nil && parsed == value
}

func containsUnsafeText(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) || character == '\u2028' || character == '\u2029' {
			return true
		}
	}
	return false
}

// Run selects the first matching rule and executes it in isolation. All
// failures are represented by Result so callers can fall back to built-in
// preview without affecting an endpoint or background job.
func (runner *Runner) Run(ctx context.Context, request Request) Result {
	result := Result{Status: StatusNoMatch, Code: CodeNoMatch, ExitCode: -1}
	if runner == nil || ctx == nil {
		result.Status = StatusRejected
		result.Code = CodeInvalidRequest
		return result
	}
	rule, ok := runner.match(request)
	if !ok {
		return result
	}
	result.Matched = true
	result.Rule = rule.name
	result.Executable = rule.command.Executable
	if err := ctx.Err(); err != nil {
		result.Status = StatusCanceled
		result.Code = CodeCanceled
		return result
	}
	if rule.requireComplete && !request.Complete {
		result.Status = StatusRejected
		result.Code = CodeIncompleteInput
		return result
	}
	if err := rule.command.Revalidate(); err != nil {
		result.Status = StatusRejected
		result.Code = CodeExecutableChanged
		return result
	}
	plan, err := externalprocess.NewPlan(rule.command, request.MaterializationPath, runner.environment)
	if err != nil {
		result.Status = StatusRejected
		result.Code = CodeInvalidMaterialization
		return result
	}
	materialization := plan.Args[len(plan.Args)-1]
	info, err := os.Stat(materialization)
	if err != nil || !info.Mode().IsRegular() {
		result.Status = StatusRejected
		result.Code = CodeInvalidMaterialization
		return result
	}
	if info.Size() > rule.maxInputBytes {
		result.Status = StatusRejected
		result.Code = CodeInputTooLarge
		return result
	}

	runCtx, cancel := context.WithTimeout(ctx, rule.timeout)
	defer cancel()
	command, err := plan.CommandContext(runCtx)
	if err != nil {
		result.Status = StatusRejected
		result.Code = CodeIdentityChanged
		return result
	}
	configureProcessGroup(command)
	command.Stdout = io.Discard
	diagnostic := &diagnosticBuffer{}
	command.Stderr = diagnostic
	redactions := append([]string(nil), rule.redact...)
	redactions = append(redactions, materialization)

	started := time.Now()
	if err := command.Start(); err != nil {
		result.Status = StatusStartFailed
		result.Code = CodeStartFailed
		result.Duration = time.Since(started)
		result.Diagnostic = diagnostic.String(redactions)
		return result
	}
	waitErr := command.Wait()
	result.Duration = time.Since(started)
	result.Diagnostic = diagnostic.String(redactions)
	if ctx.Err() != nil {
		result.Status = StatusCanceled
		result.Code = CodeCanceled
		return result
	}
	if runCtx.Err() != nil {
		result.Status = StatusTimedOut
		result.Code = CodeTimeout
		return result
	}
	if waitErr == nil {
		result.Status = StatusSucceeded
		result.Code = ""
		result.ExitCode = 0
		return result
	}
	var exitError *exec.ExitError
	if !errors.As(waitErr, &exitError) {
		result.Status = StatusNonZero
		result.Code = CodeNonZero
		return result
	}
	result.ExitCode = exitError.ExitCode()
	if status, ok := exitError.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		result.Status = StatusSignaled
		result.Code = CodeSignaled
		result.Signal = status.Signal().String()
		return result
	}
	result.Status = StatusNonZero
	result.Code = CodeNonZero
	return result
}

func (runner *Runner) match(request Request) (frozenRule, bool) {
	mediaType := ""
	if request.MediaType != "" {
		if parsed, _, err := mime.ParseMediaType(request.MediaType); err == nil {
			mediaType = strings.ToLower(parsed)
		}
	}
	extension := strings.ToLower(filepath.Ext(request.Path))
	for _, rule := range runner.rules {
		if matchMediaType(rule.match.MediaTypes, mediaType) || matchExtension(rule.match.Extensions, extension) {
			return rule, true
		}
	}
	return frozenRule{}, false
}

func matchMediaType(patterns []string, mediaType string) bool {
	if mediaType == "" {
		return false
	}
	for _, pattern := range patterns {
		if pattern == mediaType || strings.HasSuffix(pattern, "/*") && strings.HasPrefix(mediaType, strings.TrimSuffix(pattern, "*")) {
			return true
		}
	}
	return false
}

func matchExtension(extensions []string, extension string) bool {
	for _, candidate := range extensions {
		if candidate == extension {
			return true
		}
	}
	return false
}

type diagnosticBuffer struct {
	mu        sync.Mutex
	data      []byte
	discarded int
}

func (buffer *diagnosticBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	available := MaxDiagnosticBytes - len(buffer.data)
	retained := min(max(available, 0), len(value))
	buffer.data = append(buffer.data, value[:retained]...)
	discarded := len(value) - retained
	if discarded > math.MaxInt-buffer.discarded {
		buffer.discarded = math.MaxInt
	} else {
		buffer.discarded += discarded
	}
	return len(value), nil
}

func (buffer *diagnosticBuffer) String(redactions []string) string {
	buffer.mu.Lock()
	value := append([]byte(nil), buffer.data...)
	discarded := buffer.discarded
	buffer.mu.Unlock()
	cleaned := bytes.Map(func(character rune) rune {
		if unicode.IsControl(character) || character == '\u2028' || character == '\u2029' {
			return ' '
		}
		return character
	}, value)
	text := strings.TrimSpace(strings.ToValidUTF8(string(cleaned), "�"))
	for _, sensitive := range redactions {
		if sensitive != "" {
			text = strings.ReplaceAll(text, sensitive, "[redacted]")
		}
	}
	suffix := ""
	if discarded > 0 {
		suffix = fmt.Sprintf(" [stderr truncated; %d bytes discarded]", discarded)
	}
	text = truncateUTF8(text, max(MaxDiagnosticBytes-len(suffix), 0))
	return text + suffix
}

func truncateUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
