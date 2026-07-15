package ipc

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
)

const (
	testRequestID  = domain.RequestID("req_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	testEndpointID = domain.EndpointID("ep_aaaaaaaaaaaaaaaaaaaaaaaaaa")
)

func validProtocol() ProtocolVersion {
	return ProtocolVersion{Major: ProtocolMajor, Minor: ProtocolMinor}
}

func validRPCError() *RPCError {
	return &RPCError{
		Code:       domain.CodeNotFound,
		Message:    "path was not found",
		Operation:  "stat",
		EndpointID: testEndpointID,
		Location: &WireLocation{
			EndpointID: string(testEndpointID),
			Path:       EncodeWireBytes([]byte{'/', 0xff, 0x00}),
		},
		Retry:  domain.RetryAdvice{Kind: domain.RetryNever},
		Effect: domain.EffectNone,
	}
}

func TestEnvelopeValidateAcceptsEveryLegalShape(t *testing.T) {
	tests := map[string]Envelope{
		"request": {
			Protocol:  validProtocol(),
			Kind:      KindRequest,
			Name:      "stat",
			RequestID: testRequestID,
			Payload:   json.RawMessage(`{"path":{"base64":"Lw=="}}`),
		},
		"empty success response": {
			Protocol:  validProtocol(),
			Kind:      KindResponse,
			Name:      "stat",
			RequestID: testRequestID,
		},
		"error response": {
			Protocol:  validProtocol(),
			Kind:      KindResponse,
			Name:      "stat",
			RequestID: testRequestID,
			Error:     validRPCError(),
		},
		"event": {
			Protocol: validProtocol(),
			Kind:     KindEvent,
			Name:     "entry_changed",
			Cursor:   &EventCursor{Epoch: "daemon-start-1", Sequence: 7},
			Payload:  json.RawMessage(`{"path":{"base64":"Lw=="}}`),
		},
	}

	for name, envelope := range tests {
		t.Run(name, func(t *testing.T) {
			if err := envelope.Validate(); err != nil {
				t.Fatalf("Validate(): %v", err)
			}
		})
	}
}

func TestEnvelopeValidateRejectsIllegalFieldCombinations(t *testing.T) {
	request := Envelope{Protocol: validProtocol(), Kind: KindRequest, Name: "read", RequestID: testRequestID}
	response := Envelope{Protocol: validProtocol(), Kind: KindResponse, Name: "read", RequestID: testRequestID}
	event := Envelope{Protocol: validProtocol(), Kind: KindEvent, Name: "changed", Cursor: &EventCursor{Epoch: "epoch", Sequence: 1}}

	tests := map[string]Envelope{
		"wrong protocol major":            withProtocolMajor(request, ProtocolMajor+1),
		"empty name":                      withName(request, ""),
		"unknown kind":                    withKind(request, MessageKind("future")),
		"request missing request ID":      withRequestID(request, ""),
		"request invalid request ID":      withRequestID(request, "request-1"),
		"request with cursor":             withCursor(request, &EventCursor{Epoch: "epoch", Sequence: 1}),
		"request with error":              withError(request, validRPCError()),
		"response missing request ID":     withRequestID(response, ""),
		"response invalid request ID":     withRequestID(response, "request-1"),
		"response with cursor":            withCursor(response, &EventCursor{Epoch: "epoch", Sequence: 1}),
		"response with payload and error": withPayloadAndError(response, json.RawMessage(`{}`), validRPCError()),
		"event missing cursor":            withCursor(event, nil),
		"event with empty epoch":          withCursor(event, &EventCursor{Sequence: 1}),
		"event with request ID":           withRequestID(event, testRequestID),
		"event with error":                withError(event, validRPCError()),
	}

	for name, envelope := range tests {
		t.Run(name, func(t *testing.T) {
			if err := envelope.Validate(); err == nil {
				t.Fatalf("Validate() error = nil for %#v", envelope)
			}
		})
	}
}

func TestEnvelopeValidateRejectsIncompleteRPCError(t *testing.T) {
	base := Envelope{
		Protocol:  validProtocol(),
		Kind:      KindResponse,
		Name:      "stat",
		RequestID: testRequestID,
		Error:     validRPCError(),
	}

	tests := map[string]func(*RPCError){
		"missing code":    func(rpcError *RPCError) { rpcError.Code = "" },
		"missing message": func(rpcError *RPCError) { rpcError.Message = "" },
		"missing retry":   func(rpcError *RPCError) { rpcError.Retry.Kind = "" },
		"missing effect":  func(rpcError *RPCError) { rpcError.Effect = "" },
		"unknown code": func(rpcError *RPCError) {
			rpcError.Code = domain.Code("future_code")
		},
		"unknown retry": func(rpcError *RPCError) {
			rpcError.Retry.Kind = domain.RetryKind("future_retry")
		},
		"unknown effect": func(rpcError *RPCError) {
			rpcError.Effect = domain.EffectStatus("future_effect")
		},
		"negative retry delay": func(rpcError *RPCError) {
			rpcError.Retry.After = -time.Nanosecond
		},
		"invalid location base64": func(rpcError *RPCError) {
			rpcError.Location.Path.Base64 = "%%%"
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			envelope := base
			copyOfError := *base.Error
			copyOfLocation := *base.Error.Location
			copyOfError.Location = &copyOfLocation
			envelope.Error = &copyOfError
			mutate(envelope.Error)
			if err := envelope.Validate(); err == nil {
				t.Fatal("Validate() error = nil")
			}
		})
	}
}

