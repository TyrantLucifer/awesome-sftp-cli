package cachefs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cache"
)

type FindingKind string

const (
	FindingCrashTemp             FindingKind = "crash_temp"
	FindingCorruptManifest       FindingKind = "corrupt_manifest"
	FindingOrphanBlob            FindingKind = "orphan_blob"
	FindingOrphanMaterialization FindingKind = "orphan_materialization"
	FindingReservedQuarantine    FindingKind = "reserved_quarantine"
	FindingSymlink               FindingKind = "symlink"
	FindingUnknown               FindingKind = "unknown"
)

type Finding struct {
	Kind         FindingKind
	RelativePath string
	Reason       string
}

type ScanReport struct {
	VerifiedBlobs            []BlobInfo
	VerifiedEntries          []EntryManifestInfo
	VerifiedMaterializations []MaterializationManifestInfo
	Orphans                  []Finding
	Unknown                  []Finding
	Symlinks                 []Finding
	Visited                  int
	Truncated                bool
}

// Scan performs a bounded, no-follow restart inventory. It never removes,
// renames, chmods, repairs, or moves a discovered object.
func (store *Store) Scan(maxEntries int) (ScanReport, error) {
	if store == nil {
		return ScanReport{}, fmt.Errorf("scan cache filesystem: nil store")
	}
	if maxEntries <= 0 {
		return ScanReport{}, fmt.Errorf("scan cache filesystem: max entries must be positive")
	}
	if err := validatePrivateDirectory(store.root); err != nil {
		return ScanReport{}, fmt.Errorf("scan cache filesystem root: %w", err)
	}

	scanner := cacheScanner{store: store, maxEntries: maxEntries}
	if err := scanner.scanRoot(); err != nil {
		return scanner.report, err
	}
	return scanner.report, nil
}

type cacheScanner struct {
	store      *Store
	maxEntries int
	report     ScanReport
}

func (scanner *cacheScanner) scanRoot() error {
	entries, err := os.ReadDir(scanner.store.root)
	if err != nil {
		return fmt.Errorf("scan cache root: %w", err)
	}
	known := map[string]func() error{
		"blobs":            scanner.scanBlobs,
		"entries":          scanner.scanEntries,
		"materializations": scanner.scanMaterializations,
		"quarantine":       func() error { return scanner.scanUnknownDirectory("quarantine", FindingReservedQuarantine) },
		"staging":          scanner.scanStaging,
	}
	for _, entry := range entries {
		handler, exists := known[entry.Name()]
		if !exists {
			if !scanner.visit() {
				return nil
			}
			scanner.classifyUnexpected(entry, entry.Name(), FindingUnknown)
			continue
		}
		path := filepath.Join(scanner.store.root, entry.Name())
		if entry.Type()&os.ModeSymlink != 0 {
			if !scanner.visit() {
				return nil
			}
			scanner.addSymlink(entry.Name())
			continue
		}
		metadata, statErr := os.Lstat(path) //nolint:gosec // child of exact private cache root
		if statErr != nil || validatePrivateDirectoryMetadata(path, metadata) != nil {
			if !scanner.visit() {
				return nil
			}
			reason := "expected private directory"
			if statErr != nil {
				reason = statErr.Error()
			}
			scanner.report.Unknown = append(scanner.report.Unknown, Finding{Kind: FindingUnknown, RelativePath: slash(entry.Name()), Reason: reason})
			continue
		}
		if trustErr := validatePrivateDirectory(path); trustErr != nil {
			if !scanner.visit() {
				return nil
			}
			scanner.report.Unknown = append(scanner.report.Unknown, Finding{Kind: FindingUnknown, RelativePath: slash(entry.Name()), Reason: trustErr.Error()})
			continue
		}
		if err := handler(); err != nil {
			return err
		}
		if scanner.report.Truncated {
			return nil
		}
	}
	return nil
}

