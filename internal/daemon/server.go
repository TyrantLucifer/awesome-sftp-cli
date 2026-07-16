package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/diagnostic"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
)

const (
	RequestHello  = "hello"
	RequestCancel = "cancel"
)

type Session interface {
	Handle(context.Context, string, json.RawMessage) (any, error)
	Close() error
}

type SessionFactory interface {
	NewSession() Session
}

type ServerConfig struct {
	BuildVersion     string
	Epoch            string
	Features         []ipc.ProtocolFeature
	EventTypes       []string
	Sessions         SessionFactory
	MaxInFlight      int
	HandshakeTimeout time.Duration
	VerifyPeer       func(net.Conn) error
	Logger           *slog.Logger
}

type Server struct {
	buildVersion     string
	epoch            string
	features         []ipc.ProtocolFeature
	eventTypes       []string
	sessions         SessionFactory
	maxInFlight      int
	handshakeTimeout time.Duration
	verifyPeer       func(net.Conn) error
	logger           *slog.Logger
}

func NewServer(config ServerConfig) (*Server, error) {
	if config.BuildVersion == "" {
		return nil, errors.New("create daemon server: build version is empty")
	}
	if config.Epoch == "" {
		return nil, errors.New("create daemon server: event epoch is empty")
	}
	if config.Sessions == nil {
		return nil, errors.New("create daemon server: session factory is nil")
	}
	if config.MaxInFlight <= 0 {
		return nil, errors.New("create daemon server: maximum in-flight requests must be positive")
	}
	if config.HandshakeTimeout <= 0 {
		return nil, errors.New("create daemon server: handshake timeout must be positive")
	}
	if config.VerifyPeer == nil {
		return nil, errors.New("create daemon server: peer verifier is nil")
	}
	if config.Logger == nil {
		return nil, errors.New("create daemon server: logger is nil")
	}
	return &Server{
		buildVersion:     config.BuildVersion,
		epoch:            config.Epoch,
		features:         append([]ipc.ProtocolFeature(nil), config.Features...),
		eventTypes:       append([]string(nil), config.EventTypes...),
		sessions:         config.Sessions,
		maxInFlight:      config.MaxInFlight,
		handshakeTimeout: config.HandshakeTimeout,
		verifyPeer:       config.VerifyPeer,
		logger:           config.Logger,
	}, nil
}

