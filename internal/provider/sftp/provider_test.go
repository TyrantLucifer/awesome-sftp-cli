package sftp

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/provider/contracttest"
	pkgsftp "github.com/pkg/sftp"
)

const testEndpointID domain.EndpointID = "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa"
const testSessionID domain.SessionID = "sess_aaaaaaaaaaaaaaaaaaaaaaaaaa"

type contractFactory struct{}

func (contractFactory) New(t *testing.T) contracttest.Fixture {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("contract-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "child.txt"), []byte("child"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file.txt", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	serverConnection, clientConnection := net.Pipe()
	server, err := pkgsftp.NewServer(serverConnection)
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve() }()
	client, err := pkgsftp.NewClientPipe(clientConnection, clientConnection)
	if err != nil {
		t.Fatal(err)
	}
	implementation, err := New(Config{Endpoint: domain.Endpoint{ID: testEndpointID, Kind: domain.EndpointSSH, DisplayName: "test", SSHHostAlias: "test-host"}, SessionID: testSessionID, Client: client, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = implementation.Close()
		_ = server.Close()
		_ = clientConnection.Close()
		select {
		case <-serveDone:
		case <-time.After(time.Second):
		}
	})
	return contracttest.Fixture{Provider: implementation, InvalidateListing: func(context.Context, domain.Location) error {
		future := time.Now().Add(3 * time.Second)
		return os.Chtimes(root, future, future)
	}, ChangeCapabilities: func(context.Context) error {
		implementation.mu.Lock()
		defer implementation.mu.Unlock()
		snapshot, err := domain.NewCapabilitySnapshot(domain.CapabilityRevision{SessionID: testSessionID, Generation: implementation.snapshot.Capabilities.Revision.Generation + 1}, true, []domain.Capability{{Name: "read", Version: 1}, {Name: "metadata", Version: 1}})
		if err == nil {
			implementation.snapshot.Capabilities = snapshot
		}
		return err
	}}
}

func TestProviderContract(t *testing.T) {
	contracttest.Run(t, contractFactory{})
	contracttest.RunMutable(t, contractFactory{})
}

func TestMapErrorClassifiesRemoteStatusAndConnectionLoss(t *testing.T) {
	implementation := &Provider{endpoint: domain.Endpoint{ID: testEndpointID}}
	tests := []struct {
		name      string
		err       error
		code      domain.Code
		retryKind domain.RetryKind
	}{
		{name: "not found status", err: &pkgsftp.StatusError{Code: uint32(pkgsftp.ErrSSHFxNoSuchFile)}, code: domain.CodeNotFound, retryKind: domain.RetryNever},
		{name: "permission status", err: &pkgsftp.StatusError{Code: uint32(pkgsftp.ErrSSHFxPermissionDenied)}, code: domain.CodePermissionDenied, retryKind: domain.RetryNever},
		{name: "unsupported status", err: &pkgsftp.StatusError{Code: uint32(pkgsftp.ErrSSHFxOpUnsupported)}, code: domain.CodeUnsupported, retryKind: domain.RetryNever},
		{name: "connection lost", err: pkgsftp.ErrSSHFxConnectionLost, code: domain.CodeTransportInterrupted, retryKind: domain.RetryAfterReconnect},
		{name: "unexpected EOF", err: io.ErrUnexpectedEOF, code: domain.CodeTransportInterrupted, retryKind: domain.RetryAfterReconnect},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mapped := implementation.mapError("list", nil, test.err)
			var operationError *domain.OpError
			if !errors.As(mapped, &operationError) {
				t.Fatalf("mapError() = %v, want OpError", mapped)
			}
			if operationError.Code != test.code || operationError.Retry.Kind != test.retryKind {
				t.Fatalf("mapError() code/retry = %s/%s, want %s/%s", operationError.Code, operationError.Retry.Kind, test.code, test.retryKind)
			}
		})
	}
}

