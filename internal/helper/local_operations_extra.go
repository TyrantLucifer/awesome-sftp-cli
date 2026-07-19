package helper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
)

type TailRequest struct {
	Path           string `json:"path"`
	Offset         uint64 `json:"offset"`
	MaxBytes       uint64 `json:"max_bytes"`
	DurationMS     uint64 `json:"duration_ms"`
	PollIntervalMS uint64 `json:"poll_interval_ms"`
}

type TailNotice struct {
	Type            string `json:"type"`
	OldOffset       uint64 `json:"old_offset"`
	NewOffset       uint64 `json:"new_offset"`
	RequiresRefresh bool   `json:"requires_refresh"`
}

type TailChunk struct {
	Offset uint64 `json:"offset"`
	Data   []byte `json:"data"`
}

type WatchRequest struct {
	Path           string `json:"path"`
	DurationMS     uint64 `json:"duration_ms"`
	PollIntervalMS uint64 `json:"poll_interval_ms"`
	MaxEntries     uint64 `json:"max_entries"`
	MaxEvents      uint64 `json:"max_events"`
}

type WatchHint struct {
	ChangedNames    []string `json:"changed_names"`
	LostPossible    bool     `json:"lost_possible"`
	Coalesced       bool     `json:"coalesced"`
	RequiresRefresh bool     `json:"requires_refresh"`
	Semantic        string   `json:"semantic"`
}

type SameHostCopyRequest struct {
	Source         string          `json:"source"`
	Part           string          `json:"part"`
	Final          string          `json:"final"`
	JobID          domain.JobID    `json:"job_id"`
	ExpectedSource FileFingerprint `json:"expected_source"`
	ExpectedSHA256 string          `json:"expected_sha256"`
	ExpectedSize   uint64          `json:"expected_size"`
	MaxBytes       uint64          `json:"max_bytes"`
}

type SameHostCopyResult struct {
	Part        string          `json:"part"`
	Size        uint64          `json:"size"`
	SHA256      string          `json:"sha256"`
	Fingerprint FileFingerprint `json:"source_fingerprint"`
	Committed   bool            `json:"committed"`
}

