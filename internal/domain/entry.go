package domain

import "time"

type EntryKind string

const (
	EntryFile      EntryKind = "file"
	EntryDirectory EntryKind = "directory"
	EntrySymlink   EntryKind = "symlink"
	EntryOther     EntryKind = "other"
)

type TimePrecision string

type Metadata struct {
	Size              *uint64
	Mode              *uint32
	UID               *uint32
	GID               *uint32
	ModifiedAt        *time.Time
	ModifiedPrecision *TimePrecision
	FileID            *string
}

type SymlinkInfo struct {
	RawTarget    string
	ResolvedKind *EntryKind
}

type Entry struct {
	Location    Location
	Name        string
	Kind        EntryKind
	Metadata    Metadata
	Fingerprint Fingerprint
	Symlink     *SymlinkInfo
}
