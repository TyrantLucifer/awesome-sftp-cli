package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	helperruntime "github.com/TyrantLucifer/awesome-sftp-cli/internal/helper"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/transport/openssh"
)

const maxHelperLifecycleHostAliasBytes = 1024

type HelperLifecycleCommand string

const (
	HelperLifecycleStatus  HelperLifecycleCommand = "status"
	HelperLifecycleInstall HelperLifecycleCommand = "install"
	HelperLifecycleUpgrade HelperLifecycleCommand = "upgrade"
	HelperLifecycleDisable HelperLifecycleCommand = "disable"
	HelperLifecycleRemove  HelperLifecycleCommand = "remove"
)

type HelperLifecycleRequest struct {
	Command                       HelperLifecycleCommand `json:"command"`
	HostAlias                     string                 `json:"host_alias"`
	AcceptSharedSessionStableHome bool                   `json:"accept_shared_session_stable_home,omitempty"`
}

type HelperLifecycleResponse struct {
	EndpointID domain.EndpointID `json:"endpoint_id,omitempty"`
	State      string            `json:"state"`
}

type HelperLifecycleService interface {
	Execute(context.Context, helperruntime.LifecycleRequest) (helperruntime.LifecycleResult, error)
}

func (s *providerSession) handleHelperLifecycle(ctx context.Context, payload json.RawMessage) (any, error) {
	var request HelperLifecycleRequest
	if err := decodeStrictPayload(payload, &request); err != nil {
		return nil, invalidArgument("invalid Helper lifecycle request", err)
	}
	if err := validateHelperLifecycleRequest(request); err != nil {
		return nil, invalidArgument("invalid Helper lifecycle request", err)
	}
	if s.helperManagement == nil {
		return nil, &domain.OpError{
			Code:    domain.CodeUnsupported,
			Message: "Helper lifecycle service is closed",
			Retry:   domain.RetryAdvice{Kind: domain.RetryNever},
			Effect:  domain.EffectNone,
		}
	}
	response, err := s.helperManagement.Execute(ctx, helperruntime.LifecycleRequest{
		Command: helperruntime.LifecycleCommand(request.Command), HostAlias: request.HostAlias,
		AcceptSharedSessionStableHome: request.AcceptSharedSessionStableHome,
	})
	if err != nil {
		return nil, domain.FromContext("Helper lifecycle", "", nil, err)
	}
	return HelperLifecycleResponse{EndpointID: response.EndpointID, State: string(response.State)}, nil
}

func validateHelperLifecycleRequest(request HelperLifecycleRequest) error {
	if len(request.HostAlias) > maxHelperLifecycleHostAliasBytes {
		return errors.New("host alias exceeds hard limit")
	}
	if err := openssh.ValidateHostAlias(request.HostAlias); err != nil {
		return err
	}
	switch request.Command {
	case HelperLifecycleInstall, HelperLifecycleUpgrade, HelperLifecycleRemove:
		if !request.AcceptSharedSessionStableHome {
			return errors.New("explicit shared-session stable-home consent is required")
		}
	case HelperLifecycleStatus, HelperLifecycleDisable:
		if request.AcceptSharedSessionStableHome {
			return errors.New("shared-session stable-home consent is not accepted for this command")
		}
	default:
		return errors.New("unknown Helper lifecycle command")
	}
	return nil
}

func decodeStrictPayload(payload json.RawMessage, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
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