func (o *LocalOperations) Tail(parent context.Context, body json.RawMessage, emit EmitFunc) (Completion, error) {
	var request TailRequest
	if err := decodeStrictPayload(body, &request); err != nil {
		return Completion{}, err
	}
	if err := validateOperationPath(request.Path); err != nil || request.MaxBytes == 0 || request.MaxBytes > MaxHelperOutputBytes || request.DurationMS == 0 || request.DurationMS > 30_000 || request.PollIntervalMS < 10 || request.PollIntervalMS > 1000 {
		return Completion{}, errors.New("helper tail: request is invalid")
	}
	initial, err := os.Lstat(request.Path)
	if err != nil || !initial.Mode().IsRegular() || initial.Size() < 0 {
		return Completion{Status: "partial_results", Reason: "file_invalid"}, nil
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(request.DurationMS)*time.Millisecond) // #nosec G115 -- validation caps duration at 30 seconds.
	defer cancel()
	offset := request.Offset
	current := initial
	var emitted uint64
	readAvailable := func() error {
		for current.Size() >= 0 && uint64(current.Size()) > offset && emitted < request.MaxBytes { // #nosec G115 -- the non-negative check precedes conversion.
			file, err := os.Open(request.Path) // #nosec G304 -- request.Path passed strict absolute-path validation.
			if err != nil {
				return stopOperation("read_failed")
			}
			handleInfo, statErr := file.Stat()
			if statErr != nil || !os.SameFile(current, handleInfo) {
				_ = file.Close()
				return stopOperation("file_changed")
			}
			if offset > uint64(current.Size()) { // #nosec G115 -- the loop guard established a non-negative size.
				_ = file.Close()
				return stopOperation("file_changed")
			}
			if _, err := file.Seek(int64(offset), io.SeekStart); err != nil { // #nosec G115 -- offset is bounded by a non-negative int64 file size above.
				_ = file.Close()
				return stopOperation("read_failed")
			}
			maximum := uint64(32 * 1024)
			if request.MaxBytes-emitted < maximum {
				maximum = request.MaxBytes - emitted
			}
			currentSize := uint64(current.Size()) // #nosec G115 -- the loop guard requires a non-negative size.
			if currentSize-offset < maximum {
				maximum = currentSize - offset
			}
			buffer := make([]byte, int(maximum))
			read, readErr := io.ReadFull(file, buffer)
			_ = file.Close()
			if readErr != nil && readErr != io.ErrUnexpectedEOF {
				return stopOperation("read_failed")
			}
			if read == 0 {
				break
			}
			chunk := TailChunk{Offset: offset, Data: append([]byte(nil), buffer[:read]...)}
			if err := emit(FrameResult, chunk); err != nil {
				return err
			}
			offset += uint64(read)  // #nosec G115 -- io.ReadFull byte counts are non-negative.
			emitted += uint64(read) // #nosec G115 -- io.ReadFull byte counts are non-negative.
		}
		return nil
	}
	if err := readAvailable(); err != nil {
		return completionFromTail(ctx, err, emitted), nil
	}
	poll := o.tailPoll
	var ticker *time.Ticker
	if poll == nil {
		ticker = time.NewTicker(time.Duration(request.PollIntervalMS) * time.Millisecond)
		poll = ticker.C
		defer ticker.Stop()
	}
	for {
		select {
		case <-ctx.Done():
			return completionFromTail(ctx, nil, emitted), nil
		case _, ok := <-poll:
			if !ok {
				return Completion{Status: "partial_results", Reason: "poll_unavailable", Results: emitted}, nil
			}
			if o.afterTailPoll != nil {
				o.afterTailPoll()
			}
			latest, err := os.Lstat(request.Path)
			if err != nil || !latest.Mode().IsRegular() {
				return Completion{Status: "partial_results", Reason: "file_missing"}, nil
			}
			if !os.SameFile(current, latest) {
				old := offset
				offset = 0
				current = latest
				if err := emit(FrameProgress, TailNotice{Type: "rotated", OldOffset: old, NewOffset: 0, RequiresRefresh: true}); err != nil {
					return Completion{}, err
				}
			} else if latest.Size() < 0 || uint64(latest.Size()) < offset { // #nosec G115 -- the negative case is checked first.
				old := offset
				offset = 0
				current = latest
				if err := emit(FrameProgress, TailNotice{Type: "truncated", OldOffset: old, NewOffset: 0, RequiresRefresh: true}); err != nil {
					return Completion{}, err
				}
			} else {
				current = latest
			}
			if err := readAvailable(); err != nil {
				return completionFromTail(ctx, err, emitted), nil
			}
			if emitted >= request.MaxBytes {
				return Completion{Status: "partial_results", Reason: "byte_limit", Results: emitted}, nil
			}
		}
	}
}

func completionFromTail(ctx context.Context, err error, emitted uint64) Completion {
	if errors.Is(ctx.Err(), context.Canceled) {
		return Completion{Status: "canceled", Reason: "canceled", Results: emitted}
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return Completion{Status: "complete", Reason: "duration_reached", Results: emitted}
	}
	var stop stopOperation
	if errors.As(err, &stop) {
		return Completion{Status: "partial_results", Reason: string(stop), Results: emitted}
	}
	if err != nil {
		return Completion{Status: "partial_results", Reason: "read_failed", Results: emitted}
	}
	return Completion{Status: "complete", Reason: "duration_reached", Results: emitted}
}

func (o *LocalOperations) Watch(parent context.Context, body json.RawMessage, emit EmitFunc) (Completion, error) {
	var request WatchRequest
	if err := decodeStrictPayload(body, &request); err != nil {
		return Completion{}, err
	}
	if err := validateOperationPath(request.Path); err != nil || request.DurationMS == 0 || request.DurationMS > 30_000 || request.PollIntervalMS < 10 || request.PollIntervalMS > 1000 || request.MaxEntries == 0 || request.MaxEntries > 100_000 || request.MaxEvents == 0 || request.MaxEvents > 10_000 {
		return Completion{}, errors.New("helper watch: request is invalid")
	}
	previous, err := snapshotDirectory(request.Path, request.MaxEntries)
	if err != nil {
		return Completion{Status: "partial_results", Reason: "directory_invalid"}, nil
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(request.DurationMS)*time.Millisecond)
	defer cancel()
	ticker := time.NewTicker(time.Duration(request.PollIntervalMS) * time.Millisecond)
	defer ticker.Stop()
	var events uint64
	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return Completion{Status: "canceled", Reason: "canceled", Results: events}, nil
			}
			return Completion{Status: "complete", Reason: "duration_reached", Results: events}, nil
		case <-ticker.C:
			if o.afterWatchPoll != nil {
				o.afterWatchPoll()
			}
			current, err := snapshotDirectory(request.Path, request.MaxEntries)
			if err != nil {
				return Completion{Status: "partial_results", Reason: "directory_changed", Results: events}, nil
			}
			changed := changedSnapshotNames(previous, current, 64)
			previous = current
			if len(changed) == 0 {
				continue
			}
			if events >= request.MaxEvents {
				return Completion{Status: "partial_results", Reason: "event_limit", Results: events}, nil
			}
			hint := WatchHint{ChangedNames: changed, LostPossible: true, Coalesced: true, RequiresRefresh: true, Semantic: "polling_hint"}
			if err := emit(FrameResult, hint); err != nil {
				return Completion{}, err
			}
			events++
		}
	}
}

