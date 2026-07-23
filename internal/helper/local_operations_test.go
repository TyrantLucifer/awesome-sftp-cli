package helper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
)

func TestLocalFilenameSearchStreamsBoundedWalkWithoutFollowingSymlinks(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "first-target.txt"), []byte("one"))
	mustWriteFile(t, filepath.Join(root, "nested", "second-target.txt"), []byte("two"))
	mustWriteFile(t, filepath.Join(root, ".hidden-target.txt"), []byte("hidden"))
	if err := os.Symlink(root, filepath.Join(root, "nested", "loop")); err != nil {
		t.Fatal(err)
	}
	operations := NewLocalOperations()
	request := FilenameSearchRequest{
		Scope: root, Pattern: "target", Match: "relative_path", IncludeHidden: false,
		Types:  FilenameTypes{Files: true},
		Budget: OperationSearchBudget{MaxDepth: 8, MaxEntries: 100, MaxResults: 10, MaxOutputBytes: 32 << 10, MaxDurationMS: 1000, PageEntries: 2},
	}
	var results []FilenameSearchResult
	completion, err := operations.FilenameSearch(context.Background(), marshalBody(t, request), func(kind FrameType, payload any) error {
		if kind == FrameResult {
			results = append(results, payload.(FilenameSearchResult))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if completion.Status != "complete" || completion.Reason != "none" || len(results) != 2 {
		t.Fatalf("completion=%#v results=%#v", completion, results)
	}
	paths := make(map[string]struct{}, len(results))
	for _, result := range results {
		paths[result.RelativePath] = struct{}{}
	}
	for _, expected := range []string{"first-target.txt", "nested/second-target.txt"} {
		if _, ok := paths[expected]; !ok {
			t.Fatalf("stream results = %#v, missing %q", results, expected)
		}
	}
}

func TestLocalContentSearchReportsBinaryEncodingLongLineAndMatchesWithBounds(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "text.txt"), []byte("before\nneedle here\nafter\n"))
	mustWriteFile(t, filepath.Join(root, "binary.bin"), []byte("needle\x00binary"))
	mustWriteFile(t, filepath.Join(root, "invalid.txt"), []byte{'n', 'e', 'e', 'd', 'l', 'e', 0xff, '\n'})
	mustWriteFile(t, filepath.Join(root, "long.txt"), []byte(strings.Repeat("x", MaxHelperLineBytes+1)+"needle\n"))
	operations := NewLocalOperations()
	request := ContentSearchRequest{
		Scope: root, Pattern: "needle", PatternType: "literal", CaseSensitive: true,
		BinaryPolicy: "skip", ContextLines: 1,
		Budget: ContentOperationBudget{
			OperationSearchBudget: OperationSearchBudget{MaxDepth: 8, MaxEntries: 100, MaxResults: 10, MaxOutputBytes: 64 << 10, MaxDurationMS: 1000, PageEntries: 4},
			MaxFiles:              10, MaxMatchesPerFile: 4, MaxFileBytes: 2 << 20, MaxReadBytes: 8 << 20, MaxSnippetBytes: 512,
		},
	}
	var matches []ContentSearchResult
	reasons := map[string]bool{}
	completion, err := operations.ContentSearch(context.Background(), marshalBody(t, request), func(kind FrameType, payload any) error {
		switch kind {
		case FrameResult:
			matches = append(matches, payload.(ContentSearchResult))
		case FrameProgress:
			reasons[payload.(OperationProblem).Reason] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].RelativePath != "text.txt" || matches[0].Line != 2 || !strings.Contains(matches[0].Snippet, "before\nneedle here\nafter") {
		t.Fatalf("matches = %#v", matches)
	}
	for _, reason := range []string{"binary_skipped", "encoding_invalid", "line_limit"} {
		if !reasons[reason] {
			t.Fatalf("missing %s in %#v", reason, reasons)
		}
	}
	if completion.Status != "partial_results" {
		t.Fatalf("completion = %#v", completion)
	}
}

func TestLocalStrongHashInvalidatesMidReadChangeAndDiskStatsKeepsQuotaUnknown(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "payload")
	data := []byte(strings.Repeat("a", 128*1024))
	mustWriteFile(t, file, data)
	operations := NewLocalOperations()
	var hashResult StrongHashResult
	completion, err := operations.StrongHash(context.Background(), marshalBody(t, StrongHashRequest{Path: file, Algorithm: "sha256", MaxBytes: 1 << 20}), func(kind FrameType, payload any) error {
		if kind == FrameResult {
			hashResult = payload.(StrongHashResult)
		}
		return nil
	})
	if err != nil || completion.Status != "complete" || !hashResult.Valid {
		t.Fatalf("hash completion=%#v result=%#v err=%v", completion, hashResult, err)
	}
	digest := sha256.Sum256(data)
	if hashResult.Digest != hex.EncodeToString(digest[:]) || hashResult.Algorithm != "sha256" || hashResult.ComputedAtUnixNS == 0 {
		t.Fatalf("hash result = %#v", hashResult)
	}
	operations.afterHashChunk = func() {
		operations.afterHashChunk = nil
		_ = os.WriteFile(file, []byte("changed"), 0600)
	}
	var changed StrongHashResult
	completion, err = operations.StrongHash(context.Background(), marshalBody(t, StrongHashRequest{Path: file, Algorithm: "sha256", MaxBytes: 1 << 20}), func(kind FrameType, payload any) error {
		if kind == FrameResult {
			changed = payload.(StrongHashResult)
		}
		return nil
	})
	if err != nil || completion.Status != "partial_results" || changed.Valid || changed.InvalidReason != "file_changed" {
		t.Fatalf("changed completion=%#v result=%#v err=%v", completion, changed, err)
	}
	var disk DiskStatsResult
	completion, err = operations.DiskStats(context.Background(), marshalBody(t, DiskStatsRequest{Path: root}), func(kind FrameType, payload any) error {
		if kind == FrameResult {
			disk = payload.(DiskStatsResult)
		}
		return nil
	})
	if err != nil || completion.Status != "complete" || disk.TotalBytes == 0 || disk.AvailableBytes > disk.TotalBytes || disk.QuotaKnown || disk.Source != "filesystem_statfs" {
		t.Fatalf("disk completion=%#v result=%#v err=%v", completion, disk, err)
	}
}

func marshalBody(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func mustWriteFile(t *testing.T, path string, value []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, value, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLocalOperationCancellationLatency(t *testing.T) {
	root := t.TempDir()
	for index := 0; index < 2000; index++ {
		mustWriteFile(t, filepath.Join(root, "nested", strings.Repeat("x", 8), time.Unix(int64(index), 0).Format("150405.000000000")), []byte("x"))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	completion, err := NewLocalOperations().FilenameSearch(ctx, marshalBody(t, FilenameSearchRequest{
		Scope: root, Pattern: "x", Match: "name", Types: FilenameTypes{Files: true},
		Budget: OperationSearchBudget{MaxDepth: 8, MaxEntries: 10_000, MaxResults: 1000, MaxOutputBytes: 1 << 20, MaxDurationMS: 1000, PageEntries: 32},
	}), func(FrameType, any) error { return nil })
	if err != nil || completion.Status != "canceled" || time.Since(started) > 250*time.Millisecond {
		t.Fatalf("completion=%#v err=%v latency=%s", completion, err, time.Since(started))
	}
}

func TestMillionNodeHelperFilenameFixtureStreamsWithoutMaterializingTree(t *testing.T) {
	operations := NewLocalOperations()
	var maximumAlloc uint64
	operations.walk = func(ctx context.Context, _ string, _ OperationSearchBudget, _ bool, _ string, visit localVisitFunc) error {
		entry := syntheticLocalEntry{info: syntheticLocalInfo{mode: 0o600, size: 1}}
		for index := 0; index < 1_000_000; index++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			entry.name = "ordinary"
			if index%10_000 == 0 {
				entry.name = "target"
			}
			if err := visit("/fixture/"+entry.name, entry.name, entry, entry.info, 1); err != nil {
				return err
			}
			if index%10_000 == 0 {
				var memory runtime.MemStats
				runtime.ReadMemStats(&memory)
				if memory.Alloc > maximumAlloc {
					maximumAlloc = memory.Alloc
				}
			}
		}
		return nil
	}
	var before runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	started := time.Now()
	first := time.Duration(0)
	results := 0
	completion, err := operations.FilenameSearch(context.Background(), marshalBody(t, FilenameSearchRequest{
		Scope: "/fixture", Pattern: "target", Match: "name", Types: FilenameTypes{Files: true},
		Budget: OperationSearchBudget{MaxDepth: 2, MaxEntries: 1_000_000, MaxResults: 101, MaxOutputBytes: 1 << 20, MaxDurationMS: 10_000, PageEntries: 128},
	}), func(kind FrameType, _ any) error {
		if kind == FrameResult {
			results++
			if first == 0 {
				first = time.Since(started)
			}
		}
		return nil
	})
	if err != nil || completion.Status != "complete" || results != 100 || first == 0 || first > 250*time.Millisecond {
		t.Fatalf("completion=%#v results=%d first=%s err=%v", completion, results, first, err)
	}
	if maximumAlloc > before.Alloc+96<<20 {
		t.Fatalf("million-node Helper fixture allocation grew by %d bytes", maximumAlloc-before.Alloc)
	}
	t.Logf("first_result=%s peak_alloc_delta=%d results=%d", first, maximumAlloc-before.Alloc, results)
}

type syntheticLocalEntry struct {
	name string
	info syntheticLocalInfo
}

func (entry syntheticLocalEntry) Name() string               { return entry.name }
func (entry syntheticLocalEntry) IsDir() bool                { return false }
func (entry syntheticLocalEntry) Type() os.FileMode          { return 0 }
func (entry syntheticLocalEntry) Info() (os.FileInfo, error) { return entry.info, nil }

type syntheticLocalInfo struct {
	mode os.FileMode
	size int64
}

func (info syntheticLocalInfo) Name() string       { return "fixture" }
func (info syntheticLocalInfo) Size() int64        { return info.size }
func (info syntheticLocalInfo) Mode() os.FileMode  { return info.mode }
func (info syntheticLocalInfo) ModTime() time.Time { return time.Unix(0, 0) }
func (info syntheticLocalInfo) IsDir() bool        { return false }
func (info syntheticLocalInfo) Sys() any           { return nil }

func TestLocalTailDetectsTruncateAndRotateAsHints(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "tail.log")
	mustWriteFile(t, file, []byte("old"))
	operations := NewLocalOperations()
	polls := make(chan time.Time, 2)
	polls <- time.Time{}
	polls <- time.Time{}
	operations.tailPoll = polls
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	step := 0
	operations.afterTailPoll = func() {
		step++
		switch step {
		case 1:
			_ = os.WriteFile(file, []byte("n"), 0600)
		case 2:
			_ = os.Rename(file, file+".1")
			_ = os.WriteFile(file, []byte("rotated"), 0600)
			cancel()
		}
	}
	var notices []TailNotice
	var chunks []TailChunk
	completion, err := operations.Tail(ctx, marshalBody(t, TailRequest{Path: file, Offset: 3, MaxBytes: 64, DurationMS: 80, PollIntervalMS: 10}), func(kind FrameType, payload any) error {
		switch kind {
		case FrameProgress:
			notices = append(notices, payload.(TailNotice))
		case FrameResult:
			chunks = append(chunks, payload.(TailChunk))
		}
		return nil
	})
	if err != nil || completion.Status != "canceled" || completion.Reason != "canceled" {
		t.Fatalf("completion=%#v err=%v", completion, err)
	}
	foundRotate := false
	foundTruncate := false
	for _, notice := range notices {
		foundRotate = foundRotate || notice.Type == "rotated"
		foundTruncate = foundTruncate || notice.Type == "truncated"
		if !notice.RequiresRefresh {
			t.Fatalf("notice is not a hint: %#v", notice)
		}
	}
	if !foundRotate || !foundTruncate || len(chunks) == 0 {
		t.Fatalf("notices=%#v chunks=%#v", notices, chunks)
	}
}

func TestLocalWatchDeclaresLostCoalescedHintsAndRequiresRefresh(t *testing.T) {
	root := t.TempDir()
	operations := NewLocalOperations()
	operations.afterWatchPoll = func() {
		operations.afterWatchPoll = nil
		mustWriteFile(t, filepath.Join(root, "created"), []byte("x"))
	}
	var hints []WatchHint
	completion, err := operations.Watch(context.Background(), marshalBody(t, WatchRequest{Path: root, DurationMS: 60, PollIntervalMS: 10, MaxEntries: 100, MaxEvents: 10}), func(kind FrameType, payload any) error {
		if kind == FrameResult {
			hints = append(hints, payload.(WatchHint))
		}
		return nil
	})
	if err != nil || completion.Status != "complete" || len(hints) == 0 {
		t.Fatalf("completion=%#v hints=%#v err=%v", completion, hints, err)
	}
	if !hints[0].LostPossible || !hints[0].Coalesced || !hints[0].RequiresRefresh || hints[0].Semantic != "polling_hint" {
		t.Fatalf("hint overclaims consistency: %#v", hints[0])
	}
}

func TestLocalSameHostCopyStagesOnlyExactPlannerPartAndNeverCommitsFinal(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	final := filepath.Join(root, "final")
	jobID := domain.JobID("job_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	part := filepath.Join(root, ".final.part-"+string(jobID))
	data := []byte("same-host-copy")
	mustWriteFile(t, source, data)
	info, err := os.Lstat(source)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	request := SameHostCopyRequest{
		Source: source, Part: part, Final: final, JobID: jobID,
		ExpectedSource: fingerprintOf(info, nil), ExpectedSHA256: hex.EncodeToString(digest[:]), ExpectedSize: uint64(len(data)), MaxBytes: 1024,
	}
	var result SameHostCopyResult
	completion, err := NewLocalOperations().SameHostCopy(context.Background(), marshalBody(t, request), func(kind FrameType, payload any) error {
		if kind == FrameResult {
			result = payload.(SameHostCopyResult)
		}
		return nil
	})
	if err != nil || completion.Status != "complete" || result.Part != part || result.Committed {
		t.Fatalf("completion=%#v result=%#v err=%v", completion, result, err)
	}
	if _, err := os.Lstat(final); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("helper committed final: %v", err)
	}
	if got, err := os.ReadFile(part); err != nil || !bytes.Equal(got, data) { // #nosec G304 -- part is inside t.TempDir.
		t.Fatalf("part=%q err=%v", got, err)
	}
	bad := request
	bad.Part = filepath.Join(root, "arbitrary")
	if _, err := NewLocalOperations().SameHostCopy(context.Background(), marshalBody(t, bad), func(FrameType, any) error { return nil }); err == nil {
		t.Fatal("arbitrary destination bypassed Planner part grammar")
	}
	if _, err := os.Lstat(bad.Part); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("bad part was created: %v", err)
	}
}
