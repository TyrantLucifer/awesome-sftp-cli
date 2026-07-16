package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/auth"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	providerapi "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/provider/localfs"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transfer"
)

func TestProviderSessionRoutesListStatAndBoundedRead(t *testing.T) {
	implementation := testLocalProvider(t)
	factory, err := NewProviderSessions([]providerapi.Provider{implementation}, 4)
	if err != nil {
		t.Fatal(err)
	}
	session := factory.NewSession()
	t.Cleanup(func() { _ = session.Close() })

	normalized := handlePayload[ipc.ProviderNormalizeResponse](t, session, ProviderNormalize, ipc.ProviderNormalizeRequest{
		EndpointID: string(implementation.Descriptor().ID),
		Input:      ipc.EncodeWireBytes([]byte("/file")),
	})
	location, err := ipc.DecodeLocation(normalized.Location)
	if err != nil {
		t.Fatal(err)
	}

	stat := handlePayload[ipc.ProviderStatResponse](t, session, ProviderStat, ipc.ProviderStatRequest{
		Location: normalized.Location,
	})
	entry, err := ipc.DecodeEntry(stat.Entry)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Location != location || entry.Kind != domain.EntryFile {
		t.Fatalf("stat entry = %#v", entry)
	}

	read := handlePayload[ipc.ProviderReadResponse](t, session, ProviderRead, ipc.ProviderReadRequest{
		Location: normalized.Location,
		Offset:   2,
		Limit:    4,
	})
	data, err := read.Data.Decode()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "2345" {
		t.Fatalf("read data = %q, want 2345", got)
	}
	hashed := handlePayload[ipc.ProviderHashResponse](t, session, ProviderHash, ipc.ProviderHashRequest{
		Location: normalized.Location,
		MaxBytes: 10,
	})
	wantSHA := fmt.Sprintf("%x", sha256.Sum256([]byte("0123456789")))
	if hashed.Size != 10 || hashed.SHA256 != wantSHA {
		t.Fatalf("hash response = %#v, want size 10 SHA %s", hashed, wantSHA)
	}

	root := handlePayload[ipc.ProviderNormalizeResponse](t, session, ProviderNormalize, ipc.ProviderNormalizeRequest{
		EndpointID: string(implementation.Descriptor().ID),
		Input:      ipc.EncodeWireBytes([]byte("/")),
	})
	list := handlePayload[ipc.ProviderListResponse](t, session, ProviderList, ipc.ProviderListRequest{
		Location: root.Location,
		Limit:    2,
	})
	if len(list.Entries) != 2 || list.NextCursor == "" {
		t.Fatalf("list response = %#v, want bounded non-terminal page", list)
	}
}

func TestProviderSessionRejectsWriteSurfaceAndOversizedRead(t *testing.T) {
	implementation := testLocalProvider(t)
	factory, err := NewProviderSessions([]providerapi.Provider{implementation}, 4)
	if err != nil {
		t.Fatal(err)
	}
	session := factory.NewSession()
	defer session.Close()

	if _, err := session.Handle(context.Background(), "provider.remove", json.RawMessage(`{}`)); !domain.IsCode(err, domain.CodeUnsupported) {
		t.Fatalf("write method error = %v, want unsupported", err)
	}
	location := ipc.EncodeLocation(domain.Location{
		EndpointID: implementation.Descriptor().ID,
		Path:       "/file",
	})
	payload, err := json.Marshal(ipc.ProviderReadRequest{Location: location, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Handle(context.Background(), ProviderRead, payload); !domain.IsCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("oversized read error = %v, want invalid_argument", err)
	}
	payload, err = json.Marshal(ipc.ProviderHashRequest{Location: location, MaxBytes: 9})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Handle(context.Background(), ProviderHash, payload); !domain.IsCode(err, domain.CodeResourceExhausted) {
		t.Fatalf("undersized hash budget error = %v, want resource_exhausted", err)
	}
}

func TestConnectionCloseDiscardsProviderCursor(t *testing.T) {
	implementation := testLocalProvider(t)
	factory, err := NewProviderSessions([]providerapi.Provider{implementation}, 4)
	if err != nil {
		t.Fatal(err)
	}
	server := newTestServer(t, factory)
	serverConn, clientConn := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- server.ServeConn(context.Background(), serverConn) }()
	hello(t, clientConn)

	root := ipc.EncodeLocation(domain.Location{
		EndpointID: implementation.Descriptor().ID,
		Path:       "/",
	})
	writeEnvelope(t, clientConn, requestEnvelope(workRequestID, ProviderList, ipc.ProviderListRequest{
		Location: root,
		Limit:    1,
	}))
	response := readEnvelope(t, clientConn)
	if response.Error != nil {
		t.Fatalf("list error = %#v", response.Error)
	}
	var list ipc.ProviderListResponse
	if err := json.Unmarshal(response.Payload, &list); err != nil {
		t.Fatal(err)
	}
	if list.NextCursor == "" {
		t.Fatal("list did not return a cursor")
	}
	_ = clientConn.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ServeConn did not close")
	}

	_, err = implementation.List(context.Background(), providerapi.ListRequest{
		Location: domain.Location{EndpointID: implementation.Descriptor().ID, Path: "/"},
		Cursor:   list.NextCursor,
		Limit:    1,
	})
	if !domain.IsCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("discarded cursor error = %v, want invalid_argument", err)
	}
}

