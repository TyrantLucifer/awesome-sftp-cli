//go:build darwin || linux

package externalpreviewer

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/preview"
)

// MaterializeFunc obtains one complete, verified cache materialization and an
// active lease. maximumBytes lets the caller reject a remote object before it
// is downloaded. The returned release callback is mandatory.
type MaterializeFunc func(ctx context.Context, maximumBytes int64) (LeasedMaterialization, error)

type LeasedMaterialization struct {
	Path     string
	Complete bool
	Verified bool
	Release  func(context.Context) error
}

type OrchestrationRequest struct {
	Path      string
	MediaType string
	BuiltIn   preview.Result

	HasFileSize bool
	FileSize    uint64

	Capability  preview.ImageCapabilityProof
	ImageLimits preview.ImageOutputLimits
	Materialize MaterializeFunc
}

type OutcomeKind string

const (
	OutcomeBuiltIn       OutcomeKind = "built_in"
	OutcomeTerminalImage OutcomeKind = "terminal_image"
	OutcomeExternal      OutcomeKind = "external"
)

type OrchestrationCode string

const (
	CodeNoFallback             OrchestrationCode = "no_fallback"
	CodeMaterializeUnavailable OrchestrationCode = "materialize_unavailable"
	CodeMaterializeFailed      OrchestrationCode = "materialize_failed"
	CodeUnsafeMaterialization  OrchestrationCode = "unsafe_materialization"
	CodeImageFailed            OrchestrationCode = "image_failed"
	CodeOrchestrationCanceled  OrchestrationCode = "canceled"
)

// OrchestrationOutcome never contains file content, paths, or raw errors.
// TerminalBytes are populated only after an active capability proof succeeds.
type OrchestrationOutcome struct {
	Kind          OutcomeKind
	Code          OrchestrationCode
	BuiltIn       preview.Result
	TerminalBytes []byte
	ImageProtocol preview.ImageProtocol
	External      Result
	ReleaseFailed bool
}

const leaseReleaseTimeout = 5 * time.Second

// Orchestrate applies the built-in format policy before consulting external
// rules. Text, code, JSON, and explicit metadata views can never start a child
// process. Image and unknown-binary fallback uses one verified cache path while
// its lease remains active for the entire image read or child lifetime.
func Orchestrate(ctx context.Context, runner *Runner, request OrchestrationRequest) (outcome OrchestrationOutcome) {
	outcome = OrchestrationOutcome{Kind: OutcomeBuiltIn, Code: CodeNoFallback, BuiltIn: request.BuiltIn, ImageProtocol: preview.ImageProtocolNone, External: noMatchResult()}
	if ctx == nil || request.Path == "" || !externalEligible(request.BuiltIn) {
		return outcome
	}
	if ctx.Err() != nil {
		outcome.Code = CodeOrchestrationCanceled
		return outcome
	}

	mediaType := request.MediaType
	if mediaType == "" && request.BuiltIn.Image != nil {
		mediaType = request.BuiltIn.Image.MediaType
	}
	externalRequest := Request{Path: request.Path, MediaType: mediaType, Complete: true}
	selected, externalMatched := frozenRule{}, false
	if runner != nil {
		selected, externalMatched = runner.match(externalRequest)
	}

	limits := request.ImageLimits
	imageEligible := request.BuiltIn.Kind == preview.KindImage && request.Capability.Protocol() != preview.ImageProtocolNone && mediaType == "image/png" && validImageLimits(limits)
	externalEligibleBySize := externalMatched
	if externalMatched && request.HasFileSize && request.FileSize > uint64(selected.maxInputBytes) {
		outcome.External = rejectedExternal(selected, CodeInputTooLarge)
		externalEligibleBySize = false
	}
	if imageEligible && request.HasFileSize && request.FileSize > uint64(limits.MaxPayloadBytes) {
		imageEligible = false
		outcome.Code = CodeImageFailed
	}
	if !imageEligible && !externalEligibleBySize {
		return outcome
	}

	maximum := int64(0)
	if imageEligible {
		maximum = int64(limits.MaxPayloadBytes)
	}
	if externalEligibleBySize && selected.maxInputBytes > maximum {
		maximum = selected.maxInputBytes
	}
	if maximum <= 0 || request.Materialize == nil {
		outcome.Code = CodeMaterializeUnavailable
		return outcome
	}
	leased, err := request.Materialize(ctx, maximum)
	if leased.Release != nil {
		defer func() {
			releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), leaseReleaseTimeout)
			defer cancel()
			if err := leased.Release(releaseCtx); err != nil {
				outcome.ReleaseFailed = true
			}
		}()
	}
	if err != nil {
		outcome.Code = CodeMaterializeFailed
		return outcome
	}
	info, err := validateLeasedMaterialization(leased, maximum, request.HasFileSize, request.FileSize)
	if err != nil {
		outcome.Code = CodeUnsafeMaterialization
		return outcome
	}

	if imageEligible {
		payload, readErr := readLeasedPayload(leased.Path, info, limits.MaxPayloadBytes)
		if readErr == nil {
			terminal, encodeErr := preview.EncodeTerminalImageWithProof(request.Capability, mediaType, payload, limits)
			if encodeErr == nil {
				outcome.Kind = OutcomeTerminalImage
				outcome.Code = ""
				outcome.TerminalBytes = terminal
				outcome.ImageProtocol = request.Capability.Protocol()
				return outcome
			}
		}
		outcome.Code = CodeImageFailed
	}
	if !externalEligibleBySize {
		return outcome
	}

	externalRequest.MaterializationPath = leased.Path
	outcome.External = runner.Run(ctx, externalRequest)
	if current, statErr := os.Lstat(leased.Path); statErr != nil || !os.SameFile(info, current) || current.Size() != info.Size() || current.ModTime() != info.ModTime() {
		outcome.External.Status = StatusRejected
		outcome.External.Code = CodeIdentityChanged
		outcome.External.Diagnostic = ""
		outcome.External.ExitCode = -1
		return outcome
	}
	if outcome.External.Status == StatusSucceeded {
		outcome.Kind = OutcomeExternal
		outcome.Code = ""
	}
	return outcome
}

