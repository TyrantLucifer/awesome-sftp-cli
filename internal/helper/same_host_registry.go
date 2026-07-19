package helper

import (
	"context"
	"errors"
	"sync"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/transfer"
)

const maxRegisteredSameHostBackends = 4096

type sameHostBackendKey struct {
	endpoint domain.EndpointID
	artifact ArtifactID
}

// SameHostCopyRegistry keeps current planning separate from exact durable Job
// execution. Activation changes only future plans; ResolveSameHostCopy retains
// access to an older registered artifact until reference-protected uninstall.
type SameHostCopyRegistry struct {
	mu       sync.RWMutex
	active   map[domain.EndpointID]ArtifactID
	backends map[sameHostBackendKey]transfer.SameHostCopyBackend
}

func NewSameHostCopyRegistry() *SameHostCopyRegistry {
	return &SameHostCopyRegistry{
		active:   make(map[domain.EndpointID]ArtifactID),
		backends: make(map[sameHostBackendKey]transfer.SameHostCopyBackend),
	}
}

func (registry *SameHostCopyRegistry) Register(endpointID domain.EndpointID, artifact ArtifactID, backend transfer.SameHostCopyBackend) error {
	if registry == nil || backend == nil || !validRegistryIdentity(endpointID, artifact) {
		return errors.New("register Helper backend: identity and backend are required")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	key := sameHostBackendKey{endpoint: endpointID, artifact: artifact}
	if _, exists := registry.backends[key]; exists {
		return errors.New("register Helper backend: exact artifact is already registered")
	}
	if len(registry.backends) >= maxRegisteredSameHostBackends {
		return errors.New("register Helper backend: registry limit reached")
	}
	registry.backends[key] = backend
	return nil
}

func (registry *SameHostCopyRegistry) Activate(endpointID domain.EndpointID, artifact ArtifactID) error {
	if registry == nil || !validRegistryIdentity(endpointID, artifact) {
		return errors.New("activate Helper backend: identity is invalid")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.backends[sameHostBackendKey{endpoint: endpointID, artifact: artifact}] == nil {
		return errors.New("activate Helper backend: exact artifact is not registered")
	}
	registry.active[endpointID] = artifact
	return nil
}

func (registry *SameHostCopyRegistry) PrepareCopy(ctx context.Context, request transfer.SameHostCopyPrepareRequest) (transfer.SameHostCopyBinding, error) {
	if registry == nil || request.Source.EndpointID == "" {
		return transfer.SameHostCopyBinding{}, errors.New("prepare registered Helper copy: request is invalid")
	}
	registry.mu.RLock()
	artifact, exists := registry.active[request.Source.EndpointID]
	backend := registry.backends[sameHostBackendKey{endpoint: request.Source.EndpointID, artifact: artifact}]
	registry.mu.RUnlock()
	if !exists || backend == nil {
		return transfer.SameHostCopyBinding{}, errors.New("prepare registered Helper copy: endpoint has no active artifact")
	}
	binding, err := backend.PrepareCopy(ctx, request)
	if err != nil {
		return transfer.SameHostCopyBinding{}, err
	}
	if binding.EndpointID != request.Source.EndpointID || binding.ArtifactID != artifact {
		return transfer.SameHostCopyBinding{}, errors.New("prepare registered Helper copy: backend returned a different exact artifact")
	}
	return binding, nil
}

func (registry *SameHostCopyRegistry) StageCopy(ctx context.Context, request transfer.SameHostCopyStageRequest) (transfer.SameHostCopyStageResult, error) {
	if request.Binding.EndpointID != request.Source.EndpointID {
		return transfer.SameHostCopyStageResult{}, errors.New("stage registered Helper copy: binding endpoint is invalid")
	}
	backend, err := registry.ResolveSameHostCopy(ctx, request.Source.EndpointID, request.Binding.ArtifactID)
	if err != nil {
		return transfer.SameHostCopyStageResult{}, err
	}
	return backend.StageCopy(ctx, request)
}

func (registry *SameHostCopyRegistry) ResolveSameHostCopy(_ context.Context, endpointID domain.EndpointID, artifact domain.HelperArtifactID) (transfer.SameHostCopyBackend, error) {
	if registry == nil || !validRegistryIdentity(endpointID, artifact) {
		return nil, errors.New("resolve Helper backend: identity is invalid")
	}
	registry.mu.RLock()
	backend := registry.backends[sameHostBackendKey{endpoint: endpointID, artifact: artifact}]
	registry.mu.RUnlock()
	if backend == nil {
		return nil, errors.New("resolve Helper backend: exact artifact is unavailable")
	}
	return backend, nil
}

func validRegistryIdentity(endpointID domain.EndpointID, artifact ArtifactID) bool {
	if _, err := domain.ParseEndpointID(string(endpointID)); err != nil {
		return false
	}
	if artifact.ProtocolMajor == 0 || artifact.Version == "" || artifact.OS == "" || artifact.Arch == "" || len(artifact.SHA256) != 64 || !isLowerHex(artifact.SHA256) {
		return false
	}
	_, err := parseReleaseVersion(artifact.Version)
	return err == nil
}
