package helper

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/platform"
)

const (
	maxHelperStateBytes      int64 = 1 << 20
	maxHelperMetadataFiles         = 4096
	HelperStateSchemaVersion       = 2
)

type EnabledRecord struct {
	RawManifest  []byte
	RawSignature []byte
	FinalPath    string
	MetadataPath string
	ArtifactID   ArtifactID
}

type RemovalClaim struct {
	EndpointID domain.EndpointID `json:"endpoint_id"`
	ArtifactID ArtifactID        `json:"artifact_id"`
	FinalPath  string            `json:"final_path"`
}

type StateStore struct {
	root                         string
	mu                           sync.Mutex
	endpointGenerator            domain.Generator
	beforeIndexRename            func() error
	beforeEndpointRegistryRename func() error
}

type stateIndex struct {
	Schema       uint16             `json:"schema"`
	Records      []stateIndexRecord `json:"records"`
	RemovalClaim *RemovalClaim      `json:"removal_claim,omitempty"`
	sourceSchema uint16             `json:"-"`
}

type stateIndexRecord struct {
	EndpointID    string `json:"endpoint_id"`
	ProtocolMajor uint16 `json:"protocol_major"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	Version       string `json:"version"`
	SHA256        string `json:"sha256"`
	MetadataID    string `json:"metadata_id"`
	FinalPath     string `json:"final_path"`
	Enabled       bool   `json:"enabled"`
	Installed     bool   `json:"installed,omitempty"`
}

type metadataDocument struct {
	Schema       uint16 `json:"schema"`
	EndpointID   string `json:"endpoint_id"`
	RawManifest  []byte `json:"raw_manifest"`
	RawSignature []byte `json:"raw_signature"`
}

func NewStateStore(root string) (*StateStore, error) {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root || strings.IndexByte(root, 0) >= 0 {
		return nil, errors.New("create helper state store: root must be canonical absolute")
	}
	if err := platform.PreparePrivateDirectory(root, platform.ValidatePersistent); err != nil {
		return nil, fmt.Errorf("create helper state store: %w", err)
	}
	store := &StateStore{root: root, endpointGenerator: &domain.RandomGenerator{}}
	if _, err := os.Lstat(store.endpointRegistryPath()); err == nil {
		if _, err := store.loadEndpointRegistry(); err != nil {
			return nil, fmt.Errorf("create helper state store: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("create helper state store: inspect endpoint registry: %w", err)
	}
	if _, err := os.Lstat(store.indexPath()); err == nil {
		index, err := store.loadIndex()
		if err != nil {
			return nil, err
		}
		if index.sourceSchema == 1 {
			if err := store.writeIndex(index); err != nil {
				return nil, fmt.Errorf("create helper state store: migrate v1 index: %w", err)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("create helper state store: inspect index: %w", err)
	}
	return store, nil
}

// StageMetadata durably stores exact signed bytes before any remote probe. It
// does not treat them as enabled and never substitutes a parsed re-encoding.
func (s *StateStore) StageMetadata(endpointID domain.EndpointID, rawManifest, rawSignature []byte) error {
	if _, err := domain.ParseEndpointID(string(endpointID)); err != nil {
		return errors.New("stage helper metadata: endpoint ID is invalid")
	}
	manifest, err := ParseManifestV1(rawManifest)
	if err != nil {
		return err
	}
	if _, err := ParseDetachedSignature(rawSignature); err != nil {
		return err
	}
	document := metadataDocument{Schema: 1, EndpointID: string(endpointID), RawManifest: append([]byte(nil), rawManifest...), RawSignature: append([]byte(nil), rawSignature...)}
	raw, err := json.Marshal(document)
	if err != nil {
		return err
	}
	if int64(len(raw)) > maxHelperStateBytes {
		return errors.New("stage helper metadata: document exceeds hard limit")
	}
	metadataID := helperMetadataID(endpointID, manifest.Raw, rawSignature)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeMetadataNoReplace(metadataID, raw, document)
}

// CommitEnabled atomically advances the per-endpoint high-water and selects
// exact already-staged metadata only after the caller completed remote
// verification and handshake.
func (s *StateStore) CommitEnabled(endpointID domain.EndpointID, manifest Manifest, rawSignature []byte, finalPath string) error {
	if _, err := domain.ParseEndpointID(string(endpointID)); err != nil || manifest.Raw == nil {
		return errors.New("commit enabled helper: endpoint or parsed manifest is invalid")
	}
	if finalPath == "" || len(finalPath) > MaxHelperRemotePathBytes || finalPath[0] != '/' || filepath.ToSlash(filepath.Clean(finalPath)) != finalPath || strings.IndexByte(finalPath, 0) >= 0 {
		return errors.New("commit enabled helper: final path is invalid")
	}
	if _, err := ParseDetachedSignature(rawSignature); err != nil {
		return err
	}
	metadataID := helperMetadataID(endpointID, manifest.Raw, rawSignature)
	s.mu.Lock()
	defer s.mu.Unlock()
	document, err := s.loadMetadata(metadataID)
	if err != nil {
		return fmt.Errorf("commit enabled helper: staged metadata: %w", err)
	}
	if document.EndpointID != string(endpointID) || !bytes.Equal(document.RawManifest, manifest.Raw) || !bytes.Equal(document.RawSignature, rawSignature) {
		return errors.New("commit enabled helper: staged metadata does not match exact signed bytes")
	}

	index, err := s.loadIndexOrEmpty()
	if err != nil {
		return err
	}
	highWaterPosition, highWaterExists := findHighWaterRecord(index.Records, endpointID, manifest.ProtocolMajor, manifest.Target())
	if highWaterExists {
		current, parseErr := parseReleaseVersion(index.Records[highWaterPosition].Version)
		if parseErr != nil {
			return errors.New("commit enabled helper: stored version is invalid")
		}
		switch manifest.Version.Compare(current) {
		case -1:
			return errors.New("commit enabled helper: downgrade violates persistent high-water")
		case 0:
			if manifest.SHA256 != index.Records[highWaterPosition].SHA256 {
				return errors.New("commit enabled helper: same-version republish violates persistent high-water")
			}
		}
	}
	artifact := manifest.ArtifactID()
	position, exists := findExactStateRecord(index.Records, endpointID, artifact)
	if index.RemovalClaim != nil && index.RemovalClaim.EndpointID == endpointID && index.RemovalClaim.ArtifactID == artifact {
		return errors.New("commit enabled helper: artifact has a pending removal claim")
	}
	record := stateIndexRecord{
		EndpointID: string(endpointID), ProtocolMajor: manifest.ProtocolMajor, OS: manifest.OS, Arch: manifest.Arch,
		Version: manifest.Version.String(), SHA256: manifest.SHA256, MetadataID: metadataID, FinalPath: finalPath, Enabled: true, Installed: true,
	}
	for recordIndex := range index.Records {
		if sameStateScope(index.Records[recordIndex], endpointID, manifest.ProtocolMajor, manifest.Target()) {
			index.Records[recordIndex].Enabled = false
		}
	}
	if exists {
		index.Records[position] = record
	} else {
		if len(index.Records) >= 4096 {
			return errors.New("commit enabled helper: record limit reached")
		}
		index.Records = append(index.Records, record)
	}
	sortStateRecords(index.Records)
	return s.writeIndex(index)
}

func (s *StateStore) Check(endpointID domain.EndpointID, manifest Manifest, repair bool) (HighWaterDecision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index, err := s.loadIndexOrEmpty()
	if err != nil {
		return "", err
	}
	position, exists := findHighWaterRecord(index.Records, endpointID, manifest.ProtocolMajor, manifest.Target())
	if !exists {
		return HighWaterInstall, nil
	}
	current, err := parseReleaseVersion(index.Records[position].Version)
	if err != nil {
		return "", errors.New("helper state high-water: stored version is invalid")
	}
	switch manifest.Version.Compare(current) {
	case -1:
		return "", fmt.Errorf("helper state high-water: version %s is below installed %s", manifest.Version, current)
	case 0:
		if manifest.SHA256 != index.Records[position].SHA256 {
			return "", errors.New("helper state high-water: same-version republish is forbidden")
		}
		if repair {
			return HighWaterReinstall, nil
		}
		return HighWaterNoop, nil
	default:
		return HighWaterInstall, nil
	}
}

func (s *StateStore) LoadEnabled(endpointID domain.EndpointID, protocol uint16, target Target) (EnabledRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index, err := s.loadIndex()
	if err != nil {
		return EnabledRecord{}, err
	}
	position, exists := findEnabledStateRecord(index.Records, endpointID, protocol, target)
	if !exists {
		return EnabledRecord{}, errors.New("load enabled helper: record not found")
	}
	record := index.Records[position]
	if !record.Enabled || !record.Installed {
		return EnabledRecord{}, errors.New("load enabled helper: record is disabled or not installed")
	}
	return s.loadRecord(endpointID, record, "load enabled helper")
}

func (s *StateStore) LoadArtifact(endpointID domain.EndpointID, artifact ArtifactID) (EnabledRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index, err := s.loadIndex()
	if err != nil {
		return EnabledRecord{}, err
	}
	position, exists := findExactStateRecord(index.Records, endpointID, artifact)
	if !exists || !index.Records[position].Installed {
		return EnabledRecord{}, errors.New("load Helper artifact: installed record not found")
	}
	return s.loadRecord(endpointID, index.Records[position], "load Helper artifact")
}

func (s *StateStore) loadRecord(endpointID domain.EndpointID, record stateIndexRecord, operation string) (EnabledRecord, error) {
	document, err := s.loadMetadata(record.MetadataID)
	if err != nil {
		return EnabledRecord{}, fmt.Errorf("%s: %w", operation, err)
	}
	manifest, err := ParseManifestV1(document.RawManifest)
	if err != nil {
		return EnabledRecord{}, err
	}
	if _, err := ParseDetachedSignature(document.RawSignature); err != nil {
		return EnabledRecord{}, err
	}
	if document.EndpointID != record.EndpointID || document.EndpointID != string(endpointID) || manifest.ProtocolMajor != record.ProtocolMajor || manifest.Target() != (Target{OS: record.OS, Arch: record.Arch}) || manifest.Version.String() != record.Version || manifest.SHA256 != record.SHA256 || helperMetadataID(endpointID, document.RawManifest, document.RawSignature) != record.MetadataID {
		return EnabledRecord{}, fmt.Errorf("%s: metadata does not match persistent index", operation)
	}
	return EnabledRecord{RawManifest: append([]byte(nil), document.RawManifest...), RawSignature: append([]byte(nil), document.RawSignature...), FinalPath: record.FinalPath, MetadataPath: s.metadataPath(record.MetadataID), ArtifactID: manifest.ArtifactID()}, nil
}

// Activate atomically selects an already installed exact artifact. Callers own
// the fresh policy, binding, remote-byte, and handshake checks before commit.
func (s *StateStore) Activate(endpointID domain.EndpointID, artifact ArtifactID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	index, err := s.loadIndex()
	if err != nil {
		return err
	}
	position, exists := findExactStateRecord(index.Records, endpointID, artifact)
	if !exists || !index.Records[position].Installed {
		return errors.New("activate Helper artifact: installed record not found")
	}
	highWater, exists := findHighWaterRecord(index.Records, endpointID, artifact.ProtocolMajor, Target{OS: artifact.OS, Arch: artifact.Arch})
	if !exists || index.Records[highWater].Version != artifact.Version || index.Records[highWater].SHA256 != artifact.SHA256 {
		return errors.New("activate Helper artifact: downgrade violates persistent high-water")
	}
	if index.RemovalClaim != nil && index.RemovalClaim.EndpointID == endpointID && index.RemovalClaim.ArtifactID == artifact {
		return errors.New("activate Helper artifact: removal is pending")
	}
	target := Target{OS: artifact.OS, Arch: artifact.Arch}
	for recordIndex := range index.Records {
		if sameStateScope(index.Records[recordIndex], endpointID, artifact.ProtocolMajor, target) {
			index.Records[recordIndex].Enabled = recordIndex == position
		}
	}
	return s.writeIndex(index)
}

// Disable atomically clears only the enabled selection. Signed metadata and
// the monotonic high-water remain durable so disable/remove cannot reopen a
// downgrade or same-version republish path.
func (s *StateStore) Disable(endpointID domain.EndpointID, protocol uint16, target Target) error {
	if _, err := domain.ParseEndpointID(string(endpointID)); err != nil || protocol == 0 || target.OS == "" || target.Arch == "" {
		return errors.New("disable helper: identity is invalid")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index, err := s.loadIndex()
	if err != nil {
		return err
	}
	position, exists := findEnabledStateRecord(index.Records, endpointID, protocol, target)
	if !exists {
		return errors.New("disable helper: record not found")
	}
	if !index.Records[position].Enabled {
		return nil
	}
	index.Records[position].Enabled = false
	return s.writeIndex(index)
}

func (s *StateStore) BeginRemoval(endpointID domain.EndpointID, artifact ArtifactID) (RemovalClaim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index, err := s.loadIndex()
	if err != nil {
		return RemovalClaim{}, err
	}
	if index.RemovalClaim != nil {
		if index.RemovalClaim.EndpointID == endpointID && index.RemovalClaim.ArtifactID == artifact {
			return *index.RemovalClaim, nil
		}
		return RemovalClaim{}, errors.New("begin Helper removal: another removal is pending")
	}
	position, exists := findExactStateRecord(index.Records, endpointID, artifact)
	if !exists || !index.Records[position].Installed {
		return RemovalClaim{}, errors.New("begin Helper removal: installed artifact not found")
	}
	index.Records[position].Enabled = false
	claim := RemovalClaim{EndpointID: endpointID, ArtifactID: artifact, FinalPath: index.Records[position].FinalPath}
	index.RemovalClaim = &claim
	if err := s.writeIndex(index); err != nil {
		return RemovalClaim{}, err
	}
	return claim, nil
}

func (s *StateStore) PendingRemoval() (RemovalClaim, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index, err := s.loadIndex()
	if err != nil {
		return RemovalClaim{}, false, err
	}
	if index.RemovalClaim == nil {
		return RemovalClaim{}, false, nil
	}
	return *index.RemovalClaim, true, nil
}

func (s *StateStore) CompleteRemoval(endpointID domain.EndpointID, artifact ArtifactID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	index, err := s.loadIndex()
	if err != nil {
		return err
	}
	if index.RemovalClaim == nil || index.RemovalClaim.EndpointID != endpointID || index.RemovalClaim.ArtifactID != artifact {
		return errors.New("complete Helper removal: exact claim not found")
	}
	position, exists := findExactStateRecord(index.Records, endpointID, artifact)
	if !exists || !index.Records[position].Installed || index.Records[position].Enabled {
		return errors.New("complete Helper removal: record state is invalid")
	}
	index.Records[position].Installed = false
	index.RemovalClaim = nil
	return s.writeIndex(index)
}

func (s *StateStore) ReconcileMetadata(limit int) (int, error) {
	if limit < 1 || limit > 64 {
		return 0, errors.New("reconcile Helper metadata: limit must be within 1..64")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index, err := s.loadIndex()
	if err != nil {
		return 0, err
	}
	installed := make(map[string]struct{}, len(index.Records))
	removable := make(map[string]struct{}, len(index.Records))
	for _, record := range index.Records {
		if record.Installed {
			installed[record.MetadataID] = struct{}{}
		} else {
			removable[record.MetadataID] = struct{}{}
		}
	}
	metadataIDs := make([]string, 0, len(removable))
	for metadataID := range removable {
		if _, keep := installed[metadataID]; !keep {
			metadataIDs = append(metadataIDs, metadataID)
		}
	}
	sort.Strings(metadataIDs)
	removed := 0
	for _, metadataID := range metadataIDs {
		path := s.metadataPath(metadataID)
		if err := platform.ValidatePrivateFile(path, platform.ValidatePersistent); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return removed, fmt.Errorf("reconcile Helper metadata: validate orphan: %w", err)
		}
		if err := os.Remove(path); err != nil {
			return removed, fmt.Errorf("reconcile Helper metadata: remove orphan: %w", err)
		}
		removed++
		if removed == limit {
			break
		}
	}
	if removed > 0 {
		if err := syncHelperStateDirectory(s.root); err != nil {
			return removed, err
		}
	}
	return removed, nil
}

func (s *StateStore) loadIndexOrEmpty() (stateIndex, error) {
	index, err := s.loadIndex()
	if errors.Is(err, os.ErrNotExist) {
		return stateIndex{Schema: HelperStateSchemaVersion, Records: []stateIndexRecord{}}, nil
	}
	return index, err
}

func (s *StateStore) loadIndex() (stateIndex, error) {
	path := s.indexPath()
	if err := platform.ValidatePrivateFile(path, platform.ValidatePersistent); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return stateIndex{}, os.ErrNotExist
		}
		return stateIndex{}, fmt.Errorf("load helper state index: %w", err)
	}
	var index stateIndex
	if err := decodeBoundedStateFile(path, &index); err != nil {
		return stateIndex{}, fmt.Errorf("load helper state index: %w", err)
	}
	if index.Schema != 1 && index.Schema != HelperStateSchemaVersion || len(index.Records) > 4096 {
		return stateIndex{}, errors.New("load helper state index: schema or record count is invalid")
	}
	index.sourceSchema = index.Schema
	if index.Schema == 1 {
		if index.RemovalClaim != nil {
			return stateIndex{}, errors.New("load helper state index: v1 contains a removal claim")
		}
		index.Schema = HelperStateSchemaVersion
		for position := range index.Records {
			index.Records[position].Installed = true
		}
	}
	enabledScopes := make(map[string]struct{}, len(index.Records))
	exactRecords := make(map[string]struct{}, len(index.Records))
	versionHashes := make(map[string]string, len(index.Records))
	for position, record := range index.Records {
		if err := validateStateRecord(record); err != nil {
			return stateIndex{}, fmt.Errorf("load helper state index: record %d: %w", position, err)
		}
		exactKey := fmt.Sprintf("%s/%s/%s", stateScopeKey(record), record.Version, record.SHA256)
		if _, duplicate := exactRecords[exactKey]; duplicate {
			return stateIndex{}, errors.New("load helper state index: duplicate exact artifact record")
		}
		exactRecords[exactKey] = struct{}{}
		versionKey := fmt.Sprintf("%s/%s", stateScopeKey(record), record.Version)
		if hash, exists := versionHashes[versionKey]; exists && hash != record.SHA256 {
			return stateIndex{}, errors.New("load helper state index: same-version republish records conflict")
		}
		versionHashes[versionKey] = record.SHA256
		if record.Enabled {
			if !record.Installed {
				return stateIndex{}, errors.New("load helper state index: enabled record is not installed")
			}
			scope := stateScopeKey(record)
			if _, duplicate := enabledScopes[scope]; duplicate {
				return stateIndex{}, errors.New("load helper state index: multiple enabled records share one scope")
			}
			enabledScopes[scope] = struct{}{}
		}
	}
	if index.RemovalClaim != nil {
		position, exists := findExactStateRecord(index.Records, index.RemovalClaim.EndpointID, index.RemovalClaim.ArtifactID)
		if !exists || !index.Records[position].Installed || index.Records[position].Enabled || index.Records[position].FinalPath != index.RemovalClaim.FinalPath {
			return stateIndex{}, errors.New("load helper state index: removal claim is invalid")
		}
	}
	return index, nil
}

func (s *StateStore) loadMetadata(metadataID string) (metadataDocument, error) {
	if len(metadataID) != 64 || !isLowerHex(metadataID) {
		return metadataDocument{}, errors.New("load helper metadata: ID is invalid")
	}
	path := s.metadataPath(metadataID)
	if err := platform.ValidatePrivateFile(path, platform.ValidatePersistent); err != nil {
		return metadataDocument{}, err
	}
	var document metadataDocument
	if err := decodeBoundedStateFile(path, &document); err != nil {
		return metadataDocument{}, err
	}
	if document.Schema != 1 {
		return metadataDocument{}, errors.New("load helper metadata: schema is invalid")
	}
	return document, nil
}

func decodeBoundedStateFile(path string, destination any) error {
	file, err := os.Open(path) // #nosec G304 -- callers provide a validated path beneath the private store root.
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() <= 0 || info.Size() > maxHelperStateBytes {
		return errors.New("helper state file size is invalid")
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxHelperStateBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("helper state file contains trailing data")
	}
	return nil
}

func (s *StateStore) writeMetadataNoReplace(metadataID string, raw []byte, expected metadataDocument) error {
	finalPath := s.metadataPath(metadataID)
	if _, err := os.Lstat(finalPath); err == nil {
		existing, loadErr := s.loadMetadata(metadataID)
		if loadErr != nil || existing.EndpointID != expected.EndpointID || !bytes.Equal(existing.RawManifest, expected.RawManifest) || !bytes.Equal(existing.RawSignature, expected.RawSignature) {
			return errors.New("stage helper metadata: existing content-addressed file is invalid")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return fmt.Errorf("stage helper metadata: list state root: %w", err)
	}
	metadataFiles := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "metadata-") && strings.HasSuffix(entry.Name(), ".json") {
			metadataFiles++
		}
	}
	if metadataFiles >= maxHelperMetadataFiles {
		return errors.New("stage helper metadata: persistent metadata file limit reached")
	}
	temporary, err := s.writeTemporary(".helper-metadata-*.tmp", raw)
	if err != nil {
		return err
	}
	defer os.Remove(temporary)
	if err := os.Link(temporary, finalPath); err != nil {
		if _, statErr := os.Lstat(finalPath); statErr == nil {
			existing, loadErr := s.loadMetadata(metadataID)
			if loadErr == nil && existing.EndpointID == expected.EndpointID && bytes.Equal(existing.RawManifest, expected.RawManifest) && bytes.Equal(existing.RawSignature, expected.RawSignature) {
				return nil
			}
		}
		return fmt.Errorf("stage helper metadata: no-replace publish: %w", err)
	}
	if err := platform.ValidatePrivateFile(finalPath, platform.ValidatePersistent); err != nil {
		return err
	}
	return syncHelperStateDirectory(s.root)
}

func (s *StateStore) writeIndex(index stateIndex) error {
	raw, err := json.Marshal(index)
	if err != nil {
		return err
	}
	if int64(len(raw)) > maxHelperStateBytes {
		return errors.New("write helper state index: document exceeds hard limit")
	}
	finalPath := s.indexPath()
	if _, err := os.Lstat(finalPath); err == nil {
		if err := platform.ValidatePrivateFile(finalPath, platform.ValidatePersistent); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary, err := s.writeTemporary(".helper-index-*.tmp", raw)
	if err != nil {
		return err
	}
	defer os.Remove(temporary)
	if s.beforeIndexRename != nil {
		if err := s.beforeIndexRename(); err != nil {
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

func (s *StateStore) writeTemporary(pattern string, raw []byte) (string, error) {
	file, err := os.CreateTemp(s.root, pattern)
	if err != nil {
		return "", err
	}
	path := file.Name()
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if err := file.Chmod(0o600); err != nil { // #nosec G302 -- owner-private state requires exact 0600.
		return "", err
	}
	if _, err := file.Write(raw); err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := platform.ValidatePrivateFile(path, platform.ValidatePersistent); err != nil {
		return "", err
	}
	ok = true
	return path, nil
}

func helperMetadataID(endpointID domain.EndpointID, rawManifest, rawSignature []byte) string {
	hash := sha256.New()
	writeDigestField(hash, string(endpointID))
	writeDigestField(hash, string(rawManifest))
	writeDigestField(hash, string(rawSignature))
	return hex.EncodeToString(hash.Sum(nil))
}

func sameStateScope(record stateIndexRecord, endpointID domain.EndpointID, protocol uint16, target Target) bool {
	return record.EndpointID == string(endpointID) && record.ProtocolMajor == protocol && record.OS == target.OS && record.Arch == target.Arch
}

func findEnabledStateRecord(records []stateIndexRecord, endpointID domain.EndpointID, protocol uint16, target Target) (int, bool) {
	for position, record := range records {
		if sameStateScope(record, endpointID, protocol, target) && record.Enabled {
			return position, true
		}
	}
	return 0, false
}

func findExactStateRecord(records []stateIndexRecord, endpointID domain.EndpointID, artifact ArtifactID) (int, bool) {
	for position, record := range records {
		if sameStateScope(record, endpointID, artifact.ProtocolMajor, Target{OS: artifact.OS, Arch: artifact.Arch}) && record.Version == artifact.Version && record.SHA256 == artifact.SHA256 {
			return position, true
		}
	}
	return 0, false
}

func findHighWaterRecord(records []stateIndexRecord, endpointID domain.EndpointID, protocol uint16, target Target) (int, bool) {
	position := 0
	found := false
	var highest Version
	for recordIndex, record := range records {
		if !sameStateScope(record, endpointID, protocol, target) {
			continue
		}
		version, err := parseReleaseVersion(record.Version)
		if err != nil {
			continue
		}
		if !found || version.Compare(highest) > 0 {
			position, highest, found = recordIndex, version, true
		}
	}
	return position, found
}

func sortStateRecords(records []stateIndexRecord) {
	sort.Slice(records, func(left, right int) bool {
		leftKey := fmt.Sprintf("%s/%05d/%s/%s/%s/%s", records[left].EndpointID, records[left].ProtocolMajor, records[left].OS, records[left].Arch, records[left].Version, records[left].SHA256)
		rightKey := fmt.Sprintf("%s/%05d/%s/%s/%s/%s", records[right].EndpointID, records[right].ProtocolMajor, records[right].OS, records[right].Arch, records[right].Version, records[right].SHA256)
		return leftKey < rightKey
	})
}

func stateScopeKey(record stateIndexRecord) string {
	return fmt.Sprintf("%s/%05d/%s/%s", record.EndpointID, record.ProtocolMajor, record.OS, record.Arch)
}

func validateStateRecord(record stateIndexRecord) error {
	if _, err := domain.ParseEndpointID(record.EndpointID); err != nil || record.ProtocolMajor == 0 || record.OS == "" || record.Arch == "" || len(record.SHA256) != 64 || !isLowerHex(record.SHA256) || len(record.MetadataID) != 64 || !isLowerHex(record.MetadataID) {
		return errors.New("identity or digest is invalid")
	}
	if _, err := parseReleaseVersion(record.Version); err != nil {
		return err
	}
	if record.FinalPath == "" || len(record.FinalPath) > MaxHelperRemotePathBytes || record.FinalPath[0] != '/' || filepath.ToSlash(filepath.Clean(record.FinalPath)) != record.FinalPath || strings.IndexByte(record.FinalPath, 0) >= 0 {
		return errors.New("final path is invalid")
	}
	return nil
}

func (s *StateStore) indexPath() string            { return filepath.Join(s.root, "state.json") }
func (s *StateStore) endpointRegistryPath() string { return filepath.Join(s.root, "endpoints.json") }
func (s *StateStore) metadataPath(metadataID string) string {
	return filepath.Join(s.root, "metadata-"+metadataID+".json")
}

func syncHelperStateDirectory(path string) error {
	directory, err := os.Open(path) // #nosec G304 -- path is the validated private store root.
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