func (scanner *cacheScanner) scanEntries() error {
	root := filepath.Join(scanner.store.root, "entries")
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("scan cache entries: %w", err)
	}
	for _, entry := range entries {
		if !scanner.visit() {
			return nil
		}
		relative := filepath.Join("entries", entry.Name())
		entryPath := filepath.Join(root, entry.Name())
		if entry.Type()&os.ModeSymlink != 0 {
			scanner.addSymlink(relative)
			continue
		}
		id, parseErr := cache.ParseEntryID(entry.Name())
		metadata, statErr := os.Lstat(entryPath) //nolint:gosec // bounded child under exact entries root
		if parseErr != nil || statErr != nil || validatePrivateDirectoryMetadata(entryPath, metadata) != nil {
			reason := "unexpected entry basename or attributes"
			if statErr != nil {
				reason = statErr.Error()
			}
			scanner.report.Unknown = append(scanner.report.Unknown, Finding{Kind: FindingUnknown, RelativePath: slash(relative), Reason: reason})
			continue
		}
		if trustErr := validatePrivateDirectory(entryPath); trustErr != nil {
			scanner.report.Orphans = append(scanner.report.Orphans, Finding{Kind: FindingCorruptManifest, RelativePath: slash(relative), Reason: trustErr.Error()})
			continue
		}
		children, readErr := os.ReadDir(entryPath)
		if readErr != nil {
			scanner.report.Orphans = append(scanner.report.Orphans, Finding{Kind: FindingCorruptManifest, RelativePath: slash(relative), Reason: readErr.Error()})
			continue
		}
		for _, child := range children {
			if !scanner.visit() {
				return nil
			}
			childRelative := filepath.Join(relative, child.Name())
			if child.Type()&os.ModeSymlink != 0 {
				scanner.addSymlink(childRelative)
				continue
			}
			if child.Name() != ManifestFilename {
				scanner.report.Unknown = append(scanner.report.Unknown, Finding{Kind: FindingUnknown, RelativePath: slash(childRelative), Reason: "unknown cache entry file preserved"})
			}
		}
		info, inspectErr := scanner.store.ReadEntryManifest(id)
		if inspectErr != nil {
			scanner.report.Orphans = append(scanner.report.Orphans, Finding{Kind: FindingCorruptManifest, RelativePath: slash(filepath.Join(relative, ManifestFilename)), Reason: inspectErr.Error()})
			continue
		}
		scanner.report.VerifiedEntries = append(scanner.report.VerifiedEntries, info)
	}
	return nil
}