type watchEntry struct {
	Mode    os.FileMode
	Size    int64
	ModTime int64
}

func snapshotDirectory(directory string, maximum uint64) (map[string]watchEntry, error) {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() {
		return nil, errors.New("watch path is not a directory")
	}
	file, err := os.Open(directory) // #nosec G304 -- directory passed strict absolute-path validation before this helper.
	if err != nil {
		return nil, err
	}
	defer file.Close()
	result := make(map[string]watchEntry)
	for {
		entries, readErr := file.ReadDir(256)
		for _, entry := range entries {
			if uint64(len(result)) >= maximum {
				return nil, errors.New("watch entry limit reached")
			}
			entryInfo, err := entry.Info()
			if err != nil {
				continue
			}
			result[entry.Name()] = watchEntry{Mode: entryInfo.Mode(), Size: entryInfo.Size(), ModTime: entryInfo.ModTime().UnixNano()}
		}
		if readErr == io.EOF {
			return result, nil
		}
		if readErr != nil {
			return nil, readErr
		}
	}
}

func changedSnapshotNames(previous, current map[string]watchEntry, maximum int) []string {
	names := make(map[string]struct{})
	for name, value := range previous {
		if currentValue, exists := current[name]; !exists || currentValue != value {
			names[name] = struct{}{}
		}
	}
	for name, value := range current {
		if previousValue, exists := previous[name]; !exists || previousValue != value {
			names[name] = struct{}{}
		}
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	if len(result) > maximum {
		result = result[:maximum]
	}
	return result
}

func (o *LocalOperations) SameHostCopy(ctx context.Context, body json.RawMessage, emit EmitFunc) (completion Completion, returnErr error) {
	var request SameHostCopyRequest
	if err := decodeStrictPayload(body, &request); err != nil {
		return Completion{}, err
	}
	if err := validateSameHostCopyRequest(request); err != nil {
		return Completion{}, err
	}
	sourceInfo, err := os.Lstat(request.Source)
	if err != nil || !sourceInfo.Mode().IsRegular() || sourceInfo.Size() < 0 || fingerprintOf(sourceInfo, nil) != request.ExpectedSource || uint64(sourceInfo.Size()) != request.ExpectedSize { // #nosec G115 -- the negative case is rejected first.
		return Completion{Status: "partial_results", Reason: "source_changed"}, nil
	}
	source, err := os.Open(request.Source) // #nosec G304 -- source passed strict absolute-path validation.
	if err != nil {
		return Completion{Status: "partial_results", Reason: "source_unreadable"}, nil
	}
	defer source.Close()
	handleSourceInfo, err := source.Stat()
	if err != nil || !os.SameFile(sourceInfo, handleSourceInfo) {
		return Completion{Status: "partial_results", Reason: "source_changed"}, nil
	}
	part, err := os.OpenFile(request.Part, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return Completion{Status: "partial_results", Reason: "part_conflict"}, nil
		}
		return Completion{Status: "partial_results", Reason: "part_create_failed"}, nil
	}
	partInfo, err := part.Stat()
	if err != nil || !partInfo.Mode().IsRegular() || partInfo.Mode().Perm() != 0600 || partInfo.Size() != 0 {
		_ = part.Close()
		return Completion{Status: "partial_results", Reason: "part_attrs_invalid"}, nil
	}
	keepPart := false
	defer func() {
		_ = part.Close()
		if !keepPart {
			removeExactLocalFile(request.Part, partInfo)
		}
	}()
	hash := sha256.New()
	buffer := make([]byte, 32*1024)
	var total uint64
	for {
		if err := ctx.Err(); err != nil {
			return Completion{Status: "canceled", Reason: "canceled"}, nil
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			total += uint64(read)
			if total > request.ExpectedSize || total > request.MaxBytes {
				return Completion{Status: "partial_results", Reason: "byte_limit"}, nil
			}
			_, _ = hash.Write(buffer[:read])
			if _, err := part.Write(buffer[:read]); err != nil {
				return Completion{Status: "partial_results", Reason: "part_write_failed"}, nil
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil || read == 0 {
			return Completion{Status: "partial_results", Reason: "source_read_failed"}, nil
		}
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if total != request.ExpectedSize || digest != request.ExpectedSHA256 {
		return Completion{Status: "partial_results", Reason: "source_changed"}, nil
	}
	if err := part.Sync(); err != nil {
		return Completion{Status: "partial_results", Reason: "part_sync_failed"}, nil
	}
	finalSourceInfo, sourceErr := source.Stat()
	pathSourceInfo, pathErr := os.Lstat(request.Source)
	finalPartInfo, partErr := part.Stat()
	if sourceErr != nil || pathErr != nil || partErr != nil || !sameFileSnapshot(handleSourceInfo, finalSourceInfo) || !os.SameFile(finalSourceInfo, pathSourceInfo) || !sameFileSnapshot(finalSourceInfo, pathSourceInfo) || finalPartInfo.Size() != int64(total) || finalPartInfo.Mode().Perm() != 0600 {
		return Completion{Status: "partial_results", Reason: "source_changed"}, nil
	}
	if err := part.Close(); err != nil {
		return Completion{Status: "partial_results", Reason: "part_close_failed"}, nil
	}
	if digestAfter, sizeAfter, err := hashLocalFile(request.Part, request.MaxBytes); err != nil || digestAfter != digest || sizeAfter != total {
		return Completion{Status: "partial_results", Reason: "part_verify_failed"}, nil
	}
	result := SameHostCopyResult{Part: request.Part, Size: total, SHA256: digest, Fingerprint: fingerprintOf(finalSourceInfo, nil), Committed: false}
	if err := emit(FrameResult, result); err != nil {
		return Completion{}, err
	}
	keepPart = true
	return Completion{Status: "complete", Reason: "staged_not_committed"}, nil
}

func validateSameHostCopyRequest(request SameHostCopyRequest) error {
	for _, value := range []string{request.Source, request.Part, request.Final} {
		if err := validateOperationPath(value); err != nil {
			return err
		}
	}
	if request.Source == request.Part || request.Source == request.Final || request.Part == request.Final {
		return errors.New("helper same-host copy: paths are not distinct")
	}
	if _, err := domain.ParseJobID(string(request.JobID)); err != nil {
		return errors.New("helper same-host copy: job ID is invalid")
	}
	wantPart := filepath.Join(filepath.Dir(request.Final), "."+filepath.Base(request.Final)+".part-"+string(request.JobID))
	if request.Part != wantPart {
		return errors.New("helper same-host copy: part does not match frozen Planner grammar")
	}
	if request.MaxBytes == 0 || request.MaxBytes < request.ExpectedSize || request.MaxBytes > 2<<30 || len(request.ExpectedSHA256) != 64 || !isLowerHex(request.ExpectedSHA256) {
		return errors.New("helper same-host copy: expected content identity is invalid")
	}
	return nil
}

func removeExactLocalFile(path string, expected os.FileInfo) {
	current, err := os.Lstat(path)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(expected, current) {
		return
	}
	_ = os.Remove(path)
}

func hashLocalFile(path string, maximum uint64) (string, uint64, error) {
	file, err := os.Open(path) // #nosec G304 -- path is the validated request part path.
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	limited := &io.LimitedReader{R: file, N: int64(maximum) + 1} // #nosec G115 -- same-host request validation caps maximum at 2 GiB.
	written, err := io.Copy(hash, limited)
	if err != nil || written < 0 || uint64(written) > maximum {
		return "", uint64(max(written, 0)), errors.New("hash file exceeds limit")
	}
	return hex.EncodeToString(hash.Sum(nil)), uint64(written), nil
}
