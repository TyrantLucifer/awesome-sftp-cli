package helper

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
)

const (
	HelperPreface         = "amsftp-helper-wire-v1\n"
	MaxHelperFrameBytes   = 1 * 1024 * 1024
	MaxHelperJSONDepth    = 8
	MaxHelperStringBytes  = 4096
	MaxHelperConcurrent   = 4
	MaxHelperCapabilities = 16
)

type FrameType string

const (
	FrameClientHello FrameType = "client_hello"
	FrameServerHello FrameType = "server_hello"
	FrameRequest     FrameType = "request"
	FrameCancel      FrameType = "cancel"
	FrameResult      FrameType = "result"
	FrameProgress    FrameType = "progress"
	FrameComplete    FrameType = "complete"
	FrameError       FrameType = "error"
	FramePing        FrameType = "ping"
	FramePong        FrameType = "pong"
)

type Envelope struct {
	Version   uint16           `json:"v"`
	Type      FrameType        `json:"type"`
	RequestID domain.RequestID `json:"request_id,omitempty"`
	Payload   json.RawMessage  `json:"payload"`
}

func DecodeHelperEnvelope(raw []byte) (Envelope, error) {
	if len(raw) == 0 || len(raw) > MaxHelperFrameBytes || !utf8.Valid(raw) {
		return Envelope{}, errors.New("decode helper envelope: byte length or UTF-8 is invalid")
	}
	if err := validateJSONShape(raw); err != nil {
		return Envelope{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var envelope Envelope
	if err := decoder.Decode(&envelope); err != nil {
		return Envelope{}, fmt.Errorf("decode helper envelope: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return Envelope{}, err
	}
	if err := validateEnvelope(envelope); err != nil {
		return Envelope{}, err
	}
	return envelope, nil
}

func EncodeHelperEnvelope(envelope Envelope) ([]byte, error) {
	if err := validateEnvelope(envelope); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode helper envelope: %w", err)
	}
	if len(raw) > MaxHelperFrameBytes {
		return nil, errors.New("encode helper envelope: frame exceeds hard limit")
	}
	if err := validateJSONShape(raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func validateEnvelope(envelope Envelope) error {
	if envelope.Version != 1 {
		return errors.New("helper envelope: unsupported envelope version")
	}
	if !validFrameType(envelope.Type) {
		return errors.New("helper envelope: unknown frame type")
	}
	requestBound := envelope.Type == FrameRequest || envelope.Type == FrameCancel || envelope.Type == FrameResult || envelope.Type == FrameProgress || envelope.Type == FrameComplete || envelope.Type == FrameError
	if requestBound {
		if _, err := domain.ParseRequestID(string(envelope.RequestID)); err != nil {
			return errors.New("helper envelope: request-bound frame has invalid request ID")
		}
	} else if envelope.RequestID != "" {
		return errors.New("helper envelope: session frame must not have a request ID")
	}
	trimmed := bytes.TrimSpace(envelope.Payload)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return errors.New("helper envelope: payload must be one JSON object")
	}
	if err := validateJSONShape(trimmed); err != nil {
		return err
	}
	return nil
}

func validFrameType(value FrameType) bool {
	switch value {
	case FrameClientHello, FrameServerHello, FrameRequest, FrameCancel, FrameResult, FrameProgress, FrameComplete, FrameError, FramePing, FramePong:
		return true
	default:
		return false
	}
}

func validateJSONShape(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	depth := 0
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			if depth != 0 {
				return errors.New("helper JSON: unbalanced nesting")
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("helper JSON: invalid syntax: %w", err)
		}
		switch value := token.(type) {
		case json.Delim:
			if value == '{' || value == '[' {
				depth++
				if depth > MaxHelperJSONDepth {
					return errors.New("helper JSON: nesting exceeds hard limit")
				}
			} else {
				depth--
				if depth < 0 {
					return errors.New("helper JSON: unbalanced nesting")
				}
			}
		case string:
			if len(value) > MaxHelperStringBytes {
				return errors.New("helper JSON: string exceeds hard limit")
			}
		}
	}
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("helper JSON: multiple values are forbidden")
		}
		return fmt.Errorf("helper JSON: invalid trailing data: %w", err)
	}
	return nil
}

func decodeStrictPayload(raw json.RawMessage, destination any) error {
	if destination == nil {
		return errors.New("decode helper payload: nil destination")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode helper payload: %w", err)
	}
	return requireJSONEOF(decoder)
}

// DecodePayload applies the same strict unknown-field, single-value and JSON
// shape rules to client-side operation payloads.
func DecodePayload(raw json.RawMessage, destination any) error {
	if err := validateJSONShape(raw); err != nil {
		return err
	}
	return decodeStrictPayload(raw, destination)
}

type CapabilityName string

const (
	CapabilityFilenameSearch CapabilityName = "filename_search"
	CapabilityContentSearch  CapabilityName = "content_search"
	CapabilityStrongHash     CapabilityName = "strong_hash"
	CapabilityDiskStats      CapabilityName = "disk_stats"
	CapabilityTail           CapabilityName = "tail"
	CapabilityWatch          CapabilityName = "watch"
	CapabilitySameHostCopy   CapabilityName = "same_host_copy"
)

