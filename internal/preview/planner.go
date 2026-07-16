package preview

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
)

const (
	ReadChunkBytes   uint64 = 64 * 1024
	MaxRetainedBytes uint64 = 512 * 1024

	maxFingerprintComponentBytes = 4 * 1024
)

type ReadMode string

const (
	ReadHead     ReadMode = "head"
	ReadTail     ReadMode = "tail"
	ReadRange    ReadMode = "range"
	ReadContinue ReadMode = "continue"
)

// RetainedWindow describes the contiguous source bytes currently retained by
// one Preview. It never represents more than MaxRetainedBytes.
type RetainedWindow struct {
	Offset uint64
	Bytes  uint64
}

// ReadPlan is a bounded provider range read plus the retained window that will
// result after merging the response. DiscardBytes is removed from the oldest
// retained prefix before the newly read bytes are appended.
type ReadPlan struct {
	Mode         ReadMode
	Offset       uint64
	Limit        uint64
	RetainOffset uint64
	RetainBytes  uint64
	DiscardBytes uint64
	Complete     bool
}

// ReadWindow owns the bounded bytes retained for one Preview after a read.
// Data never exceeds MaxRetainedBytes and is never aliased with provider or
// caller buffers.
type ReadWindow struct {
	Offset   uint64
	Data     []byte
	Complete bool
}

func PlanHead(fileSize uint64) ReadPlan {
	return initialReadPlan(ReadHead, fileSize, 0, min(fileSize, ReadChunkBytes))
}

func PlanTail(fileSize uint64) ReadPlan {
	limit := min(fileSize, ReadChunkBytes)
	return initialReadPlan(ReadTail, fileSize, fileSize-limit, limit)
}

func PlanRange(fileSize, offset, requestedLimit uint64) (ReadPlan, error) {
	if requestedLimit == 0 {
		return ReadPlan{}, fmt.Errorf("plan preview range: requested limit must be positive")
	}
	if offset > fileSize {
		return ReadPlan{}, fmt.Errorf("plan preview range: offset %d exceeds file size %d", offset, fileSize)
	}
	limit := min(requestedLimit, ReadChunkBytes)
	limit = min(limit, fileSize-offset)
	return initialReadPlan(ReadRange, fileSize, offset, limit), nil
}

func PlanContinue(fileSize uint64, retained RetainedWindow) (ReadPlan, error) {
	if retained.Bytes == 0 {
		return ReadPlan{}, fmt.Errorf("plan preview continue: retained window is empty")
	}
	if retained.Bytes > MaxRetainedBytes {
		return ReadPlan{}, fmt.Errorf("plan preview continue: retained bytes %d exceed cap %d", retained.Bytes, MaxRetainedBytes)
	}
	if retained.Offset > fileSize || retained.Bytes > fileSize-retained.Offset {
		return ReadPlan{}, fmt.Errorf("plan preview continue: retained window exceeds file size %d", fileSize)
	}
	readOffset := retained.Offset + retained.Bytes
	if readOffset == fileSize {
		return ReadPlan{}, fmt.Errorf("plan preview continue: retained window is already at end of file")
	}
	readBytes := min(ReadChunkBytes, fileSize-readOffset)
	combinedBytes := retained.Bytes + readBytes
	discardBytes := uint64(0)
	if combinedBytes > MaxRetainedBytes {
		discardBytes = combinedBytes - MaxRetainedBytes
		combinedBytes = MaxRetainedBytes
	}
	retainOffset := retained.Offset + discardBytes
	return ReadPlan{
		Mode:         ReadContinue,
		Offset:       readOffset,
		Limit:        readBytes,
		RetainOffset: retainOffset,
		RetainBytes:  combinedBytes,
		DiscardBytes: discardBytes,
		Complete:     retainOffset == 0 && combinedBytes == fileSize,
	}, nil
}

