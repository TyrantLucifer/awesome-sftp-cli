package releasepack

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxNoticeFilesPerModule = 32
	maxNoticeFileBytes      = 1 << 20
	maxThirdPartyNoticeSize = 8 << 20
)

// ModuleNotice binds reviewed redistribution text to both the declared module
// identity and the exact resolved module identity used by a release binary.
type ModuleNotice struct {
	Path            string
	Version         string
	ResolvedPath    string
	ResolvedVersion string
	License         string
	Files           []NoticeFile
}

// NoticeFile is one reviewed, digest-bound license or notice source.
type NoticeFile struct {
	Name   string
	SHA256 string
	Bytes  []byte
}

// BuildThirdPartyNotice renders one deterministic redistribution notice for
// the complete declared runtime module inventory. It does not choose or
// substitute the project's own license.
func BuildThirdPartyNotice(modules []Module, materials []ModuleNotice) ([]byte, error) {
	if len(modules) == 0 || len(materials) != len(modules) {
		return nil, errors.New("build third-party notice: material inventory must cover every runtime module exactly once")
	}

	declared := make(map[string]Module, len(modules))
	for _, module := range modules {
		key := noticeModuleKey(module.Path, module.Version)
		if !validDeclaredModule(module) || key == "" {
			return nil, errors.New("build third-party notice: runtime module identity is invalid")
		}
		if _, duplicate := declared[key]; duplicate {
			return nil, errors.New("build third-party notice: duplicate runtime module")
		}
		declared[key] = module
	}

	validated := make([]ModuleNotice, 0, len(materials))
	seen := make(map[string]struct{}, len(materials))
	for _, material := range materials {
		key := noticeModuleKey(material.Path, material.Version)
		module, exists := declared[key]
		if !exists {
			return nil, errors.New("build third-party notice: material references an undeclared runtime module")
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, errors.New("build third-party notice: duplicate module material")
		}
		seen[key] = struct{}{}
		if err := validateModuleNotice(module, material); err != nil {
			return nil, err
		}
		copyValue := material
		copyValue.Files = append([]NoticeFile(nil), material.Files...)
		sort.Slice(copyValue.Files, func(left, right int) bool { return copyValue.Files[left].Name < copyValue.Files[right].Name })
		validated = append(validated, copyValue)
	}
	if len(seen) != len(declared) {
		return nil, errors.New("build third-party notice: runtime module material is missing")
	}

	sort.Slice(validated, func(left, right int) bool {
		return noticeModuleKey(validated[left].Path, validated[left].Version) < noticeModuleKey(validated[right].Path, validated[right].Version)
	})
	var output bytes.Buffer
	output.WriteString("AMSFTP THIRD-PARTY SOFTWARE NOTICES\n")
	output.WriteString("\n")
	output.WriteString("This file records license and redistribution texts for the exact runtime module inventory.\n")
	output.WriteString("It does not replace or select the AMSFTP project license.\n")
	for _, material := range validated {
		output.WriteString("\n================================================================================\n")
		fmt.Fprintf(&output, "Declared module: %s@%s\n", material.Path, material.Version)
		if material.ResolvedPath != material.Path || material.ResolvedVersion != material.Version {
			fmt.Fprintf(&output, "Resolved module: %s@%s\n", material.ResolvedPath, material.ResolvedVersion)
		}
		fmt.Fprintf(&output, "SPDX license expression: %s\n", material.License)
		for _, file := range material.Files {
			output.WriteString("\n")
			fmt.Fprintf(&output, "License source: %s\n", file.Name)
			fmt.Fprintf(&output, "License SHA-256: %s\n", file.SHA256)
			output.WriteString("----- BEGIN REVIEWED LICENSE TEXT -----\n")
			output.Write(file.Bytes)
			output.WriteString("----- END REVIEWED LICENSE TEXT -----\n")
		}
		if output.Len() > maxThirdPartyNoticeSize {
			return nil, errors.New("build third-party notice: output exceeds the hard size limit")
		}
	}
	return output.Bytes(), nil
}

func validateModuleNotice(module Module, material ModuleNotice) error {
	resolvedPath, resolvedVersion := module.Path, module.Version
	if module.Replacement != nil {
		resolvedPath, resolvedVersion = module.Replacement.Path, module.Replacement.Version
	}
	if material.Path != module.Path || material.Version != module.Version ||
		material.ResolvedPath != resolvedPath || material.ResolvedVersion != resolvedVersion || material.License != module.License {
		return errors.New("build third-party notice: material identity does not match the declared and resolved module")
	}
	if len(material.Files) == 0 || len(material.Files) > maxNoticeFilesPerModule {
		return errors.New("build third-party notice: each module requires a bounded non-empty file set")
	}
	seen := make(map[string]struct{}, len(material.Files))
	for _, file := range material.Files {
		if !validNoticeFileName(file.Name) {
			return errors.New("build third-party notice: license source name is invalid")
		}
		if _, duplicate := seen[file.Name]; duplicate {
			return errors.New("build third-party notice: duplicate license source")
		}
		seen[file.Name] = struct{}{}
		if len(file.Bytes) == 0 || len(file.Bytes) > maxNoticeFileBytes || !utf8.Valid(file.Bytes) ||
			bytes.IndexByte(file.Bytes, 0) >= 0 || bytes.Contains(file.Bytes, []byte{'\r'}) || !bytes.HasSuffix(file.Bytes, []byte{'\n'}) {
			return errors.New("build third-party notice: license source bytes are invalid")
		}
		digest := sha256.Sum256(file.Bytes)
		if file.SHA256 != hex.EncodeToString(digest[:]) {
			return errors.New("build third-party notice: license source digest does not match bytes")
		}
	}
	return nil
}

func validNoticeFileName(value string) bool {
	return value != "" && len(value) <= 128 && utf8.ValidString(value) &&
		path.Base(value) == value && path.Clean(value) == value &&
		!strings.ContainsAny(value, "\x00\r\n\t ")
}

func noticeModuleKey(modulePath, version string) string {
	if !validModuleText(modulePath, 512) || !validModuleText(version, 256) {
		return ""
	}
	return modulePath + "@" + version
}