var capabilityOrder = []CapabilityName{
	CapabilityFilenameSearch,
	CapabilityContentSearch,
	CapabilityStrongHash,
	CapabilityDiskStats,
	CapabilityTail,
	CapabilityWatch,
	CapabilitySameHostCopy,
}

type CapabilityRequest struct {
	Name           CapabilityName `json:"name"`
	MaximumVersion uint16         `json:"maximum_version"`
}

type Capability struct {
	Name    CapabilityName `json:"name"`
	Version uint16         `json:"version"`
}

type ClientHello struct {
	MinimumProtocol   uint16
	MaximumProtocol   uint16
	MaximumFrame      uint32
	MaximumConcurrent uint16
	ClientVersion     Version
	Capabilities      []CapabilityRequest
}

type clientHelloWire struct {
	MinimumProtocol   uint16              `json:"minimum_protocol"`
	MaximumProtocol   uint16              `json:"maximum_protocol"`
	MaximumFrame      uint32              `json:"maximum_frame"`
	MaximumConcurrent uint16              `json:"maximum_concurrent"`
	ClientVersion     string              `json:"client_version"`
	Capabilities      []CapabilityRequest `json:"capabilities"`
}

func (h ClientHello) MarshalJSON() ([]byte, error) {
	return json.Marshal(clientHelloWire{
		MinimumProtocol: h.MinimumProtocol, MaximumProtocol: h.MaximumProtocol,
		MaximumFrame: h.MaximumFrame, MaximumConcurrent: h.MaximumConcurrent,
		ClientVersion: h.ClientVersion.String(), Capabilities: h.Capabilities,
	})
}

func ParseClientHello(envelope Envelope) (ClientHello, error) {
	if envelope.Type != FrameClientHello || envelope.RequestID != "" {
		return ClientHello{}, errors.New("parse helper client hello: wrong envelope type")
	}
	var wire clientHelloWire
	if err := decodeStrictPayload(envelope.Payload, &wire); err != nil {
		return ClientHello{}, err
	}
	version, err := parseReleaseVersion(wire.ClientVersion)
	if err != nil {
		return ClientHello{}, errors.New("parse helper client hello: client version is invalid")
	}
	hello := ClientHello{
		MinimumProtocol: wire.MinimumProtocol, MaximumProtocol: wire.MaximumProtocol,
		MaximumFrame: wire.MaximumFrame, MaximumConcurrent: wire.MaximumConcurrent,
		ClientVersion: version, Capabilities: append([]CapabilityRequest(nil), wire.Capabilities...),
	}
	if err := validateClientHello(hello); err != nil {
		return ClientHello{}, err
	}
	return hello, nil
}

func validateClientHello(hello ClientHello) error {
	if hello.MinimumProtocol == 0 || hello.MaximumProtocol < hello.MinimumProtocol {
		return errors.New("helper client hello: protocol range is invalid")
	}
	if hello.MaximumFrame < 1024 || hello.MaximumFrame > MaxHelperFrameBytes {
		return errors.New("helper client hello: frame limit is invalid")
	}
	if hello.MaximumConcurrent == 0 || hello.MaximumConcurrent > MaxHelperConcurrent {
		return errors.New("helper client hello: concurrency limit is invalid")
	}
	if len(hello.Capabilities) > MaxHelperCapabilities {
		return errors.New("helper client hello: too many capabilities")
	}
	seen := make(map[CapabilityName]struct{}, len(hello.Capabilities))
	for _, capability := range hello.Capabilities {
		if !knownCapability(capability.Name) || capability.MaximumVersion == 0 {
			return errors.New("helper client hello: capability is invalid")
		}
		if _, duplicate := seen[capability.Name]; duplicate {
			return errors.New("helper client hello: duplicate capability")
		}
		seen[capability.Name] = struct{}{}
	}
	return nil
}

type ServerConfig struct {
	Protocol          uint16
	HelperVersion     Version
	MinimumClient     Version
	MaximumFrame      uint32
	MaximumConcurrent uint16
	Capabilities      []Capability
}

type Negotiated struct {
	Protocol          uint16       `json:"protocol"`
	HelperVersion     string       `json:"helper_version"`
	MaximumFrame      uint32       `json:"maximum_frame"`
	MaximumConcurrent uint16       `json:"maximum_concurrent"`
	Capabilities      []Capability `json:"capabilities"`
	PathSemantics     string       `json:"path_semantics"`
	TimeSemantics     string       `json:"time_semantics"`
}