// ApplyReadPlan validates an exact provider response and produces the next
// retained window. A short response is rejected because the file size used to
// make the plan is part of the frozen Preview identity; accepting it would
// silently render bytes from a changed or incompletely read source.
func ApplyReadPlan(prior ReadWindow, plan ReadPlan, response []byte) (ReadWindow, error) {
	if plan.Limit > ReadChunkBytes {
		return ReadWindow{}, fmt.Errorf("apply preview read: limit %d exceeds chunk cap %d", plan.Limit, ReadChunkBytes)
	}
	if plan.RetainBytes > MaxRetainedBytes {
		return ReadWindow{}, fmt.Errorf("apply preview read: retained bytes %d exceed cap %d", plan.RetainBytes, MaxRetainedBytes)
	}
	if uint64(len(response)) != plan.Limit { //nolint:gosec // slice length is non-negative
		return ReadWindow{}, fmt.Errorf("apply preview read: response bytes %d do not match planned limit %d", len(response), plan.Limit)
	}

	switch plan.Mode {
	case ReadHead, ReadTail, ReadRange:
		if len(prior.Data) != 0 || prior.Offset != 0 || prior.Complete {
			return ReadWindow{}, fmt.Errorf("apply preview read: initial read has a prior window")
		}
		if plan.DiscardBytes != 0 || plan.RetainOffset != plan.Offset || plan.RetainBytes != plan.Limit {
			return ReadWindow{}, fmt.Errorf("apply preview read: inconsistent initial retention")
		}
		return ReadWindow{Offset: plan.RetainOffset, Data: append([]byte(nil), response...), Complete: plan.Complete}, nil
	case ReadContinue:
		return applyContinueRead(prior, plan, response)
	default:
		return ReadWindow{}, fmt.Errorf("apply preview read: unsupported mode %q", plan.Mode)
	}
}

func applyContinueRead(prior ReadWindow, plan ReadPlan, response []byte) (ReadWindow, error) {
	priorBytes := uint64(len(prior.Data)) //nolint:gosec // slice length is non-negative
	if priorBytes == 0 || priorBytes > MaxRetainedBytes {
		return ReadWindow{}, fmt.Errorf("apply preview read: prior retained bytes must be in [1,%d]", MaxRetainedBytes)
	}
	if prior.Offset > ^uint64(0)-priorBytes || plan.Offset != prior.Offset+priorBytes {
		return ReadWindow{}, fmt.Errorf("apply preview read: continue offset does not follow prior window")
	}
	if plan.DiscardBytes > priorBytes || prior.Offset > ^uint64(0)-plan.DiscardBytes || plan.RetainOffset != prior.Offset+plan.DiscardBytes {
		return ReadWindow{}, fmt.Errorf("apply preview read: invalid discarded prefix")
	}
	retainedPrior := priorBytes - plan.DiscardBytes
	if retainedPrior > MaxRetainedBytes-plan.Limit || plan.RetainBytes != retainedPrior+plan.Limit {
		return ReadWindow{}, fmt.Errorf("apply preview read: inconsistent continue retention")
	}
	if plan.RetainBytes > math.MaxInt || plan.DiscardBytes > math.MaxInt {
		return ReadWindow{}, fmt.Errorf("apply preview read: retained byte counts exceed platform limits")
	}
	data := make([]byte, 0, int(plan.RetainBytes))
	data = append(data, prior.Data[int(plan.DiscardBytes):]...)
	data = append(data, response...)
	return ReadWindow{Offset: plan.RetainOffset, Data: data, Complete: plan.Complete}, nil
}

func initialReadPlan(mode ReadMode, fileSize, offset, limit uint64) ReadPlan {
	return ReadPlan{
		Mode: mode, Offset: offset, Limit: limit,
		RetainOffset: offset, RetainBytes: limit,
		Complete: offset == 0 && limit == fileSize,
	}
}

