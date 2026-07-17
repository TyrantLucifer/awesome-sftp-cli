package helper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
)

const (
	MaxHelperRequestDuration = 10 * time.Minute
	MaxHelperResults         = 100_000
	MaxHelperOutputBytes     = 64 << 20
	maxHelperTerminalBytes   = 4 << 10
)

type RequestPayload struct {
	Operation CapabilityName  `json:"operation"`
	Body      json.RawMessage `json:"body"`
}

type Completion struct {
	Status      string `json:"status"`
	Reason      string `json:"reason"`
	Entries     uint64 `json:"entries,omitempty"`
	Files       uint64 `json:"files,omitempty"`
	BytesRead   uint64 `json:"bytes_read,omitempty"`
	Results     uint64 `json:"results,omitempty"`
	OutputBytes uint64 `json:"output_bytes,omitempty"`
}

type StructuredError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type EmitFunc func(FrameType, any) error

type RequestHandler func(context.Context, json.RawMessage, EmitFunc) (Completion, error)

type ServiceConfig struct {
	Server                 ServerConfig
	MaximumRequestDuration time.Duration
	Handlers               map[CapabilityName]RequestHandler
}

type serviceWriter struct {
	mu     sync.Mutex
	writer *ipc.Writer
}

func (w *serviceWriter) send(envelope Envelope, payload any) (int, error) {
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	return w.sendEncoded(envelope, rawPayload)
}

func (w *serviceWriter) sendEncoded(envelope Envelope, rawPayload json.RawMessage) (int, error) {
	envelope.Payload = rawPayload
	raw, err := EncodeHelperEnvelope(envelope)
	if err != nil {
		return 0, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.writer.WriteFrame(raw); err != nil {
		return 0, err
	}
	return len(rawPayload), nil
}

type activeRequest struct{ cancel context.CancelFunc }

func Serve(ctx context.Context, reader io.Reader, writer io.Writer, config ServiceConfig) error {
	if ctx == nil {
		return errors.New("serve helper: context is required")
	}
	if config.MaximumRequestDuration <= 0 || config.MaximumRequestDuration > MaxHelperRequestDuration {
		return errors.New("serve helper: request duration is outside hard limits")
	}
	negotiated, err := ServeHandshake(reader, writer, config.Server)
	if err != nil {
		return err
	}
	frameReader, err := ipc.NewReader(reader, negotiated.MaximumFrame)
	if err != nil {
		return err
	}
	frameWriter, err := ipc.NewWriter(writer, negotiated.MaximumFrame)
	if err != nil {
		return err
	}
	out := &serviceWriter{writer: frameWriter}
	available := make(map[CapabilityName]struct{}, len(negotiated.Capabilities))
	for _, capability := range negotiated.Capabilities {
		available[capability.Name] = struct{}{}
	}
	serviceCtx, cancelService := context.WithCancel(ctx)
	defer cancelService()
	active := make(map[domain.RequestID]activeRequest)
	seen := make(map[domain.RequestID]struct{})
	var activeMu sync.Mutex
	var requests sync.WaitGroup
	cancelAll := func() {
		activeMu.Lock()
		defer activeMu.Unlock()
		for _, request := range active {
			request.cancel()
		}
	}
	defer func() {
		cancelAll()
		requests.Wait()
	}()

	for {
		raw, err := frameReader.ReadFrame()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("serve helper: read frame: %w", err)
		}
		envelope, err := DecodeHelperEnvelope(raw)
		if err != nil {
			return err
		}
		switch envelope.Type {
		case FramePing:
			var ping struct {
				Nonce string `json:"nonce"`
			}
			if err := decodeStrictPayload(envelope.Payload, &ping); err != nil || len(ping.Nonce) != 16 || !isLowerHex(ping.Nonce) {
				return errors.New("serve helper: invalid ping")
			}
			if _, err := out.send(Envelope{Version: 1, Type: FramePong}, ping); err != nil {
				return err
			}
		case FrameCancel:
			var empty struct{}
			if err := decodeStrictPayload(envelope.Payload, &empty); err != nil {
				return err
			}
			activeMu.Lock()
			request, exists := active[envelope.RequestID]
			activeMu.Unlock()
			if exists {
				request.cancel()
			}
		case FrameRequest:
			var request RequestPayload
			if err := decodeStrictPayload(envelope.Payload, &request); err != nil {
				return err
			}
			trimmedBody := json.RawMessage(bytesTrimSpace(request.Body))
			if len(trimmedBody) == 0 || trimmedBody[0] != '{' {
				return errors.New("serve helper: request body must be an object")
			}
			activeMu.Lock()
			_, duplicate := seen[envelope.RequestID]
			if duplicate {
				activeMu.Unlock()
				return errors.New("serve helper: request ID reuse is forbidden")
			}
			seen[envelope.RequestID] = struct{}{}
			activeMu.Unlock()
			if _, ok := available[request.Operation]; !ok || config.Handlers[request.Operation] == nil {
				if err := sendRejected(out, envelope.RequestID, "capability_unavailable", "requested capability was not negotiated"); err != nil {
					return err
				}
				continue
			}
			activeMu.Lock()
			if len(active) >= int(negotiated.MaximumConcurrent) {
				activeMu.Unlock()
				if err := sendRejected(out, envelope.RequestID, "concurrency_limit", "maximum concurrent requests reached"); err != nil {
					return err
				}
				continue
			}
			requestCtx, cancel := context.WithTimeout(serviceCtx, config.MaximumRequestDuration)
			active[envelope.RequestID] = activeRequest{cancel: cancel}
			activeMu.Unlock()
			requests.Add(1)
			go func(runCtx context.Context, requestID domain.RequestID, body json.RawMessage, handler RequestHandler, requestCancel context.CancelFunc) {
				defer requests.Done()
				var finishOnce sync.Once
				finish := func() {
					finishOnce.Do(func() {
						requestCancel()
						activeMu.Lock()
						delete(active, requestID)
						activeMu.Unlock()
					})
				}
				defer finish()
				runRequest(runCtx, out, requestID, body, handler, finish)
			}(requestCtx, envelope.RequestID, append(json.RawMessage(nil), trimmedBody...), config.Handlers[request.Operation], cancel)
		default:
			return fmt.Errorf("serve helper: client frame type %q is forbidden", envelope.Type)
		}
	}
}