func ServeHandshake(reader io.Reader, writer io.Writer, config ServerConfig) (Negotiated, error) {
	if reader == nil || writer == nil {
		return Negotiated{}, errors.New("serve helper handshake: stdio is required")
	}
	if err := validateServerConfig(config); err != nil {
		return Negotiated{}, err
	}
	if err := writeAll(writer, []byte(HelperPreface)); err != nil {
		return Negotiated{}, fmt.Errorf("serve helper handshake: preface: %w", err)
	}
	frameReader, err := ipc.NewReader(reader, MaxHelperFrameBytes)
	if err != nil {
		return Negotiated{}, err
	}
	raw, err := frameReader.ReadFrame()
	if err != nil {
		return Negotiated{}, fmt.Errorf("serve helper handshake: %w", err)
	}
	envelope, err := DecodeHelperEnvelope(raw)
	if err != nil {
		return Negotiated{}, err
	}
	hello, err := ParseClientHello(envelope)
	if err != nil {
		return Negotiated{}, err
	}
	negotiated, err := negotiateHello(hello, config)
	if err != nil {
		return Negotiated{}, err
	}
	payload, err := json.Marshal(negotiated)
	if err != nil {
		return Negotiated{}, err
	}
	response, err := EncodeHelperEnvelope(Envelope{Version: 1, Type: FrameServerHello, Payload: payload})
	if err != nil {
		return Negotiated{}, err
	}
	frameWriter, err := ipc.NewWriter(writer, negotiated.MaximumFrame)
	if err != nil {
		return Negotiated{}, err
	}
	if err := frameWriter.WriteFrame(response); err != nil {
		return Negotiated{}, fmt.Errorf("serve helper handshake: response: %w", err)
	}
	return negotiated, nil
}

func validateServerConfig(config ServerConfig) error {
	if config.Protocol == 0 || config.MaximumFrame < 1024 || config.MaximumFrame > MaxHelperFrameBytes || config.MaximumConcurrent == 0 || config.MaximumConcurrent > MaxHelperConcurrent {
		return errors.New("helper server config: protocol or resource bounds are invalid")
	}
	if len(config.Capabilities) > MaxHelperCapabilities {
		return errors.New("helper server config: too many capabilities")
	}
	seen := make(map[CapabilityName]struct{}, len(config.Capabilities))
	for _, capability := range config.Capabilities {
		if !knownCapability(capability.Name) || capability.Version == 0 {
			return errors.New("helper server config: capability is invalid")
		}
		if _, duplicate := seen[capability.Name]; duplicate {
			return errors.New("helper server config: duplicate capability")
		}
		seen[capability.Name] = struct{}{}
	}
	return nil
}

func negotiateHello(client ClientHello, server ServerConfig) (Negotiated, error) {
	if server.Protocol < client.MinimumProtocol || server.Protocol > client.MaximumProtocol {
		return Negotiated{}, errors.New("helper handshake: protocol range is incompatible")
	}
	if server.MinimumClient != (Version{}) && client.ClientVersion.Compare(server.MinimumClient) < 0 {
		return Negotiated{}, errors.New("helper handshake: client is below helper minimum")
	}
	requested := make(map[CapabilityName]uint16, len(client.Capabilities))
	for _, capability := range client.Capabilities {
		requested[capability.Name] = capability.MaximumVersion
	}
	available := make(map[CapabilityName]uint16, len(server.Capabilities))
	for _, capability := range server.Capabilities {
		available[capability.Name] = capability.Version
	}
	capabilities := make([]Capability, 0, len(client.Capabilities))
	for _, name := range capabilityOrder {
		clientMaximum, requestedByClient := requested[name]
		serverVersion, offeredByServer := available[name]
		if !requestedByClient || !offeredByServer {
			continue
		}
		version := serverVersion
		if clientMaximum < version {
			version = clientMaximum
		}
		capabilities = append(capabilities, Capability{Name: name, Version: version})
	}
	maximumFrame := server.MaximumFrame
	if client.MaximumFrame < maximumFrame {
		maximumFrame = client.MaximumFrame
	}
	maximumConcurrent := server.MaximumConcurrent
	if client.MaximumConcurrent < maximumConcurrent {
		maximumConcurrent = client.MaximumConcurrent
	}
	return Negotiated{
		Protocol: server.Protocol, HelperVersion: server.HelperVersion.String(),
		MaximumFrame: maximumFrame, MaximumConcurrent: maximumConcurrent,
		Capabilities: capabilities, PathSemantics: "absolute_or_scope_relative_bytes", TimeSemantics: "unix_nanoseconds_best_effort",
	}, nil
}

func knownCapability(name CapabilityName) bool {
	for _, candidate := range capabilityOrder {
		if candidate == name {
			return true
		}
	}
	return false
}

func ValidateHelperPreface(reader io.Reader) error {
	if reader == nil {
		return errors.New("validate helper preface: reader is nil")
	}
	buffer := make([]byte, len(HelperPreface))
	if _, err := io.ReadFull(reader, buffer); err != nil {
		return fmt.Errorf("validate helper preface: %w", err)
	}
	if string(buffer) != HelperPreface {
		return errors.New("validate helper preface: stdout byte zero does not begin the protocol")
	}
	return nil
}

func writeAll(writer io.Writer, value []byte) error {
	for len(value) > 0 {
		written, err := writer.Write(value)
		if written > 0 {
			value = value[written:]
		}
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
