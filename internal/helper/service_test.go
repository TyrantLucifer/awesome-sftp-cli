package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/ipc"
)

func TestHelperServiceStreamsCorrelatedResultsAndCancellationTerminal(t *testing.T) {
	requestID := domain.RequestID("req_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	hello := ClientHello{
		MinimumProtocol: 1, MaximumProtocol: 1, MaximumFrame: MaxHelperFrameBytes,
		MaximumConcurrent: 1, ClientVersion: Version{Major: 4},
		Capabilities: []CapabilityRequest{{Name: CapabilityFilenameSearch, MaximumVersion: 1}},
	}
	request := RequestPayload{Operation: CapabilityFilenameSearch, Body: json.RawMessage(`{"scope":"/srv"}`)}
	input := append(framedEnvelope(t, Envelope{Version: 1, Type: FrameClientHello}, hello), framedEnvelope(t, Envelope{Version: 1, Type: FrameRequest, RequestID: requestID}, request)...)
	input = append(input, framedEnvelope(t, Envelope{Version: 1, Type: FrameCancel, RequestID: requestID}, struct{}{})...)
	var output bytes.Buffer
	err := Serve(context.Background(), bytes.NewReader(input), &output, ServiceConfig{
		Server:                 ServerConfig{Protocol: 1, HelperVersion: Version{Major: 4}, MaximumFrame: MaxHelperFrameBytes, MaximumConcurrent: 1, Capabilities: []Capability{{Name: CapabilityFilenameSearch, Version: 1}}},
		MaximumRequestDuration: time.Second,
		Handlers: map[CapabilityName]RequestHandler{
			CapabilityFilenameSearch: func(ctx context.Context, body json.RawMessage, emit EmitFunc) (Completion, error) {
				if string(body) != `{"scope":"/srv"}` {
					return Completion{}, errors.New("wrong body")
				}
				_ = emit(FrameResult, map[string]any{"path": "first"})
				<-ctx.Done()
				return Completion{Status: "canceled", Reason: "canceled"}, nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	frames := decodeServiceFrames(t, output.Bytes())
	if len(frames) < 2 || frames[0].Type != FrameServerHello {
		t.Fatalf("frames = %#v", frames)
	}
	foundTerminal := false
	for _, frame := range frames[1:] {
		if frame.RequestID != requestID {
			t.Fatalf("uncorrelated frame = %#v", frame)
		}
		if frame.Type == FrameComplete {
			var completion Completion
			if err := decodeStrictPayload(frame.Payload, &completion); err != nil {
				t.Fatal(err)
			}
			foundTerminal = completion.Status == "canceled" && completion.Reason == "canceled"
		}
	}
	if !foundTerminal {
		t.Fatalf("missing canceled terminal in %#v", frames)
	}
}

func TestHelperServiceEnforcesNegotiatedConcurrencyAndCapability(t *testing.T) {
	firstID := domain.RequestID("req_bbbbbbbbbbbbbbbbbbbbbbbbbb")
	secondID := domain.RequestID("req_cccccccccccccccccccccccccc")
	hello := ClientHello{
		MinimumProtocol: 1, MaximumProtocol: 1, MaximumFrame: MaxHelperFrameBytes,
		MaximumConcurrent: 1, ClientVersion: Version{Major: 4},
		Capabilities: []CapabilityRequest{{Name: CapabilityStrongHash, MaximumVersion: 1}},
	}
	input := framedEnvelope(t, Envelope{Version: 1, Type: FrameClientHello}, hello)
	input = append(input, framedEnvelope(t, Envelope{Version: 1, Type: FrameRequest, RequestID: firstID}, RequestPayload{Operation: CapabilityStrongHash, Body: json.RawMessage(`{}`)})...)
	input = append(input, framedEnvelope(t, Envelope{Version: 1, Type: FrameRequest, RequestID: secondID}, RequestPayload{Operation: CapabilityDiskStats, Body: json.RawMessage(`{}`)})...)
	input = append(input, framedEnvelope(t, Envelope{Version: 1, Type: FrameCancel, RequestID: firstID}, struct{}{})...)
	var output bytes.Buffer
	err := Serve(context.Background(), bytes.NewReader(input), &output, ServiceConfig{
		Server:                 ServerConfig{Protocol: 1, HelperVersion: Version{Major: 4}, MaximumFrame: MaxHelperFrameBytes, MaximumConcurrent: 1, Capabilities: []Capability{{Name: CapabilityStrongHash, Version: 1}}},
		MaximumRequestDuration: time.Second,
		Handlers: map[CapabilityName]RequestHandler{
			CapabilityStrongHash: func(ctx context.Context, _ json.RawMessage, _ EmitFunc) (Completion, error) {
				<-ctx.Done()
				return Completion{Status: "canceled", Reason: "canceled"}, nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	frames := decodeServiceFrames(t, output.Bytes())
	foundCapabilityError := false
	for _, frame := range frames {
		if frame.RequestID == secondID && frame.Type == FrameError {
			var failure StructuredError
			if err := decodeStrictPayload(frame.Payload, &failure); err != nil {
				t.Fatal(err)
			}
			foundCapabilityError = failure.Code == "capability_unavailable"
		}
	}
	if !foundCapabilityError {
		t.Fatalf("missing independent capability error in %#v", frames)
	}
}

func TestHelperServiceForbidsReuseAfterCapabilityAndConcurrencyRejection(t *testing.T) {
	tests := []struct {
		name  string
		input func(*testing.T, ClientHello) []byte
	}{
		{
			name: "capability",
			input: func(t *testing.T, hello ClientHello) []byte {
				requestID := domain.RequestID("req_mmmmmmmmmmmmmmmmmmmmmmmmmm")
				input := framedEnvelope(t, Envelope{Version: 1, Type: FrameClientHello}, hello)
				request := RequestPayload{Operation: CapabilityDiskStats, Body: json.RawMessage(`{}`)}
				input = append(input, framedEnvelope(t, Envelope{Version: 1, Type: FrameRequest, RequestID: requestID}, request)...)
				return append(input, framedEnvelope(t, Envelope{Version: 1, Type: FrameRequest, RequestID: requestID}, request)...)
			},
		},
		{
			name: "concurrency",
			input: func(t *testing.T, hello ClientHello) []byte {
				firstID := domain.RequestID("req_nnnnnnnnnnnnnnnnnnnnnnnnnn")
				rejectedID := domain.RequestID("req_oooooooooooooooooooooooooo")
				input := framedEnvelope(t, Envelope{Version: 1, Type: FrameClientHello}, hello)
				request := RequestPayload{Operation: CapabilityStrongHash, Body: json.RawMessage(`{}`)}
				input = append(input, framedEnvelope(t, Envelope{Version: 1, Type: FrameRequest, RequestID: firstID}, request)...)
				input = append(input, framedEnvelope(t, Envelope{Version: 1, Type: FrameRequest, RequestID: rejectedID}, request)...)
				return append(input, framedEnvelope(t, Envelope{Version: 1, Type: FrameRequest, RequestID: rejectedID}, request)...)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hello := ClientHello{
				MinimumProtocol: 1, MaximumProtocol: 1, MaximumFrame: MaxHelperFrameBytes, MaximumConcurrent: 1,
				ClientVersion: Version{Major: 4}, Capabilities: []CapabilityRequest{{Name: CapabilityStrongHash, MaximumVersion: 1}},
			}
			var output bytes.Buffer
			err := Serve(context.Background(), bytes.NewReader(test.input(t, hello)), &output, ServiceConfig{
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
			if err == nil || !strings.Contains(err.Error(), "request ID reuse") {
				t.Fatalf("Serve() error = %v", err)
			}
		})
	}
}

func TestServiceWriterReportsExactlyThePayloadBytesUsedByOutputBudget(t *testing.T) {
	var output bytes.Buffer
	frameWriter, err := ipc.NewWriter(&output, MaxHelperFrameBytes)
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]string{"path": "/srv/file"}
	written, err := (&serviceWriter{writer: frameWriter}).send(Envelope{Version: 1, Type: FrameResult, RequestID: "req_pppppppppppppppppppppppppp"}, payload)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if written != len(encoded) {
		t.Fatalf("budget bytes = %d, want payload bytes %d", written, len(encoded))
	}
}

func TestRunRequestUsesOneEncodingAndReservesTerminalWithinSharedOutputLimit(t *testing.T) {
	const limit = 8 << 10
	var output bytes.Buffer
	frameWriter, err := ipc.NewWriter(&output, MaxHelperFrameBytes)
	if err != nil {
		t.Fatal(err)
	}
	payload := &changingJSONPayload{}
	runRequestWithOutputLimit(context.Background(), &serviceWriter{writer: frameWriter}, "req_rrrrrrrrrrrrrrrrrrrrrrrrrr", json.RawMessage(`{}`), func(_ context.Context, _ json.RawMessage, emit EmitFunc) (Completion, error) {
		for index := 0; index < 8; index++ {
			if err := emit(FrameResult, payload); err != nil {
				return Completion{}, err
			}
		}
		return Completion{Status: "complete", Reason: "none"}, nil
	}, func() {}, limit)
	if payload.calls != 2 {
		t.Fatalf("MarshalJSON calls = %d, want one call per two attempted emits", payload.calls)
	}
	reader := bytes.NewReader(output.Bytes())
	frames, err := ipc.NewReader(reader, MaxHelperFrameBytes)
	if err != nil {
		t.Fatal(err)
	}
	var total uint64
	var terminal Completion
	for reader.Len() > 0 {
		raw, err := frames.ReadFrame()
		if err != nil {
			t.Fatal(err)
		}
		envelope, err := DecodeHelperEnvelope(raw)
		if err != nil {
			t.Fatal(err)
		}
		total += uint64(len(envelope.Payload))
		if envelope.Type == FrameComplete {
			if err := decodeStrictPayload(envelope.Payload, &terminal); err != nil {
				t.Fatal(err)
			}
		}
	}
	if total > limit || terminal.Status != "partial_results" || terminal.Reason != "output_limit" || terminal.OutputBytes >= total {
		t.Fatalf("total=%d terminal=%#v", total, terminal)
	}
}

type changingJSONPayload struct{ calls int }

func (payload *changingJSONPayload) MarshalJSON() ([]byte, error) {
	payload.calls++
	return json.Marshal(map[string]string{"value": strings.Repeat("x", 2000*payload.calls)})
}

func decodeServiceFrames(t *testing.T, raw []byte) []Envelope {
	t.Helper()
	reader := bytes.NewReader(raw)
	if err := ValidateHelperPreface(reader); err != nil {
		t.Fatal(err)
	}
	frameReader, err := ipc.NewReader(reader, MaxHelperFrameBytes)
	if err != nil {
		t.Fatal(err)
	}
	var frames []Envelope
	for reader.Len() > 0 {
		payload, err := frameReader.ReadFrame()
		if err != nil {
			t.Fatal(err)
		}
		envelope, err := DecodeHelperEnvelope(payload)
		if err != nil {
			t.Fatal(err)
		}
		frames = append(frames, envelope)
	}
	return frames
}
