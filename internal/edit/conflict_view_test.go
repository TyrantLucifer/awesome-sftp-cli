package edit

import (
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
)

func TestBuildConflictViewShowsRemoteToLocalDiff(t *testing.T) {
	view := BuildConflictView(ConflictViewRequest{
		Local:  []byte("first\nlocal edit\nlast\n"),
		Remote: []byte("first\nremote edit\nlast\n"),
		RemoteObservation: RemoteObservation{
			Status: RemotePresent,
			Kind:   domain.EntryFile,
		},
	})
	if !strings.Contains(view.Text, "--- remote\n+++ local\n") ||
		!strings.Contains(view.Text, "-remote edit\n+local edit\n") {
		t.Fatalf("conflict view did not show the overwrite effect:\n%s", view.Text)
	}
	if view.Truncated || view.Summary != "remote → local conflict diff" {
		t.Fatalf("conflict view metadata = %#v", view)
	}
}

func TestBuildConflictViewDescribesDeletedAndReplacedRemote(t *testing.T) {
	tests := []struct {
		name        string
		observation RemoteObservation
		want        string
	}{
		{name: "deleted", observation: RemoteObservation{Status: RemoteDeleted}, want: "--- remote (deleted)"},
		{name: "replaced by symlink", observation: RemoteObservation{Status: RemotePresent, Kind: domain.EntrySymlink}, want: "--- remote (replaced by symlink)"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			view := BuildConflictView(ConflictViewRequest{Local: []byte("retained local edit\n"), RemoteObservation: test.observation})
			if !strings.Contains(view.Text, test.want) || !strings.Contains(view.Text, "+retained local edit") {
				t.Fatalf("conflict view = %q", view.Text)
			}
		})
	}
}

func TestBuildConflictViewEnforcesInputLineAndOutputBudgets(t *testing.T) {
	oversized := []byte(strings.Repeat("remote line\n", ConflictViewLineLimit+100))
	view := BuildConflictView(ConflictViewRequest{
		Local:             []byte(strings.Repeat("local line\n", ConflictViewLineLimit+100)),
		Remote:            oversized,
		RemoteObservation: RemoteObservation{Status: RemotePresent, Kind: domain.EntryFile},
	})
	if !view.Truncated || len(view.Text) > ConflictViewOutputByteLimit {
		t.Fatalf("bounded view = truncated %v bytes %d", view.Truncated, len(view.Text))
	}
	if !strings.Contains(view.Summary, "bounded") {
		t.Fatalf("summary = %q", view.Summary)
	}
}
