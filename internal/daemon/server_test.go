package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/diagnostic"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
)

const (
	helloRequestID  domain.RequestID = "req_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	workRequestID   domain.RequestID = "req_bbbbbbbbbbbbbbbbbbbbbbbbbb"
	cancelRequestID domain.RequestID = "req_cccccccccccccccccccccccccc"
)

type testSession struct {
	started     chan struct{}
	done        chan struct{}
	closed      chan struct{}
	startedOnce sync.Once
	doneOnce    sync.Once
	closedOnce  sync.Once
}

func (s *testSession) Handle(ctx context.Context, name string, payload json.RawMessage) (any, error) {
	switch name {
	case "echo":
		var value map[string]string
		if err := json.Unmarshal(payload, &value); err != nil {
			return nil, err
		}
		return value, nil
	case "block":
		s.startedOnce.Do(func() { close(s.started) })
		<-ctx.Done()
		s.doneOnce.Do(func() { close(s.done) })
		return nil, domain.FromContext("block", "", nil, ctx.Err())
	default:
		return nil, &domain.OpError{
			Code:    domain.CodeUnsupported,
			Message: "unsupported request",
			Retry:   domain.RetryAdvice{Kind: domain.RetryNever},
			Effect:  domain.EffectNone,
		}
	}
}

func (s *testSession) Close() error {
	if s.closed != nil {
		s.closedOnce.Do(func() { close(s.closed) })
	}
	return nil
}

type sessionFactory func() Session

func (factory sessionFactory) NewSession() Session { return factory() }

func TestServeConnRequiresHelloAsFirstFrame(t *testing.T) {
	server := newTestServer(t, sessionFactory(func() Session { return &testSession{} }))
	serverConn, clientConn := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- server.ServeConn(context.Background(), serverConn) }()

	writeEnvelope(t, clientConn, requestEnvelope(workRequestID, "echo", map[string]string{"x": "y"}))
	response := readEnvelope(t, clientConn)
	if response.Error == nil || response.Error.Code != domain.CodeProtocolIncompatible {
		t.Fatalf("first response error = %#v, want protocol_incompatible", response.Error)
	}
	_ = clientConn.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ServeConn did not stop after invalid first frame")
	}
}

func TestServeConnNegotiatesAndRoutesRequest(t *testing.T) {
	server := newTestServer(t, sessionFactory(func() Session { return &testSession{} }))
	serverConn, clientConn := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- server.ServeConn(context.Background(), serverConn) }()
	hello(t, clientConn)

	writeEnvelope(t, clientConn, requestEnvelope(workRequestID, "echo", map[string]string{"value": "ok"}))
	response := readEnvelope(t, clientConn)
	if response.Error != nil {
		t.Fatalf("response error = %#v", response.Error)
	}
	var got map[string]string
	if err := json.Unmarshal(response.Payload, &got); err != nil {
		t.Fatal(err)
	}
	if got["value"] != "ok" {
		t.Fatalf("response payload = %#v", got)
	}
	_ = clientConn.Close()
	<-done
}

func TestServeConnCancelsRequestAndReclaimsSession(t *testing.T) {
	session := &testSession{
		started: make(chan struct{}),
		done:    make(chan struct{}),
		closed:  make(chan struct{}),
	}
	server := newTestServer(t, sessionFactory(func() Session { return session }))
	serverConn, clientConn := net.Pipe()
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.ServeConn(context.Background(), serverConn) }()
	hello(t, clientConn)

	writeEnvelope(t, clientConn, requestEnvelope(workRequestID, "block", struct{}{}))
	select {
	case <-session.started:
	case <-time.After(5 * time.Second):
		t.Fatal("blocking request did not start")
	}
	writeEnvelope(t, clientConn, requestEnvelope(cancelRequestID, RequestCancel, ipc.CancelRequest{
		TargetRequestID: workRequestID,
	}))

	responses := map[domain.RequestID]ipc.Envelope{}
	for len(responses) < 2 {
		response := readEnvelope(t, clientConn)
		responses[response.RequestID] = response
	}
	var cancelResult ipc.CancelResult
	if err := json.Unmarshal(responses[cancelRequestID].Payload, &cancelResult); err != nil {
		t.Fatal(err)
	}
	if cancelResult.State != ipc.CancelAccepted {
		t.Fatalf("cancel state = %q, want accepted", cancelResult.State)
	}
	if response := responses[workRequestID]; response.Error == nil || response.Error.Code != domain.CodeCanceled {
		t.Fatalf("blocked response = %#v, want canceled", response)
	}
	select {
	case <-session.done:
	case <-time.After(5 * time.Second):
		t.Fatal("request context was not canceled")
	}

	_ = clientConn.Close()
	<-serveDone
	select {
	case <-session.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("connection session was not closed")
	}
}

func TestServeConnDisconnectCancelsInflightRequest(t *testing.T) {
	session := &testSession{
		started: make(chan struct{}),
		done:    make(chan struct{}),
		closed:  make(chan struct{}),
	}
	server := newTestServer(t, sessionFactory(func() Session { return session }))
	serverConn, clientConn := net.Pipe()
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.ServeConn(context.Background(), serverConn) }()
	hello(t, clientConn)
	writeEnvelope(t, clientConn, requestEnvelope(workRequestID, "block", struct{}{}))
	<-session.started
	_ = clientConn.Close()

	select {
	case <-session.done:
	case <-time.After(5 * time.Second):
		t.Fatal("disconnect did not cancel request")
	}
	select {
	case <-serveDone:
	case <-time.After(5 * time.Second):
		t.Fatal("ServeConn did not return after disconnect")
	}
}

