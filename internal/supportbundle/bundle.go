package supportbundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/redaction"
)

const (
	OutputVersion    = 1
	MaxFileBytes     = 512 * 1024
	MaxExpandedBytes = 3 * 1024 * 1024
	MaxBundleBytes   = 4 * 1024 * 1024
)

type Source struct {
	Name        string
	Sensitivity redaction.Sensitivity
	Bytes       []byte
}

type File struct {
	Name        string                `json:"name"`
	Sensitivity redaction.Sensitivity `json:"sensitivity"`
	Size        int64                 `json:"size"`
	SHA256      string                `json:"sha256"`
}

type Plan struct {
	OutputVersion int    `json:"output_version"`
	Files         []File `json:"files"`
	ConsentDigest string `json:"consent_digest"`
}

type manifest struct {
	OutputVersion int    `json:"output_version"`
	Files         []File `json:"files"`
}

type layoutEntry struct {
	name        string
	sensitivity redaction.Sensitivity
}

var layout = [...]layoutEntry{
	{name: "version.json", sensitivity: redaction.SystemMetadata},
	{name: "platform.json", sensitivity: redaction.SystemMetadata},
	{name: "config-shape.json", sensitivity: redaction.Pseudonymous},
	{name: "doctor.json", sensitivity: redaction.SystemMetadata},
	{name: "diagnostics.json", sensitivity: redaction.Pseudonymous},
	{name: "jobs.json", sensitivity: redaction.Pseudonymous},
	{name: "database-health.json", sensitivity: redaction.SystemMetadata},
	{name: "capabilities.json", sensitivity: redaction.SystemMetadata},
}

type normalizedSource struct {
	file  File
	bytes []byte
}

func Preview(sources []Source) (Plan, error) {
	files, manifestBytes, err := normalize(sources)
	if err != nil {
		return Plan{}, err
	}
	return planFrom(files, manifestBytes)
}

func planFrom(files []normalizedSource, manifestBytes []byte) (Plan, error) {
	manifestFile := previewFile("manifest.json", redaction.Public, manifestBytes)
	allFiles := make([]File, 0, len(files)+1)
	allFiles = append(allFiles, manifestFile)
	for _, source := range files {
		allFiles = append(allFiles, source.file)
	}
	consentBytes, err := json.Marshal(struct {
		OutputVersion int    `json:"output_version"`
		Files         []File `json:"files"`
	}{OutputVersion: OutputVersion, Files: allFiles})
	if err != nil {
		return Plan{}, fmt.Errorf("encode support-bundle preview: %w", err)
	}
	digest := sha256.Sum256(consentBytes)
	return Plan{OutputVersion: OutputVersion, Files: allFiles, ConsentDigest: hex.EncodeToString(digest[:])}, nil
}

func Build(sources []Source, consentDigest string) ([]byte, error) {
	normalized, manifestBytes, err := normalize(sources)
	if err != nil {
		return nil, err
	}
	plan, err := planFrom(normalized, manifestBytes)
	if err != nil {
		return nil, err
	}
	if consentDigest == "" || consentDigest != plan.ConsentDigest {
		return nil, errors.New("build support bundle: preview consent digest does not match")
	}

	var destination bytes.Buffer
	compressed, err := gzip.NewWriterLevel(&destination, gzip.BestCompression)
	if err != nil {
		return nil, fmt.Errorf("build support bundle: create gzip writer: %w", err)
	}
	compressed.ModTime = time.Unix(0, 0)
	compressed.OS = 255
	archive := tar.NewWriter(compressed)
	if err := writeEntry(archive, "manifest.json", manifestBytes); err != nil {
		return nil, closeBuildWriters(archive, compressed, err)
	}
	for _, source := range normalized {
		if err := writeEntry(archive, source.file.Name, source.bytes); err != nil {
			return nil, closeBuildWriters(archive, compressed, err)
		}
	}
	if err := archive.Close(); err != nil {
		_ = compressed.Close()
		return nil, fmt.Errorf("build support bundle: close tar: %w", err)
	}
	if err := compressed.Close(); err != nil {
		return nil, fmt.Errorf("build support bundle: close gzip: %w", err)
	}
	if destination.Len() > MaxBundleBytes {
		return nil, errors.New("build support bundle: compressed output exceeds maximum size")
	}
	return append([]byte(nil), destination.Bytes()...), nil
}