func externalEligible(result preview.Result) bool {
	if result.View != "" && result.View != preview.ViewAuto {
		return false
	}
	return result.Kind == preview.KindImage || result.Kind == preview.KindBinary
}

func validImageLimits(limits preview.ImageOutputLimits) bool {
	return limits.MaxPayloadBytes > 0 && limits.MaxPayloadBytes <= math.MaxInt64 && limits.MaxOutputBytes > 0 && limits.ChunkBytes > 0 && limits.MaxPixels > 0
}

func validateLeasedMaterialization(leased LeasedMaterialization, maximum int64, hasFileSize bool, fileSize uint64) (os.FileInfo, error) {
	if !leased.Verified || !leased.Complete || leased.Release == nil || leased.Path == "" || !filepath.IsAbs(leased.Path) || filepath.Clean(leased.Path) != leased.Path {
		return nil, fmt.Errorf("invalid verified cache lease")
	}
	canonical, err := filepath.EvalSymlinks(leased.Path)
	if err != nil || canonical != leased.Path {
		return nil, fmt.Errorf("cache materialization is not a canonical no-symlink path")
	}
	info, err := os.Lstat(leased.Path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maximum {
		return nil, fmt.Errorf("cache materialization is not a bounded regular file")
	}
	if hasFileSize && (fileSize > math.MaxInt64 || info.Size() != int64(fileSize)) {
		return nil, fmt.Errorf("cache materialization size changed")
	}
	return info, nil
}

func readLeasedPayload(path string, expected os.FileInfo, maximum int) ([]byte, error) {
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open verified image: %w", err)
	}
	file := os.NewFile(uintptr(descriptor), "verified-preview-image")
	if file == nil {
		_ = unix.Close(descriptor)
		return nil, fmt.Errorf("open verified image: invalid descriptor")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(expected, opened) || opened.Size() > int64(maximum) {
		return nil, fmt.Errorf("verified image identity changed")
	}
	payload, err := io.ReadAll(io.LimitReader(file, int64(maximum)+1))
	if err != nil || len(payload) > maximum || int64(len(payload)) != opened.Size() {
		return nil, fmt.Errorf("read verified image exceeded its budget")
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(opened, after) || opened.Size() != after.Size() || opened.ModTime() != after.ModTime() {
		return nil, fmt.Errorf("verified image changed during read")
	}
	return payload, nil
}

func noMatchResult() Result {
	return Result{Status: StatusNoMatch, Code: CodeNoMatch, ExitCode: -1}
}

func rejectedExternal(rule frozenRule, code Code) Result {
	return Result{Matched: true, Rule: rule.name, Status: StatusRejected, Code: code, Executable: rule.command.Executable, ExitCode: -1}
}
