package provider

import (
	"context"
	"io"
)

// ReadStreamOptions applies admission before each remote data request, including
// read-ahead. Options are fixed for the lifetime of an opened read stream.
type ReadStreamOptions struct {
	MaxBytes uint32
	// MaxRequestBytes optionally tightens packet size without expanding concurrency.
	MaxRequestBytes uint32
	BeforeRead      func(context.Context, uint32) error
}

// StreamReadHandle keeps a bounded request window while applying rate admission
// before requests enter the transport. It must never fall back to unmetered I/O.
type StreamReadHandle interface {
	ReadHandle
	ReadStream(context.Context, []byte, ReadStreamOptions) (int, error)
}

// StreamWriteHandle consumes a stream with a bounded request window. A successful
// return means all consumed bytes have been acknowledged, but not synchronized.
// On error, the destination may contain a suffix beyond the last checkpoint;
// callers must not infer durable progress from the returned byte count.
type StreamWriteHandle interface {
	WriteHandle
	WriteFrom(context.Context, io.Reader) (int64, error)
}

// TruncatingWriteHandle rolls a proven Job-owned suffix back to a durable
// prefix. It must reject expansion and operate on the already checked handle.
type TruncatingWriteHandle interface {
	WriteHandle
	Truncate(context.Context, int64) error
}
