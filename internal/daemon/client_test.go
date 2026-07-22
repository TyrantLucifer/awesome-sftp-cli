package daemon

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestClientNegotiatesCallsAndCancels(t *testing.T) {
	session := &testSession{started: make(chan struct{}), done: make(chan struct{})}
	server := newTestServer(t, sessionFactory(func() Session { return session }))
	serverConn, clientConn := net.Pipe()
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.ServeConn(context.Background(), serverConn) }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := NewClient(ctx, clientConn, "test-client", "client-test")
	if err != nil {
		t.Fatal(err)
	}
	info := client.Info()
	if info.DaemonVersion != "test" || info.Protocol.Major != 1 || info.Protocol.Minor != 0 {
		t.Fatalf("client info = %#v", info)
	}
	var echoed map[string]string
	if err := client.Call(ctx, "echo", map[string]string{"value": "ok"}, &echoed); err != nil {
		t.Fatal(err)
	}
	if echoed["value"] != "ok" {
		t.Fatalf("echo = %#v", echoed)
	}

	requestCtx, cancelRequest := context.WithCancel(ctx)
	callDone := make(chan error, 1)
	go func() { callDone <- client.Call(requestCtx, "block", struct{}{}, nil) }()
	<-session.started
	cancelRequest()
	if err := <-callDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("Call() error = %v", err)
	}
	select {
	case <-session.done:
	case <-time.After(5 * time.Second):
		t.Fatal("server request was not canceled")
	}
	_ = client.Close()
	<-serveDone
}