func (s *Server) ServeConn(parent context.Context, conn net.Conn) error {
	if conn == nil {
		return errors.New("serve daemon connection: nil connection")
	}
	defer conn.Close()
	if err := s.verifyPeer(conn); err != nil {
		return fmt.Errorf("serve daemon connection: verify peer: %w", err)
	}

	ctx, cancelConnection := context.WithCancel(parent)
	defer cancelConnection()
	connectionDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-connectionDone:
		}
	}()
	defer close(connectionDone)
	session := s.sessions.NewSession()
	if session == nil {
		return errors.New("serve daemon connection: session factory returned nil")
	}

	reader, err := ipc.NewReader(conn, ipc.MaxFrameBytes)
	if err != nil {
		_ = session.Close()
		return err
	}
	frameWriter, err := ipc.NewWriter(conn, ipc.MaxFrameBytes)
	if err != nil {
		_ = session.Close()
		return err
	}
	writer := &connectionWriter{writer: frameWriter, conn: conn, cancel: cancelConnection}

	active := make(map[domain.RequestID]context.CancelFunc)
	var activeMu sync.Mutex
	var requests sync.WaitGroup
	defer func() {
		cancelConnection()
		activeMu.Lock()
		for _, cancel := range active {
			cancel()
		}
		activeMu.Unlock()
		requests.Wait()
		_ = session.Close()
	}()

	if err := conn.SetReadDeadline(time.Now().Add(s.handshakeTimeout)); err != nil {
		return fmt.Errorf("serve daemon connection: set handshake deadline: %w", err)
	}
	first, err := readRequestEnvelope(reader)
	if err != nil {
		return fmt.Errorf("serve daemon connection: read hello: %w", err)
	}
	selected, err := s.handleHello(first, writer)
	if err != nil {
		return err
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("serve daemon connection: clear handshake deadline: %w", err)
	}

	semaphore := make(chan struct{}, s.maxInFlight)
	for {
		request, err := readRequestEnvelope(reader)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("serve daemon connection: read request: %w", err)
		}
		if request.Kind != ipc.KindRequest {
			_ = writer.writeError(selected, request, protocolError("only request envelopes are accepted"))
			continue
		}
		if request.Name == RequestHello {
			_ = writer.writeError(selected, request, protocolError("hello is only valid as the first frame"))
			continue
		}
		if request.Name == RequestCancel {
			result := ipc.CancelResult{State: ipc.CancelNotFound}
			var cancelRequest ipc.CancelRequest
			if err := decodePayload(request.Payload, &cancelRequest); err != nil {
				_ = writer.writeError(selected, request, invalidArgument("decode cancel request", err))
				continue
			}
			activeMu.Lock()
			cancel := active[cancelRequest.TargetRequestID]
			if cancel != nil {
				result.State = ipc.CancelAccepted
				cancel()
			}
			activeMu.Unlock()
			_ = writer.writePayload(selected, request, result)
			continue
		}

		activeMu.Lock()
		_, duplicate := active[request.RequestID]
		activeMu.Unlock()
		if duplicate {
			_ = writer.writeError(selected, request, invalidArgument("request ID is already active", nil))
			continue
		}
		select {
		case semaphore <- struct{}{}:
		default:
			_ = writer.writeError(selected, request, &domain.OpError{
				Code:    domain.CodeResourceExhausted,
				Message: "too many in-flight requests",
				Retry:   domain.RetryAdvice{Kind: domain.RetryBackoff},
				Effect:  domain.EffectNone,
			})
			continue
		}
		requestCtx, cancelRequest := context.WithCancel(ctx)
		activeMu.Lock()
		active[request.RequestID] = cancelRequest
		activeMu.Unlock()
		requests.Add(1)
		go func(request ipc.Envelope) {
			defer requests.Done()
			defer func() { <-semaphore }()
			defer cancelRequest()
			s.logRequest("rpc_request_started", request.RequestID, nil)
			payload, handleErr := session.Handle(requestCtx, request.Name, request.Payload)
			activeMu.Lock()
			delete(active, request.RequestID)
			activeMu.Unlock()
			if handleErr != nil {
				wireError := rpcError(handleErr)
				s.logRequest("rpc_request_failed", request.RequestID, wireError)
				_ = writer.writeError(selected, request, handleErr)
				return
			}
			s.logRequest("rpc_request_succeeded", request.RequestID, nil)
			_ = writer.writePayload(selected, request, payload)
		}(request)
	}
}

func (s *Server) logRequest(event string, requestID domain.RequestID, failure *ipc.RPCError) {
	attributes := []slog.Attr{
		diagnostic.Component("daemon"),
		diagnostic.Event(event),
		diagnostic.RequestID(requestID),
	}
	if failure != nil {
		attributes = append(attributes, diagnostic.ErrorCode(failure.Code))
		if failure.EndpointID != "" {
			attributes = append(attributes, diagnostic.EndpointID(failure.EndpointID))
		}
	}
	s.logger.LogAttrs(context.Background(), slog.LevelInfo, "daemon request", attributes...)
}

func (s *Server) handleHello(request ipc.Envelope, writer *connectionWriter) (ipc.ProtocolVersion, error) {
	defaultVersion := ipc.ProtocolVersion{Major: ipc.ProtocolMajor, Minor: ipc.ProtocolMinor}
	if request.Kind != ipc.KindRequest || request.Name != RequestHello {
		_ = writer.writeError(defaultVersion, request, protocolError("first frame must be hello request"))
		return ipc.ProtocolVersion{}, errors.New("serve daemon connection: first frame is not hello")
	}
	var hello ipc.HelloRequest
	if err := decodePayload(request.Payload, &hello); err != nil {
		_ = writer.writeError(defaultVersion, request, invalidArgument("decode hello request", err))
		return ipc.ProtocolVersion{}, fmt.Errorf("serve daemon connection: decode hello: %w", err)
	}
	if hello.ClientVersion == "" || hello.ClientInstanceID == "" {
		err := invalidArgument("hello client identity is empty", nil)
		_ = writer.writeError(defaultVersion, request, err)
		return ipc.ProtocolVersion{}, err
	}
	selected, features, err := ipc.Negotiate(
		hello.Protocols,
		[]ipc.VersionRange{{Major: ipc.ProtocolMajor, MinMinor: 0, MaxMinor: ipc.ProtocolMinor}},
		hello.Features,
		s.features,
	)
	if err != nil {
		_ = writer.writeError(defaultVersion, request, err)
		return ipc.ProtocolVersion{}, fmt.Errorf("serve daemon connection: negotiate hello: %w", err)
	}
	cursor := ipc.EventCursor{Epoch: s.epoch, Sequence: 0}
	response := ipc.HelloResponse{
		DaemonVersion: s.buildVersion,
		Selected:      selected,
		Features:      features,
		EventTypes:    intersectStrings(hello.EventTypes, s.eventTypes),
		CurrentCursor: cursor,
		OldestCursor:  cursor,
	}
	if err := writer.writePayload(selected, request, response); err != nil {
		return ipc.ProtocolVersion{}, fmt.Errorf("serve daemon connection: write hello: %w", err)
	}
	return selected, nil
}

