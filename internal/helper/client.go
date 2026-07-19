package helper

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
)

type ClientEvent struct {
	Type      FrameType
	RequestID domain.RequestID
	Payload   json.RawMessage
	Err       error
}

type clientRequest struct {
	events      chan ClientEvent
	stop        func() bool
	results     uint64
	outputBytes uint64
}

const clientEventBuffer = 64

type Client struct {
	ctx        context.Context
	cancel     context.CancelFunc
	reader     *ipc.Reader
	write      func(Envelope, any) error
	input      io.Reader
	output     io.Writer
	negotiated Negotiated

	mu               sync.Mutex
	requests         map[domain.RequestID]clientRequest
	seen             map[domain.RequestID]struct{}
	failure          error
	closed           bool
	done             chan struct{}
	pong             chan string
	heartbeatStarted bool
	heartbeatNonce   string
	transportOnce    sync.Once
	fatalOnce        sync.Once
	onFatal          func(error)
	requestDuration  time.Duration
}

func NewClient(parent context.Context, reader io.Reader, writer io.Writer, hello ClientHello) (*Client, error) {
	return newClient(parent, reader, writer, hello, nil)
}

func newClient(parent context.Context, reader io.Reader, writer io.Writer, hello ClientHello, onFatal func(error)) (*Client, error) {
	if parent == nil || reader == nil || writer == nil {
		return nil, errors.New("create helper client: context and stdio are required")
	}
	if err := validateClientHello(hello); err != nil {
		return nil, err
	}
	if err := ValidateHelperPreface(reader); err != nil {
		return nil, err
	}
	write, err := newHelperFrameWriter(writer, hello.MaximumFrame)
	if err != nil {
		return nil, err
	}
	if err := write(Envelope{Version: EnvelopeVersion, Type: FrameClientHello}, hello); err != nil {
		return nil, fmt.Errorf("create helper client: send hello: %w", err)
	}
	frameReader, err := ipc.NewReader(reader, hello.MaximumFrame)
	if err != nil {
		return nil, err
	}
	raw, err := frameReader.ReadFrame()
	if err != nil {
		return nil, fmt.Errorf("create helper client: read hello: %w", err)
	}
	envelope, err := DecodeHelperEnvelope(raw)
	if err != nil {
		return nil, err
	}
	negotiated, err := parseServerHello(envelope, hello)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	client := &Client{
		ctx: ctx, cancel: cancel, reader: frameReader, write: write,
		input: reader, output: writer, negotiated: negotiated,
		requests: make(map[domain.RequestID]clientRequest), seen: make(map[domain.RequestID]struct{}), done: make(chan struct{}), pong: make(chan string, 1),
		onFatal: onFatal, requestDuration: MaxHelperRequestDuration,
	}
	go client.readLoop()
	return client, nil
}

func newHelperFrameWriter(writer io.Writer, maximum uint32) (func(Envelope, any) error, error) {
	frameWriter, err := ipc.NewWriter(writer, maximum)
	if err != nil {
		return nil, err
	}
	var mu sync.Mutex
	return func(envelope Envelope, payload any) error {
		rawPayload, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		envelope.Payload = rawPayload
		raw, err := EncodeHelperEnvelope(envelope)
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		return frameWriter.WriteFrame(raw)
	}, nil
}

func parseServerHello(envelope Envelope, client ClientHello) (Negotiated, error) {
	if envelope.Type != FrameServerHello || envelope.RequestID != "" {
		return Negotiated{}, errors.New("parse helper server hello: wrong envelope type")
	}
	var negotiated Negotiated
	if err := decodeStrictPayload(envelope.Payload, &negotiated); err != nil {
		return Negotiated{}, err
	}
	if negotiated.Protocol < client.MinimumProtocol || negotiated.Protocol > client.MaximumProtocol || negotiated.MaximumFrame < 1024 || negotiated.MaximumFrame > client.MaximumFrame || negotiated.MaximumConcurrent == 0 || negotiated.MaximumConcurrent > client.MaximumConcurrent || negotiated.PathSemantics != "absolute_or_scope_relative_bytes" || negotiated.TimeSemantics != "unix_nanoseconds_best_effort" {
		return Negotiated{}, errors.New("parse helper server hello: negotiated bounds or semantics are invalid")
	}
	if _, err := parseReleaseVersion(negotiated.HelperVersion); err != nil {
		return Negotiated{}, errors.New("parse helper server hello: helper version is invalid")
	}
	requested := make(map[CapabilityName]uint16, len(client.Capabilities))
	for _, capability := range client.Capabilities {
		requested[capability.Name] = capability.MaximumVersion
	}
	seen := make(map[CapabilityName]struct{}, len(negotiated.Capabilities))
	lastOrder := -1
	for _, capability := range negotiated.Capabilities {
		maximum, ok := requested[capability.Name]
		order := capabilityIndex(capability.Name)
		if !ok || capability.Version == 0 || capability.Version > maximum || order <= lastOrder {
			return Negotiated{}, errors.New("parse helper server hello: capability violates client offer")
		}
		if _, duplicate := seen[capability.Name]; duplicate {
			return Negotiated{}, errors.New("parse helper server hello: duplicate capability")
		}
		seen[capability.Name] = struct{}{}
		lastOrder = order
	}
	return negotiated, nil
}