func TestSFTPMetadataReportsProtocolSecondPrecision(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "entry")
	if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	provider := &Provider{endpoint: domain.Endpoint{ID: testEndpointID}}
	location, err := domain.NewLocation(testEndpointID, "/entry")
	if err != nil {
		t.Fatal(err)
	}
	entry := provider.entryFromInfo(location, "entry", info)
	if entry.Metadata.ModifiedPrecision == nil || *entry.Metadata.ModifiedPrecision != "second" {
		t.Fatalf("metadata precision = %#v, want second", entry.Metadata.ModifiedPrecision)
	}
	if entry.Fingerprint.ModifiedPrecision == nil || *entry.Fingerprint.ModifiedPrecision != "second" {
		t.Fatalf("fingerprint precision = %#v, want second", entry.Fingerprint.ModifiedPrecision)
	}
}

func TestListReturnsFirstPageBeforeReadingEntireRemoteDirectory(t *testing.T) {
	directory := t.TempDir()
	infos := make([]os.FileInfo, 2)
	for index, name := range []string{"first.txt", "second.txt"} {
		filePath := filepath.Join(directory, name)
		if err := os.WriteFile(filePath, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(filePath)
		if err != nil {
			t.Fatal(err)
		}
		infos[index] = info
	}
	directoryInfo, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	lister := newBlockingDirectoryLister(directoryInfo, infos)
	serverConnection, clientConnection := net.Pipe()
	server := pkgsftp.NewRequestServer(serverConnection, pkgsftp.Handlers{FileList: lister})
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve() }()
	client, err := pkgsftp.NewClientPipe(clientConnection, clientConnection)
	if err != nil {
		t.Fatal(err)
	}
	implementation, err := New(Config{
		Endpoint:  domain.Endpoint{ID: testEndpointID, Kind: domain.EndpointSSH, DisplayName: "test", SSHHostAlias: "test-host"},
		SessionID: testSessionID,
		Client:    client,
		Root:      directory,
		Close: func() error {
			_ = server.Close()
			return client.Close()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		lister.release()
		_ = implementation.Close()
		_ = clientConnection.Close()
		select {
		case <-serveDone:
		case <-time.After(time.Second):
		}
	})

	type listResult struct {
		page providerapi.ListPage
		err  error
	}
	result := make(chan listResult, 1)
	go func() {
		page, listErr := implementation.List(context.Background(), providerapi.ListRequest{
			Location: domain.Location{EndpointID: testEndpointID, Path: "/"},
			Limit:    1,
		})
		result <- listResult{page: page, err: listErr}
	}()
	select {
	case <-lister.secondRead:
		lister.release()
		<-result
		t.Fatal("List requested a second source batch before returning the first page")
	case listed := <-result:
		if listed.err != nil {
			t.Fatal(listed.err)
		}
		if len(listed.page.Entries) != 1 || listed.page.Done || listed.page.NextCursor == "" {
			t.Fatalf("first page = %#v, want one entry and a continuation cursor", listed.page)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("List did not return or request the next source batch")
	}
}

type blockingDirectoryLister struct {
	directory  os.FileInfo
	entries    []os.FileInfo
	secondRead chan struct{}
	unblock    chan struct{}
	once       sync.Once
}

func newBlockingDirectoryLister(directory os.FileInfo, entries []os.FileInfo) *blockingDirectoryLister {
	return &blockingDirectoryLister{
		directory:  directory,
		entries:    append([]os.FileInfo(nil), entries...),
		secondRead: make(chan struct{}, 1),
		unblock:    make(chan struct{}),
	}
}

func (l *blockingDirectoryLister) Filelist(request *pkgsftp.Request) (pkgsftp.ListerAt, error) {
	if request.Method == "List" {
		return l, nil
	}
	return staticLister{entries: []os.FileInfo{l.directory}}, nil
}

func (l *blockingDirectoryLister) ListAt(target []os.FileInfo, offset int64) (int, error) {
	if offset == 0 {
		return copy(target, l.entries), nil
	}
	select {
	case l.secondRead <- struct{}{}:
	default:
	}
	<-l.unblock
	return 0, io.EOF
}

func (l *blockingDirectoryLister) Close() error {
	l.release()
	return nil
}

func (l *blockingDirectoryLister) release() {
	l.once.Do(func() { close(l.unblock) })
}

type staticLister struct{ entries []os.FileInfo }

func (l staticLister) ListAt(target []os.FileInfo, offset int64) (int, error) {
	if offset >= int64(len(l.entries)) {
		return 0, io.EOF
	}
	count := copy(target, l.entries[offset:])
	if int(offset)+count == len(l.entries) {
		return count, io.EOF
	}
	return count, nil
}
