package search

import "time"

// DefaultFilenameBudget freezes the resource envelope used by an interactive
// filename search before Stage 6 made that envelope configurable.
func DefaultFilenameBudget() Budget {
	return Budget{
		PageItems: 256, EventBuffer: 64, ConcurrentLists: 1, MaxDepth: 128,
		MaxEntries: 1_000_000, MaxResults: 10_000, MaxOutputBytes: 8 << 20,
		MaxDuration: 5 * time.Minute,
	}
}

// DefaultContentBudget freezes the resource envelope used by an interactive
// content search before Stage 6 made that envelope configurable.
func DefaultContentBudget() ContentBudget {
	return ContentBudget{
		PageItems: 128, EventBuffer: 32, MaxDepth: 32, MaxEntries: 10_000,
		MaxFiles: 1_000, MaxResults: 5_000, MaxMatchesPerFile: 100,
		MaxFileBytes: 1 << 20, MaxReadBytes: 32 << 20, MaxSnippetBytes: 512,
		MaxOutputBytes: 8 << 20, MaxDuration: 2 * time.Minute,
	}
}