func capabilityIndex(name CapabilityName) int {
	for index, candidate := range capabilityOrder {
		if candidate == name {
			return index
		}
	}
	return -1
}

func (c *Client) Start(ctx context.Context, requestID domain.RequestID, operation CapabilityName, body json.RawMessage) (<-chan ClientEvent, error) {
	if ctx == nil {
		return nil, errors.New("start helper request: context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := domain.ParseRequestID(string(requestID)); err != nil {
		return nil, errors.New("start helper request: request ID is invalid")
	}
	if !c.HasCapability(operation) {
		return nil, errors.New("start helper request: capability was not negotiated")
	}
	trimmed := bytesTrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' || len(trimmed) > MaxHelperFrameBytes {
		return nil, errors.New("start helper request: body must be a bounded JSON object")
	}
	if err := validateJSONShape(trimmed); err != nil {
		return nil, err
	}
	events := make(chan ClientEvent, clientEventBuffer)
	c.mu.Lock()
	if c.closed || c.failure != nil {
		failure := c.failure
		if failure == nil {
			failure = errors.New("helper client is closed")
		}
		c.mu.Unlock()
		return nil, failure
	}
	if _, duplicate := c.seen[requestID]; duplicate {
		c.mu.Unlock()
		return nil, errors.New("start helper request: request ID reuse is forbidden")
	}
	if len(c.requests) >= int(c.negotiated.MaximumConcurrent) {
		c.mu.Unlock()
		return nil, errors.New("start helper request: concurrent request limit reached")
	}
	c.requests[requestID] = clientRequest{events: events}
	c.seen[requestID] = struct{}{}
	c.mu.Unlock()
	if err := c.write(Envelope{Version: EnvelopeVersion, Type: FrameRequest, RequestID: requestID}, RequestPayload{Operation: operation, Body: append(json.RawMessage(nil), trimmed...)}); err != nil {
		c.fail(fmt.Errorf("start helper request: write: %w", err))
		return nil, err
	}
	stopAfter := context.AfterFunc(ctx, func() { _ = c.Cancel(requestID) })
	hardTimer := time.AfterFunc(c.requestDuration, func() {
		c.failAndClose(errors.New("helper request exceeded the hard duration limit"))
	})
	stop := func() bool {
		stopped := stopAfter()
		hardStopped := hardTimer.Stop()
		return stopped && hardStopped
	}
	c.mu.Lock()
	request, stillActive := c.requests[requestID]
	if stillActive {
		request.stop = stop
		c.requests[requestID] = request
	}
	c.mu.Unlock()
	if !stillActive {
		stop()
	}
	return events, nil
}

func (c *Client) Cancel(requestID domain.RequestID) error {
	c.mu.Lock()
	_, active := c.requests[requestID]
	failure := c.failure
	closed := c.closed
	c.mu.Unlock()
	if !active {
		return nil
	}
	if failure != nil {
		return failure
	}
	if closed {
		return errors.New("cancel helper request: client is closed")
	}
	return c.write(Envelope{Version: EnvelopeVersion, Type: FrameCancel, RequestID: requestID}, struct{}{})
}

func (c *Client) HasCapability(name CapabilityName) bool {
	for _, capability := range c.negotiated.Capabilities {
		if capability.Name == name {
			return true
		}
	}
	return false
}

func (c *Client) Negotiated() Negotiated { return c.negotiated }

func (c *Client) Level() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failure != nil || c.closed {
		return 0
	}
	return 1
}

func (c *Client) Failure() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failure
}

// EnableHeartbeat starts one bounded session liveness loop. A missed or
// malformed pong degrades only this Helper client; callers can continue using
// the independent Level 0 Provider session.
func (c *Client) EnableHeartbeat(interval, timeout time.Duration) error {
	if interval < time.Millisecond || interval > time.Minute || timeout < time.Millisecond || timeout > time.Minute {
		return errors.New("enable helper heartbeat: interval or timeout is outside hard limits")
	}
	if _, ok := c.input.(io.Closer); !ok {
		if _, ok := c.output.(io.Closer); !ok {
			return errors.New("enable helper heartbeat: transport cannot be closed on timeout")
		}
	}
	c.mu.Lock()
	if c.closed || c.failure != nil {
		c.mu.Unlock()
		return errors.New("enable helper heartbeat: client is unavailable")
	}
	if c.heartbeatStarted {
		c.mu.Unlock()
		return errors.New("enable helper heartbeat: heartbeat is already enabled")
	}
	c.heartbeatStarted = true
	c.mu.Unlock()
	go c.heartbeatLoop(interval, timeout)
	return nil
}

