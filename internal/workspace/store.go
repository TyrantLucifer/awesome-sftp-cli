package workspace

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/platform"
)

const maximumDocumentBytes int64 = 256 * 1024

type Summary struct {
	Name      string
	UpdatedAt time.Time
	Problem   string
}

type Store struct {
	root         string
	beforeRename func() error
	now          func() time.Time
}

type storedDocument struct {
	document      Document
	sourceVersion int
	raw           []byte
}

func NewStore(root string) (*Store, error) {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root || strings.IndexByte(root, 0) >= 0 {
		return nil, errors.New("create workspace store: root must be canonical absolute")
	}
	if err := platform.PreparePrivateDirectory(root, platform.ValidatePersistent); err != nil {
		return nil, fmt.Errorf("create workspace store: %w", err)
	}
	return &Store{root: root, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (s *Store) Save(name string, document Document) (resultErr error) {
	if err := ValidateName(name); err != nil {
		return err
	}
	document.UpdatedAt = s.now().UTC()
	if err := document.Validate(); err != nil {
		return fmt.Errorf("save workspace %q: %w", name, err)
	}
	finalPath := s.path(name)
	var existing *storedDocument
	if _, err := os.Lstat(finalPath); err == nil {
		loaded, loadErr := s.loadStored(name)
		if loadErr != nil {
			return fmt.Errorf("save workspace %q: preserve invalid existing document: %w", name, loadErr)
		}
		existing = &loaded
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("save workspace %q: inspect existing document: %w", name, err)
	}
	temporary, err := os.CreateTemp(s.root, ".workspace-*.tmp")
	if err != nil {
		return fmt.Errorf("save workspace %q: create temporary file: %w", name, err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		if resultErr != nil {
			_ = os.Remove(temporaryPath)
		}
	}()
	// #nosec G302 -- workspace files are owner-private state with exact mode 0600.
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("save workspace %q: set temporary mode: %w", name, err)
	}
	if err := Encode(temporary, document); err != nil {
		return fmt.Errorf("save workspace %q: %w", name, err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("save workspace %q: sync temporary file: %w", name, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("save workspace %q: close temporary file: %w", name, err)
	}
	if err := platform.ValidatePrivateFile(temporaryPath, platform.ValidatePersistent); err != nil {
		return fmt.Errorf("save workspace %q: validate temporary file: %w", name, err)
	}
	if existing != nil && existing.sourceVersion == legacySchemaVersion {
		if err := s.preserveLegacyBackup(name, existing.raw); err != nil {
			return fmt.Errorf("save workspace %q: %w", name, err)
		}
	}
	if s.beforeRename != nil {
		if err := s.beforeRename(); err != nil {
			return fmt.Errorf("save workspace %q before replace: %w", name, err)
		}
	}
	if existing != nil {
		current, err := s.loadStored(name)
		if err != nil {
			return fmt.Errorf("save workspace %q: revalidate existing document: %w", name, err)
		}
		if !bytes.Equal(current.raw, existing.raw) {
			return fmt.Errorf("save workspace %q: existing document changed before replace", name)
		}
	}
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		return fmt.Errorf("save workspace %q: replace final file: %w", name, err)
	}
	if err := platform.ValidatePrivateFile(finalPath, platform.ValidatePersistent); err != nil {
		return fmt.Errorf("save workspace %q: validate final file: %w", name, err)
	}
	if err := syncDirectory(s.root); err != nil {
		return fmt.Errorf("save workspace %q: sync directory: %w", name, err)
	}
	return nil
}

func (s *Store) Load(name string) (Document, error) {
	stored, err := s.loadStored(name)
	if err != nil {
		return Document{}, err
	}
	return stored.document, nil
}

func (s *Store) loadStored(name string) (storedDocument, error) {
	if err := ValidateName(name); err != nil {
		return storedDocument{}, err
	}
	workspacePath := s.path(name)
	raw, err := readPrivateWorkspaceFile(workspacePath)
	if err != nil {
		return storedDocument{}, fmt.Errorf("load workspace %q: %w", name, err)
	}
	document, err := Decode(bytes.NewReader(raw))
	if err != nil {
		return storedDocument{}, fmt.Errorf("load workspace %q: %w", name, err)
	}
	sourceVersion, err := decodeSchemaVersion(raw)
	if err != nil {
		return storedDocument{}, fmt.Errorf("load workspace %q: %w", name, err)
	}
	return storedDocument{document: document, sourceVersion: sourceVersion, raw: raw}, nil
}

func readPrivateWorkspaceFile(path string) ([]byte, error) {
	if err := platform.ValidatePrivateFile(path, platform.ValidatePersistent); err != nil {
		return nil, err
	}
	// #nosec G304 -- callers pass only store-derived paths beneath an owner-private root.
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect file: %w", err)
	}
	if info.Size() > maximumDocumentBytes {
		return nil, fmt.Errorf("file exceeds %d bytes", maximumDocumentBytes)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximumDocumentBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	if int64(len(raw)) > maximumDocumentBytes {
		return nil, fmt.Errorf("file exceeds %d bytes", maximumDocumentBytes)
	}
	return raw, nil
}

func (s *Store) List() ([]Summary, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	summaries := make([]Summary, 0, len(entries))
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		if ValidateName(name) != nil {
			continue
		}
		document, loadErr := s.Load(name)
		summary := Summary{Name: name}
		if loadErr != nil {
			summary.Problem = loadErr.Error()
		} else {
			summary.UpdatedAt = document.UpdatedAt
		}
		summaries = append(summaries, summary)
	}
	sort.Slice(summaries, func(left, right int) bool {
		if (summaries[left].Problem == "") != (summaries[right].Problem == "") {
			return summaries[left].Problem == ""
		}
		if !summaries[left].UpdatedAt.Equal(summaries[right].UpdatedAt) {
			return summaries[left].UpdatedAt.After(summaries[right].UpdatedAt)
		}
		return summaries[left].Name < summaries[right].Name
	})
	return summaries, nil
}

func (s *Store) path(name string) string {
	return filepath.Join(s.root, name+".json")
}

func (s *Store) legacyBackupPath(name string) string {
	return filepath.Join(s.root, name+".schema-v1.backup")
}

func (s *Store) preserveLegacyBackup(name string, source []byte) error {
	backupPath := s.legacyBackupPath(name)
	if _, err := os.Lstat(backupPath); err == nil {
		return s.validateLegacyBackup(backupPath, source)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect migration backup: %w", err)
	}
	current, err := readPrivateWorkspaceFile(s.path(name))
	if err != nil {
		return fmt.Errorf("revalidate migration source: %w", err)
	}
	if !bytes.Equal(current, source) {
		return errors.New("existing document changed before migration backup")
	}
	temporary, err := os.CreateTemp(s.root, ".workspace-migration-backup-*.tmp")
	if err != nil {
		return fmt.Errorf("create migration backup temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		if temporaryPath != "" {
			_ = os.Remove(temporaryPath)
		}
	}()
	// #nosec G302 -- migration backups are owner-private state with exact mode 0600.
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("set migration backup temporary mode: %w", err)
	}
	written, err := io.Copy(temporary, bytes.NewReader(source))
	if err != nil {
		return fmt.Errorf("write migration backup temporary file: %w", err)
	}
	if written != int64(len(source)) {
		return fmt.Errorf("write migration backup temporary file: wrote %d bytes, want %d", written, len(source))
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync migration backup temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close migration backup temporary file: %w", err)
	}
	if err := platform.ValidatePrivateFile(temporaryPath, platform.ValidatePersistent); err != nil {
		return fmt.Errorf("validate migration backup temporary file: %w", err)
	}
	if err := os.Link(temporaryPath, backupPath); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("publish migration backup: %w", err)
	}
	if err := s.validateLegacyBackup(backupPath, source); err != nil {
		return err
	}
	if err := os.Remove(temporaryPath); err != nil {
		return fmt.Errorf("remove migration backup temporary file: %w", err)
	}
	temporaryPath = ""
	if err := syncDirectory(s.root); err != nil {
		return fmt.Errorf("sync migration backup: %w", err)
	}
	return nil
}

func (s *Store) validateLegacyBackup(backupPath string, source []byte) error {
	preserved, err := readPrivateWorkspaceFile(backupPath)
	if err != nil {
		return fmt.Errorf("validate migration backup: %w", err)
	}
	if !bytes.Equal(preserved, source) {
		return errors.New("migration backup conflicts with existing preserved bytes")
	}
	if err := syncDirectory(s.root); err != nil {
		return fmt.Errorf("sync migration backup: %w", err)
	}
	return nil
}

func ValidateName(name string) error {
	if len(name) == 0 || len(name) > 64 || !isASCIIAlphaNumeric(rune(name[0])) {
		return errors.New("workspace name must start with an ASCII letter or digit and contain at most 64 bytes")
	}
	for _, value := range name {
		if !isASCIIAlphaNumeric(value) && value != '-' && value != '_' && value != '.' {
			return errors.New("workspace name contains an unsupported character")
		}
	}
	return nil
}

func isASCIIAlphaNumeric(value rune) bool {
	return value <= unicode.MaxASCII && (value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9')
}

func syncDirectory(path string) error {
	// #nosec G304 -- callers pass only the store-owned root directory.
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
