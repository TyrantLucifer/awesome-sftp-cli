package helper

import (
	"encoding/json"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/directprotocol"
)

const (
	DirectProtocolVersion  = directprotocol.Version
	MaxDirectTransferBytes = directprotocol.MaxTransferBytes
)

type DirectPreflightStatus = directprotocol.Status

const (
	DirectPreflightPass    = directprotocol.Pass
	DirectPreflightFail    = directprotocol.Fail
	DirectPreflightUnknown = directprotocol.Unknown
)

type DirectPreflightCheckName = directprotocol.CheckName

const (
	DirectCheckProtocol           = directprotocol.CheckProtocol
	DirectCheckCapability         = directprotocol.CheckCapability
	DirectCheckNetwork            = directprotocol.CheckNetwork
	DirectCheckTargetAddress      = directprotocol.CheckTargetAddress
	DirectCheckTargetWrite        = directprotocol.CheckTargetWrite
	DirectCheckTargetTemp         = directprotocol.CheckTargetTemp
	DirectCheckSpace              = directprotocol.CheckSpace
	DirectCheckQuota              = directprotocol.CheckQuota
	DirectCheckNoninteractiveAuth = directprotocol.CheckNoninteractiveAuth
	DirectCheckHostKey            = directprotocol.CheckHostKey
	DirectCheckUserPolicy         = directprotocol.CheckUserPolicy
	DirectCheckWorkspacePolicy    = directprotocol.CheckWorkspacePolicy
	DirectCheckDataPolicy         = directprotocol.CheckDataPolicy
	DirectCheckStrongHash         = directprotocol.CheckStrongHash
)

type DirectPreflightRequest = directprotocol.Request
type DirectPreflightCheck = directprotocol.Check
type DirectPreflightResult = directprotocol.Result
type DirectControlLimits = directprotocol.ControlLimits

func DirectPreflightCheckOrder() []DirectPreflightCheckName { return directprotocol.CheckOrder() }

func DecodeDirectPreflightRequest(raw json.RawMessage, now time.Time) (DirectPreflightRequest, error) {
	return directprotocol.DecodeRequest(raw, now)
}

func DecodeDirectPreflightResult(raw json.RawMessage) (DirectPreflightResult, error) {
	return directprotocol.DecodeResult(raw)
}

func EvaluateDirectPreflight(request DirectPreflightRequest, result DirectPreflightResult, now time.Time) (bool, DirectPreflightCheck, error) {
	return directprotocol.Evaluate(request, result, now)
}
