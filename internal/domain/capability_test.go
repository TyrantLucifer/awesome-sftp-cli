package domain

import "testing"

func TestNewCapabilitySnapshotRejectsInvalidRevision(t *testing.T) {
	tests := []struct {
		name     string
		revision CapabilityRevision
	}{
		{
			name:     "empty session",
			revision: CapabilityRevision{Generation: 1},
		},
		{
			name:     "zero generation",
			revision: CapabilityRevision{SessionID: "sess_aaaaaaaaaaaaaaaaaaaaaaaaaa"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewCapabilitySnapshot(test.revision, true, nil); err == nil {
				t.Fatal("NewCapabilitySnapshot() error = nil, want invalid-revision error")
			}
		})
	}
}

func TestNewCapabilitySnapshotRejectsDuplicateNames(t *testing.T) {
	items := []Capability{
		{Name: "hash", Version: 1},
		{Name: "search", Version: 1},
		{Name: "hash", Version: 2},
	}

	if _, err := NewCapabilitySnapshot(validCapabilityRevision(), true, items); err == nil {
		t.Fatal("NewCapabilitySnapshot() error = nil, want duplicate-name error")
	}
}

func TestNewCapabilitySnapshotSortsItemsAndLookupFindsByName(t *testing.T) {
	items := []Capability{
		{Name: "search", Version: 2},
		{Name: "hash", Version: 1},
		{Name: "watch", Version: 3},
	}

	snapshot, err := NewCapabilitySnapshot(validCapabilityRevision(), false, items)
	if err != nil {
		t.Fatalf("NewCapabilitySnapshot(): %v", err)
	}
	wantOrder := []CapabilityName{"hash", "search", "watch"}
	for index, want := range wantOrder {
		if got := snapshot.Items[index].Name; got != want {
			t.Fatalf("Items[%d].Name = %q, want %q", index, got, want)
		}
	}

	got, ok := snapshot.Lookup("search")
	if !ok {
		t.Fatal("Lookup(search) found = false, want true")
	}
	if got.Version != 2 {
		t.Fatalf("Lookup(search).Version = %d, want 2", got.Version)
	}
	if _, ok := snapshot.Lookup("missing"); ok {
		t.Fatal("Lookup(missing) found = true, want false")
	}
}

func TestNewCapabilitySnapshotDoesNotShareCallerOwnedSlices(t *testing.T) {
	constraints := []CapabilityConstraint{{Name: "algorithm", Value: "sha256"}}
	items := []Capability{{Name: "hash", Version: 1, Constraints: constraints}}

	snapshot, err := NewCapabilitySnapshot(validCapabilityRevision(), true, items)
	if err != nil {
		t.Fatalf("NewCapabilitySnapshot(): %v", err)
	}
	items[0].Name = "changed"
	constraints[0].Value = "changed"

	if got := snapshot.Items[0].Name; got != "hash" {
		t.Fatalf("snapshot item name after caller mutation = %q, want hash", got)
	}
	if got := snapshot.Items[0].Constraints[0].Value; got != "sha256" {
		t.Fatalf("snapshot constraint after caller mutation = %q, want sha256", got)
	}
}

func TestCapabilitySnapshotLookupReturnsIndependentConstraints(t *testing.T) {
	snapshot, err := NewCapabilitySnapshot(
		validCapabilityRevision(),
		true,
		[]Capability{{
			Name:        "hash",
			Version:     1,
			Constraints: []CapabilityConstraint{{Name: "algorithm", Value: "sha256"}},
		}},
	)
	if err != nil {
		t.Fatalf("NewCapabilitySnapshot(): %v", err)
	}

	first, ok := snapshot.Lookup("hash")
	if !ok {
		t.Fatal("Lookup(hash) found = false, want true")
	}
	first.Constraints[0].Value = "changed"
	second, ok := snapshot.Lookup("hash")
	if !ok {
		t.Fatal("second Lookup(hash) found = false, want true")
	}
	if got := second.Constraints[0].Value; got != "sha256" {
		t.Fatalf("second Lookup(hash) constraint = %q, want sha256", got)
	}
}

func validCapabilityRevision() CapabilityRevision {
	return CapabilityRevision{
		SessionID:  "sess_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		Generation: 1,
	}
}
