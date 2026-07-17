package testkit

import (
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestConcurrentBufferAllowsSnapshotsDuringWrites(t *testing.T) {
	var buffer ConcurrentBuffer
	var writers sync.WaitGroup
	writers.Add(4)
	for writer := range 4 {
		go func() {
			defer writers.Done()
			for entry := range 1_000 {
				_, _ = buffer.Write([]byte(strconv.Itoa(writer*1_000 + entry)))
			}
		}()
	}

	for range 1_000 {
		_ = buffer.String()
	}
	writers.Wait()

	if strings.TrimSpace(buffer.String()) == "" {
		t.Fatal("concurrent buffer snapshot is empty after writes")
	}
}
