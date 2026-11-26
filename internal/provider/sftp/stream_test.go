package sftp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

func TestWriteStreamConsumesReaderAndAcknowledgesContent(t *testing.T) {
	fixture := (contractFactory{}).New(t)
	location := domain.Location{EndpointID: testEndpointID, Path: "/stream.bin"}
	handle, err := fixture.Provider.(providerapi.MutableProvider).OpenWrite(context.Background(), providerapi.OpenWriteRequest{Location: location, Disposition: providerapi.WriteCreateNew})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close(context.Background())
	stream, ok := handle.(providerapi.StreamWriteHandle)
	if !ok {
		t.Fatal("SFTP write handle does not expose a bounded stream")
	}
	payload := bytes.Repeat([]byte("stream-content"), 1<<16)
	n, err := stream.WriteFrom(context.Background(), bytes.NewReader(payload))
	if err != nil || n != int64(len(payload)) {
		t.Fatalf("WriteFrom = %d, %v", n, err)
	}
	if err := handle.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	reader, err := fixture.Provider.OpenRead(context.Background(), providerapi.OpenReadRequest{Location: location})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close(context.Background())
	got := make([]byte, len(payload))
	nread, err := reader.Read(context.Background(), got)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if nread != len(payload) || !bytes.Equal(got, payload) {
		t.Fatal("acknowledged stream differs from destination")
	}
}

