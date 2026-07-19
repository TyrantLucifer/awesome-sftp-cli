package search

import (
	"reflect"
	"testing"
	"time"
)

func TestDefaultSearchBudgetsFreezeCurrentClientBehavior(t *testing.T) {
	wantFilename := Budget{
		PageItems: 256, EventBuffer: 64, ConcurrentLists: 1, MaxDepth: 128,
		MaxEntries: 1_000_000, MaxResults: 10_000, MaxOutputBytes: 8 << 20,
		MaxDuration: 5 * time.Minute,
	}
	wantContent := ContentBudget{
		PageItems: 128, EventBuffer: 32, MaxDepth: 32, MaxEntries: 10_000,
		MaxFiles: 1_000, MaxResults: 5_000, MaxMatchesPerFile: 100,
		MaxFileBytes: 1 << 20, MaxReadBytes: 32 << 20, MaxSnippetBytes: 512,
		MaxOutputBytes: 8 << 20, MaxDuration: 2 * time.Minute,
	}
	if got := DefaultFilenameBudget(); !reflect.DeepEqual(got, wantFilename) {
		t.Fatalf("filename defaults = %#v, want %#v", got, wantFilename)
	}
	if got := DefaultContentBudget(); !reflect.DeepEqual(got, wantContent) {
		t.Fatalf("content defaults = %#v, want %#v", got, wantContent)
	}
}