func (c *Client) heartbeatLoop(interval, timeout time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
		}
		var random [8]byte
		if _, err := rand.Read(random[:]); err != nil {
			c.failAndClose(fmt.Errorf("helper heartbeat entropy: %w", err))
			return
		}
		nonce := hex.EncodeToString(random[:])
		c.mu.Lock()
		if c.closed || c.failure != nil {
			c.mu.Unlock()
			return
		}
		c.heartbeatNonce = nonce
		c.mu.Unlock()
		timer := time.NewTimer(timeout)
		writeDone := make(chan error, 1)
		go func() {
			writeDone <- c.write(Envelope{Version: EnvelopeVersion, Type: FramePing}, struct {
				Nonce string `json:"nonce"`
			}{Nonce: nonce})
		}()
		select {
		case err := <-writeDone:
			if err != nil {
				if !timer.Stop() {
					<-timer.C
				}
				c.failAndClose(fmt.Errorf("helper heartbeat write: %w", err))
				return
			}
		case <-timer.C:
			c.failAndClose(errors.New("helper heartbeat: write timeout"))
			return
		case <-c.ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		}
		select {
		case received := <-c.pong:
			if !timer.Stop() {
				<-timer.C
			}
			if received != nonce {
				c.failAndClose(errors.New("helper heartbeat: pong nonce mismatch"))
				return
			}
			c.mu.Lock()
			c.heartbeatNonce = ""
			c.mu.Unlock()
		case <-timer.C:
			c.failAndClose(errors.New("helper heartbeat: pong timeout"))
			return
		case <-c.ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		}
	}
}

func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	c.cancel()
	c.closeTransport()
	<-c.done
	return nil
}

func (c *Client) closeTransport() {
	c.transportOnce.Do(func() {
		if closer, ok := c.output.(io.Closer); ok {
			_ = closer.Close()
		}
		if closer, ok := c.input.(io.Closer); ok {
			_ = closer.Close()
		}
	})
}

func (c *Client) failAndClose(err error) {
	c.fail(err)
	c.closeTransport()
}

func (c *Client) readLoop() {
	defer close(c.done)
	for {
		raw, err := c.reader.ReadFrame()
		if err != nil {
			if c.ctx.Err() != nil {
				c.failRequests(errors.New("helper session closed"))
				return
			}
			c.fail(fmt.Errorf("helper session read: %w", err))
			return
		}
		envelope, err := DecodeHelperEnvelope(raw)
		if err != nil {
			c.fail(err)
			return
		}
		if envelope.Type == FramePong {
			var pong struct {
				Nonce string `json:"nonce"`
			}
			if err := decodeStrictPayload(envelope.Payload, &pong); err != nil || len(pong.Nonce) != 16 || !isLowerHex(pong.Nonce) {
				c.fail(errors.New("helper session: invalid pong"))
				return
			}
			c.mu.Lock()
			expected := c.heartbeatNonce
			c.mu.Unlock()
			if expected == "" || pong.Nonce != expected {
				c.fail(errors.New("helper session: unexpected pong"))
				return
			}
			select {
			case c.pong <- pong.Nonce:
			default:
				c.fail(errors.New("helper session: duplicate pong"))
				return
			}
			continue
		}
		if envelope.Type != FrameResult && envelope.Type != FrameProgress && envelope.Type != FrameError && envelope.Type != FrameComplete {
			c.fail(fmt.Errorf("helper session: unexpected server frame %q", envelope.Type))
			return
		}
		request, err := c.acceptResponse(envelope)
		if err != nil {
			c.failAndClose(err)
			return
		}
		if envelope.Type == FrameComplete && request.stop != nil {
			request.stop()
		}
		event := ClientEvent{Type: envelope.Type, RequestID: envelope.RequestID, Payload: append(json.RawMessage(nil), envelope.Payload...)}
		select {
		case request.events <- event:
		case <-c.ctx.Done():
			c.failRequests(errors.New("helper session closed"))
			return
		}
		if envelope.Type == FrameComplete {
			close(request.events)
		}
	}
}

func (c *Client) acceptResponse(envelope Envelope) (clientRequest, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	request, exists := c.requests[envelope.RequestID]
	if !exists {
		return clientRequest{}, errors.New("helper session: orphan or duplicate response")
	}
	request.outputBytes += uint64(len(envelope.Payload))
	if envelope.Type == FrameResult {
		request.results++
	}
	if request.results > MaxHelperResults || request.outputBytes > MaxHelperOutputBytes {
		return clientRequest{}, errors.New("helper session: response exceeded hard result or output limit")
	}
	if envelope.Type == FrameComplete {
		delete(c.requests, envelope.RequestID)
	} else {
		c.requests[envelope.RequestID] = request
	}
	return request, nil
}

func (c *Client) fail(err error) {
	c.mu.Lock()
	if c.failure == nil {
		c.failure = err
	}
	c.mu.Unlock()
	c.fatalOnce.Do(func() {
		if c.onFatal != nil {
			c.onFatal(err)
		}
	})
	c.cancel()
	c.failRequests(err)
}

func (c *Client) failRequests(err error) {
	c.mu.Lock()
	requests := c.requests
	c.requests = make(map[domain.RequestID]clientRequest)
	c.mu.Unlock()
	for requestID, request := range requests {
		if request.stop != nil {
			request.stop()
		}
		select {
		case request.events <- ClientEvent{RequestID: requestID, Err: err}:
		default:
		}
		close(request.events)
	}
}
