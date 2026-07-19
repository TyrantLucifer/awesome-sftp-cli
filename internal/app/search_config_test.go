package app

import (
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/search"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/tui"
)

func TestPendingSearchIdentitiesUseConfiguredBudgets(t *testing.T) {
	location, err := domain.NewLocation("ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", "/srv")
	if err != nil {
		t.Fatal(err)
	}
	intent := tui.Intent{Location: location, SearchPattern: "needle"}
	filenameBudget := search.Budget{PageItems: 1, EventBuffer: 1, ConcurrentLists: 1, MaxDepth: 1, MaxEntries: 1, MaxResults: 1, MaxOutputBytes: 1, MaxDuration: time.Millisecond}
	contentBudget := search.ContentBudget{PageItems: 2, EventBuffer: 2, MaxDepth: 2, MaxEntries: 2, MaxFiles: 2, MaxResults: 2, MaxMatchesPerFile: 2, MaxFileBytes: 2, MaxReadBytes: 2, MaxSnippetBytes: 2, MaxOutputBytes: 2, MaxDuration: 2 * time.Millisecond}
	requestID := domain.RequestID("req_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	if got := pendingFilenameSearchIdentity(requestID, 7, intent, filenameBudget).Budget; got != filenameBudget {
		t.Fatalf("filename budget = %#v, want %#v", got, filenameBudget)
	}
	if got := pendingContentSearchIdentity(requestID, 7, intent, contentBudget).Budget; got != contentBudget {
		t.Fatalf("content budget = %#v, want %#v", got, contentBudget)
	}
}
