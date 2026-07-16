package cache

import (
	"crypto/sha256"
	"strings"
	"testing"
	"time"
)

func TestBlobIDFromDigestUsesLowerSHA256Hex(t *testing.T) {
	t.Parallel()

	digest := sha256.Sum256([]byte("verified content"))
	const expected = BlobID("034311adcf7e54dc9c9d35f583590b4c865b4b7ffa132b2acf9812c5a509f779")
	if got := BlobIDFromDigest(digest); got != expected {
		t.Fatalf("blob ID = %q, want %q", got, expected)
	}
}

func TestTypedIDParsersEnforceFrozenLowerHexWidths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		parse func(string) error
		valid string
	}{
		{name: "blob", parse: func(value string) error { _, err := ParseBlobID(value); return err }, valid: strings.Repeat("a", 64)},
		{name: "entry", parse: func(value string) error { _, err := ParseEntryID(value); return err }, valid: strings.Repeat("b", 64)},
		{name: "materialization", parse: func(value string) error { _, err := ParseMaterializationID(value); return err }, valid: strings.Repeat("c", 32)},
		{name: "reference", parse: func(value string) error { _, err := ParseReferenceID(value); return err }, valid: strings.Repeat("d", 32)},
		{name: "lease", parse: func(value string) error { _, err := ParseLeaseID(value); return err }, valid: strings.Repeat("e", 32)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.parse(test.valid); err != nil {
				t.Fatalf("parse valid ID: %v", err)
			}
			if err := test.parse(strings.ToUpper(test.valid)); err == nil {
				t.Fatal("uppercase ID accepted")
			}
			if err := test.parse(test.valid[:len(test.valid)-1]); err == nil {
				t.Fatal("short ID accepted")
			}
		})
	}
}

func TestDeriveEntryIDBindsLocationAndFingerprint(t *testing.T) {
	t.Parallel()

	first, err := DeriveEntryID("endpoint_local", []byte("/srv/report.txt"), []byte("strong:size=4;sha256=aaaa"))
	if err != nil {
		t.Fatalf("derive entry ID: %v", err)
	}
	again, err := DeriveEntryID("endpoint_local", []byte("/srv/report.txt"), []byte("strong:size=4;sha256=aaaa"))
	if err != nil {
		t.Fatalf("derive same entry ID: %v", err)
	}
	if first != again {
		t.Fatalf("entry ID is not deterministic: %q != %q", first, again)
	}
	const expected = EntryID("693cffde05672c57dbdcdb616417287e6127f169d9e512587e0469b58c1cc922")
	if first != expected {
		t.Fatalf("entry ID = %q, want frozen encoding %q", first, expected)
	}

	differentFingerprint, err := DeriveEntryID("endpoint_local", []byte("/srv/report.txt"), []byte("strong:size=4;sha256=bbbb"))
	if err != nil {
		t.Fatalf("derive changed fingerprint: %v", err)
	}
	differentPath, err := DeriveEntryID("endpoint_local", []byte("/srv/other.txt"), []byte("strong:size=4;sha256=aaaa"))
	if err != nil {
		t.Fatalf("derive changed path: %v", err)
	}
	if first == differentFingerprint || first == differentPath {
		t.Fatal("entry ID did not bind both path and fingerprint")
	}
}

