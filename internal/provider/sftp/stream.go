package sftp

import (
	"context"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
	"io"
)

var _ providerapi.StreamReadHandle = (*readHandle)(nil)
var _ providerapi.StreamWriteHandle = (*writeHandle)(nil)

// WriteFrom retains the protocol request window across reader calls. It drains
// every write acknowledgement before returning to the durability coordinator.
func (h *writeHandle) WriteFrom(ctx context.Context, source io.Reader) (int64, error) {
	return h.WriteFromWindow(ctx, source, providerapi.MaxSFTPWriteWindowRequests)
}

func (h *writeHandle) WriteFromWindow(ctx context.Context, source io.Reader, requests uint32) (int64, error) {
	if requests == 0 || requests > providerapi.MaxSFTPWriteWindowRequests {
		return 0, h.provider.invalid("write", &h.location, "stream request budget is outside 1..64")
	}
	if err := h.provider.check(ctx, "write", &h.location); err != nil {
		return 0, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return 0, h.provider.invalid("write", &h.location, "write handle is closed")
	}
	n, err := h.file.ReadFromWithConcurrency(contextStreamReader{ctx: ctx, reader: source}, int(requests))
	if err != nil {
		return n, h.provider.mapMutationError("write", &h.location, err, domain.EffectUnknown)
	}
	return n, nil
}

type contextStreamReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextStreamReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
