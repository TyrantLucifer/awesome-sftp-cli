package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/ipc"
)

type RemoteError struct {
	RequestID domain.RequestID
	RPC       ipc.RPCError
}

func (e *RemoteError) Error() string { return fmt.Sprintf("%s: %s", e.RPC.Code, e.RPC.Message) }

type SafeDiagnosticSummary struct {
	RequestID  domain.RequestID
	ErrorCode  domain.Code
	EndpointID domain.EndpointID
	Retry      domain.RetryKind
	Effect     domain.EffectStatus
}

func (summary SafeDiagnosticSummary) String() string {
	return fmt.Sprintf("request_id=%s error_code=%s endpoint_id=%s retry=%s effect=%s", summary.RequestID, summary.ErrorCode, summary.EndpointID, summary.Retry, summary.Effect)
}

func DiagnosticSummary(err error) SafeDiagnosticSummary {
	var remote *RemoteError
	if !errors.As(err, &remote) || remote == nil {
		return SafeDiagnosticSummary{}
	}
	return SafeDiagnosticSummary{
		RequestID:  remote.RequestID,
		ErrorCode:  remote.RPC.Code,
		EndpointID: remote.RPC.EndpointID,
		Retry:      remote.RPC.Retry.Kind,
		Effect:     remote.RPC.Effect,
	}
}

type Client struct {
	conn      net.Conn
	reader    *ipc.Reader
	writer    *ipc.Writer
	generator domain.RandomGenerator
	version   ipc.ProtocolVersion
	writeMu   sync.Mutex
	mu        sync.Mutex
	pending   map[domain.RequestID]chan ipc.Envelope
	closed    chan struct{}
	closeOnce sync.Once
	err       error
	info      ClientInfo
}

type ClientInfo struct {
	DaemonVersion string
	Protocol      ipc.ProtocolVersion
	Features      []ipc.ProtocolFeature
	EventTypes    []string
}

func NewClient(ctx context.Context, conn net.Conn, clientVersion, instanceID string) (*Client, error) {
	if conn == nil {
		return nil, errors.New("create daemon client: nil connection")
	}
	reader, err := ipc.NewReader(conn, ipc.MaxFrameBytes)
	if err != nil {
		return nil, err
	}
	writer, err := ipc.NewWriter(conn, ipc.MaxFrameBytes)
	if err != nil {
		return nil, err
	}
	c := &Client{conn: conn, reader: reader, writer: writer, version: ipc.ProtocolVersion{Major: ipc.ProtocolMajor, Minor: ipc.ProtocolMinor}, pending: make(map[domain.RequestID]chan ipc.Envelope), closed: make(chan struct{})}
	var hello ipc.HelloResponse
	if err := c.callBeforeReadLoop(ctx, RequestHello, ipc.HelloRequest{ClientVersion: clientVersion, ClientInstanceID: instanceID, Protocols: []ipc.VersionRange{{Major: ipc.ProtocolMajor, MinMinor: 0, MaxMinor: ipc.ProtocolMinor}}}, &hello); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("create daemon client: hello: %w", err)
	}
	c.version = hello.Selected
	c.info = ClientInfo{
		DaemonVersion: hello.DaemonVersion,
		Protocol:      hello.Selected,
		Features:      append([]ipc.ProtocolFeature(nil), hello.Features...),
		EventTypes:    append([]string(nil), hello.EventTypes...),
	}
	go c.readLoop()
	return c, nil
}

func (c *Client) Info() ClientInfo {
	if c == nil {
		return ClientInfo{}
	}
	return ClientInfo{
		DaemonVersion: c.info.DaemonVersion,
		Protocol:      c.info.Protocol,
		Features:      append([]ipc.ProtocolFeature(nil), c.info.Features...),
		EventTypes:    append([]string(nil), c.info.EventTypes...),
	}
}

func (c *Client) Call(ctx context.Context, name string, request, response any) error {
	id, err := domain.NewRequestID(&c.generator)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode %s request: %w", name, err)
	}
	result := make(chan ipc.Envelope, 1)
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return err
	}
	c.pending[id] = result
	c.mu.Unlock()
	if err := c.write(ipc.Envelope{Protocol: c.version, Kind: ipc.KindRequest, Name: name, RequestID: id, Payload: payload}); err != nil {
		c.removePending(id)
		return err
	}
	select {
	case envelope := <-result:
		return decodeClientResponse(envelope, response)
	case <-ctx.Done():
		c.removePending(id)
		go c.sendCancel(id)
		return ctx.Err()
	case <-c.closed:
		c.mu.Lock()
		err := c.err
		c.mu.Unlock()
		if err == nil {
			err = net.ErrClosed
		}
		return err
	}
}

func (c *Client) Close() error { c.fail(net.ErrClosed); return nil }

func (c *Client) callBeforeReadLoop(ctx context.Context, name string, request, response any) error {
	id, err := domain.NewRequestID(&c.generator)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.conn.SetDeadline(deadline)
		defer c.conn.SetDeadline(noDeadline)
	}
	if err := c.write(ipc.Envelope{Protocol: c.version, Kind: ipc.KindRequest, Name: name, RequestID: id, Payload: payload}); err != nil {
		return err
	}
	frame, err := c.reader.ReadFrame()
	if err != nil {
		return err
	}
	envelope, err := ipc.DecodeEnvelope(frame)
	if err != nil {
		return err
	}
	if envelope.RequestID != id {
		return errors.New("daemon hello response request ID mismatch")
	}
	return decodeClientResponse(envelope, response)
}

var noDeadline = func() (zero time.Time) { return }()

func (c *Client) readLoop() {
	for {
		frame, err := c.reader.ReadFrame()
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = net.ErrClosed
			}
			c.fail(err)
			return
		}
		envelope, err := ipc.DecodeEnvelope(frame)
		if err != nil {
			c.fail(err)
			return
		}
		if envelope.Kind != ipc.KindResponse {
			continue
		}
		c.mu.Lock()
		target := c.pending[envelope.RequestID]
		delete(c.pending, envelope.RequestID)
		c.mu.Unlock()
		if target != nil {
			target <- envelope
		}
	}
}

func (c *Client) write(envelope ipc.Envelope) error {
	data, err := ipc.EncodeEnvelope(envelope)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.writer.WriteFrame(data)
}
func (c *Client) removePending(id domain.RequestID) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}
func (c *Client) sendCancel(target domain.RequestID) {
	id, err := domain.NewRequestID(&c.generator)
	if err != nil {
		return
	}
	payload, _ := json.Marshal(ipc.CancelRequest{TargetRequestID: target})
	_ = c.write(ipc.Envelope{Protocol: c.version, Kind: ipc.KindRequest, Name: RequestCancel, RequestID: id, Payload: payload})
}
func (c *Client) fail(err error) {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.err = err
		c.pending = make(map[domain.RequestID]chan ipc.Envelope)
		c.mu.Unlock()
		_ = c.conn.Close()
		close(c.closed)
	})
}

func decodeClientResponse(envelope ipc.Envelope, destination any) error {
	if envelope.Error != nil {
		return &RemoteError{RequestID: envelope.RequestID, RPC: *envelope.Error}
	}
	if destination == nil {
		return nil
	}
	if err := json.Unmarshal(envelope.Payload, destination); err != nil {
		return fmt.Errorf("decode %s response: %w", envelope.Name, err)
	}
	return nil
}
