package statefs

import (
	"context"
	"strings"
	"testing"
)

func TestProbeAfterIdentityProvesWALAndLeavesNoArtifacts(t *testing.T) {
	t.Parallel()

	root := privateTempDir(t)
	before := directoryEntries(t, root)
	err := ProbeAfterIdentity(context.Background(), root, ProbeConfig{Random: strings.NewReader(strings.Repeat("p", probeRandomBytes))})
	if err != nil {
		t.Fatalf("ProbeAfterIdentity(): %v", err)
	}
	after := directoryEntries(t, root)
	if len(before) != len(after) {
		t.Fatalf("probe artifacts remain: before=%v after=%v", before, after)
	}
}
