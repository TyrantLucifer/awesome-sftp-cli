package cachefs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cache"
)

type BlobIdentity struct {
	ID   cache.BlobID
	Size int64
}

type BlobInfo struct {
	Identity BlobIdentity
	Path     string
}

type PublishResult struct {
	BlobInfo
	Deduplicated bool
}

func (store *Store) PublishBlob(ctx context.Context, source io.Reader, maxBytes int64, expected *BlobIdentity) (PublishResult, error) {
	if store == nil {
		return PublishResult{}, fmt.Errorf("publish cache blob: nil store")
	}
	if ctx == nil {
		return PublishResult{}, fmt.Errorf("publish cache blob: nil context")
	}
	if source == nil {
		return PublishResult{}, fmt.Errorf("publish cache blob: nil source")
	}
	if maxBytes < 0 || maxBytes == math.MaxInt64 {
		return PublishResult{}, fmt.Errorf("publish cache blob: max bytes must be in [0,%d]", int64(math.MaxInt64-1))
	}
	if expected != nil {
		if _, err := cache.ParseBlobID(string(expected.ID)); err != nil {
			return PublishResult{}, fmt.Errorf("publish cache blob expected identity: %w", err)
		}
		if expected.Size < 0 {
			return PublishResult{}, fmt.Errorf("publish cache blob expected identity: negative size")
		}
	}

	store.publishMu.Lock()
	defer store.publishMu.Unlock()

	staging := filepath.Join(store.root, "staging")
	if err := validatePrivateDirectory(staging); err != nil {
		return PublishResult{}, fmt.Errorf("publish cache blob staging: %w", err)
	}
	temp, tempInfo, err := createBlobTemp(staging)
	if err != nil {
		return PublishResult{}, err
	}
	tempPath := temp.Name()
	closed := false
	cleanup := func() {
		if !closed {
			_ = temp.Close()
			closed = true
		}
		if removeExactFile(tempPath, tempInfo) == nil {
			_ = syncDirectory(staging)
		}
	}

	digest := sha256.New()
	limited := &io.LimitedReader{R: &contextReader{ctx: ctx, reader: source}, N: maxBytes + 1}
	written, copyErr := io.CopyBuffer(io.MultiWriter(temp, digest), limited, make([]byte, 64<<10))
	if copyErr != nil {
		cleanup()
		return PublishResult{}, fmt.Errorf("publish cache blob stream: %w", copyErr)
	}
	if written > maxBytes {
		cleanup()
		return PublishResult{}, fmt.Errorf("%w: limit=%d", ErrLimitExceeded, maxBytes)
	}
	var sum [sha256.Size]byte
	copy(sum[:], digest.Sum(nil))
	identity := BlobIdentity{ID: cache.BlobIDFromDigest(sum), Size: written}
	if expected != nil && identity != *expected {
		cleanup()
		return PublishResult{}, fmt.Errorf("%w: expected id=%s size=%d, computed id=%s size=%d", ErrIdentityMismatch, expected.ID, expected.Size, identity.ID, identity.Size)
	}
	if err := fullSyncFile(temp); err != nil {
		cleanup()
		return PublishResult{}, fmt.Errorf("publish cache blob temp durability: %w", err)
	}
	if err := temp.Close(); err != nil {
		closed = true
		cleanup()
		return PublishResult{}, fmt.Errorf("publish cache blob close temp: %w", err)
	}
	closed = true

	finalPath, err := store.BlobPath(identity.ID)
	if err != nil {
		cleanup()
		return PublishResult{}, err
	}
	parent := filepath.Dir(finalPath)
	if err := ensurePrivateDirectory(parent); err != nil {
		cleanup()
		return PublishResult{}, fmt.Errorf("publish cache blob shard: %w", err)
	}
	if err := os.Link(tempPath, finalPath); err != nil {
		if !errors.Is(err, os.ErrExist) {
			cleanup()
			return PublishResult{}, fmt.Errorf("publish cache blob no-replace: %w", err)
		}
		cleanup()
		info, inspectErr := store.InspectBlob(identity.ID)
		if inspectErr != nil {
			return PublishResult{}, fmt.Errorf("publish cache blob verify concurrent publication: %w", inspectErr)
		}
		if info.Identity != identity {
			return PublishResult{}, fmt.Errorf("%w: concurrent publication differs", ErrIdentityMismatch)
		}
		return PublishResult{BlobInfo: info, Deduplicated: true}, nil
	}
	if err := os.Remove(tempPath); err != nil {
		return PublishResult{}, fmt.Errorf("publish cache blob remove linked temp: %w", err)
	}
	if err := syncDirectory(parent); err != nil {
		return PublishResult{}, fmt.Errorf("publish cache blob parent durability: %w", err)
	}
	if err := syncDirectory(staging); err != nil {
		return PublishResult{}, fmt.Errorf("publish cache blob staging durability: %w", err)
	}
	info, err := store.InspectBlob(identity.ID)
	if err != nil {
		return PublishResult{}, fmt.Errorf("publish cache blob final verification: %w", err)
	}
	return PublishResult{BlobInfo: info}, nil
}

func createBlobTemp(staging string) (*os.File, os.FileInfo, error) {
	for range 8 {
		var random [16]byte
		if _, err := io.ReadFull(rand.Reader, random[:]); err != nil {
			return nil, nil, fmt.Errorf("publish cache blob random temp identity: %w", err)
		}
		path := filepath.Join(staging, ".amsftp-cache-blob-"+hex.EncodeToString(random[:])+".tmp")
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // random O_EXCL path beneath verified private staging
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return nil, nil, fmt.Errorf("publish cache blob create temp: %w", err)
		}
		metadata, err := file.Stat()
		if err != nil {
			_ = file.Close()
			return nil, nil, fmt.Errorf("publish cache blob inspect temp: %w", err)
		}
		if err := validatePrivateRegular(path, metadata); err != nil {
			_ = file.Close()
			return nil, nil, err
		}
		return file, metadata, nil
	}
	return nil, nil, fmt.Errorf("publish cache blob: exhausted random temp collisions")
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *contextReader) Read(destination []byte) (int, error) {
	select {
	case <-reader.ctx.Done():
		return 0, reader.ctx.Err()
	default:
		return reader.reader.Read(destination)
	}
}
