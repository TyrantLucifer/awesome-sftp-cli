// Package directprotocol owns the bounded typed control contract shared by the
// transfer planner and the remote Helper. It intentionally exposes no command
// string or generic RPC primitive.
package directprotocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
)

const (
	Version            uint16 = 1
	MaxTransferBytes          = uint64(1) << 40
	MaxRequestDuration        = 10 * time.Minute
	MaxFrameBytes             = 1 * 1024 * 1024
	MaxConcurrent             = 4
	maxPayloadBytes           = 1 * 1024 * 1024
	maxJSONDepth              = 8
	maxStringBytes            = 4096
)

type ControlLimits struct {
	MaxFrameBytes       int    `json:"max_frame_bytes"`
	MaxConcurrent       int    `json:"max_concurrent"`
	HeartbeatIntervalMS uint32 `json:"heartbeat_interval_ms"`
	HeartbeatTimeoutMS  uint32 `json:"heartbeat_timeout_ms"`
	CancelSemantics     string `json:"cancel_semantics"`
	ProgressSemantics   string `json:"progress_semantics"`
	ResultSemantics     string `json:"result_semantics"`
}

func FrozenControlLimits() ControlLimits {
	return ControlLimits{
		MaxFrameBytes: MaxFrameBytes, MaxConcurrent: MaxConcurrent,
		HeartbeatIntervalMS: 5_000, HeartbeatTimeoutMS: 15_000,
		CancelSemantics: "request_context", ProgressSemantics: "target_durable_bytes", ResultSemantics: "staged_not_committed",
	}
}

type Status string

const (
	Pass    Status = "pass"
	Fail    Status = "fail"
	Unknown Status = "unknown"
)

type CheckName string

const (
	CheckProtocol           CheckName = "protocol"
	CheckCapability         CheckName = "capability"
	CheckNetwork            CheckName = "network"
	CheckTargetAddress      CheckName = "target_address"
	CheckTargetWrite        CheckName = "target_write"
	CheckTargetTemp         CheckName = "target_temp"
	CheckSpace              CheckName = "space"
	CheckQuota              CheckName = "quota"
	CheckNoninteractiveAuth CheckName = "noninteractive_auth"
	CheckHostKey            CheckName = "host_key"
	CheckUserPolicy         CheckName = "user_policy"
	CheckWorkspacePolicy    CheckName = "workspace_policy"
	CheckDataPolicy         CheckName = "data_policy"
	CheckStrongHash         CheckName = "strong_hash"
)

var checkOrder = []CheckName{
	CheckProtocol, CheckCapability, CheckNetwork, CheckTargetAddress,
	CheckTargetWrite, CheckTargetTemp, CheckSpace, CheckQuota,
	CheckNoninteractiveAuth, CheckHostKey, CheckUserPolicy,
	CheckWorkspacePolicy, CheckDataPolicy, CheckStrongHash,
}

func CheckOrder() []CheckName { return append([]CheckName(nil), checkOrder...) }

type Request struct {
	Version               uint16             `json:"version"`
	RequestID             domain.RequestID   `json:"request_id"`
	JobID                 domain.JobID       `json:"job_id"`
	SourceEndpointID      domain.EndpointID  `json:"source_endpoint_id"`
	SourcePath            string             `json:"source_path"`
	DestinationEndpointID domain.EndpointID  `json:"destination_endpoint_id"`
	PartPath              string             `json:"part_path"`
	FinalPath             string             `json:"final_path"`
	TargetHostAlias       string             `json:"target_host_alias"`
	ExpectedSize          uint64             `json:"expected_size"`
	SourceFingerprint     domain.Fingerprint `json:"source_fingerprint"`
	IntegrityPolicy       string             `json:"integrity_policy"`
	DeadlineUnix          int64              `json:"deadline_unix"`
	Nonce                 string             `json:"nonce"`
	Control               ControlLimits      `json:"control"`
}

type Check struct {
	Name   CheckName `json:"name"`
	Status Status    `json:"status"`
	Reason string    `json:"reason"`
}

type Result struct {
	Version           uint16             `json:"version"`
	RequestID         domain.RequestID   `json:"request_id"`
	JobID             domain.JobID       `json:"job_id"`
	CheckedAtUnix     int64              `json:"checked_at_unix"`
	ExpiresAtUnix     int64              `json:"expires_at_unix"`
	Checks            []Check            `json:"checks"`
	SourceFingerprint domain.Fingerprint `json:"source_fingerprint"`
	SourceSize        uint64             `json:"source_size"`
	SourceSHA256      string             `json:"source_sha256"`
}

func DecodeRequest(raw json.RawMessage, now time.Time) (Request, error) {
	var request Request
	if err := decodePayload(raw, &request); err != nil {
		return Request{}, err
	}
	if err := ValidateRequest(request, now); err != nil {
		return Request{}, err
	}
	return request, nil
}

func DecodeResult(raw json.RawMessage) (Result, error) {
	var result Result
	if err := decodePayload(raw, &result); err != nil {
		return Result{}, err
	}
	return result, nil
}

func Evaluate(request Request, result Result, now time.Time) (bool, Check, error) {
	if err := ValidateRequest(request, now); err != nil {
		return false, Check{}, err
	}
	if err := ValidateResult(request, result, now); err != nil {
		return false, Check{}, err
	}
	for _, check := range result.Checks {
		if check.Status != Pass {
			return false, check, nil
		}
	}
	return true, Check{}, nil
}