func normalize(sources []Source) ([]normalizedSource, []byte, error) {
	if len(sources) == 0 || len(sources) > len(layout) {
		return nil, nil, errors.New("preview support bundle: source count is outside bounds")
	}
	byName := make(map[string]Source, len(sources))
	for _, source := range sources {
		if _, exists := byName[source.Name]; exists {
			return nil, nil, errors.New("preview support bundle: duplicate source name")
		}
		byName[source.Name] = source
	}

	normalized := make([]normalizedSource, 0, len(sources))
	var expanded int64
	for _, allowed := range layout {
		source, exists := byName[allowed.name]
		if !exists {
			continue
		}
		delete(byName, allowed.name)
		if source.Sensitivity != allowed.sensitivity {
			return nil, nil, fmt.Errorf("preview support bundle: source %q has unreviewed sensitivity", source.Name)
		}
		if len(source.Bytes) == 0 || len(source.Bytes) > MaxFileBytes {
			return nil, nil, fmt.Errorf("preview support bundle: source %q size is outside bounds", source.Name)
		}
		if !json.Valid(source.Bytes) {
			return nil, nil, fmt.Errorf("preview support bundle: source %q is not valid JSON", source.Name)
		}
		content := append([]byte(nil), source.Bytes...)
		expanded += int64(len(content))
		if expanded > MaxExpandedBytes {
			return nil, nil, errors.New("preview support bundle: expanded sources exceed maximum size")
		}
		normalized = append(normalized, normalizedSource{file: previewFile(source.Name, source.Sensitivity, content), bytes: content})
	}
	if len(byName) != 0 {
		return nil, nil, errors.New("preview support bundle: source name is not in the reviewed layout")
	}
	files := make([]File, 0, len(normalized))
	for _, source := range normalized {
		files = append(files, source.file)
	}
	manifestBytes, err := json.Marshal(manifest{OutputVersion: OutputVersion, Files: files})
	if err != nil {
		return nil, nil, fmt.Errorf("preview support bundle: encode manifest: %w", err)
	}
	if len(manifestBytes) > MaxFileBytes || int64(len(manifestBytes))+expanded > MaxExpandedBytes {
		return nil, nil, errors.New("preview support bundle: manifest exceeds maximum size")
	}
	return normalized, manifestBytes, nil
}

func previewFile(name string, sensitivity redaction.Sensitivity, content []byte) File {
	digest := sha256.Sum256(content)
	return File{Name: name, Sensitivity: sensitivity, Size: int64(len(content)), SHA256: hex.EncodeToString(digest[:])}
}

func writeEntry(archive *tar.Writer, name string, content []byte) error {
	header := &tar.Header{
		Name:     name,
		Mode:     0o600,
		Uid:      0,
		Gid:      0,
		Size:     int64(len(content)),
		ModTime:  time.Unix(0, 0),
		Typeflag: tar.TypeReg,
		Format:   tar.FormatUSTAR,
	}
	if err := archive.WriteHeader(header); err != nil {
		return fmt.Errorf("build support bundle: write %q header: %w", name, err)
	}
	if _, err := archive.Write(content); err != nil {
		return fmt.Errorf("build support bundle: write %q: %w", name, err)
	}
	return nil
}

func closeBuildWriters(archive *tar.Writer, compressed *gzip.Writer, buildErr error) error {
	archiveErr := archive.Close()
	compressedErr := compressed.Close()
	return errors.Join(buildErr, archiveErr, compressedErr)
}
