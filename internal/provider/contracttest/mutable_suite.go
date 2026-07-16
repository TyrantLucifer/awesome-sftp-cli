package contracttest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
)

// RunMutable applies the reusable mutation facet contract. The fixture is
// recreated for every subtest so a failed mutation cannot contaminate another
// provider implementation or assertion.
func RunMutable(t *testing.T, factory Factory) {
	t.Helper()

	t.Run("write dispositions and durability boundary", func(t *testing.T) {
		implementation, mutable := mutableFixture(t, factory)
		created := normalizeChild(t, implementation, "mutable-created")

		handle, err := mutable.OpenWrite(context.Background(), provider.OpenWriteRequest{
			Location:    created,
			Disposition: provider.WriteCreateNew,
		})
		if err != nil {
			t.Fatalf("OpenWrite(create): %v", err)
		}
		writeAll(t, handle, []byte("created"))
		if err := handle.Sync(context.Background()); err != nil {
			var operationError *domain.OpError
			if !errors.As(err, &operationError) || operationError.Code != domain.CodeUnsupported || operationError.Effect != domain.EffectUnknown {
				t.Fatalf("Sync(create): %v", err)
			}
		}
		if err := handle.Close(context.Background()); err != nil {
			t.Fatalf("Close(create): %v", err)
		}
		if err := handle.Close(context.Background()); err != nil {
			t.Fatalf("Close(create again): %v", err)
		}
		assertBytes(t, implementation, created, []byte("created"))

		_, err = mutable.OpenWrite(context.Background(), provider.OpenWriteRequest{
			Location:    created,
			Disposition: provider.WriteCreateNew,
		})
		requireMutableError(t, err, domain.CodeAlreadyExists)

		entry := statMutable(t, implementation, created)
		resume, err := mutable.OpenWrite(context.Background(), provider.OpenWriteRequest{
			Location:            created,
			Offset:              int64(len("created")),
			Disposition:         provider.WriteResumeExisting,
			ExpectedFingerprint: &entry.Fingerprint,
		})
		if err != nil {
			t.Fatalf("OpenWrite(resume): %v", err)
		}
		writeAll(t, resume, []byte("-resumed"))
		if err := resume.Close(context.Background()); err != nil {
			t.Fatalf("Close(resume): %v", err)
		}
		assertBytes(t, implementation, created, []byte("created-resumed"))

		beforeTruncate := statMutable(t, implementation, created)
		mismatch := beforeTruncate.Fingerprint
		wrongSize := uint64(1)
		mismatch.Size = &wrongSize
		_, err = mutable.OpenWrite(context.Background(), provider.OpenWriteRequest{
			Location:            created,
			Disposition:         provider.WriteTruncate,
			ExpectedFingerprint: &mismatch,
		})
		requireMutableError(t, err, domain.CodeConflict)
		assertBytes(t, implementation, created, []byte("created-resumed"))
	})

	t.Run("publish conflicts and fingerprint guards", func(t *testing.T) {
		implementation, mutable := mutableFixture(t, factory)
		source := normalizeChild(t, implementation, "mutable-part")
		final := normalizeChild(t, implementation, "mutable-final")
		other := normalizeChild(t, implementation, "mutable-other")
		createMutableFile(t, mutable, source, []byte("source"))
		createMutableFile(t, mutable, final, []byte("existing"))
		_, err := mutable.Rename(context.Background(), provider.RenameRequest{
			Source:      source,
			Destination: final,
		})
		requireMutableError(t, err, domain.CodeAlreadyExists)
		assertBytes(t, implementation, source, []byte("source"))
		assertBytes(t, implementation, final, []byte("existing"))

		sourceEntry := statMutable(t, implementation, source)
		mismatch := sourceEntry.Fingerprint
		wrongSize := uint64(999)
		mismatch.Size = &wrongSize
		_, err = mutable.Rename(context.Background(), provider.RenameRequest{
			Source:         source,
			Destination:    other,
			ExpectedSource: &mismatch,
		})
		requireMutableError(t, err, domain.CodeConflict)

		result, err := mutable.Rename(context.Background(), provider.RenameRequest{
			Source:         source,
			Destination:    other,
			ExpectedSource: &sourceEntry.Fingerprint,
		})
		if err != nil {
			if domain.IsCode(err, domain.CodeUnsupported) {
				return
			}
			t.Fatalf("Rename(no replace): %v", err)
		}
		if result.Replaced {
			t.Fatalf("Rename(no replace) = %#v, want Replaced=false", result)
		}
		assertBytes(t, implementation, other, []byte("source"))

		otherEntry := statMutable(t, implementation, other)
		removeMismatch := otherEntry.Fingerprint
		removeMismatch.Size = &wrongSize
		err = mutable.Remove(context.Background(), provider.RemoveRequest{
			Location: other,
			Expected: &removeMismatch,
		})
		requireMutableError(t, err, domain.CodeConflict)
		if err := mutable.Remove(context.Background(), provider.RemoveRequest{
			Location: other,
			Expected: &otherEntry.Fingerprint,
		}); err != nil {
			t.Fatalf("Remove(): %v", err)
		}
		_, err = implementation.Stat(context.Background(), provider.StatRequest{Location: other})
		if !domain.IsCode(err, domain.CodeNotFound) {
			t.Fatalf("Stat(removed) error = %v, want not_found", err)
		}
	})

	t.Run("preserve destination moves exact bytes to a no-replace backup", func(t *testing.T) {
		implementation, mutable := mutableFixture(t, factory)
		preserver, ok := mutable.(provider.DestinationPreserver)
		if !ok {
			t.Fatal("mutable provider has no destination preservation facet")
		}
		source := normalizeChild(t, implementation, "preserve-source")
		backup := normalizeChild(t, implementation, "preserve-backup")
		content := []byte("remote original")
		createMutableFile(t, mutable, source, content)
		entry := statMutable(t, implementation, source)
		digest := sha256.Sum256(content)
		request := provider.PreserveDestinationRequest{
			Source: source, Backup: backup, ExpectedFingerprint: entry.Fingerprint,
			ExpectedSHA256: fmt.Sprintf("%x", digest), ExpectedSize: int64(len(content)), MaxBytes: int64(len(content)),
		}
		if _, err := preserver.PreserveDestination(context.Background(), request); err != nil {
			t.Fatalf("PreserveDestination: %v", err)
		}
		assertBytes(t, implementation, backup, content)
		if _, err := implementation.Stat(context.Background(), provider.StatRequest{Location: source}); !domain.IsCode(err, domain.CodeNotFound) {
			t.Fatalf("preserved source still exists: %v", err)
		}
		if _, err := preserver.PreserveDestination(context.Background(), request); err != nil {
			t.Fatalf("PreserveDestination(idempotent): %v", err)
		}
	})

	t.Run("directories and roots", func(t *testing.T) {
		implementation, mutable := mutableFixture(t, factory)
		directory := normalizeChild(t, implementation, "mutable-directory")
		entry, err := mutable.Mkdir(context.Background(), provider.MkdirRequest{
			Location:  directory,
			Exclusive: true,
		})
		if err != nil {
			t.Fatalf("Mkdir(exclusive): %v", err)
		}
		if entry.Kind != domain.EntryDirectory {
			t.Fatalf("Mkdir() kind = %q, want directory", entry.Kind)
		}
		if _, err := mutable.Mkdir(context.Background(), provider.MkdirRequest{Location: directory}); err != nil {
			t.Fatalf("Mkdir(existing non-exclusive): %v", err)
		}
		_, err = mutable.Mkdir(context.Background(), provider.MkdirRequest{Location: directory, Exclusive: true})
		requireMutableError(t, err, domain.CodeAlreadyExists)

		root := normalizeRoot(t, implementation)
		if _, err := mutable.Mkdir(context.Background(), provider.MkdirRequest{Location: root}); !domain.IsCode(err, domain.CodeInvalidArgument) {
			t.Fatalf("Mkdir(root) error = %v, want invalid_argument", err)
		}
		if err := mutable.Remove(context.Background(), provider.RemoveRequest{Location: root}); !domain.IsCode(err, domain.CodeInvalidArgument) {
			t.Fatalf("Remove(root) error = %v, want invalid_argument", err)
		}
	})
}

