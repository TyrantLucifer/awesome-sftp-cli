package testkit

import (
	"bytes"
	"sync"
)

// ConcurrentBuffer is an io.Writer whose string snapshots may be read while
// another goroutine is still writing process output.
type ConcurrentBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *ConcurrentBuffer) Write(payload []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(payload)
}

func (buffer *ConcurrentBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}