func TestServeConnContextCancellationClosesIdleConnection(t *testing.T) {
	server := newTestServer(t, sessionFactory(func() Session { return &testSession{} }))
	serverConn, clientConn := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.ServeConn(ctx, serverConn) }()
	hello(t, clientConn)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ServeConn() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ServeConn did not stop after context cancellation")
	}
	_ = clientConn.Close()
}

func TestServeConnAcknowledgesShutdownBeforeRequestingItOnce(t *testing.T) {
	shutdown := make(chan struct{})
	server := newTestServer(t, sessionFactory(func() Session { return &testSession{} }))
	server.shutdown = func() { close(shutdown) }

	serverConn, clientConn := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- server.ServeConn(context.Background(), serverConn) }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := NewClient(ctx, clientConn, "test-client", "client-test")
	if err != nil {
		t.Fatal(err)
	}
	var response ShutdownResponse
	if err := client.Call(ctx, RequestShutdown, ShutdownRequest{}, &response); err != nil {
		t.Fatalf("shutdown call was not acknowledged: %v", err)
	}
	if !response.Accepted {
		t.Fatalf("shutdown response = %#v", response)
	}
	select {
	case <-shutdown:
	case <-ctx.Done():
		t.Fatal("server did not request shutdown after acknowledging it")
	}

	var repeated ShutdownResponse
	if err := client.Call(ctx, RequestShutdown, ShutdownRequest{}, &repeated); err != nil {
		t.Fatal(err)
	}
	if !repeated.Accepted {
		t.Fatalf("repeated shutdown response = %#v", repeated)
	}
	_ = client.Close()
	<-done
}

func newTestServer(t *testing.T, factory SessionFactory) *Server {
	t.Helper()
	server, err := NewServer(ServerConfig{
		BuildVersion:     "test",
		Epoch:            "epoch-test",
		Sessions:         factory,
		MaxInFlight:      4,
		HandshakeTimeout: time.Second,
		VerifyPeer:       func(net.Conn) error { return nil },
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func TestLogRequestWritesOnlySafeCorrelationFields(t *testing.T) {
	var output bytes.Buffer
	server := newTestServer(t, sessionFactory(func() Session { return &testSession{} }))
	server.logger = slog.New(diagnostic.NewJSONHandler(&output, nil))
	server.logRequest("rpc_request_failed", workRequestID, &ipc.RPCError{
		Code:       domain.CodePermissionDenied,
		Message:    "secret-canary /private/path",
		EndpointID: domain.EndpointID("ep_aaaaaaaaaaaaaaaaaaaaaaaaaa"),
	})
	encoded := output.String()
	for _, required := range []string{string(workRequestID), string(domain.CodePermissionDenied), "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa"} {
		if !strings.Contains(encoded, required) {
			t.Fatalf("log does not contain %q: %s", required, encoded)
		}
	}
	for _, forbidden := range []string{"secret-canary", "/private/path"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("log contains %q: %s", forbidden, encoded)
		}
	}
}

func hello(t *testing.T, conn net.Conn) {
	t.Helper()
	writeEnvelope(t, conn, requestEnvelope(helloRequestID, RequestHello, ipc.HelloRequest{
		ClientVersion:    "test-client",
		ClientInstanceID: "client-test",
		Protocols:        []ipc.VersionRange{{Major: ipc.ProtocolMajor, MinMinor: 0, MaxMinor: ipc.ProtocolMinor}},
	}))
	response := readEnvelope(t, conn)
	if response.Error != nil {
		t.Fatalf("hello response error = %#v", response.Error)
	}
	var helloResponse ipc.HelloResponse
	if err := json.Unmarshal(response.Payload, &helloResponse); err != nil {
		t.Fatal(err)
	}
	if helloResponse.Selected != (ipc.ProtocolVersion{Major: ipc.ProtocolMajor, Minor: ipc.ProtocolMinor}) {
		t.Fatalf("selected protocol = %#v", helloResponse.Selected)
	}
}

func requestEnvelope(requestID domain.RequestID, name string, payload any) ipc.Envelope {
	encoded, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return ipc.Envelope{
		Protocol:  ipc.ProtocolVersion{Major: ipc.ProtocolMajor, Minor: ipc.ProtocolMinor},
		Kind:      ipc.KindRequest,
		Name:      name,
		RequestID: requestID,
		Payload:   encoded,
	}
}

func writeEnvelope(t *testing.T, conn net.Conn, envelope ipc.Envelope) {
	t.Helper()
	encoded, err := ipc.EncodeEnvelope(envelope)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := ipc.NewWriter(conn, ipc.MaxFrameBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteFrame(encoded); err != nil {
		t.Fatal(err)
	}
}

func readEnvelope(t *testing.T, conn net.Conn) ipc.Envelope {
	t.Helper()
	reader, err := ipc.NewReader(conn, ipc.MaxFrameBytes)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := reader.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := ipc.DecodeEnvelope(frame)
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}
