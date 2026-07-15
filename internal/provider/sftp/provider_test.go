package sftp

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
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

func TestProviderContract(t *testing.T) { contracttest.Run(t, contractFactory{}) }

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