func ValidateRequest(request Request, now time.Time) error {
	if request.Version != Version {
		return errors.New("direct preflight: unsupported protocol version")
	}
	if _, err := domain.ParseRequestID(string(request.RequestID)); err != nil {
		return errors.New("direct preflight: invalid request correlation")
	}
	if _, err := domain.ParseJobID(string(request.JobID)); err != nil {
		return errors.New("direct preflight: invalid Job correlation")
	}
	if _, err := domain.ParseEndpointID(string(request.SourceEndpointID)); err != nil {
		return errors.New("direct preflight: invalid source Endpoint")
	}
	if _, err := domain.ParseEndpointID(string(request.DestinationEndpointID)); err != nil || request.SourceEndpointID == request.DestinationEndpointID {
		return errors.New("direct preflight: invalid destination Endpoint")
	}
	if !validOperationPath(request.SourcePath) || !validOperationPath(request.PartPath) || !validOperationPath(request.FinalPath) {
		return errors.New("direct preflight: invalid operation path")
	}
	if path.Dir(request.PartPath) != path.Dir(request.FinalPath) ||
		!strings.HasSuffix(path.Base(request.PartPath), ".part-"+string(request.JobID)) || request.PartPath == request.FinalPath {
		return errors.New("direct preflight: part is not the frozen Job-owned target")
	}
	if !validHostAlias(request.TargetHostAlias) {
		return errors.New("direct preflight: target Host alias is not a trusted typed value")
	}
	if request.ExpectedSize > MaxTransferBytes || request.SourceFingerprint.Size == nil || *request.SourceFingerprint.Size != request.ExpectedSize || request.SourceFingerprint.Strength() == domain.FingerprintWeak {
		return errors.New("direct preflight: source identity or size is invalid")
	}
	if request.IntegrityPolicy != "strong" && request.IntegrityPolicy != "require_strong" {
		return errors.New("direct preflight: integrity policy is invalid")
	}
	deadline := time.Unix(request.DeadlineUnix, 0)
	if !deadline.After(now) || deadline.After(now.Add(MaxRequestDuration)) {
		return errors.New("direct preflight: deadline is outside hard limits")
	}
	if len(request.Nonce) != 32 || !isLowerHex(request.Nonce) {
		return errors.New("direct preflight: nonce is invalid")
	}
	if request.Control != FrozenControlLimits() {
		return errors.New("direct preflight: control limits or semantics changed")
	}
	return nil
}

func ValidateResult(request Request, result Result, now time.Time) error {
	if result.Version != Version || result.RequestID != request.RequestID || result.JobID != request.JobID {
		return errors.New("direct preflight: response correlation is invalid")
	}
	checkedAt := time.Unix(result.CheckedAtUnix, 0)
	expiresAt := time.Unix(result.ExpiresAtUnix, 0)
	if checkedAt.After(now) || checkedAt.After(expiresAt) || !expiresAt.After(now) || expiresAt.After(time.Unix(request.DeadlineUnix, 0)) {
		return errors.New("direct preflight: response freshness is invalid")
	}
	if !reflect.DeepEqual(result.SourceFingerprint, request.SourceFingerprint) || result.SourceSize != request.ExpectedSize {
		return errors.New("direct preflight: source identity changed")
	}
	if len(result.Checks) != len(checkOrder) {
		return errors.New("direct preflight: required check set is incomplete")
	}
	allPassed := true
	for index, check := range result.Checks {
		if check.Name != checkOrder[index] {
			return errors.New("direct preflight: required checks are duplicated or out of order")
		}
		if check.Status != Pass && check.Status != Fail && check.Status != Unknown {
			return errors.New("direct preflight: check status is invalid")
		}
		if !validReason(check.Reason) {
			return errors.New("direct preflight: check reason is invalid")
		}
		allPassed = allPassed && check.Status == Pass
	}
	if result.SourceSHA256 != "" && (len(result.SourceSHA256) != 64 || !isLowerHex(result.SourceSHA256)) {
		return errors.New("direct preflight: source digest is invalid")
	}
	if allPassed && len(result.SourceSHA256) != 64 {
		return errors.New("direct preflight: passed evidence lacks a strong source digest")
	}
	return nil
}

func decodePayload(raw json.RawMessage, destination any) error {
	if destination == nil || len(raw) == 0 || len(raw) > maxPayloadBytes || !utf8.Valid(raw) {
		return errors.New("direct preflight: invalid payload")
	}
	if err := validateJSONShape(raw); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("direct preflight: multiple payload values are forbidden")
	}
	return nil
}

func validateJSONShape(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	depth := 0
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			if depth != 0 {
				return errors.New("direct preflight: unbalanced JSON")
			}
			return nil
		}
		if err != nil {
			return err
		}
		switch value := token.(type) {
		case json.Delim:
			if value == '{' || value == '[' {
				depth++
				if depth > maxJSONDepth {
					return errors.New("direct preflight: JSON nesting exceeds hard limit")
				}
			} else {
				depth--
			}
		case string:
			if len(value) > maxStringBytes {
				return errors.New("direct preflight: JSON string exceeds hard limit")
			}
		}
	}
}

func validOperationPath(value string) bool {
	return len(value) > 0 && len(value) <= 1000 && value[0] == '/' && path.Clean(value) == value && !strings.ContainsRune(value, 0)
}

func validHostAlias(value string) bool {
	if len(value) == 0 || len(value) > 255 || value[0] == '-' || strings.IndexByte(value, 0) >= 0 {
		return false
	}
	for _, character := range value {
		if unicode.IsSpace(character) || unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validReason(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for index := range value {
		character := value[index]
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '_' && index > 0 && index < len(value)-1 {
			continue
		}
		return false
	}
	return true
}

func isLowerHex(value string) bool {
	for index := range value {
		if value[index] < '0' || value[index] > '9' && value[index] < 'a' || value[index] > 'f' {
			return false
		}
	}
	return true
}