func TestDecodeEnvelopeAcceptsSameMajorUnknownFields(t *testing.T) {
	data := []byte(`{
		"protocol":{"major":1,"minor":99,"future_protocol_field":true},
		"kind":"request",
		"name":"future_request",
		"request_id":"req_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		"payload":{"known":true,"future_payload_field":"kept"},
		"future_envelope_field":{"anything":true}
	}`)

	got, err := DecodeEnvelope(data)
	if err != nil {
		t.Fatalf("DecodeEnvelope(): %v", err)
	}
	if got.Protocol != (ProtocolVersion{Major: ProtocolMajor, Minor: 99}) {
		t.Fatalf("Protocol = %#v", got.Protocol)
	}
	if !bytes.Contains(got.Payload, []byte("future_payload_field")) {
		t.Fatalf("Payload lost unknown payload field: %s", got.Payload)
	}
}

func TestDecodeEnvelopeRejectsNonObjectAndTrailingJSON(t *testing.T) {
	valid := `{"protocol":{"major":1,"minor":0},"kind":"request","name":"stat","request_id":"req_aaaaaaaaaaaaaaaaaaaaaaaaaa"}`
	tests := map[string][]byte{
		"array":    []byte(`[]`),
		"null":     []byte(`null`),
		"string":   []byte(`"request"`),
		"trailing": []byte(valid + ` {}`),
	}

	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeEnvelope(data); err == nil {
				t.Fatalf("DecodeEnvelope(%q) error = nil", data)
			}
		})
	}
}

func TestDecodeEnvelopeRejectsInvalidUTF8WithoutNormalization(t *testing.T) {
	prefix := []byte(`{"protocol":{"major":1,"minor":0},"kind":"event","name":"changed","cursor":{"epoch":"`)
	suffix := []byte(`","sequence":1}}`)

	for _, invalid := range [][]byte{{0xff}, {0xfe}} {
		data := make([]byte, 0, len(prefix)+len(invalid)+len(suffix))
		data = append(data, prefix...)
		data = append(data, invalid...)
		data = append(data, suffix...)
		if _, err := DecodeEnvelope(data); err == nil {
			t.Fatalf("DecodeEnvelope() accepted invalid UTF-8 epoch %x", invalid)
		}
	}
}

func TestEncodeEnvelopeRejectsInvalidUTF8Fields(t *testing.T) {
	invalid := string([]byte{0xff})
	event := Envelope{
		Protocol: validProtocol(),
		Kind:     KindEvent,
		Name:     "changed",
		Cursor:   &EventCursor{Epoch: "epoch", Sequence: 1},
	}
	responseWithError := func(mutate func(*RPCError)) Envelope {
		rpcError := validRPCError()
		mutate(rpcError)
		return Envelope{
			Protocol:  validProtocol(),
			Kind:      KindResponse,
			Name:      "stat",
			RequestID: testRequestID,
			Error:     rpcError,
		}
	}
	invalidPayload := append([]byte(`{"value":"`), 0xff)
	invalidPayload = append(invalidPayload, []byte(`"}`)...)

	tests := map[string]Envelope{
		"name":         withName(event, invalid),
		"cursor epoch": withCursor(event, &EventCursor{Epoch: invalid, Sequence: 1}),
		"payload":      withPayloadAndError(event, invalidPayload, nil),
		"error code": responseWithError(func(rpcError *RPCError) {
			rpcError.Code = domain.Code(invalid)
		}),
		"error message": responseWithError(func(rpcError *RPCError) {
			rpcError.Message = invalid
		}),
		"error operation": responseWithError(func(rpcError *RPCError) {
			rpcError.Operation = invalid
		}),
		"retry kind": responseWithError(func(rpcError *RPCError) {
			rpcError.Retry.Kind = domain.RetryKind(invalid)
		}),
		"effect": responseWithError(func(rpcError *RPCError) {
			rpcError.Effect = domain.EffectStatus(invalid)
		}),
	}

	for name, envelope := range tests {
		t.Run(name, func(t *testing.T) {
			data, err := EncodeEnvelope(envelope)
			if err == nil {
				t.Fatalf("EncodeEnvelope() accepted invalid UTF-8 and returned %x", data)
			}
			if len(data) != 0 {
				t.Fatalf("EncodeEnvelope() returned %d bytes on error", len(data))
			}
		})
	}
}