func mutableFixture(t *testing.T, factory Factory) (provider.Provider, provider.MutableProvider) {
	t.Helper()
	implementation := newFixture(t, factory).Provider
	mutable, ok := implementation.(provider.MutableProvider)
	if !ok {
		t.Fatalf("provider %T does not implement MutableProvider", implementation)
	}
	return implementation, mutable
}

func normalizeChild(t *testing.T, implementation provider.Provider, name string) domain.Location {
	t.Helper()
	root := normalizeRoot(t, implementation)
	location, err := implementation.Normalize(context.Background(), domain.NormalizeRequest{
		EndpointID: implementation.Descriptor().ID,
		Base:       &root,
		Input:      name,
	})
	if err != nil {
		t.Fatalf("Normalize(%q): %v", name, err)
	}
	return location
}

func createMutableFile(t *testing.T, mutable provider.MutableProvider, location domain.Location, data []byte) {
	t.Helper()
	handle, err := mutable.OpenWrite(context.Background(), provider.OpenWriteRequest{
		Location:    location,
		Disposition: provider.WriteCreateNew,
	})
	if err != nil {
		t.Fatalf("OpenWrite(%q): %v", location.Path, err)
	}
	writeAll(t, handle, data)
	if err := handle.Close(context.Background()); err != nil {
		t.Fatalf("Close(%q): %v", location.Path, err)
	}
}