// FrozenFingerprint is a comparable, pointer-free copy of a provider
// fingerprint. Presence bits preserve nil versus present zero/empty values.
type FrozenFingerprint struct {
	HasSize bool
	Size    uint64

	HasModifiedAt     bool
	ModifiedAt        time.Time
	ModifiedPrecision domain.TimePrecision

	HasFileID bool
	FileID    string

	HasVersionID bool
	VersionID    string

	HasHash       bool
	HashAlgorithm string
	HashHex       string
}

// FrozenSource binds one canonical Location to the exact fingerprint observed
// when a Preview was requested. Its fields contain no caller-owned pointers.
type FrozenSource struct {
	Location    domain.Location
	Fingerprint FrozenFingerprint
}

func FreezeSource(location domain.Location, fingerprint domain.Fingerprint) (FrozenSource, error) {
	canonical, err := domain.NewLocation(location.EndpointID, location.Path)
	if err != nil {
		return FrozenSource{}, fmt.Errorf("freeze preview source: %w", err)
	}
	frozen, err := freezeFingerprint(fingerprint)
	if err != nil {
		return FrozenSource{}, err
	}
	canonical.EndpointID = domain.EndpointID(strings.Clone(string(canonical.EndpointID)))
	canonical.Path = domain.CanonicalPath(strings.Clone(string(canonical.Path)))
	return FrozenSource{Location: canonical, Fingerprint: frozen}, nil
}

func (source FrozenSource) Matches(location domain.Location, fingerprint domain.Fingerprint) bool {
	other, err := FreezeSource(location, fingerprint)
	return err == nil && source == other
}

func freezeFingerprint(fingerprint domain.Fingerprint) (FrozenFingerprint, error) {
	if fingerprint.ModifiedAt == nil != (fingerprint.ModifiedPrecision == nil) {
		return FrozenFingerprint{}, fmt.Errorf("freeze preview source: modified time and precision must both be present")
	}
	if fingerprint.HashAlgorithm == nil != (fingerprint.HashHex == nil) {
		return FrozenFingerprint{}, fmt.Errorf("freeze preview source: hash algorithm and value must both be present")
	}
	if fingerprint.Size == nil && fingerprint.ModifiedAt == nil && fingerprint.FileID == nil && fingerprint.VersionID == nil && fingerprint.HashAlgorithm == nil {
		return FrozenFingerprint{}, fmt.Errorf("freeze preview source: fingerprint is empty")
	}

	var frozen FrozenFingerprint
	if fingerprint.Size != nil {
		frozen.HasSize = true
		frozen.Size = *fingerprint.Size
	}
	if fingerprint.ModifiedAt != nil {
		frozen.HasModifiedAt = true
		frozen.ModifiedAt = fingerprint.ModifiedAt.Round(0).UTC()
		frozen.ModifiedPrecision = *fingerprint.ModifiedPrecision
	}
	var err error
	if fingerprint.FileID != nil {
		frozen.HasFileID = true
		frozen.FileID, err = cloneFingerprintComponent("file ID", *fingerprint.FileID)
		if err != nil {
			return FrozenFingerprint{}, err
		}
	}
	if fingerprint.VersionID != nil {
		frozen.HasVersionID = true
		frozen.VersionID, err = cloneFingerprintComponent("version ID", *fingerprint.VersionID)
		if err != nil {
			return FrozenFingerprint{}, err
		}
	}
	if fingerprint.HashAlgorithm != nil {
		frozen.HasHash = true
		frozen.HashAlgorithm, err = cloneFingerprintComponent("hash algorithm", *fingerprint.HashAlgorithm)
		if err != nil {
			return FrozenFingerprint{}, err
		}
		frozen.HashHex, err = cloneFingerprintComponent("hash value", *fingerprint.HashHex)
		if err != nil {
			return FrozenFingerprint{}, err
		}
	}
	return frozen, nil
}

func cloneFingerprintComponent(name, value string) (string, error) {
	if len(value) == 0 || len(value) > maxFingerprintComponentBytes {
		return "", fmt.Errorf("freeze preview source: %s length must be in [1,%d]", name, maxFingerprintComponentBytes)
	}
	return strings.Clone(value), nil
}
