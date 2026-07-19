package provider

import (
	"context"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
)

// Provider is the minimum read-only endpoint contract. Descriptor must remain
// stable for the provider's lifetime. Implementations must honor context
// cancellation and return contextual *domain.OpError values.
//
// Snapshot capability revisions belong to Snapshot.SessionID. Generation is
// greater than zero and increases whenever capabilities change within that
// session. When Complete is false, a missing capability remains unknown.
type Provider interface {
	Descriptor() domain.Endpoint
	Snapshot(context.Context) (domain.EndpointSnapshot, error)
	Normalize(context.Context, domain.NormalizeRequest) (domain.Location, error)
	List(context.Context, ListRequest) (ListPage, error)
	Stat(context.Context, StatRequest) (domain.Entry, error)
	OpenRead(context.Context, OpenReadRequest) (ReadHandle, error)
}

// StatRequest selects lstat-like behavior when FollowSymlinks is false and
// target metadata when it is true.
type StatRequest struct {
	Location       domain.Location
	FollowSymlinks bool
}

// OpenReadRequest selects a byte range. Offset is non-negative. A nil Limit is
// unbounded, zero yields immediate EOF, and a positive limit caps bytes read.
// ExpectedFingerprint mismatches return a typed conflict before bytes escape.
type OpenReadRequest struct {
	Location            domain.Location
	Offset              int64
	Limit               *int64
	ExpectedFingerprint *domain.Fingerprint
}

type ReadInfo struct {
	Entry       domain.Entry
	Fingerprint domain.Fingerprint
}

// ReadHandle is a context-aware, bounded byte stream. Read follows io.Reader
// short-I/O and EOF conventions: callers must process n bytes before err and
// must not assume the buffer is filled. Close must be safe to call repeatedly.
type ReadHandle interface {
	Info() ReadInfo
	Read(context.Context, []byte) (int, error)
	Close(context.Context) error
}