func writeAll(t *testing.T, handle provider.WriteHandle, data []byte) {
	t.Helper()
	for len(data) > 0 {
		n, err := handle.Write(context.Background(), data)
		if n < 0 || n > len(data) {
			t.Fatalf("Write() n = %d for %d bytes", n, len(data))
		}
		data = data[n:]
		if err != nil {
			t.Fatalf("Write(): %v", err)
		}
		if n == 0 {
			t.Fatal("Write() made no progress")
		}
	}
}

func assertBytes(t *testing.T, implementation provider.Provider, location domain.Location, want []byte) {
	t.Helper()
	handle, err := implementation.OpenRead(context.Background(), provider.OpenReadRequest{Location: location})
	if err != nil {
		t.Fatalf("OpenRead(%q): %v", location.Path, err)
	}
	defer func() { _ = handle.Close(context.Background()) }()
	var got bytes.Buffer
	buffer := make([]byte, 3)
	for {
		n, readErr := handle.Read(context.Background(), buffer)
		if n > 0 {
			_, _ = got.Write(buffer[:n])
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			t.Fatalf("Read(%q): %v", location.Path, readErr)
		}
		if n == 0 {
			t.Fatal("Read() made no progress")
		}
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("read %q = %q, want %q", location.Path, got.Bytes(), want)
	}
}

func statMutable(t *testing.T, implementation provider.Provider, location domain.Location) domain.Entry {
	t.Helper()
	entry, err := implementation.Stat(context.Background(), provider.StatRequest{Location: location})
	if err != nil {
		t.Fatalf("Stat(%q): %v", location.Path, err)
	}
	return entry
}

func requireMutableError(t *testing.T, err error, code domain.Code) {
	t.Helper()
	var operationError *domain.OpError
	if !errors.As(err, &operationError) {
		t.Fatalf("error = %v, want OpError", err)
	}
	if operationError.Code != code || operationError.Effect != domain.EffectNone {
		t.Fatalf("error code/effect = %s/%s, want %s/%s", operationError.Code, operationError.Effect, code, domain.EffectNone)
	}
}
