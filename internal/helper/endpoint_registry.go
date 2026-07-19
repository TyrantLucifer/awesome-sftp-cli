package helper

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/platform"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transport/openssh"
)

const (
	maxHelperHostAliasBytes       = 1024
	maxHelperEndpointMappings     = 4096
	helperEndpointRegistrySchema  = 1
	maxEndpointGenerationAttempts = 8
)

type endpointRegistry struct {
	Schema   uint16            `json:"schema"`
	Mappings []endpointMapping `json:"mappings"`
}

type endpointMapping struct {
	HostAlias  string `json:"host_alias"`
	EndpointID string `json:"endpoint_id"`
}

// LookupEndpoint returns the durable opaque identity for one exact validated
// OpenSSH Host alias without creating any state.
func (s *StateStore) LookupEndpoint(hostAlias string) (domain.EndpointID, bool, error) {
	if err := validateHelperHostAlias(hostAlias); err != nil {
		return "", false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	registry, err := s.loadEndpointRegistryOrEmpty()
	if err != nil {
		return "", false, err
	}
	position := sort.Search(len(registry.Mappings), func(position int) bool {
		return registry.Mappings[position].HostAlias >= hostAlias
	})
	if position == len(registry.Mappings) || registry.Mappings[position].HostAlias != hostAlias {
		return "", false, nil
	}
	return domain.EndpointID(registry.Mappings[position].EndpointID), true, nil
}

// LookupHostAlias reverses an exact durable mapping for restart recovery. It
// does not infer an alias or create state when the identity is unknown.
func (s *StateStore) LookupHostAlias(endpointID domain.EndpointID) (string, bool, error) {
	if _, err := domain.ParseEndpointID(string(endpointID)); err != nil {
		return "", false, errors.New("lookup Helper Host alias: endpoint ID is invalid")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	registry, err := s.loadEndpointRegistryOrEmpty()
	if err != nil {
		return "", false, err
	}
	for _, mapping := range registry.Mappings {
		if mapping.EndpointID == string(endpointID) {
			return mapping.HostAlias, true, nil
		}
	}
	return "", false, nil
}

// ResolveEndpoint returns or atomically creates the durable opaque identity
// for one exact validated OpenSSH Host alias. Only install/upgrade admission
// should create mappings; status/disable/remove use LookupEndpoint.
func (s *StateStore) ResolveEndpoint(hostAlias string) (domain.EndpointID, error) {
	if err := validateHelperHostAlias(hostAlias); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	registry, err := s.loadEndpointRegistryOrEmpty()
	if err != nil {
		return "", err
	}
	position := sort.Search(len(registry.Mappings), func(position int) bool {
		return registry.Mappings[position].HostAlias >= hostAlias
	})
	if position < len(registry.Mappings) && registry.Mappings[position].HostAlias == hostAlias {
		return domain.EndpointID(registry.Mappings[position].EndpointID), nil
	}
	if len(registry.Mappings) >= maxHelperEndpointMappings {
		return "", errors.New("resolve Helper endpoint: mapping limit reached")
	}
	if s.endpointGenerator == nil {
		return "", errors.New("resolve Helper endpoint: ID generator is unavailable")
	}
	var endpointID domain.EndpointID
	for attempt := 0; attempt < maxEndpointGenerationAttempts; attempt++ {
		candidate, generateErr := domain.NewEndpointID(s.endpointGenerator)
		if generateErr != nil {
			return "", fmt.Errorf("resolve Helper endpoint: generate identity: %w", generateErr)
		}
		if !endpointRegistryContainsID(registry.Mappings, candidate) {
			endpointID = candidate
			break
		}
	}
	if endpointID == "" {
		return "", errors.New("resolve Helper endpoint: repeated identity collision")
	}
	registry.Mappings = append(registry.Mappings, endpointMapping{HostAlias: hostAlias, EndpointID: string(endpointID)})
	sort.Slice(registry.Mappings, func(left, right int) bool {
		return registry.Mappings[left].HostAlias < registry.Mappings[right].HostAlias
	})
	if err := s.writeEndpointRegistry(registry); err != nil {
		return "", fmt.Errorf("resolve Helper endpoint: %w", err)
	}
	return endpointID, nil
}

func validateHelperHostAlias(hostAlias string) error {
	if len(hostAlias) > maxHelperHostAliasBytes {
		return errors.New("helper endpoint Host alias exceeds hard limit")
	}
	if err := openssh.ValidateHostAlias(hostAlias); err != nil {
		return fmt.Errorf("helper endpoint Host alias is invalid: %w", err)
	}
	return nil
}

func (s *StateStore) loadEndpointRegistryOrEmpty() (endpointRegistry, error) {
	registry, err := s.loadEndpointRegistry()
	if errors.Is(err, os.ErrNotExist) {
		return endpointRegistry{Schema: helperEndpointRegistrySchema, Mappings: []endpointMapping{}}, nil
	}
	return registry, err
}

func (s *StateStore) loadEndpointRegistry() (endpointRegistry, error) {
	path := s.endpointRegistryPath()
	if err := platform.ValidatePrivateFile(path, platform.ValidatePersistent); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return endpointRegistry{}, os.ErrNotExist
		}
		return endpointRegistry{}, fmt.Errorf("load Helper endpoint registry: %w", err)
	}
	var registry endpointRegistry
	if err := decodeBoundedStateFile(path, &registry); err != nil {
		return endpointRegistry{}, fmt.Errorf("load Helper endpoint registry: %w", err)
	}
	if registry.Schema != helperEndpointRegistrySchema || len(registry.Mappings) > maxHelperEndpointMappings {
		return endpointRegistry{}, errors.New("load Helper endpoint registry: schema or mapping count is invalid")
	}
	identities := make(map[domain.EndpointID]struct{}, len(registry.Mappings))
	previousAlias := ""
	for position, mapping := range registry.Mappings {
		if err := validateHelperHostAlias(mapping.HostAlias); err != nil {
			return endpointRegistry{}, fmt.Errorf("load Helper endpoint registry: mapping %d: %w", position, err)
		}
		if position > 0 && mapping.HostAlias <= previousAlias {
			return endpointRegistry{}, errors.New("load Helper endpoint registry: mappings are duplicate or noncanonical")
		}
		endpointID, err := domain.ParseEndpointID(mapping.EndpointID)
		if err != nil {
			return endpointRegistry{}, fmt.Errorf("load Helper endpoint registry: mapping %d has invalid endpoint ID", position)
		}
		if _, duplicate := identities[endpointID]; duplicate {
			return endpointRegistry{}, errors.New("load Helper endpoint registry: endpoint ID is reused")
		}
		identities[endpointID] = struct{}{}
		previousAlias = mapping.HostAlias
	}
	return registry, nil
}