func runRequest(ctx context.Context, out *serviceWriter, requestID domain.RequestID, body json.RawMessage, handler RequestHandler, beforeComplete func()) {
	runRequestWithOutputLimit(ctx, out, requestID, body, handler, beforeComplete, MaxHelperOutputBytes)
}

func runRequestWithOutputLimit(ctx context.Context, out *serviceWriter, requestID domain.RequestID, body json.RawMessage, handler RequestHandler, beforeComplete func(), maximumOutputBytes uint64) {
	var results uint64
	var outputBytes uint64
	var payloadBudget uint64
	if maximumOutputBytes > maxHelperTerminalBytes {
		payloadBudget = maximumOutputBytes - maxHelperTerminalBytes
	}
	budgetExceeded := false
	emit := func(kind FrameType, payload any) error {
		if kind != FrameResult && kind != FrameProgress {
			return errors.New("helper handler: only result and progress may be emitted")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if kind == FrameResult && results >= MaxHelperResults {
			budgetExceeded = true
			return errors.New("helper handler: result limit reached")
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if outputBytes+uint64(len(encoded)) > payloadBudget {
			budgetExceeded = true
			return errors.New("helper handler: output byte limit reached")
		}
		written, err := out.sendEncoded(Envelope{Version: 1, Type: kind, RequestID: requestID}, encoded)
		if err != nil {
			return err
		}
		if kind == FrameResult {
			results++
		}
		outputBytes += uint64(written) // #nosec G115 -- send reports a non-negative encoded byte count.
		return nil
	}
	completion, handlerErr := handler(ctx, body, emit)
	if handlerErr != nil {
		message := sanitizeHelperError(handlerErr.Error())
		failure := StructuredError{Code: "operation_failed", Message: message, Retryable: false}
		encoded, marshalErr := json.Marshal(failure)
		if marshalErr == nil && outputBytes+uint64(len(encoded)) <= payloadBudget {
			if written, sendErr := out.sendEncoded(Envelope{Version: 1, Type: FrameError, RequestID: requestID}, encoded); sendErr == nil {
				outputBytes += uint64(written) // #nosec G115 -- sendEncoded reports a non-negative payload byte count.
			}
		} else {
			budgetExceeded = true
		}
		if completion.Status == "" {
			completion = Completion{Status: "partial_results", Reason: "operation_failed"}
		}
	}
	if budgetExceeded {
		completion.Status = "partial_results"
		completion.Reason = "output_limit"
	}
	if err := ctx.Err(); err != nil {
		completion.Status = "canceled"
		if errors.Is(err, context.DeadlineExceeded) {
			completion.Status = "partial_results"
			completion.Reason = "time_limit"
		} else {
			completion.Reason = "canceled"
		}
	}
	if !validCompletion(completion) {
		failure := StructuredError{Code: "capability_violation", Message: "handler returned an invalid completion", Retryable: false}
		encoded, marshalErr := json.Marshal(failure)
		if marshalErr == nil && outputBytes+uint64(len(encoded)) <= payloadBudget {
			if written, sendErr := out.sendEncoded(Envelope{Version: 1, Type: FrameError, RequestID: requestID}, encoded); sendErr == nil {
				outputBytes += uint64(written) // #nosec G115 -- sendEncoded reports a non-negative payload byte count.
			}
		}
		completion = Completion{Status: "partial_results", Reason: "capability_violation"}
	}
	completion.Results = results
	completion.OutputBytes = outputBytes
	// A client may submit its next request as soon as it observes Complete. Make
	// the concurrency slot available before that terminal frame becomes visible.
	beforeComplete()
	encoded, err := json.Marshal(completion)
	if err != nil || outputBytes+uint64(len(encoded)) > maximumOutputBytes {
		return
	}
	_, _ = out.sendEncoded(Envelope{Version: 1, Type: FrameComplete, RequestID: requestID}, encoded)
}

func validCompletion(completion Completion) bool {
	if completion.Status != "complete" && completion.Status != "partial_results" && completion.Status != "canceled" {
		return false
	}
	if len(completion.Reason) == 0 || len(completion.Reason) > 64 {
		return false
	}
	for index := range completion.Reason {
		if !isCompletionReasonByte(completion.Reason[index]) {
			return false
		}
	}
	return true
}

func isCompletionReasonByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || value == '_'
}

func sendRejected(out *serviceWriter, requestID domain.RequestID, code, message string) error {
	if _, err := out.send(Envelope{Version: 1, Type: FrameError, RequestID: requestID}, StructuredError{Code: code, Message: message, Retryable: false}); err != nil {
		return err
	}
	_, err := out.send(Envelope{Version: 1, Type: FrameComplete, RequestID: requestID}, Completion{Status: "partial_results", Reason: code})
	return err
}

func sanitizeHelperError(message string) string {
	if !utf8.ValidString(message) {
		return "helper operation failed with invalid diagnostic text"
	}
	message = strings.Map(func(value rune) rune {
		if value < 0x20 || value == 0x7f {
			return -1
		}
		return value
	}, message)
	if len(message) > 256 {
		message = message[:256]
	}
	if message == "" {
		return "helper operation failed"
	}
	return message
}

func isLowerHex(value string) bool {
	for index := range value {
		if !isLowerHexByte(value[index]) {
			return false
		}
	}
	return true
}

func isLowerHexByte(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'f'
}

func bytesTrimSpace(value []byte) []byte {
	start := 0
	for start < len(value) && (value[start] == ' ' || value[start] == '\n' || value[start] == '\r' || value[start] == '\t') {
		start++
	}
	end := len(value)
	for end > start && (value[end-1] == ' ' || value[end-1] == '\n' || value[end-1] == '\r' || value[end-1] == '\t') {
		end--
	}
	return value[start:end]
}
