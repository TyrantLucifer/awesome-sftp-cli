package helper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const (
	MaxHelperLineBytes    = 1 << 20
	maxOperationDepth     = 256
	maxOperationEntries   = 10_000_000
	maxOperationFileBytes = 64 << 20
	maxOperationReadBytes = 512 << 20
)

type OperationSearchBudget struct {
	MaxDepth       uint32 `json:"max_depth"`
	MaxEntries     uint64 `json:"max_entries"`
	MaxResults     uint64 `json:"max_results"`
	MaxOutputBytes uint64 `json:"max_output_bytes"`
	MaxDurationMS  uint64 `json:"max_duration_ms"`
	PageEntries    uint32 `json:"page_entries"`
}

type FilenameTypes struct {
	Files       bool `json:"files"`
	Directories bool `json:"directories"`
	Symlinks    bool `json:"symlinks"`
}

type FilenameSearchRequest struct {
	Scope         string                `json:"scope"`
	Pattern       string                `json:"pattern"`
	Match         string                `json:"match"`
	CaseSensitive bool                  `json:"case_sensitive"`
	IncludeHidden bool                  `json:"include_hidden"`
	Ignore        string                `json:"ignore"`
	Types         FilenameTypes         `json:"types"`
	Budget        OperationSearchBudget `json:"budget"`
}

type FilenameSearchResult struct {
	RelativePath   string `json:"relative_path"`
	Kind           string `json:"kind"`
	Size           uint64 `json:"size,omitempty"`
	Mode           uint32 `json:"mode"`
	ModifiedUnixNS int64  `json:"modified_unix_ns"`
}

type OperationProblem struct {
	RelativePath string `json:"relative_path"`
	Reason       string `json:"reason"`
}

type ContentOperationBudget struct {
	OperationSearchBudget
	MaxFiles          uint64 `json:"max_files"`
	MaxMatchesPerFile uint64 `json:"max_matches_per_file"`
	MaxFileBytes      uint64 `json:"max_file_bytes"`
	MaxReadBytes      uint64 `json:"max_read_bytes"`
	MaxSnippetBytes   uint32 `json:"max_snippet_bytes"`
}

type ContentSearchRequest struct {
	Scope            string                 `json:"scope"`
	Pattern          string                 `json:"pattern"`
	PatternType      string                 `json:"pattern_type"`
	CaseSensitive    bool                   `json:"case_sensitive"`
	IncludeHidden    bool                   `json:"include_hidden"`
	FileNameContains string                 `json:"file_name_contains"`
	BinaryPolicy     string                 `json:"binary_policy"`
	ContextLines     uint32                 `json:"context_lines"`
	Budget           ContentOperationBudget `json:"budget"`
}

type ContentSearchResult struct {
	RelativePath string `json:"relative_path"`
	Line         uint64 `json:"line"`
	Column       uint64 `json:"column"`
	Offset       uint64 `json:"offset"`
	Snippet      string `json:"snippet"`
}

type StrongHashRequest struct {
	Path      string `json:"path"`
	Algorithm string `json:"algorithm"`
	MaxBytes  uint64 `json:"max_bytes"`
}

type FileFingerprint struct {
	Size           uint64 `json:"size"`
	Mode           uint32 `json:"mode"`
	ModifiedUnixNS int64  `json:"modified_unix_ns"`
	FileID         string `json:"file_id,omitempty"`
}

type StrongHashResult struct {
	Algorithm        string          `json:"algorithm"`
	Digest           string          `json:"digest,omitempty"`
	Fingerprint      FileFingerprint `json:"fingerprint"`
	ComputedAtUnixNS int64           `json:"computed_at_unix_ns"`
	Valid            bool            `json:"valid"`
	InvalidReason    string          `json:"invalid_reason,omitempty"`
}

type DiskStatsRequest struct {
	Path string `json:"path"`
}

