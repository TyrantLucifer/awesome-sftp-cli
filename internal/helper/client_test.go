package helper

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
)

func TestHelperClientHandshakeStreamsAndCancelsOneRequestWithoutClosingSession(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close()
	defer clientSide.Close()
	serviceDone := make(chan error, 1)
	go func() {
		serviceDone <- Serve(context.Background(), serverSide, serverSide, ServiceConfig{
			Server:                 ServerConfig{Protocol: 1, HelperVersion: Version{Major: 4}, MaximumFrame: MaxHelperFrameBytes, MaximumConcurrent: 2, Capabilities: []Capability{{Name: CapabilityFilenameSearch, Version: 1}, {Name: CapabilityDiskStats, Version: 1}}},
			MaximumRequestDuration: time.Second,
			Handlers: map[CapabilityName]RequestHandler{
				CapabilityFilenameSearch: func(ctx context.Context, _ json.RawMessage, emit EmitFunc) (Completion, error) {
					if err := emit(FrameResult, map[string]any{"relative_path": "first"}); err != nil {
						return Completion{}, err
					}
					<-ctx.Done()
					return Completion{Status: "canceled", Reason: "canceled"}, nil
				},
				CapabilityDiskStats: func(context.Context, json.RawMessage, EmitFunc) (Completion, error) {
					return Completion{Status: "complete", Reason: "none"}, nil
				},
			},
		})
	}()
	hello := ClientHello{
		MinimumProtocol: 1, MaximumProtocol: 1, MaximumFrame: MaxHelperFrameBytes, MaximumConcurrent: 2, ClientVersion: Version{Major: 4},
		Capabilities: []CapabilityRequest{{Name: CapabilityFilenameSearch, MaximumVersion: 1}, {Name: CapabilityDiskStats, MaximumVersion: 1}},
	}
	client, err := NewClient(context.Background(), clientSide, clientSide, hello)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	searchID := domain.RequestID("req_dddddddddddddddddddddddddd")
	events, err := client.Start(context.Background(), searchID, CapabilityFilenameSearch, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	first := <-events
	if first.Type != FrameResult || first.RequestID != searchID {
		t.Fatalf("first = %#v", first)
	}
	if err := client.Cancel(searchID); err != nil {
		t.Fatal(err)
	}
	terminal := <-events
	if terminal.Type != FrameComplete {
		t.Fatalf("terminal = %#v", terminal)
	}
	var completion Completion
	if err := decodeStrictPayload(terminal.Payload, &completion); err != nil || completion.Status != "canceled" {
		t.Fatalf("completion=%#v err=%v", completion, err)
	}
	otherID := domain.RequestID("req_eeeeeeeeeeeeeeeeeeeeeeeeee")
	other, err := client.Start(context.Background(), otherID, CapabilityDiskStats, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if event := <-other; event.Type != FrameComplete {
		t.Fatalf("unrelated request after cancel = %#v", event)
	}
	if client.Level() != 1 || client.Failure() != nil {
		t.Fatalf("client degraded after request cancel: level=%d failure=%v", client.Level(), client.Failure())
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-serviceDone; err != nil {
		t.Fatal(err)
	}
}

func TestHelperClientMayStartNextRequestImmediatelyAfterCompleteAtConcurrencyOne(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	serviceDone := make(chan error, 1)
	go func() {
		serviceDone <- Serve(context.Background(), serverSide, serverSide, ServiceConfig{
			Server: ServerConfig{
				Protocol: 1, HelperVersion: Version{Major: 4}, MaximumFrame: MaxHelperFrameBytes, MaximumConcurrent: 1,
				Capabilities: []Capability{{Name: CapabilityStrongHash, Version: 1}},
			},
			MaximumRequestDuration: time.Second,
			Handlers: map[CapabilityName]RequestHandler{
				CapabilityStrongHash: func(context.Context, json.RawMessage, EmitFunc) (Completion, error) {
					return Completion{Status: "complete", Reason: "none"}, nil
				},
			},
		})
	}()
	client, err := NewClient(context.Background(), clientSide, clientSide, ClientHello{
		MinimumProtocol: 1, MaximumProtocol: 1, MaximumFrame: MaxHelperFrameBytes, MaximumConcurrent: 1,
		ClientVersion: Version{Major: 4}, Capabilities: []CapabilityRequest{{Name: CapabilityStrongHash, MaximumVersion: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	alphabet := "abcdefghijklmnopqrstuvwxyz234567"
	for index := 0; index < 100; index++ {
		requestID := domain.RequestID("req_" + strings.Repeat("a", 24) + string([]byte{alphabet[index/32], alphabet[index%32]}))
		events, err := client.Start(context.Background(), requestID, CapabilityStrongHash, json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		event := <-events
		if event.Type != FrameComplete {
			t.Fatalf("request %d terminal = %#v", index, event)
		}
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-serviceDone; err != nil {
		t.Fatal(err)
	}
}

func TestHelperClientRejectsInboundResultAndOutputBudgetOverflow(t *testing.T) {
	requestID := domain.RequestID("req_yyyyyyyyyyyyyyyyyyyyyyyyyy")
	client := &Client{requests: map[domain.RequestID]clientRequest{
		requestID: {events: make(chan ClientEvent, 1), results: MaxHelperResults},
	}}
	if _, err := client.acceptResponse(Envelope{Type: FrameResult, RequestID: requestID, Payload: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("client accepted a result beyond its hard per-request limit")
	}
	client.requests[requestID] = clientRequest{events: make(chan ClientEvent, 1), outputBytes: MaxHelperOutputBytes}
	if _, err := client.acceptResponse(Envelope{Type: FrameProgress, RequestID: requestID, Payload: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("client accepted output beyond its hard per-request byte limit")
	}
}

func TestHelperClientHardRequestDeadlineTriggersFatalTransportHook(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	serviceDone := make(chan error, 1)
	go func() {
		serviceDone <- Serve(context.Background(), serverSide, serverSide, ServiceConfig{
			Server: ServerConfig{
				Protocol: 1, HelperVersion: Version{Major: 4}, MaximumFrame: MaxHelperFrameBytes, MaximumConcurrent: 1,
				Capabilities: []Capability{{Name: CapabilityStrongHash, Version: 1}},
			},
			MaximumRequestDuration: time.Second,
			Handlers: map[CapabilityName]RequestHandler{CapabilityStrongHash: func(ctx context.Context, _ json.RawMessage, _ EmitFunc) (Completion, error) {
				<-ctx.Done()
				return Completion{Status: "canceled", Reason: "canceled"}, nil
			}},
		})
	}()
	fatal := make(chan error, 1)
	client, err := newClient(context.Background(), clientSide, clientSide, ClientHello{
		MinimumProtocol: 1, MaximumProtocol: 1, MaximumFrame: MaxHelperFrameBytes, MaximumConcurrent: 1,
		ClientVersion: Version{Major: 4}, Capabilities: []CapabilityRequest{{Name: CapabilityStrongHash, MaximumVersion: 1}},
	}, func(err error) { fatal <- err })
	if err != nil {
		t.Fatal(err)
	}
	client.requestDuration = 20 * time.Millisecond
	events, err := client.Start(context.Background(), "req_qqqqqqqqqqqqqqqqqqqqqqqqqq", CapabilityStrongHash, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event.Err == nil || !strings.Contains(event.Err.Error(), "hard duration") {
			t.Fatalf("deadline event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("hard request deadline did not fail the request")
	}
	select {
	case err := <-fatal:
		if !strings.Contains(err.Error(), "hard duration") {
			t.Fatalf("fatal hook error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("hard request deadline did not trigger the fatal transport hook")
	}
	_ = client.Close()
	_ = serverSide.Close()
	if err := <-serviceDone; err != nil && !strings.Contains(err.Error(), "closed") {
		t.Fatal(err)
	}
}

func TestHelperClientProtocolViolationFailsOnlyHelperSession(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close()
	defer clientSide.Close()
	go func() {
		_, _ = ServeHandshake(serverSide, serverSide, ServerConfig{Protocol: 1, HelperVersion: Version{Major: 4}, MaximumFrame: MaxHelperFrameBytes, MaximumConcurrent: 1, Capabilities: []Capability{{Name: CapabilityStrongHash, Version: 1}}})
		writer, _ := newHelperFrameWriter(serverSide, MaxHelperFrameBytes)
		_ = writer(Envelope{Version: 1, Type: FrameResult, RequestID: domain.RequestID("req_ffffffffffffffffffffffffff")}, map[string]any{"digest": "orphan"})
	}()
	client, err := NewClient(context.Background(), clientSide, clientSide, ClientHello{
		MinimumProtocol: 1, MaximumProtocol: 1, MaximumFrame: MaxHelperFrameBytes, MaximumConcurrent: 1, ClientVersion: Version{Major: 4},
		Capabilities: []CapabilityRequest{{Name: CapabilityStrongHash, MaximumVersion: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	deadline := time.Now().Add(time.Second)
	for client.Failure() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if client.Failure() == nil || client.Level() != 0 {
		t.Fatalf("protocol violation did not degrade helper: level=%d failure=%v", client.Level(), client.Failure())
	}
}

func TestHelperClientHeartbeatKeepsResponsiveSessionAndFailsClosedOnTimeout(t *testing.T) {
	t.Run("responsive", func(t *testing.T) {
		serverSide, clientSide := net.Pipe()
		serviceDone := make(chan error, 1)
		go func() {
			serviceDone <- Serve(context.Background(), serverSide, serverSide, ServiceConfig{
				Server:                 ServerConfig{Protocol: 1, HelperVersion: Version{Major: 4}, MaximumFrame: MaxHelperFrameBytes, MaximumConcurrent: 1},
				MaximumRequestDuration: time.Second,
				Handlers:               map[CapabilityName]RequestHandler{},
			})
		}()
		client, err := NewClient(context.Background(), clientSide, clientSide, ClientHello{MinimumProtocol: 1, MaximumProtocol: 1, MaximumFrame: MaxHelperFrameBytes, MaximumConcurrent: 1, ClientVersion: Version{Major: 4}})
		if err != nil {
			t.Fatal(err)
		}
		if err := client.EnableHeartbeat(10*time.Millisecond, 100*time.Millisecond); err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
		if client.Level() != 1 || client.Failure() != nil {
			t.Fatalf("responsive heartbeat degraded client: level=%d failure=%v", client.Level(), client.Failure())
		}
		_ = client.Close()
		_ = serverSide.Close()
		if err := <-serviceDone; err != nil && !errors.Is(err, io.ErrClosedPipe) && !errors.Is(err, net.ErrClosed) {
			t.Fatal(err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		serverSide, clientSide := net.Pipe()
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = ServeHandshake(serverSide, serverSide, ServerConfig{Protocol: 1, HelperVersion: Version{Major: 4}, MaximumFrame: MaxHelperFrameBytes, MaximumConcurrent: 1})
			var one [1]byte
			_, _ = serverSide.Read(one[:])
		}()
		client, err := NewClient(context.Background(), clientSide, clientSide, ClientHello{MinimumProtocol: 1, MaximumProtocol: 1, MaximumFrame: MaxHelperFrameBytes, MaximumConcurrent: 1, ClientVersion: Version{Major: 4}})
		if err != nil {
			t.Fatal(err)
		}
		if err := client.EnableHeartbeat(10*time.Millisecond, 20*time.Millisecond); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(time.Second)
		for client.Level() != 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if client.Level() != 0 || client.Failure() == nil {
			t.Fatalf("heartbeat timeout did not degrade client: level=%d failure=%v", client.Level(), client.Failure())
		}
		_ = client.Close()
		_ = serverSide.Close()
		<-done
	})
}
