package workspace

import (
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
	if s.beforeRename != nil {
		if err := s.beforeRename(); err != nil {
			return fmt.Errorf("save workspace %q before replace: %w", name, err)
		}
	}
	finalPath := s.path(name)
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
	if err := ValidateName(name); err != nil {
		return Document{}, err
	}
	workspacePath := s.path(name)
	if err := platform.ValidatePrivateFile(workspacePath, platform.ValidatePersistent); err != nil {
		return Document{}, fmt.Errorf("load workspace %q: %w", name, err)
	}
	file, err := os.Open(workspacePath)
	if err != nil {
		return Document{}, fmt.Errorf("load workspace %q: %w", name, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Document{}, fmt.Errorf("load workspace %q: inspect file: %w", name, err)
	}
	if info.Size() > maximumDocumentBytes {
		return Document{}, fmt.Errorf("load workspace %q: file exceeds %d bytes", name, maximumDocumentBytes)
	}
	document, err := Decode(io.LimitReader(file, maximumDocumentBytes+1))
	if err != nil {
		return Document{}, fmt.Errorf("load workspace %q: %w", name, err)
	}
	return document, nil
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
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