func TestReadStreamHonorsAdmissionFailureBeforeDataRequest(t *testing.T) {
	fixture := (contractFactory{}).New(t)
	handle, err := fixture.Provider.OpenRead(context.Background(), providerapi.OpenReadRequest{Location: domain.Location{EndpointID: testEndpointID, Path: "/file.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close(context.Background())
	stream, ok := handle.(providerapi.StreamReadHandle)
	if !ok {
		t.Fatal("SFTP read handle does not expose a scheduled request window")
	}
	called := false
	n, err := stream.ReadStream(context.Background(), make([]byte, 32<<10), providerapi.ReadStreamOptions{
		MaxBytes: providerapi.MaxReadAheadBytes,
		BeforeRead: func(_ context.Context, count uint32) error {
			called = true
			if count == 0 || count > 32<<10 {
				t.Errorf("request = %d bytes", count)
			}
			return context.Canceled
		},
	})
	if !called || n != 0 || err == nil {
		t.Fatalf("admission called=%v, ReadStream=%d,%v", called, n, err)
	}
}

func writeStreamFixture(t *testing.T, p providerapi.Provider, payload []byte) domain.Location {
	t.Helper()
	location := domain.Location{EndpointID: testEndpointID, Path: "/window.bin"}
	h, err := p.(providerapi.MutableProvider).OpenWrite(context.Background(), providerapi.OpenWriteRequest{Location: location, Disposition: providerapi.WriteCreateNew})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(context.Background())
	n, err := h.Write(context.Background(), payload)
	if err != nil || n != len(payload) {
		t.Fatalf("fixture write=%d,%v", n, err)
	}
	if err := h.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	return location
}

func TestReadStreamLimitsRangeAndAdmissionPacketSize(t *testing.T) {
	fixture := (contractFactory{}).New(t)
	payload := bytes.Repeat([]byte("0123456789"), 100)
	location := writeStreamFixture(t, fixture.Provider, payload)
	limit := int64(17)
	h, err := fixture.Provider.OpenRead(context.Background(), providerapi.OpenReadRequest{Location: location, Offset: 5, Limit: &limit})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(context.Background())
	var admitted atomic.Uint64
	options := providerapi.ReadStreamOptions{MaxBytes: providerapi.MaxReadAheadBytes, MaxRequestBytes: 4, BeforeRead: func(_ context.Context, n uint32) error {
		if n == 0 || n > 4 {
			t.Errorf("admission packet=%d", n)
		}
		admitted.Add(uint64(n))
		return nil
	}}
	var got []byte
	for {
		buffer := make([]byte, 9)
		n, err := h.(providerapi.StreamReadHandle).ReadStream(context.Background(), buffer, options)
		got = append(got, buffer[:n]...)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(got, payload[5:22]) || admitted.Load() != 17 {
		t.Fatalf("range=%q,admitted=%d", got, admitted.Load())
	}
}

func TestReadStreamAdmissionErrorCancelsOtherWindowWaiters(t *testing.T) {
	fixture := (contractFactory{}).New(t)
	location := writeStreamFixture(t, fixture.Provider, make([]byte, 2<<20))
	h, err := fixture.Provider.OpenRead(context.Background(), providerapi.OpenReadRequest{Location: location})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(context.Background())
	var called atomic.Uint32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := h.(providerapi.StreamReadHandle).ReadStream(ctx, make([]byte, 32<<10), providerapi.ReadStreamOptions{
			MaxBytes: providerapi.MaxReadAheadBytes, BeforeRead: func(ctx context.Context, _ uint32) error {
				if called.Add(1) == 64 {
					return errors.New("fixture admission denied")
				}
				<-ctx.Done()
				return ctx.Err()
			},
		})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("missing admission failure")
		}
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("failed request waited behind other admission waiters")
	}
}

func TestPreservedDestinationReadbackUsesPacketAdmission(t *testing.T) {
	fixture := (contractFactory{}).New(t)
	payload := bytes.Repeat([]byte("preserved-version"), 1<<14)
	location := writeStreamFixture(t, fixture.Provider, payload)
	entry, err := fixture.Provider.Stat(context.Background(), providerapi.StatRequest{Location: location})
	if err != nil {
		t.Fatal(err)
	}
	var admitted atomic.Uint64
	result, err := fixture.Provider.(providerapi.DestinationPreserver).PreserveDestination(context.Background(), providerapi.PreserveDestinationRequest{
		Source: location, Backup: domain.Location{EndpointID: testEndpointID, Path: "/preserved.bin"}, ExpectedFingerprint: entry.Fingerprint, ExpectedSHA256: fmt.Sprintf("%x", sha256.Sum256(payload)), ExpectedSize: int64(len(payload)), MaxBytes: int64(len(payload)),
		ReadStream: providerapi.ReadStreamOptions{MaxBytes: providerapi.MaxReadAheadBytes, BeforeRead: func(_ context.Context, n uint32) error { admitted.Add(uint64(n)); return nil }},
	})
	if err != nil || !result.BackupPresent {
		t.Fatalf("preserve=%#v,%v", result, err)
	}
	if admitted.Load() != uint64(len(payload)) {
		t.Fatalf("admitted=%d, want full preserved readback=%d", admitted.Load(), len(payload))
	}
}

func TestWriteStreamHonorsExplicitWindowBounds(t *testing.T) {
	for _, requests := range []uint32{0, 1, 2, 64, 65} {
		t.Run(fmt.Sprint(requests), func(t *testing.T) {
			fixture := (contractFactory{}).New(t)
			location := domain.Location{EndpointID: testEndpointID, Path: "/windowed.bin"}
			handle, err := fixture.Provider.(providerapi.MutableProvider).OpenWrite(t.Context(), providerapi.OpenWriteRequest{Location: location, Disposition: providerapi.WriteCreateNew})
			if err != nil {
				t.Fatal(err)
			}
			defer handle.Close(context.Background())
			stream, ok := handle.(providerapi.WindowedStreamWriteHandle)
			if !ok {
				t.Fatal("missing bounded write facet")
			}
			data := bytes.Repeat([]byte("window"), 20000)
			reader := bytes.NewReader(data)
			n, err := stream.WriteFromWindow(t.Context(), reader, requests)
			if requests == 0 || requests > 64 {
				if err == nil || n != 0 || reader.Len() != len(data) {
					t.Fatal("invalid window consumed source data")
				}
				return
			}
			if err != nil || n != int64(len(data)) {
				t.Fatalf("acknowledged=%d: %v", n, err)
			}
			if err := handle.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			read, err := fixture.Provider.OpenRead(t.Context(), providerapi.OpenReadRequest{Location: location})
			if err != nil {
				t.Fatal(err)
			}
			defer read.Close(context.Background())
			actual := make([]byte, len(data))
			offset := 0
			for offset < len(actual) {
				n, err := read.Read(t.Context(), actual[offset:])
				offset += n
				if err != nil {
					t.Fatal(err)
				}
				if n == 0 {
					t.Fatal("read made no progress")
				}
			}
			if !bytes.Equal(actual, data) {
				t.Fatal("windowed transfer content differs")
			}
		})
	}
}

func TestOpenWriteCreateNewCachesCreatedEntryAndRejectsExisting(t *testing.T) {
	fixture := (contractFactory{}).New(t)
	mutable := fixture.Provider.(providerapi.MutableProvider)
	location := domain.Location{EndpointID: testEndpointID, Path: "/created-entry.bin"}

	handle, err := mutable.OpenWrite(t.Context(), providerapi.OpenWriteRequest{
		Location:    location,
		Disposition: providerapi.WriteCreateNew,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close(context.Background())

	cached, ok := handle.(providerapi.CreatedEntryHandle)
	if !ok {
		t.Fatal("SFTP write handle does not implement CreatedEntryHandle")
	}
	entry, ok := cached.CreatedEntry()
	if !ok {
		t.Fatal("WriteCreateNew did not cache created entry metadata")
	}
	if entry.Location != location || entry.Kind != domain.EntryFile || entry.Fingerprint.Size == nil {
		t.Fatalf("unexpected cached entry=%+v", entry)
	}
	if _, err := handle.Write(t.Context(), []byte("original")); err != nil {
		t.Fatal(err)
	}
	if err := handle.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	_, err = mutable.OpenWrite(t.Context(), providerapi.OpenWriteRequest{
		Location:    location,
		Disposition: providerapi.WriteCreateNew,
	})
	var opErr *domain.OpError
	if !errors.As(err, &opErr) || opErr.Code != domain.CodeAlreadyExists {
		t.Fatalf("duplicate WriteCreateNew err=%v, want already_exists", err)
	}
}

func TestRenameNonReplaceRejectsExistingDestination(t *testing.T) {
	fixture := (contractFactory{}).New(t)
	mutable := fixture.Provider.(providerapi.MutableProvider)
	src := domain.Location{EndpointID: testEndpointID, Path: "/rename-src.bin"}
	dst := domain.Location{EndpointID: testEndpointID, Path: "/rename-dst.bin"}

	for _, item := range []struct {
		loc  domain.Location
		data []byte
	}{{src, []byte("source")}, {dst, []byte("existing")}} {
		h, err := mutable.OpenWrite(t.Context(), providerapi.OpenWriteRequest{
			Location:    item.loc,
			Disposition: providerapi.WriteCreateNew,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.Write(t.Context(), item.data); err != nil {
			_ = h.Close(context.Background())
			t.Fatal(err)
		}
		if err := h.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	}

	_, err := mutable.Rename(t.Context(), providerapi.RenameRequest{
		Source:      src,
		Destination: dst,
		Replace:     false,
	})
	var opErr *domain.OpError
	if !errors.As(err, &opErr) || opErr.Code != domain.CodeAlreadyExists {
		t.Fatalf("non-replace Rename err=%v, want already_exists", err)
	}

	reader, err := fixture.Provider.OpenRead(t.Context(), providerapi.OpenReadRequest{Location: dst})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close(context.Background())
	buf := make([]byte, 32)
	n, err := reader.Read(t.Context(), buf)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if string(buf[:n]) != "existing" {
		t.Fatalf("destination overwritten: got %q, want %q", string(buf[:n]), "existing")
	}
}