func TestSnapshotValidateAcceptsCompleteTypedGraph(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	blobID := testBlobID('a')
	entryID := testEntryID('b')
	materializationID := testMaterializationID('c')
	snapshot := Snapshot{
		Blobs: []Blob{{
			ID: blobID, Size: 64, State: BlobPublished,
			CreatedAt: now, LastAccessAt: now,
		}},
		Entries: []Entry{{
			ID: entryID, EndpointID: "endpoint_local", CanonicalPath: []byte("/tmp/data"),
			Fingerprint: Fingerprint{Strength: FingerprintStrong, Canonical: []byte("size=64;sha256=aaaa")},
			Freshness:   EntryFresh, Policy: PolicyLRU, WorkspaceID: "workspace-a", BlobID: blobID,
			CreatedAt: now, LastAccessAt: now,
		}},
		Materializations: []Materialization{{
			ID: materializationID, EntryID: entryID, BaselineBlobID: blobID,
			CurrentBlobID: blobID, Size: 64, State: MaterializationClean,
			CreatedAt: now, LastAccessAt: now,
		}},
		References: []Reference{{
			ID: testReferenceID('d'), OwnerKind: ReferenceOwnerEdit, OwnerID: "edit-1",
			Target: MaterializationTarget(materializationID), CreatedAt: now,
		}},
		Leases: []Lease{{
			ID: testLeaseID('e'), OwnerKind: LeaseOwnerEditor, OwnerID: "editor-1",
			DaemonInstanceID: "daemon-1", Target: BlobTarget(blobID), State: LeaseActive,
			HeartbeatAt: now, ExpiresAt: now.Add(2 * time.Minute), GraceUntil: now.Add(3 * time.Minute),
			Process: &ProcessIdentity{PID: 4321, BirthID: "linux-start-ticks:12345"},
		}},
	}

	if err := snapshot.Validate(); err != nil {
		t.Fatalf("validate complete snapshot: %v", err)
	}
}

func TestSnapshotValidateRejectsBrokenReachability(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	snapshot := Snapshot{
		Entries: []Entry{{
			ID: testEntryID('a'), EndpointID: "endpoint_local", CanonicalPath: []byte("/missing"),
			Fingerprint: Fingerprint{Strength: FingerprintStrong, Canonical: []byte("size=1;sha256=aaaa")},
			Freshness:   EntryFresh, Policy: PolicyLRU, WorkspaceID: "workspace-a",
			BlobID: testBlobID('f'), CreatedAt: now, LastAccessAt: now,
		}},
	}
	if err := snapshot.Validate(); err == nil || !strings.Contains(err.Error(), "missing blob") {
		t.Fatalf("broken reachability error = %v, want missing blob", err)
	}
}

func TestRecordValidationRejectsUnsafeStateCombinations(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	entry := Entry{
		ID: testEntryID('a'), EndpointID: "endpoint_local", CanonicalPath: []byte("/offline"),
		Fingerprint: Fingerprint{Strength: FingerprintStrong, Canonical: []byte("strong")},
		Freshness:   EntryFresh, Policy: PolicyPinnedOffline, WorkspaceID: "workspace-a",
		BlobID: testBlobID('b'), CreatedAt: now, LastAccessAt: now,
	}
	if err := entry.Validate(); err == nil || !strings.Contains(err.Error(), "pinned") {
		t.Fatalf("unpinned offline entry error = %v", err)
	}

	target := Target{BlobID: testBlobID('c'), MaterializationID: testMaterializationID('d')}
	if err := target.Validate(); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("ambiguous target error = %v", err)
	}

	lease := Lease{
		ID: testLeaseID('e'), OwnerKind: LeaseOwnerEditor, OwnerID: "editor-1",
		DaemonInstanceID: "daemon-1", Target: BlobTarget(testBlobID('f')), State: LeaseActive,
		HeartbeatAt: now, ExpiresAt: now.Add(time.Minute), GraceUntil: now.Add(2 * time.Minute),
		Process: &ProcessIdentity{PID: 0, BirthID: "birth"},
	}
	if err := lease.Validate(); err == nil || !strings.Contains(err.Error(), "PID") {
		t.Fatalf("invalid process identity error = %v", err)
	}
}

func testBlobID(value byte) BlobID {
	return BlobID(strings.Repeat(string(value), 64))
}

func testEntryID(value byte) EntryID {
	return EntryID(strings.Repeat(string(value), 64))
}

func testMaterializationID(value byte) MaterializationID {
	return MaterializationID(strings.Repeat(string(value), 32))
}

func testReferenceID(value byte) ReferenceID {
	return ReferenceID(strings.Repeat(string(value), 32))
}

func testLeaseID(value byte) LeaseID {
	return LeaseID(strings.Repeat(string(value), 32))
}
