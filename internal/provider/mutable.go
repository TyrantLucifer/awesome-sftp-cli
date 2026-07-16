package provider

import (
	"context"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
)

// MutableProvider is an optional facet. Read-only consumers depend only on
// Provider. Mutating callers must require both this facet and the current
// capability revision rather than infer write support from either one alone.
type MutableProvider interface {
	OpenWrite(context.Context, OpenWriteRequest) (WriteHandle, error)
	Mkdir(context.Context, MkdirRequest) (domain.Entry, error)
	Rename(context.Context, RenameRequest) (RenameResult, error)
	Remove(context.Context, RemoveRequest) error
}

// TrashProvider is an optional mutation facet. Callers may use it only when
// the same frozen capability snapshot explicitly advertises "trash".
type TrashProvider interface {
	Trash(context.Context, TrashRequest) error
}

type TrashRequest struct {
	Location domain.Location
	Expected *domain.Fingerprint
}

type WriteDisposition string

const (
	WriteCreateNew      WriteDisposition = "create_new"
	WriteResumeExisting WriteDisposition = "resume_existing"
	WriteTruncate       WriteDisposition = "truncate"
)

// OpenWriteRequest selects a non-negative byte offset and an explicit
// disposition. ExpectedFingerprint protects conditional writes.
type OpenWriteRequest struct {
	Location            domain.Location
	Offset              int64
	Disposition         WriteDisposition
	ExpectedFingerprint *domain.Fingerprint
}

// WriteHandle accepts short writes. Callers must retry unwritten bytes. Sync
// reports durable-flush failures, and Close must be idempotent.
type WriteHandle interface {
	Write(context.Context, []byte) (int, error)
	Sync(context.Context) error
	Close(context.Context) error
}

type MkdirRequest struct {
	Location  domain.Location
	Exclusive bool
}

type RenameRequest struct {
	Source              domain.Location
	Destination         domain.Location
	Replace             bool
	ExpectedSource      *domain.Fingerprint
	ExpectedDestination *domain.Fingerprint
}

type RenameResult struct {
	Atomic   bool
	Replaced bool
}

// RemoveRequest addresses one file or one empty directory. Remove never
// recurses; recursive planning belongs above the Provider boundary.
type RemoveRequest struct {
	Location domain.Location
	Expected *domain.Fingerprint
}
