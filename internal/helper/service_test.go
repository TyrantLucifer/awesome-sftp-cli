package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
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