func TestEncodeEnvelopeProducesOneJSONObjectAndValidates(t *testing.T) {
	envelope := Envelope{Protocol: validProtocol(), Kind: KindRequest, Name: "stat", RequestID: testRequestID}
	data, err := EncodeEnvelope(envelope)
	if err != nil {
		t.Fatalf("EncodeEnvelope(): %v", err)
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		t.Fatalf("encoded envelope is not one JSON object: %q", data)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil {
		t.Fatal(err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("second Decode() error = %v, want EOF", err)
	}

	invalid := envelope
	invalid.Kind = "invalid"
	if _, err := EncodeEnvelope(invalid); err == nil {
		t.Fatal("EncodeEnvelope(invalid) error = nil")
	}
}

func TestEnvelopeErrorsDoNotExposePayloadDetail(t *testing.T) {
	const secret = "sensitive-payload-marker"
	envelope := Envelope{
		Protocol:  validProtocol(),
		Kind:      KindRequest,
		Name:      "write",
		RequestID: testRequestID,
		Payload:   json.RawMessage(`{"value":"` + secret + `"} trailing`),
	}
	_, err := EncodeEnvelope(envelope)
	if err == nil {
		t.Fatal("EncodeEnvelope() error = nil")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("EncodeEnvelope() error leaked payload: %q", err)
	}
}

func TestCompareCursor(t *testing.T) {
	tests := map[string]struct {
		previous EventCursor
		next     EventCursor
		want     CursorRelation
	}{
		"next": {
			previous: EventCursor{Epoch: "epoch-1", Sequence: 8},
			next:     EventCursor{Epoch: "epoch-1", Sequence: 9},
			want:     CursorNext,
		},
		"duplicate": {
			previous: EventCursor{Epoch: "epoch-1", Sequence: 8},
			next:     EventCursor{Epoch: "epoch-1", Sequence: 8},
			want:     CursorDuplicate,
		},
		"forward gap": {
			previous: EventCursor{Epoch: "epoch-1", Sequence: 8},
			next:     EventCursor{Epoch: "epoch-1", Sequence: 10},
			want:     CursorGap,
		},
		"rollback gap": {
			previous: EventCursor{Epoch: "epoch-1", Sequence: 8},
			next:     EventCursor{Epoch: "epoch-1", Sequence: 7},
			want:     CursorGap,
		},
		"sequence wrap is a gap": {
			previous: EventCursor{Epoch: "epoch-1", Sequence: math.MaxUint64},
			next:     EventCursor{Epoch: "epoch-1", Sequence: 0},
			want:     CursorGap,
		},
		"epoch change": {
			previous: EventCursor{Epoch: "epoch-1", Sequence: 8},
			next:     EventCursor{Epoch: "epoch-2", Sequence: 9},
			want:     CursorEpochChange,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := CompareCursor(test.previous, test.next); got != test.want {
				t.Fatalf("CompareCursor() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCancelStatesAndJSONContract(t *testing.T) {
	states := map[CancelState]string{
		CancelAccepted:        "accepted",
		CancelAlreadyFinished: "already_finished",
		CancelNotFound:        "not_found",
		CancelNotCancellable:  "not_cancellable",
	}
	if len(states) != 4 {
		t.Fatalf("cancel state count = %d, want 4", len(states))
	}
	for state, want := range states {
		if string(state) != want {
			t.Fatalf("state = %q, want %q", state, want)
		}
	}

	request := CancelRequest{TargetRequestID: domain.RequestID("req_aaaaaaaaaaaaaaaaaaaaaaaaaa")}
	requestJSON, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(requestJSON), `{"target_request_id":"req_aaaaaaaaaaaaaaaaaaaaaaaaaa"}`; got != want {
		t.Fatalf("CancelRequest JSON = %s, want %s", got, want)
	}

	resultJSON, err := json.Marshal(CancelResult{State: CancelAccepted})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(resultJSON), `{"state":"accepted"}`; got != want {
		t.Fatalf("CancelResult JSON = %s, want %s", got, want)
	}
}

func withProtocolMajor(envelope Envelope, major uint16) Envelope {
	envelope.Protocol.Major = major
	return envelope
}

func withName(envelope Envelope, name string) Envelope {
	envelope.Name = name
	return envelope
}

func withKind(envelope Envelope, kind MessageKind) Envelope {
	envelope.Kind = kind
	return envelope
}

func withRequestID(envelope Envelope, requestID domain.RequestID) Envelope {
	envelope.RequestID = requestID
	return envelope
}

func withCursor(envelope Envelope, cursor *EventCursor) Envelope {
	envelope.Cursor = cursor
	return envelope
}

func withError(envelope Envelope, rpcError *RPCError) Envelope {
	envelope.Error = rpcError
	return envelope
}

func withPayloadAndError(envelope Envelope, payload json.RawMessage, rpcError *RPCError) Envelope {
	envelope.Payload = payload
	envelope.Error = rpcError
	return envelope
}