type DiskStatsResult struct {
	TotalBytes     uint64 `json:"total_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
	QuotaKnown     bool   `json:"quota_known"`
	Source         string `json:"source"`
}

type LocalOperations struct {
	afterHashChunk func()
	afterTailPoll  func()
	afterWatchPoll func()
	walk           localWalkFunc
}

type localVisitFunc func(string, string, os.DirEntry, os.FileInfo, uint32) error
type localWalkFunc func(context.Context, string, OperationSearchBudget, bool, string, localVisitFunc) error

func NewLocalOperations() *LocalOperations { return &LocalOperations{walk: walkLocal} }

func NewLocalServiceConfig(version Version) ServiceConfig {
	operations := NewLocalOperations()
	capabilities := make([]Capability, 0, len(capabilityOrder))
	for _, name := range capabilityOrder {
		capabilities = append(capabilities, Capability{Name: name, Version: 1})
	}
	return ServiceConfig{
		Server: ServerConfig{
			Protocol: 1, HelperVersion: version, MinimumClient: Version{Major: 4},
			MaximumFrame: MaxHelperFrameBytes, MaximumConcurrent: MaxHelperConcurrent,
			Capabilities: capabilities,
		},
		MaximumRequestDuration: MaxHelperRequestDuration,
		Handlers:               operations.Handlers(),
	}
}

func (o *LocalOperations) Handlers() map[CapabilityName]RequestHandler {
	return map[CapabilityName]RequestHandler{
		CapabilityFilenameSearch: o.FilenameSearch,
		CapabilityContentSearch:  o.ContentSearch,
		CapabilityStrongHash:     o.StrongHash,
		CapabilityDiskStats:      o.DiskStats,
		CapabilityTail:           o.Tail,
		CapabilityWatch:          o.Watch,
		CapabilitySameHostCopy:   o.SameHostCopy,
	}
}

type operationState struct {
	requestBudget OperationSearchBudget
	emit          EmitFunc
	entries       uint64
	results       uint64
	outputBytes   uint64
	partialReason string
}

func (o *LocalOperations) FilenameSearch(parent context.Context, body json.RawMessage, emit EmitFunc) (Completion, error) {
	var request FilenameSearchRequest
	if err := decodeStrictPayload(body, &request); err != nil {
		return Completion{}, err
	}
	if err := validateFilenameRequest(request); err != nil {
		return Completion{}, err
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(request.Budget.MaxDurationMS)*time.Millisecond) // #nosec G115 -- validation caps duration at 30 seconds.
	defer cancel()
	state := &operationState{requestBudget: request.Budget, emit: emit}
	err := o.walk(ctx, request.Scope, request.Budget, request.IncludeHidden, request.Ignore, func(fullPath, relative string, entry os.DirEntry, info os.FileInfo, depth uint32) error {
		state.entries++
		if state.entries > request.Budget.MaxEntries {
			return stopOperation("entry_limit")
		}
		kind := localKind(entry)
		selected := kind == "file" && request.Types.Files || kind == "directory" && request.Types.Directories || kind == "symlink" && request.Types.Symlinks
		candidate := entry.Name()
		if request.Match == "relative_path" {
			candidate = relative
		}
		if selected && containsPattern(candidate, request.Pattern, request.CaseSensitive) {
			result := FilenameSearchResult{RelativePath: filepath.ToSlash(relative), Kind: kind, Mode: uint32(info.Mode().Perm()), ModifiedUnixNS: info.ModTime().UnixNano()}
			if info.Size() > 0 {
				result.Size = uint64(info.Size()) // #nosec G115 -- guarded positive immediately above.
			}
			if err := state.emitResult(result); err != nil {
				return err
			}
		}
		return nil
	})
	return state.completion(ctx, err), nil
}

func validateFilenameRequest(request FilenameSearchRequest) error {
	if err := validateOperationPath(request.Scope); err != nil {
		return err
	}
	if request.Pattern == "" || len(request.Pattern) > MaxHelperStringBytes || !utf8.ValidString(request.Pattern) || strings.IndexByte(request.Pattern, 0) >= 0 {
		return errors.New("helper filename search: pattern is invalid")
	}
	if request.Match != "name" && request.Match != "relative_path" || request.Ignore != "" && request.Ignore != "none" && request.Ignore != "default" {
		return errors.New("helper filename search: options are invalid")
	}
	if !request.Types.Files && !request.Types.Directories && !request.Types.Symlinks {
		return errors.New("helper filename search: at least one type is required")
	}
	return validateOperationBudget(request.Budget)
}

func (o *LocalOperations) ContentSearch(parent context.Context, body json.RawMessage, emit EmitFunc) (Completion, error) {
	var request ContentSearchRequest
	if err := decodeStrictPayload(body, &request); err != nil {
		return Completion{}, err
	}
	matcher, err := validateContentOperationRequest(request)
	if err != nil {
		return Completion{}, err
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(request.Budget.MaxDurationMS)*time.Millisecond) // #nosec G115 -- validation caps duration at 30 seconds.
	defer cancel()
	state := &operationState{requestBudget: request.Budget.OperationSearchBudget, emit: emit}
	var files uint64
	var readBytes uint64
	err = o.walk(ctx, request.Scope, request.Budget.OperationSearchBudget, request.IncludeHidden, "none", func(fullPath, relative string, entry os.DirEntry, info os.FileInfo, depth uint32) error {
		state.entries++
		if state.entries > request.Budget.MaxEntries {
			return stopOperation("entry_limit")
		}
		if entry.Type()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil
		}
		if request.FileNameContains != "" && !strings.Contains(strings.ToLower(entry.Name()), strings.ToLower(request.FileNameContains)) {
			return nil
		}
		if files >= request.Budget.MaxFiles {
			return stopOperation("file_limit")
		}
		files++
		problem, err := o.scanContentFile(ctx, fullPath, filepath.ToSlash(relative), info, request, matcher, state, &readBytes)
		if err != nil {
			return err
		}
		if problem != "" {
			state.markPartial(problem)
			if err := state.emitProgress(OperationProblem{RelativePath: filepath.ToSlash(relative), Reason: problem}); err != nil {
				return err
			}
		}
		return nil
	})
	completion := state.completion(ctx, err)
	completion.Files = files
	completion.BytesRead = readBytes
	return completion, nil
}

func validateContentOperationRequest(request ContentSearchRequest) (*regexp.Regexp, error) {
	if err := validateOperationPath(request.Scope); err != nil {
		return nil, err
	}
	if request.Pattern == "" || len(request.Pattern) > MaxHelperStringBytes || !utf8.ValidString(request.Pattern) || strings.IndexByte(request.Pattern, 0) >= 0 || request.BinaryPolicy != "skip" || request.ContextLines > 3 {
		return nil, errors.New("helper content search: options are invalid")
	}
	pattern := request.Pattern
	if request.PatternType == "literal" {
		pattern = regexp.QuoteMeta(pattern)
	} else if request.PatternType != "regex" {
		return nil, errors.New("helper content search: pattern type is invalid")
	}
	if !request.CaseSensitive {
		pattern = "(?i:" + pattern + ")"
	}
	matcher, err := regexp.Compile(pattern)
	if err != nil {
		return nil, errors.New("helper content search: regex is invalid")
	}
	if err := validateOperationBudget(request.Budget.OperationSearchBudget); err != nil {
		return nil, err
	}
	b := request.Budget
	if b.MaxFiles == 0 || b.MaxFiles > 1_000_000 || b.MaxMatchesPerFile == 0 || b.MaxMatchesPerFile > MaxHelperResults || b.MaxFileBytes == 0 || b.MaxFileBytes > maxOperationFileBytes || b.MaxReadBytes == 0 || b.MaxReadBytes > maxOperationReadBytes || b.MaxSnippetBytes == 0 || b.MaxSnippetBytes > MaxHelperStringBytes {
		return nil, errors.New("helper content search: budget is outside hard limits")
	}
	return matcher, nil
}

func (o *LocalOperations) scanContentFile(ctx context.Context, fullPath, relative string, before os.FileInfo, request ContentSearchRequest, matcher *regexp.Regexp, state *operationState, readBytes *uint64) (string, error) {
	if before.Size() < 0 || uint64(before.Size()) > request.Budget.MaxFileBytes { // #nosec G115 -- the negative case is rejected first.
		return "file_byte_limit", nil
	}
	file, err := os.Open(fullPath) // #nosec G304 -- fullPath is derived below a validated absolute search scope.
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return "permission_denied", nil
		}
		return "provider_error", nil
	}
	defer file.Close()
	handleBefore, err := file.Stat()
	if err != nil || !os.SameFile(before, handleBefore) {
		return "file_changed", nil
	}
	limit := request.Budget.MaxFileBytes + 1
	data, err := io.ReadAll(io.LimitReader(file, int64(limit))) // #nosec G115 -- request validation caps limit at 64 MiB + 1.
	if err != nil {
		return "provider_error", nil
	}
	*readBytes += uint64(len(data))
	if *readBytes > request.Budget.MaxReadBytes {
		return "read_byte_limit", stopOperation("read_byte_limit")
	}
	if uint64(len(data)) > request.Budget.MaxFileBytes {
		return "file_byte_limit", nil
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return "binary_skipped", nil
	}
	if !utf8.Valid(data) {
		return "encoding_invalid", nil
	}
	lines := bytes.Split(data, []byte{'\n'})
	for _, line := range lines {
		if len(line) > MaxHelperLineBytes {
			return "line_limit", nil
		}
	}
	var fileResults uint64
	var offset uint64
	for lineIndex, line := range lines {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		matches := matcher.FindAllIndex(line, -1)
		for _, match := range matches {
			if fileResults >= request.Budget.MaxMatchesPerFile {
				return "file_result_limit", nil
			}
			start := lineIndex - int(request.ContextLines)
			if start < 0 {
				start = 0
			}
			end := lineIndex + int(request.ContextLines) + 1
			if end > len(lines) {
				end = len(lines)
			}
			snippet := strings.Join(byteLinesToStrings(lines[start:end]), "\n")
			snippet = truncateUTF8(snippet, int(request.Budget.MaxSnippetBytes))
			result := ContentSearchResult{RelativePath: relative, Line: uint64(lineIndex + 1), Column: uint64(match[0] + 1), Offset: offset + uint64(match[0]), Snippet: snippet} // #nosec G115 -- indexes come from in-memory slices and are non-negative.
			if err := state.emitResult(result); err != nil {
				return "", err
			}
			fileResults++
		}
		offset += uint64(len(line)) + 1
	}
	handleAfter, handleErr := file.Stat()
	pathAfter, pathErr := os.Lstat(fullPath)
	if handleErr != nil || pathErr != nil || !sameFileSnapshot(handleBefore, handleAfter) || !os.SameFile(handleAfter, pathAfter) || !sameFileSnapshot(handleAfter, pathAfter) {
		return "file_changed", nil
	}
	return "", nil
}

func (o *LocalOperations) StrongHash(ctx context.Context, body json.RawMessage, emit EmitFunc) (Completion, error) {
	var request StrongHashRequest
	if err := decodeStrictPayload(body, &request); err != nil {
		return Completion{}, err
	}
	if err := validateOperationPath(request.Path); err != nil || request.Algorithm != "sha256" || request.MaxBytes == 0 || request.MaxBytes > 2<<30 {
		return Completion{}, errors.New("helper strong hash: request is invalid")
	}
	before, err := os.Lstat(request.Path)
	if err != nil || !before.Mode().IsRegular() || before.Size() < 0 || uint64(before.Size()) > request.MaxBytes { // #nosec G115 -- the negative case is rejected first.
		return Completion{Status: "partial_results", Reason: "file_invalid"}, nil
	}
	file, err := os.Open(request.Path)
	if err != nil {
		return Completion{Status: "partial_results", Reason: "permission_denied"}, nil
	}
	defer file.Close()
	handleBefore, err := file.Stat()
	if err != nil || !os.SameFile(before, handleBefore) {
		return Completion{Status: "partial_results", Reason: "file_changed"}, nil
	}
	hash := sha256.New()
	buffer := make([]byte, 32*1024)
	var total uint64
	hookCalled := false
	for {
		if err := ctx.Err(); err != nil {
			return Completion{Status: "canceled", Reason: "canceled"}, nil
		}
		read, readErr := file.Read(buffer)
		if read > 0 {
			total += uint64(read)
			if total > request.MaxBytes {
				return Completion{Status: "partial_results", Reason: "byte_limit"}, nil
			}
			_, _ = hash.Write(buffer[:read])
			if !hookCalled && o.afterHashChunk != nil {
				hookCalled = true
				o.afterHashChunk()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return Completion{Status: "partial_results", Reason: "read_failed"}, nil
		}
		if read == 0 {
			return Completion{Status: "partial_results", Reason: "read_failed"}, nil
		}
	}
	handleAfter, handleErr := file.Stat()
	pathAfter, pathErr := os.Lstat(request.Path)
	valid := handleErr == nil && pathErr == nil && handleAfter.Size() >= 0 && sameFileSnapshot(handleBefore, handleAfter) && os.SameFile(handleAfter, pathAfter) && sameFileSnapshot(handleAfter, pathAfter) && total == uint64(handleAfter.Size()) // #nosec G115 -- the non-negative check is part of this conjunction.
	result := StrongHashResult{
		Algorithm: "sha256", Fingerprint: fingerprintOf(handleAfter, before),
		ComputedAtUnixNS: time.Now().UnixNano(), Valid: valid,
	}
	completion := Completion{Status: "complete", Reason: "none"}
	if valid {
		result.Digest = hex.EncodeToString(hash.Sum(nil))
	} else {
		result.InvalidReason = "file_changed"
		completion = Completion{Status: "partial_results", Reason: "file_changed"}
	}
	if err := emit(FrameResult, result); err != nil {
		return Completion{}, err
	}
	return completion, nil
}

func (o *LocalOperations) DiskStats(_ context.Context, body json.RawMessage, emit EmitFunc) (Completion, error) {
	var request DiskStatsRequest
	if err := decodeStrictPayload(body, &request); err != nil {
		return Completion{}, err
	}
	if err := validateOperationPath(request.Path); err != nil {
		return Completion{}, err
	}
	var status unix.Statfs_t
	if err := unix.Statfs(request.Path, &status); err != nil {
		return Completion{Status: "partial_results", Reason: "statfs_failed"}, nil //nolint:nilerr // The protocol models this query failure as a partial completion.
	}
	blockSize := uint64(status.Bsize)
	total, overflow := multiplyUint64(status.Blocks, blockSize)
	if overflow {
		return Completion{Status: "partial_results", Reason: "overflow"}, nil
	}
	available, overflow := multiplyUint64(status.Bavail, blockSize)
	if overflow {
		return Completion{Status: "partial_results", Reason: "overflow"}, nil
	}
	if err := emit(FrameResult, DiskStatsResult{TotalBytes: total, AvailableBytes: available, QuotaKnown: false, Source: "filesystem_statfs"}); err != nil {
		return Completion{}, err
	}
	return Completion{Status: "complete", Reason: "none"}, nil
}

type stopOperation string

func (s stopOperation) Error() string { return string(s) }

func walkLocal(ctx context.Context, root string, budget OperationSearchBudget, includeHidden bool, ignore string, visit localVisitFunc) error {
	var walk func(string, string, uint32) error
	walk = func(directory, relativeDirectory string, depth uint32) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		file, err := os.Open(directory) // #nosec G304 -- directory is rooted beneath the validated absolute request scope.
		if err != nil {
			return stopOperation("permission_denied")
		}
		defer file.Close()
		for {
			entries, readErr := file.ReadDir(int(budget.PageEntries))
			for _, entry := range entries {
				if err := ctx.Err(); err != nil {
					return err
				}
				relative := entry.Name()
				if relativeDirectory != "" {
					relative = filepath.Join(relativeDirectory, entry.Name())
				}
				if !includeHidden && hasHiddenLocalComponent(relative) || ignore == "default" && ignoredLocal(entry.Name()) {
					continue
				}
				fullPath := filepath.Join(directory, entry.Name())
				info, infoErr := entry.Info()
				if infoErr != nil {
					return stopOperation("provider_error")
				}
				if err := visit(fullPath, relative, entry, info, depth); err != nil {
					return err
				}
				if entry.Type()&os.ModeSymlink != 0 || !info.IsDir() {
					continue
				}
				if depth+1 >= budget.MaxDepth {
					return stopOperation("depth_limit")
				}
				if err := walk(fullPath, relative, depth+1); err != nil {
					return err
				}
			}
			if readErr == io.EOF {
				return nil
			}
			if readErr != nil {
				return stopOperation("provider_error")
			}
		}
	}
	return walk(root, "", 0)
}

func (s *operationState) emitResult(value any) error {
	if s.results >= s.requestBudget.MaxResults {
		return stopOperation("result_limit")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if s.outputBytes+uint64(len(raw)) > s.requestBudget.MaxOutputBytes {
		return stopOperation("output_limit")
	}
	if err := s.emit(FrameResult, value); err != nil {
		return err
	}
	s.results++
	s.outputBytes += uint64(len(raw))
	return nil
}

func (s *operationState) emitProgress(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if s.outputBytes+uint64(len(raw)) > s.requestBudget.MaxOutputBytes {
		return stopOperation("output_limit")
	}
	if err := s.emit(FrameProgress, value); err != nil {
		return err
	}
	s.outputBytes += uint64(len(raw))
	return nil
}

func (s *operationState) markPartial(reason string) {
	if s.partialReason == "" {
		s.partialReason = reason
	}
}

func (s *operationState) completion(ctx context.Context, err error) Completion {
	completion := Completion{Status: "complete", Reason: "none", Entries: s.entries, Results: s.results, OutputBytes: s.outputBytes}
	if errors.Is(ctx.Err(), context.Canceled) {
		completion.Status, completion.Reason = "canceled", "canceled"
		return completion
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		completion.Status, completion.Reason = "partial_results", "time_limit"
		return completion
	}
	var stop stopOperation
	if errors.As(err, &stop) {
		completion.Status, completion.Reason = "partial_results", string(stop)
		return completion
	}
	if err != nil {
		completion.Status, completion.Reason = "partial_results", "provider_error"
		return completion
	}
	if s.partialReason != "" {
		completion.Status, completion.Reason = "partial_results", s.partialReason
	}
	return completion
}

func validateOperationBudget(b OperationSearchBudget) error {
	if b.MaxDepth == 0 || b.MaxDepth > maxOperationDepth || b.MaxEntries == 0 || b.MaxEntries > maxOperationEntries || b.MaxResults == 0 || b.MaxResults > MaxHelperResults || b.MaxOutputBytes == 0 || b.MaxOutputBytes > MaxHelperOutputBytes || b.MaxDurationMS == 0 || b.MaxDurationMS > uint64(MaxHelperRequestDuration/time.Millisecond) || b.PageEntries == 0 || b.PageEntries > 4096 {
		return errors.New("helper operation: search budget is outside hard limits")
	}
	return nil
}

func validateOperationPath(value string) error {
	if value == "" || len(value) > MaxHelperStringBytes || !filepath.IsAbs(value) || filepath.Clean(value) != value || strings.IndexByte(value, 0) >= 0 || !utf8.ValidString(value) {
		return errors.New("helper operation: path is invalid")
	}
	return nil
}

func containsPattern(value, pattern string, caseSensitive bool) bool {
	if !caseSensitive {
		value, pattern = strings.ToLower(value), strings.ToLower(pattern)
	}
	return strings.Contains(value, pattern)
}

func localKind(entry os.DirEntry) string {
	if entry.Type()&os.ModeSymlink != 0 {
		return "symlink"
	}
	if entry.IsDir() {
		return "directory"
	}
	if entry.Type().IsRegular() {
		return "file"
	}
	return "other"
}

func hasHiddenLocalComponent(value string) bool {
	for _, component := range strings.Split(filepath.ToSlash(value), "/") {
		if strings.HasPrefix(component, ".") && component != "." && component != ".." {
			return true
		}
	}
	return false
}

func ignoredLocal(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", "node_modules":
		return true
	default:
		return false
	}
}

func byteLinesToStrings(lines [][]byte) []string {
	result := make([]string, len(lines))
	for index := range lines {
		result[index] = string(lines[index])
	}
	return result
}

func truncateUTF8(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	value = value[:maximum]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func sameFileSnapshot(left, right os.FileInfo) bool {
	return os.SameFile(left, right) && left.Size() == right.Size() && left.Mode() == right.Mode() && left.ModTime().Equal(right.ModTime())
}

func fingerprintOf(preferred, fallback os.FileInfo) FileFingerprint {
	info := preferred
	if info == nil {
		info = fallback
	}
	if info == nil {
		return FileFingerprint{}
	}
	size := info.Size()
	if size < 0 {
		return FileFingerprint{}
	}
	return FileFingerprint{Size: uint64(size), Mode: uint32(info.Mode().Perm()), ModifiedUnixNS: info.ModTime().UnixNano(), FileID: localFileID(info)}
}

func localFileID(info os.FileInfo) string {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return ""
	}
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino)
}

func multiplyUint64(left, right uint64) (uint64, bool) {
	if left != 0 && right > ^uint64(0)/left {
		return 0, true
	}
	return left * right, false
}