type connectionWriter struct {
	mu sync.Mutex

	writer *ipc.Writer
	conn   net.Conn
	cancel context.CancelFunc
}

func (w *connectionWriter) writePayload(version ipc.ProtocolVersion, request ipc.Envelope, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return w.writeError(version, request, &domain.OpError{
			Code:    domain.CodeInternal,
			Message: "encode response payload",
			Retry:   domain.RetryAdvice{Kind: domain.RetryNever},
			Effect:  domain.EffectNone,
			Cause:   err,
		})
	}
	return w.write(ipc.Envelope{
		Protocol:  version,
		Kind:      ipc.KindResponse,
		Name:      request.Name,
		RequestID: request.RequestID,
		Payload:   encoded,
	})
}

func (w *connectionWriter) writeError(version ipc.ProtocolVersion, request ipc.Envelope, err error) error {
	return w.write(ipc.Envelope{
		Protocol:  version,
		Kind:      ipc.KindResponse,
		Name:      request.Name,
		RequestID: request.RequestID,
		Error:     rpcError(err),
	})
}

func (w *connectionWriter) write(envelope ipc.Envelope) error {
	encoded, err := ipc.EncodeEnvelope(envelope)
	if err != nil {
		return err
	}
	w.mu.Lock()
	err = w.writer.WriteFrame(encoded)
	w.mu.Unlock()
	if err != nil {
		w.cancel()
		_ = w.conn.Close()
	}
	return err
}

func readRequestEnvelope(reader *ipc.Reader) (ipc.Envelope, error) {
	frame, err := reader.ReadFrame()
	if err != nil {
		return ipc.Envelope{}, err
	}
	return ipc.DecodeEnvelope(frame)
}

func decodePayload(payload json.RawMessage, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func rpcError(err error) *ipc.RPCError {
	var operationError *domain.OpError
	if !errors.As(err, &operationError) || operationError == nil {
		operationError = &domain.OpError{
			Code:    domain.CodeInternal,
			Message: "request failed",
			Retry:   domain.RetryAdvice{Kind: domain.RetryNever},
			Effect:  domain.EffectUnknown,
		}
	}
	wire := &ipc.RPCError{
		Code:       operationError.Code,
		Message:    operationError.Message,
		Operation:  operationError.Operation,
		EndpointID: operationError.EndpointID,
		Retry:      operationError.Retry,
		Effect:     operationError.Effect,
	}
	if operationError.Location != nil {
		location := ipc.EncodeLocation(*operationError.Location)
		wire.Location = &location
	}
	return wire
}

func protocolError(message string) error {
	return &domain.OpError{
		Code:    domain.CodeProtocolIncompatible,
		Message: message,
		Retry:   domain.RetryAdvice{Kind: domain.RetryNever},
		Effect:  domain.EffectNone,
	}
}

func invalidArgument(message string, cause error) error {
	return &domain.OpError{
		Code:    domain.CodeInvalidArgument,
		Message: message,
		Retry:   domain.RetryAdvice{Kind: domain.RetryNever},
		Effect:  domain.EffectNone,
		Cause:   cause,
	}
}

func intersectStrings(left, right []string) []string {
	rightSet := make(map[string]struct{}, len(right))
	for _, value := range right {
		if value != "" {
			rightSet[value] = struct{}{}
		}
	}
	seen := make(map[string]struct{})
	var result []string
	for _, value := range left {
		if _, exists := rightSet[value]; !exists {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