func TestProviderSessionClosesSSHProviderThatConnectsAfterSessionClose(t *testing.T) {
	local := testLocalProvider(t)
	factory, err := NewProviderSessions([]providerapi.Provider{local}, 4)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	remote := &closingProvider{Provider: local, descriptor: domain.Endpoint{ID: "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb", Kind: domain.EndpointSSH, DisplayName: "work", SSHHostAlias: "work"}, closed: make(chan struct{})}
	factory.SetSSHConnector(func(context.Context, string) (providerapi.Provider, error) {
		close(started)
		<-release
		return remote, nil
	})
	session := factory.NewSession()
	done := make(chan error, 1)
	go func() {
		payload, marshalErr := json.Marshal(ipc.ProviderConnectSSHRequest{HostAlias: "work"})
		if marshalErr != nil {
			done <- marshalErr
			return
		}
		_, handleErr := session.Handle(context.Background(), ProviderConnectSSH, payload)
		done <- handleErr
	}()
	<-started
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; !domain.IsCode(err, domain.CodeCanceled) {
		t.Fatalf("late connect error = %v, want canceled", err)
	}
	select {
	case <-remote.closed:
	case <-time.After(time.Second):
		t.Fatal("late SSH provider was not closed")
	}
}

func TestProviderSessionReleasesOwnedSSHEndpoint(t *testing.T) {
	local := testLocalProvider(t)
	factory, err := NewProviderSessions([]providerapi.Provider{local}, 4)
	if err != nil {
		t.Fatal(err)
	}
	remote := &closingProvider{Provider: local, descriptor: domain.Endpoint{ID: "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb", Kind: domain.EndpointSSH, DisplayName: "work", SSHHostAlias: "work"}, closed: make(chan struct{})}
	factory.SetSSHConnector(func(context.Context, string) (providerapi.Provider, error) { return remote, nil })
	session := factory.NewSession()
	defer session.Close()
	connectPayload, err := json.Marshal(ipc.ProviderConnectSSHRequest{HostAlias: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Handle(context.Background(), ProviderConnectSSH, connectPayload); err != nil {
		t.Fatal(err)
	}
	releasePayload, err := json.Marshal(ipc.ProviderReleaseRequest{EndpointID: string(remote.descriptor.ID)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Handle(context.Background(), ProviderRelease, releasePayload); err != nil {
		t.Fatal(err)
	}
	select {
	case <-remote.closed:
	case <-time.After(time.Second):
		t.Fatal("released SSH provider was not closed")
	}
	snapshotPayload, err := json.Marshal(ipc.ProviderSnapshotRequest{EndpointID: string(remote.descriptor.ID)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Handle(context.Background(), ProviderSnapshot, snapshotPayload); !domain.IsCode(err, domain.CodeNotFound) {
		t.Fatalf("released provider snapshot error = %v, want not_found", err)
	}
	if _, err := session.Handle(context.Background(), ProviderRelease, releasePayload); !domain.IsCode(err, domain.CodeNotFound) {
		t.Fatalf("second release error = %v, want not_found", err)
	}
}

func TestProviderSessionJobLeaseOutlivesClientAndClosesAfterRelease(t *testing.T) {
	local := testLocalProvider(t)
	factory, err := NewProviderSessions([]providerapi.Provider{local}, 4)
	if err != nil {
		t.Fatal(err)
	}
	remote := &closingProvider{Provider: local, descriptor: domain.Endpoint{ID: "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb", Kind: domain.EndpointSSH, DisplayName: "work", SSHHostAlias: "work"}, closed: make(chan struct{})}
	factory.SetSSHConnector(func(context.Context, string) (providerapi.Provider, error) { return remote, nil })
	session := factory.NewSession()
	connectPayload, err := json.Marshal(ipc.ProviderConnectSSHRequest{HostAlias: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Handle(context.Background(), ProviderConnectSSH, connectPayload); err != nil {
		t.Fatal(err)
	}
	releaseLease, err := factory.Acquire(context.Background(), transfer.Plan{
		SourceEndpoint: remote.descriptor, DestinationEndpoint: local.Descriptor(),
	})
	if err != nil {
		t.Fatalf("Acquire(): %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-remote.closed:
		t.Fatal("client close disposed an endpoint retained by a Job")
	default:
	}
	if resolved, err := factory.Resolve(remote.descriptor.ID); err != nil || resolved != remote {
		t.Fatalf("Resolve() = (%T, %v), want retained remote", resolved, err)
	}
	releaseLease()
	select {
	case <-remote.closed:
	case <-time.After(time.Second):
		t.Fatal("last Job lease did not close detached SSH provider")
	}
}

func TestProviderSessionsAcquireRehydratesFrozenEndpointDescriptor(t *testing.T) {
	local := testLocalProvider(t)
	factory, err := NewProviderSessions([]providerapi.Provider{local}, 4)
	if err != nil {
		t.Fatal(err)
	}
	descriptor := domain.Endpoint{ID: "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb", Kind: domain.EndpointSSH, DisplayName: "work", SSHHostAlias: "work"}
	remote := &closingProvider{Provider: local, descriptor: descriptor, closed: make(chan struct{})}
	var requested domain.Endpoint
	factory.SetEndpointConnector(func(_ context.Context, endpoint domain.Endpoint) (providerapi.Provider, error) {
		requested = endpoint
		return remote, nil
	})
	releaseLease, err := factory.Acquire(context.Background(), transfer.Plan{
		SourceEndpoint: descriptor, DestinationEndpoint: local.Descriptor(),
	})
	if err != nil {
		t.Fatalf("Acquire(): %v", err)
	}
	if requested != descriptor {
		t.Fatalf("connector endpoint = %#v, want %#v", requested, descriptor)
	}
	if resolved, err := factory.Resolve(descriptor.ID); err != nil || resolved != remote {
		t.Fatalf("Resolve() = (%T, %v), want rehydrated remote", resolved, err)
	}
	releaseLease()
	select {
	case <-remote.closed:
	case <-time.After(time.Second):
		t.Fatal("rehydrated endpoint was not closed after Job release")
	}
}

func TestProviderSessionsRouteAuthPromptToClaimingSession(t *testing.T) {
	local := testLocalProvider(t)
	factory, err := NewProviderSessions([]providerapi.Provider{local}, 4)
	if err != nil {
		t.Fatal(err)
	}
	broker, err := auth.NewBroker(auth.Config{MaxPrompts: 4})
	if err != nil {
		t.Fatal(err)
	}
	factory.SetAuthBroker(broker)
	askpass := factory.NewSession()
	ui := factory.NewSession()
	other := factory.NewSession()
	defer askpass.Close()
	defer ui.Close()
	defer other.Close()
	attempt, err := broker.BeginAttempt(context.Background(), "work-host", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer attempt.Close()
	promptPayload, err := json.Marshal(ipc.AuthPromptRequest{AttemptToken: string(attempt.Token()), Prompt: "Password:", Kind: string(auth.PromptSecret)})
	if err != nil {
		t.Fatal(err)
	}
	type handleResult struct {
		value any
		err   error
	}
	promptDone := make(chan handleResult, 1)
	go func() {
		value, handleErr := askpass.Handle(context.Background(), AuthPrompt, promptPayload)
		promptDone <- handleResult{value: value, err: handleErr}
	}()
	claim := handlePayload[ipc.AuthClaimResponse](t, ui, AuthClaim, ipc.AuthClaimRequest{})
	secret := "stage1-secret-canary"
	resolvePayload, err := json.Marshal(ipc.AuthResolveRequest{ChallengeID: claim.ChallengeID, Action: ipc.AuthActionAnswer, Answer: secret})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Handle(context.Background(), AuthResolve, resolvePayload); !domain.IsCode(err, domain.CodePermissionDenied) {
		t.Fatalf("other session resolve error = %v, want permission_denied", err)
	}
	if _, err := ui.Handle(context.Background(), AuthResolve, resolvePayload); err != nil {
		t.Fatal(err)
	}
	result := <-promptDone
	if result.err != nil {
		t.Fatal(result.err)
	}
	encoded, err := json.Marshal(result.value)
	if err != nil {
		t.Fatal(err)
	}
	var response ipc.AuthPromptResponse
	if err := json.Unmarshal(encoded, &response); err != nil {
		t.Fatal(err)
	}
	if response.Answer != secret {
		t.Fatalf("prompt answer = %q", response.Answer)
	}
}

type closingProvider struct {
	providerapi.Provider
	descriptor domain.Endpoint
	once       sync.Once
	closed     chan struct{}
}

func (p *closingProvider) Descriptor() domain.Endpoint { return p.descriptor }

func (p *closingProvider) Close() error {
	p.once.Do(func() { close(p.closed) })
	return nil
}

func handlePayload[T any](t *testing.T, session Session, name string, request any) T {
	t.Helper()
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	response, err := session.Handle(context.Background(), name, payload)
	if err != nil {
		t.Fatalf("Handle(%s): %v", name, err)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var result T
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func testLocalProvider(t *testing.T) *localfs.Provider {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"file", "other", "third"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("0123456789"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	implementation, err := localfs.New(localfs.Config{
		Endpoint: domain.Endpoint{
			ID:          "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa",
			Kind:        domain.EndpointLocal,
			DisplayName: "Local",
		},
		SessionID:  "sess_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		Root:       root,
		MaxCursors: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = implementation.Close() })
	return implementation
}
