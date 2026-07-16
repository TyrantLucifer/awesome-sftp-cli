package preview

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
)

func TestPlanHeadTailAndRangeUseExactBoundedOffsets(t *testing.T) {
	const fileSize = uint64(100) << 30

	head := PlanHead(fileSize)
	if head.Mode != ReadHead || head.Offset != 0 || head.Limit != ReadChunkBytes || head.RetainOffset != 0 || head.RetainBytes != ReadChunkBytes || head.Complete {
		t.Fatalf("head = %#v", head)
	}

	tail := PlanTail(fileSize)
	if tail.Mode != ReadTail || tail.Offset != fileSize-ReadChunkBytes || tail.Limit != ReadChunkBytes || tail.RetainOffset != tail.Offset || tail.RetainBytes != ReadChunkBytes || tail.Complete {
		t.Fatalf("tail = %#v", tail)
	}

	ranged, err := PlanRange(fileSize, 12345, ReadChunkBytes+1)
	if err != nil {
		t.Fatal(err)
	}
	if ranged.Mode != ReadRange || ranged.Offset != 12345 || ranged.Limit != ReadChunkBytes || ranged.RetainOffset != 12345 || ranged.RetainBytes != ReadChunkBytes {
		t.Fatalf("range = %#v", ranged)
	}
}

func TestPlanReadBoundariesDoNotOverflowOrReadPastEOF(t *testing.T) {
	for _, size := range []uint64{0, 1, ReadChunkBytes - 1, ReadChunkBytes, ReadChunkBytes + 1, math.MaxUint64} {
		head := PlanHead(size)
		if head.Offset > size || head.Limit > size-head.Offset || head.Limit > ReadChunkBytes {
			t.Fatalf("head size %d = %#v", size, head)
		}
		tail := PlanTail(size)
		if tail.Offset > size || tail.Limit > size-tail.Offset || tail.Limit > ReadChunkBytes {
			t.Fatalf("tail size %d = %#v", size, tail)
		}
	}

	if _, err := PlanRange(10, 11, 1); err == nil {
		t.Fatal("range past EOF succeeded")
	}
	if _, err := PlanRange(10, 0, 0); err == nil {
		t.Fatal("zero range limit succeeded")
	}
	last, err := PlanRange(math.MaxUint64, math.MaxUint64-1, math.MaxUint64)
	if err != nil {
		t.Fatal(err)
	}
	if last.Offset != math.MaxUint64-1 || last.Limit != 1 || last.Complete {
		t.Fatalf("last-byte range = %#v", last)
	}
}

func TestPlanContinueUsesChunksAndSlidesAtRetainedCap(t *testing.T) {
	const fileSize = uint64(2) * MaxRetainedBytes
	retained := RetainedWindow{Offset: 0, Bytes: MaxRetainedBytes}
	plan, err := PlanContinue(fileSize, retained)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != ReadContinue || plan.Offset != MaxRetainedBytes || plan.Limit != ReadChunkBytes {
		t.Fatalf("continue read = %#v", plan)
	}
	if plan.DiscardBytes != ReadChunkBytes || plan.RetainOffset != ReadChunkBytes || plan.RetainBytes != MaxRetainedBytes {
		t.Fatalf("continue retention = %#v", plan)
	}

	if _, err := PlanContinue(fileSize, RetainedWindow{Offset: 1, Bytes: MaxRetainedBytes + 1}); err == nil {
		t.Fatal("oversize retained window succeeded")
	}
	if _, err := PlanContinue(10, RetainedWindow{Offset: 9, Bytes: 2}); err == nil {
		t.Fatal("retained window past EOF succeeded")
	}
	if _, err := PlanContinue(10, RetainedWindow{Offset: 0, Bytes: 10}); err == nil || !strings.Contains(err.Error(), "end of file") {
		t.Fatalf("continue at EOF error = %v", err)
	}
}

func TestFreezeSourceOwnsAndCanonicalizesFingerprintIdentity(t *testing.T) {
	location, err := domain.NewLocation("ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", "/srv/file")
	if err != nil {
		t.Fatal(err)
	}
	size := uint64(42)
	modified := time.Date(2026, 7, 16, 8, 0, 0, 123, time.FixedZone("offset", 8*60*60))
	precision := domain.TimePrecision("nanosecond")
	fileID := "node-7"
	versionID := "version-1"
	algorithm := "sha256"
	hashHex := strings.Repeat("a", 64)
	fingerprint := domain.Fingerprint{
		Size: &size, ModifiedAt: &modified, ModifiedPrecision: &precision, FileID: &fileID,
		VersionID: &versionID, HashAlgorithm: &algorithm, HashHex: &hashHex,
	}
	frozen, err := FreezeSource(location, fingerprint)
	if err != nil {
		t.Fatal(err)
	}

	size = 99
	modified = modified.Add(time.Hour)
	fileID = "mutated"
	if !frozen.Matches(location, domain.Fingerprint{
		Size:              pointer(uint64(42)),
		ModifiedAt:        pointer(time.Date(2026, 7, 16, 0, 0, 0, 123, time.UTC)),
		ModifiedPrecision: pointer(domain.TimePrecision("nanosecond")),
		FileID:            pointer("node-7"), VersionID: pointer("version-1"),
		HashAlgorithm: pointer("sha256"), HashHex: pointer(strings.Repeat("a", 64)),
	}) {
		t.Fatalf("frozen source did not retain the original identity: %#v", frozen)
	}
	if frozen.Matches(location, fingerprint) {
		t.Fatal("frozen source matched caller-mutated fingerprint")
	}
}

func TestFreezeSourceRejectsMissingOrMalformedIdentity(t *testing.T) {
	location := domain.Location{EndpointID: "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", Path: "/srv/file"}
	if _, err := FreezeSource(location, domain.Fingerprint{}); err == nil {
		t.Fatal("empty fingerprint succeeded")
	}
	if _, err := FreezeSource(domain.Location{}, domain.Fingerprint{Size: pointer(uint64(1))}); err == nil {
		t.Fatal("empty location succeeded")
	}
	if _, err := FreezeSource(location, domain.Fingerprint{HashAlgorithm: pointer("sha256")}); err == nil {
		t.Fatal("half hash fingerprint succeeded")
	}
}

func pointer[T any](value T) *T {
	return &value
}