func (scanner *cacheScanner) scanBlobs() error {
	shaRoot := filepath.Join(scanner.store.root, "blobs", "sha256")
	if err := scanner.requireStructuralDirectory(filepath.Join(scanner.store.root, "blobs"), "blobs"); err != nil {
		return err
	}
	if err := scanner.requireStructuralDirectory(shaRoot, "blobs/sha256"); err != nil {
		return err
	}
	shards, err := os.ReadDir(shaRoot)
	if err != nil {
		return fmt.Errorf("scan cache blob shards: %w", err)
	}
	for _, shard := range shards {
		if !scanner.visit() {
			return nil
		}
		relative := filepath.Join("blobs", "sha256", shard.Name())
		path := filepath.Join(shaRoot, shard.Name())
		if shard.Type()&os.ModeSymlink != 0 {
			scanner.addSymlink(relative)
			continue
		}
		metadata, statErr := os.Lstat(path) //nolint:gosec // bounded child under exact blob root
		if statErr != nil || !isLowerHex(shard.Name(), 2) || validatePrivateDirectoryMetadata(path, metadata) != nil {
			reason := "unexpected shard name or attributes"
			if statErr != nil {
				reason = statErr.Error()
			}
			scanner.report.Unknown = append(scanner.report.Unknown, Finding{Kind: FindingUnknown, RelativePath: slash(relative), Reason: reason})
			continue
		}
		if trustErr := validatePrivateDirectory(path); trustErr != nil {
			scanner.report.Unknown = append(scanner.report.Unknown, Finding{Kind: FindingUnknown, RelativePath: slash(relative), Reason: trustErr.Error()})
			continue
		}
		children, err := os.ReadDir(path)
		if err != nil {
			return fmt.Errorf("scan cache blob shard %q: %w", shard.Name(), err)
		}
		for _, child := range children {
			if !scanner.visit() {
				return nil
			}
			childRelative := filepath.Join(relative, child.Name())
			if child.Type()&os.ModeSymlink != 0 {
				scanner.addSymlink(childRelative)
				continue
			}
			idText := strings.TrimSuffix(child.Name(), ".blob")
			id, parseErr := cache.ParseBlobID(idText)
			if parseErr != nil || child.Name() != idText+".blob" || !strings.HasPrefix(idText, shard.Name()) {
				scanner.report.Unknown = append(scanner.report.Unknown, Finding{Kind: FindingUnknown, RelativePath: slash(childRelative), Reason: "unexpected blob basename"})
				continue
			}
			info, inspectErr := scanner.store.InspectBlob(id)
			if inspectErr != nil {
				scanner.report.Orphans = append(scanner.report.Orphans, Finding{Kind: FindingOrphanBlob, RelativePath: slash(childRelative), Reason: inspectErr.Error()})
				continue
			}
			scanner.report.VerifiedBlobs = append(scanner.report.VerifiedBlobs, info)
		}
	}
	return nil
}

func (scanner *cacheScanner) scanMaterializations() error {
	root := filepath.Join(scanner.store.root, "materializations")
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("scan cache materializations: %w", err)
	}
	for _, entry := range entries {
		if !scanner.visit() {
			return nil
		}
		relative := filepath.Join("materializations", entry.Name())
		path := filepath.Join(root, entry.Name())
		if entry.Type()&os.ModeSymlink != 0 {
			scanner.addSymlink(relative)
			continue
		}
		id, parseErr := cache.ParseMaterializationID(entry.Name())
		metadata, statErr := os.Lstat(path) //nolint:gosec // bounded child under exact materialization root
		if parseErr != nil || statErr != nil || validatePrivateDirectoryMetadata(path, metadata) != nil {
			reason := "unexpected materialization basename or attributes"
			if statErr != nil {
				reason = statErr.Error()
			}
			scanner.report.Unknown = append(scanner.report.Unknown, Finding{Kind: FindingUnknown, RelativePath: slash(relative), Reason: reason})
			continue
		}
		if trustErr := validatePrivateDirectory(path); trustErr != nil {
			scanner.report.Orphans = append(scanner.report.Orphans, Finding{Kind: FindingOrphanMaterialization, RelativePath: slash(relative), Reason: trustErr.Error()})
			continue
		}
		children, readErr := os.ReadDir(path)
		if readErr != nil {
			scanner.report.Orphans = append(scanner.report.Orphans, Finding{Kind: FindingOrphanMaterialization, RelativePath: slash(relative), Reason: readErr.Error()})
			continue
		}
		for _, child := range children {
			if !scanner.visit() {
				return nil
			}
			childRelative := filepath.Join(relative, child.Name())
			if child.Type()&os.ModeSymlink != 0 {
				scanner.addSymlink(childRelative)
				continue
			}
			if child.Name() != "content" && child.Name() != "manifest-v1.json" {
				scanner.report.Unknown = append(scanner.report.Unknown, Finding{Kind: FindingUnknown, RelativePath: slash(childRelative), Reason: "unknown materialization entry preserved"})
			}
		}
		manifest, inspectErr := scanner.store.ReadMaterializationManifest(id)
		if inspectErr != nil {
			scanner.report.Orphans = append(scanner.report.Orphans, Finding{Kind: FindingOrphanMaterialization, RelativePath: slash(relative), Reason: inspectErr.Error()})
			continue
		}
		info, inspectErr := scanner.store.InspectMaterialization(manifest.MaterializationID)
		if inspectErr != nil {
			scanner.report.Orphans = append(scanner.report.Orphans, Finding{Kind: FindingOrphanMaterialization, RelativePath: slash(relative), Reason: inspectErr.Error()})
			continue
		}
		scanner.report.VerifiedMaterializations = append(scanner.report.VerifiedMaterializations, MaterializationManifestInfo{Manifest: manifest, Info: info})
	}
	return nil
}

