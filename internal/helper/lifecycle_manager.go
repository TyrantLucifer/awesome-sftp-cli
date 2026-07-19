package helper

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
)

type LifecycleCommand string

const (
	LifecycleStatus  LifecycleCommand = "status"
	LifecycleInstall LifecycleCommand = "install"
	LifecycleUpgrade LifecycleCommand = "upgrade"
	LifecycleDisable LifecycleCommand = "disable"
	LifecycleRemove  LifecycleCommand = "remove"
)

type LifecycleState string

const (
	LifecycleStateLevel0   LifecycleState = "level0"
	LifecycleStateEnabled  LifecycleState = "enabled"
	LifecycleStateDisabled LifecycleState = "disabled"
	LifecycleStateRemoved  LifecycleState = "removed"
)

type LifecycleRequest struct {
	Command                       LifecycleCommand
	HostAlias                     string
	AcceptSharedSessionStableHome bool
}

type LifecycleResult struct {
	EndpointID domain.EndpointID
	State      LifecycleState
}

type LifecycleRelease interface {
	VerifiedManifest() Manifest
	BindInstallRequest(InstallRequest, Verifier, Policy) (InstallRequest, error)
}

type LifecycleRemoteLease struct {
	Remote    InstallRemote
	Handshake func(context.Context, string, Manifest) error
	Close     func() error
}

type LifecycleManagerConfig struct {
	Version        string
	Target         Target
	State          *StateStore
	Verifier       Verifier
	Policy         Policy
	Leaser         RemovalLeaser
	ResolveRelease func(context.Context, string, Target, Verifier, Policy) (LifecycleRelease, error)
	OpenRemote     func(context.Context, string) (LifecycleRemoteLease, error)
	Consent        func(LifecycleRequest) InstallConsent
}

// LifecycleManager is the daemon-owned composition boundary for Helper
// metadata, persistent state, independent remote transports and durable Job
// removal leases. It intentionally exposes no artifact path or command input.
type LifecycleManager struct {
	mu             sync.Mutex
	version        string
	target         Target
	state          *StateStore
	verifier       Verifier
	policy         Policy
	leaser         RemovalLeaser
	resolveRelease func(context.Context, string, Target, Verifier, Policy) (LifecycleRelease, error)
	openRemote     func(context.Context, string) (LifecycleRemoteLease, error)
	consent        func(LifecycleRequest) InstallConsent
}

func NewLifecycleManager(config LifecycleManagerConfig) (*LifecycleManager, error) {
	version, versionErr := parseReleaseVersion(config.Version)
	if versionErr != nil || version.String() != config.Version || !supportedReleaseTarget(config.Target) {
		return nil, errors.New("create Helper lifecycle manager: release identity is invalid")
	}
	if config.State == nil || config.Leaser == nil || config.ResolveRelease == nil || config.OpenRemote == nil || config.Consent == nil {
		return nil, errors.New("create Helper lifecycle manager: owner dependencies are incomplete")
	}
	return &LifecycleManager{
		version: config.Version, target: config.Target, state: config.State,
		verifier: config.Verifier, policy: config.Policy, leaser: config.Leaser,
		resolveRelease: config.ResolveRelease, openRemote: config.OpenRemote,
		consent: config.Consent,
	}, nil
}

