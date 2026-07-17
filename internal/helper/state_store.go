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

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/platform"
)

const (
	maxHelperStateBytes    int64 = 1 << 20
	maxHelperMetadataFiles       = 4096
)

type EnabledRecord struct {
	RawManifest  []byte
	RawSignature []byte
	FinalPath    string
	MetadataPath string
}

type StateStore struct {
	root              string
	mu                sync.Mutex
	beforeIndexRename func() error
}

type stateIndex struct {
	Schema  uint16             `json:"schema"`
	Records []stateIndexRecord `json:"records"`
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
	store := &StateStore{root: root}
	if _, err := os.Lstat(store.indexPath()); err == nil {
		if _, err := store.loadIndex(); err != nil {
			return nil, err
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
	document, err := s.loadMetadata(metadataID)
	if err != nil {
		return fmt.Errorf("commit enabled helper: staged metadata: %w", err)
	}
	if document.EndpointID != string(endpointID) || !bytes.Equal(document.RawManifest, manifest.Raw) || !bytes.Equal(document.RawSignature, rawSignature) {
		return errors.New("commit enabled helper: staged metadata does not match exact signed bytes")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	index, err := s.loadIndexOrEmpty()
	if err != nil {
		return err
	}
	position, exists := findStateRecord(index.Records, endpointID, manifest.ProtocolMajor, manifest.Target())
	if exists {
		current, parseErr := parseReleaseVersion(index.Records[position].Version)
		if parseErr != nil {
			return errors.New("commit enabled helper: stored version is invalid")
		}
		switch manifest.Version.Compare(current) {
		case -1:
			return errors.New("commit enabled helper: downgrade violates persistent high-water")
		case 0:
			if manifest.SHA256 != index.Records[position].SHA256 {
				return errors.New("commit enabled helper: same-version republish violates persistent high-water")
			}
		}
	}
	record := stateIndexRecord{
		EndpointID: string(endpointID), ProtocolMajor: manifest.ProtocolMajor, OS: manifest.OS, Arch: manifest.Arch,
		Version: manifest.Version.String(), SHA256: manifest.SHA256, MetadataID: metadataID, FinalPath: finalPath, Enabled: true,
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
	position, exists := findStateRecord(index.Records, endpointID, manifest.ProtocolMajor, manifest.Target())
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
	position, exists := findStateRecord(index.Records, endpointID, protocol, target)
	if !exists {
		return EnabledRecord{}, errors.New("load enabled helper: record not found")
	}
	record := index.Records[position]
	if !record.Enabled {
		return EnabledRecord{}, errors.New("load enabled helper: record is disabled")
	}
	document, err := s.loadMetadata(record.MetadataID)
	if err != nil {
		return EnabledRecord{}, fmt.Errorf("load enabled helper: %w", err)
	}
	manifest, err := ParseManifestV1(document.RawManifest)
	if err != nil {
		return EnabledRecord{}, err
	}
	if _, err := ParseDetachedSignature(document.RawSignature); err != nil {
		return EnabledRecord{}, err
	}
	if document.EndpointID != record.EndpointID || document.EndpointID != string(endpointID) || manifest.ProtocolMajor != record.ProtocolMajor || manifest.Target() != target || manifest.Version.String() != record.Version || manifest.SHA256 != record.SHA256 || helperMetadataID(endpointID, document.RawManifest, document.RawSignature) != record.MetadataID {
		return EnabledRecord{}, errors.New("load enabled helper: metadata does not match persistent index")
	}
	return EnabledRecord{RawManifest: append([]byte(nil), document.RawManifest...), RawSignature: append([]byte(nil), document.RawSignature...), FinalPath: record.FinalPath, MetadataPath: s.metadataPath(record.MetadataID)}, nil
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
	position, exists := findStateRecord(index.Records, endpointID, protocol, target)
	if !exists {
		return errors.New("disable helper: record not found")
	}
	if !index.Records[position].Enabled {
		return nil
	}
	index.Records[position].Enabled = false
	return s.writeIndex(index)
}

func (s *StateStore) loadIndexOrEmpty() (stateIndex, error) {
	index, err := s.loadIndex()
	if errors.Is(err, os.ErrNotExist) {
		return stateIndex{Schema: 1, Records: []stateIndexRecord{}}, nil
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
	if index.Schema != 1 || len(index.Records) > 4096 {
		return stateIndex{}, errors.New("load helper state index: schema or record count is invalid")
	}
	for position, record := range index.Records {
		if err := validateStateRecord(record); err != nil {
			return stateIndex{}, fmt.Errorf("load helper state index: record %d: %w", position, err)
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

func findStateRecord(records []stateIndexRecord, endpointID domain.EndpointID, protocol uint16, target Target) (int, bool) {
	for position, record := range records {
		if record.EndpointID == string(endpointID) && record.ProtocolMajor == protocol && record.OS == target.OS && record.Arch == target.Arch {
			return position, true
		}
	}
	return 0, false
}

func sortStateRecords(records []stateIndexRecord) {
	sort.Slice(records, func(left, right int) bool {
		leftKey := fmt.Sprintf("%s/%05d/%s/%s", records[left].EndpointID, records[left].ProtocolMajor, records[left].OS, records[left].Arch)
		rightKey := fmt.Sprintf("%s/%05d/%s/%s", records[right].EndpointID, records[right].ProtocolMajor, records[right].OS, records[right].Arch)
		return leftKey < rightKey
	})
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

func (s *StateStore) indexPath() string { return filepath.Join(s.root, "state.json") }
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