func (scanner *cacheScanner) scanStaging() error {
	root := filepath.Join(scanner.store.root, "staging")
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("scan cache staging: %w", err)
	}
	for _, entry := range entries {
		if !scanner.visit() {
			return nil
		}
		relative := filepath.Join("staging", entry.Name())
		if entry.Type()&os.ModeSymlink != 0 {
			scanner.addSymlink(relative)
			continue
		}
		if isCrashTempName(entry.Name()) {
			scanner.report.Orphans = append(scanner.report.Orphans, Finding{Kind: FindingCrashTemp, RelativePath: slash(relative), Reason: "incomplete publication temp preserved"})
			continue
		}
		scanner.report.Unknown = append(scanner.report.Unknown, Finding{Kind: FindingUnknown, RelativePath: slash(relative), Reason: "unknown staging entry preserved"})
	}
	return nil
}

func (scanner *cacheScanner) scanUnknownDirectory(name string, kind FindingKind) error {
	root := filepath.Join(scanner.store.root, name)
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("scan cache %s: %w", name, err)
	}
	for _, entry := range entries {
		if !scanner.visit() {
			return nil
		}
		relative := filepath.Join(name, entry.Name())
		if entry.Type()&os.ModeSymlink != 0 {
			scanner.addSymlink(relative)
			continue
		}
		scanner.report.Unknown = append(scanner.report.Unknown, Finding{Kind: kind, RelativePath: slash(relative), Reason: "entry preserved for catalog reconciliation"})
	}
	return nil
}

func (scanner *cacheScanner) requireStructuralDirectory(path, relative string) error {
	metadata, err := os.Lstat(path) //nolint:gosec // exact frozen layout path
	if err != nil {
		return fmt.Errorf("scan cache structural directory %q: %w", relative, err)
	}
	if err := validatePrivateDirectoryMetadata(path, metadata); err != nil {
		return err
	}
	return validatePrivateDirectory(path)
}

func (scanner *cacheScanner) visit() bool {
	if scanner.report.Visited >= scanner.maxEntries {
		scanner.report.Truncated = true
		return false
	}
	scanner.report.Visited++
	return true
}

func (scanner *cacheScanner) addSymlink(relative string) {
	scanner.report.Symlinks = append(scanner.report.Symlinks, Finding{Kind: FindingSymlink, RelativePath: slash(relative), Reason: "symlink rejected and preserved"})
}

func (scanner *cacheScanner) classifyUnexpected(entry os.DirEntry, relative string, kind FindingKind) {
	if entry.Type()&os.ModeSymlink != 0 {
		scanner.addSymlink(relative)
		return
	}
	scanner.report.Unknown = append(scanner.report.Unknown, Finding{Kind: kind, RelativePath: slash(relative), Reason: "unknown cache-root entry preserved"})
}

func isCrashTempName(name string) bool {
	const prefix = ".amsftp-cache-"
	const suffix = ".tmp"
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return false
	}
	middle := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
	separator := strings.LastIndexByte(middle, '-')
	if separator <= 0 {
		return false
	}
	kind, randomID := middle[:separator], middle[separator+1:]
	switch kind {
	case "blob", "entry", "manifest", "materialization":
	default:
		return false
	}
	return isLowerHex(randomID, 32)
}

func isLowerHex(value string, width int) bool {
	if len(value) != width {
		return false
	}
	for index := range value {
		character := value[index]
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func slash(path string) string {
	return filepath.ToSlash(path)
}