func (s *StateStore) writeEndpointRegistry(registry endpointRegistry) error {
	raw, err := json.Marshal(registry)
	if err != nil {
		return err
	}
	if int64(len(raw)) > maxHelperStateBytes {
		return errors.New("write Helper endpoint registry: document exceeds hard limit")
	}
	finalPath := s.endpointRegistryPath()
	if _, err := os.Lstat(finalPath); err == nil {
		if err := platform.ValidatePrivateFile(finalPath, platform.ValidatePersistent); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary, err := s.writeTemporary(".helper-endpoints-*.tmp", raw)
	if err != nil {
		return err
	}
	defer os.Remove(temporary)
	if s.beforeEndpointRegistryRename != nil {
		if err := s.beforeEndpointRegistryRename(); err != nil {
			return err
		}
	}
	if err := os.Rename(temporary, finalPath); err != nil {
		return err
	}
	if err := platform.ValidatePrivateFile(finalPath, platform.ValidatePersistent); err != nil {
		return err
	}
	return syncHelperStateDirectory(s.root)
}

func endpointRegistryContainsID(mappings []endpointMapping, endpointID domain.EndpointID) bool {
	for _, mapping := range mappings {
		if mapping.EndpointID == string(endpointID) {
			return true
		}
	}
	return false
}
