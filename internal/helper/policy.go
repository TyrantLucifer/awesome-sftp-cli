package helper

import (
	"errors"
	"fmt"
	"sync"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
)

type ArtifactID = domain.HelperArtifactID

func (m Manifest) ArtifactID() ArtifactID {
	return ArtifactID{ProtocolMajor: m.ProtocolMajor, Version: m.Version.String(), OS: m.OS, Arch: m.Arch, SHA256: m.SHA256}
}

type FloorKey struct {
	ProtocolMajor uint16
	OS            string
	Arch          string
}

func (m Manifest) FloorKey() FloorKey {
	return FloorKey{ProtocolMajor: m.ProtocolMajor, OS: m.OS, Arch: m.Arch}
}

type Policy struct {
	clientVersion Version
	floors        map[FloorKey]Version
	revokedKeys   map[string]struct{}
	denied        map[ArtifactID]struct{}
}

// ProductionDistributionOpen remains false until the protected release trust,
// notarization, final-byte manifest, and offline-signing gates are complete.
const ProductionDistributionOpen = false

// NewProductionPolicy stays fail-closed while production Helper distribution
// is CLOSED: there are no trusted keys, supported floors, or installable assets.
func NewProductionPolicy() Policy { return Policy{} }

func (p Policy) configured() bool { return len(p.floors) != 0 }

func (p Policy) Check(manifest Manifest) error {
	if _, revoked := p.revokedKeys[manifest.KeyID]; revoked {
		return errors.New("helper current policy: signing key is revoked")
	}
	if _, denied := p.denied[manifest.ArtifactID()]; denied {
		return errors.New("helper current policy: artifact is denied")
	}
	floor, supported := p.floors[manifest.FloorKey()]
	if !supported {
		return errors.New("helper current policy: protocol or target is unsupported")
	}
	if manifest.Version.Compare(floor) < 0 {
		return fmt.Errorf("helper current policy: version %s is below release floor %s", manifest.Version, floor)
	}
	if p.clientVersion.Compare(manifest.MinClient) < 0 {
		return fmt.Errorf("helper current policy: client %s is below required %s", p.clientVersion, manifest.MinClient)
	}
	return nil
}

type HighWaterDecision string

const (
	HighWaterInstall   HighWaterDecision = "install"
	HighWaterNoop      HighWaterDecision = "noop"
	HighWaterReinstall HighWaterDecision = "reinstall_same_hash"
)

type highWaterKey struct {
	EndpointID    domain.EndpointID
	ProtocolMajor uint16
	OS            string
	Arch          string
}

type highWaterValue struct {
	Version Version
	SHA256  string
}

type HighWater struct {
	mu      sync.Mutex
	records map[highWaterKey]highWaterValue
}

func NewHighWater() *HighWater { return &HighWater{records: make(map[highWaterKey]highWaterValue)} }

func (h *HighWater) Check(endpointID domain.EndpointID, manifest Manifest, repair bool) (HighWaterDecision, error) {
	if h == nil || endpointID == "" {
		return "", errors.New("helper high-water: endpoint and store are required")
	}
	key := highWaterKey{EndpointID: endpointID, ProtocolMajor: manifest.ProtocolMajor, OS: manifest.OS, Arch: manifest.Arch}
	h.mu.Lock()
	record, exists := h.records[key]
	h.mu.Unlock()
	if !exists {
		return HighWaterInstall, nil
	}
	switch manifest.Version.Compare(record.Version) {
	case -1:
		return "", fmt.Errorf("helper high-water: version %s is below installed %s", manifest.Version, record.Version)
	case 0:
		if manifest.SHA256 != record.SHA256 {
			return "", errors.New("helper high-water: same-version republish is forbidden")
		}
		if repair {
			return HighWaterReinstall, nil
		}
		return HighWaterNoop, nil
	default:
		return HighWaterInstall, nil
	}
}

// Commit is called only after install and handshake succeed.
func (h *HighWater) Commit(endpointID domain.EndpointID, manifest Manifest) error {
	decision, err := h.Check(endpointID, manifest, false)
	if err != nil {
		return err
	}
	if decision == HighWaterNoop {
		return nil
	}
	key := highWaterKey{EndpointID: endpointID, ProtocolMajor: manifest.ProtocolMajor, OS: manifest.OS, Arch: manifest.Arch}
	h.mu.Lock()
	defer h.mu.Unlock()
	if current, exists := h.records[key]; exists {
		comparison := manifest.Version.Compare(current.Version)
		if comparison < 0 || comparison == 0 && manifest.SHA256 != current.SHA256 {
			return errors.New("helper high-water: concurrent monotonicity violation")
		}
	}
	h.records[key] = highWaterValue{Version: manifest.Version, SHA256: manifest.SHA256}
	return nil
}
