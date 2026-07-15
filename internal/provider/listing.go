package provider

import "github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"

// PageCursor is opaque. A non-empty cursor is valid only for the endpoint,
// canonical path, exact sort hint, and listing generation that created it.
// Binding mismatches are invalid arguments; stale generations are conflicts
// that require the caller to restart the listing.
type PageCursor string

type SortKey string

type SortDirection string

const (
	SortAscending  SortDirection = "ascending"
	SortDescending SortDirection = "descending"
)

type SortHint struct {
	Key              SortKey
	Direction        SortDirection
	DirectoriesFirst bool
}

// ListRequest requires a Limit from 1 through 4096. Sort keys are
// provider-defined, while direction uses the canonical values above.
type ListRequest struct {
	Location domain.Location
	Cursor   PageCursor
	Limit    uint32
	Sort     *SortHint
}

// ListPage is terminal exactly when Done is true and NextCursor is empty.
// Entries never exceed the request limit.
type ListPage struct {
	Entries              []domain.Entry
	NextCursor           PageCursor
	Done                 bool
	RequestedSortApplied bool
	Consistency          ListConsistency
	DirectoryFingerprint domain.Fingerprint
}

type ListConsistency string

const (
	ConsistencySnapshot   ListConsistency = "snapshot"
	ConsistencyBestEffort ListConsistency = "best_effort"
)