func (manager *LifecycleManager) Execute(ctx context.Context, request LifecycleRequest) (LifecycleResult, error) {
	if manager == nil || ctx == nil {
		return LifecycleResult{}, errors.New("execute Helper lifecycle: manager or context is unavailable")
	}
	if err := validateLifecycleRequest(request); err != nil {
		return LifecycleResult{}, err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if request.Command != LifecycleStatus {
		if err := manager.recoverLocked(ctx); err != nil {
			return LifecycleResult{}, fmt.Errorf("execute Helper lifecycle: recover pending removal: %w", err)
		}
	}
	switch request.Command {
	case LifecycleStatus:
		return manager.statusLocked(request.HostAlias)
	case LifecycleInstall, LifecycleUpgrade:
		return manager.installLocked(ctx, request)
	case LifecycleDisable:
		return manager.disableLocked(request.HostAlias)
	case LifecycleRemove:
		return manager.removeLocked(ctx, request.HostAlias)
	default:
		return LifecycleResult{}, errors.New("execute Helper lifecycle: unknown command")
	}
}

// Recover resumes at most the one exact durable removal claim. Daemon startup
// calls this before exposing the lifecycle RPC; Execute also enforces it before
// every later mutation.
func (manager *LifecycleManager) Recover(ctx context.Context) error {
	if manager == nil || ctx == nil {
		return errors.New("recover Helper lifecycle: manager or context is unavailable")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.recoverLocked(ctx)
}

func validateLifecycleRequest(request LifecycleRequest) error {
	if err := validateHelperHostAlias(request.HostAlias); err != nil {
		return err
	}
	switch request.Command {
	case LifecycleInstall, LifecycleUpgrade, LifecycleRemove:
		if !request.AcceptSharedSessionStableHome {
			return errors.New("helper lifecycle: explicit shared-session stable-home consent is required")
		}
	case LifecycleStatus, LifecycleDisable:
		if request.AcceptSharedSessionStableHome {
			return errors.New("helper lifecycle: stable-home consent is not accepted for this command")
		}
	default:
		return errors.New("helper lifecycle: unknown command")
	}
	return nil
}

func (manager *LifecycleManager) statusLocked(hostAlias string) (LifecycleResult, error) {
	endpointID, exists, err := manager.state.LookupEndpoint(hostAlias)
	if err != nil || !exists {
		return LifecycleResult{State: LifecycleStateLevel0}, err
	}
	state, err := manager.state.lifecycleState(endpointID, manager.target)
	if err != nil {
		return LifecycleResult{}, err
	}
	return LifecycleResult{EndpointID: endpointID, State: state}, nil
}

func (manager *LifecycleManager) installLocked(ctx context.Context, request LifecycleRequest) (result LifecycleResult, resultErr error) {
	if !manager.verifier.configured() || !manager.policy.configured() {
		return LifecycleResult{}, errors.New("resolve Helper lifecycle release: production trust and current policy are closed")
	}
	release, err := manager.resolveRelease(ctx, manager.version, manager.target, manager.verifier, manager.policy)
	if err != nil {
		return LifecycleResult{}, fmt.Errorf("resolve Helper lifecycle release: %w", err)
	}
	if release == nil {
		return LifecycleResult{}, errors.New("resolve Helper lifecycle release: resolver returned no release")
	}
	manifest := release.VerifiedManifest()
	if manifest.Raw == nil || manifest.Version.String() != manager.version || manifest.Target() != manager.target {
		return LifecycleResult{}, errors.New("resolve Helper lifecycle release: signed identity does not match daemon release")
	}
	consent := manager.consent(request)
	if consent == nil {
		return LifecycleResult{}, errors.New("install Helper lifecycle: consent owner is unavailable")
	}
	endpointID, err := manager.state.ResolveEndpoint(request.HostAlias)
	if err != nil {
		return LifecycleResult{}, err
	}
	lease, err := manager.openRemote(ctx, request.HostAlias)
	if err != nil {
		return LifecycleResult{}, fmt.Errorf("install Helper lifecycle: open independent remote: %w", err)
	}
	if lease.Remote == nil || lease.Handshake == nil || lease.Close == nil {
		if lease.Close != nil {
			_ = lease.Close()
		}
		return LifecycleResult{}, errors.New("install Helper lifecycle: remote lease is incomplete")
	}
	defer func() { resultErr = joinLifecycleCloseError(resultErr, lease.Close()) }()
	installRequest, err := release.BindInstallRequest(InstallRequest{
		EndpointID: endpointID, EndpointLabel: request.HostAlias, State: manager.state,
		Consent: consent, Remote: lease.Remote,
		Handshake: func(handshakeContext context.Context, finalPath string, current Manifest) error {
			return lease.Handshake(handshakeContext, finalPath, current)
		},
	}, manager.verifier, manager.policy)
	if err != nil {
		return LifecycleResult{}, fmt.Errorf("install Helper lifecycle: bind release: %w", err)
	}
	if _, err := (&Installer{}).Install(ctx, installRequest); err != nil {
		return LifecycleResult{}, fmt.Errorf("install Helper lifecycle: %w", err)
	}
	return LifecycleResult{EndpointID: endpointID, State: LifecycleStateEnabled}, nil
}

func (manager *LifecycleManager) disableLocked(hostAlias string) (LifecycleResult, error) {
	endpointID, exists, err := manager.state.LookupEndpoint(hostAlias)
	if err != nil {
		return LifecycleResult{}, err
	}
	if !exists {
		return LifecycleResult{}, errors.New("disable Helper lifecycle: endpoint mapping not found")
	}
	state, err := manager.state.lifecycleState(endpointID, manager.target)
	if err != nil {
		return LifecycleResult{}, err
	}
	if state != LifecycleStateEnabled {
		return LifecycleResult{}, errors.New("disable Helper lifecycle: enabled artifact not found")
	}
	record, err := manager.state.LoadEnabledAnyProtocol(endpointID, manager.target)
	if err != nil {
		return LifecycleResult{}, err
	}
	if err := manager.state.Disable(endpointID, record.ArtifactID.ProtocolMajor, manager.target); err != nil {
		return LifecycleResult{}, err
	}
	return LifecycleResult{EndpointID: endpointID, State: LifecycleStateDisabled}, nil
}

func (manager *LifecycleManager) removeLocked(ctx context.Context, hostAlias string) (result LifecycleResult, resultErr error) {
	endpointID, exists, err := manager.state.LookupEndpoint(hostAlias)
	if err != nil {
		return LifecycleResult{}, err
	}
	if !exists {
		return LifecycleResult{}, errors.New("remove Helper lifecycle: endpoint mapping not found")
	}
	record, err := manager.state.LoadEnabledAnyProtocol(endpointID, manager.target)
	if err != nil {
		return LifecycleResult{}, err
	}
	lease, err := manager.openRemote(ctx, hostAlias)
	if err != nil {
		return LifecycleResult{}, fmt.Errorf("remove Helper lifecycle: open independent remote: %w", err)
	}
	if lease.Remote == nil || lease.Close == nil {
		if lease.Close != nil {
			_ = lease.Close()
		}
		return LifecycleResult{}, errors.New("remove Helper lifecycle: remote lease is incomplete")
	}
	defer func() { resultErr = joinLifecycleCloseError(resultErr, lease.Close()) }()
	if err := RemoveEnabled(ctx, EnableRequest{
		EndpointID: endpointID, ProtocolMajor: record.ArtifactID.ProtocolMajor, Target: manager.target,
		Verifier: manager.verifier, Policy: manager.policy, State: manager.state, Remote: lease.Remote,
	}, manager.leaser); err != nil {
		return LifecycleResult{}, err
	}
	return LifecycleResult{EndpointID: endpointID, State: LifecycleStateRemoved}, nil
}

func (manager *LifecycleManager) recoverLocked(ctx context.Context) (resultErr error) {
	claim, exists, err := manager.state.PendingRemoval()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !exists {
		return err
	}
	hostAlias, exists, err := manager.state.LookupHostAlias(claim.EndpointID)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("recover Helper lifecycle: pending endpoint has no exact Host alias mapping")
	}
	lease, err := manager.openRemote(ctx, hostAlias)
	if err != nil {
		return fmt.Errorf("recover Helper lifecycle: open independent remote: %w", err)
	}
	if lease.Remote == nil || lease.Close == nil {
		if lease.Close != nil {
			_ = lease.Close()
		}
		return errors.New("recover Helper lifecycle: remote lease is incomplete")
	}
	defer func() { resultErr = joinLifecycleCloseError(resultErr, lease.Close()) }()
	return ResumePendingRemoval(ctx, EnableRequest{
		EndpointID: claim.EndpointID, ProtocolMajor: claim.ArtifactID.ProtocolMajor,
		Target:   Target{OS: claim.ArtifactID.OS, Arch: claim.ArtifactID.Arch},
		Verifier: manager.verifier, Policy: manager.policy, State: manager.state, Remote: lease.Remote,
	}, manager.leaser)
}

func joinLifecycleCloseError(operationErr, closeErr error) error {
	if closeErr == nil {
		return operationErr
	}
	if operationErr == nil {
		return fmt.Errorf("close Helper lifecycle remote: %w", closeErr)
	}
	return errors.Join(operationErr, fmt.Errorf("close Helper lifecycle remote: %w", closeErr))
}

func (s *StateStore) lifecycleState(endpointID domain.EndpointID, target Target) (LifecycleState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index, err := s.loadIndexOrEmpty()
	if err != nil {
		return "", err
	}
	state := LifecycleStateLevel0
	for _, record := range index.Records {
		if record.EndpointID != string(endpointID) || record.OS != target.OS || record.Arch != target.Arch {
			continue
		}
		if record.Enabled {
			return LifecycleStateEnabled, nil
		}
		if record.Installed {
			state = LifecycleStateDisabled
		} else if state == LifecycleStateLevel0 {
			state = LifecycleStateRemoved
		}
	}
	return state, nil
}

// LoadEnabledAnyProtocol returns the one enabled target selection while
// rejecting ambiguous/corrupt state. Protocol is derived only from signed
// persisted metadata, never from RPC input.
func (s *StateStore) LoadEnabledAnyProtocol(endpointID domain.EndpointID, target Target) (EnabledRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index, err := s.loadIndex()
	if err != nil {
		return EnabledRecord{}, err
	}
	position := -1
	for candidate, record := range index.Records {
		if record.EndpointID == string(endpointID) && record.OS == target.OS && record.Arch == target.Arch && record.Enabled {
			if position >= 0 {
				return EnabledRecord{}, errors.New("load enabled Helper target: multiple protocols are active")
			}
			position = candidate
		}
	}
	if position < 0 {
		return EnabledRecord{}, errors.New("load enabled Helper target: record not found")
	}
	return s.loadRecord(endpointID, index.Records[position], "load enabled Helper target")
}
