package supportbundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/redaction"
)

func TestPreviewAndBuildAreDeterministicBoundedAndConsentBound(t *testing.T) {
	sources := []Source{
		{Name: "doctor.json", Sensitivity: redaction.SystemMetadata, Bytes: []byte(`{"output_version":1}`)},
		{Name: "version.json", Sensitivity: redaction.SystemMetadata, Bytes: []byte(`{"version":"1.0.0"}`)},
		{Name: "config-shape.json", Sensitivity: redaction.Pseudonymous, Bytes: []byte(`{"host":"<redacted>"}`)},
	}
	preview, err := Preview(sources)
	if err != nil {
		t.Fatalf("Preview(): %v", err)
	}
	if preview.OutputVersion != OutputVersion || preview.ConsentDigest == "" || len(preview.Files) != len(sources)+1 {
		t.Fatalf("preview = %#v", preview)
	}
	if preview.Files[0].Name != "manifest.json" || preview.Files[1].Name != "version.json" || preview.Files[2].Name != "config-shape.json" || preview.Files[3].Name != "doctor.json" {
		t.Fatalf("preview order = %#v", preview.Files)
	}
	for _, file := range preview.Files {
		if file.Size > MaxFileBytes || file.SHA256 == "" || file.Sensitivity == "" {
			t.Fatalf("unbounded preview file = %#v", file)
		}
	}

	reordered := []Source{sources[2], sources[0], sources[1]}
	secondPreview, err := Preview(reordered)
	if err != nil {
		t.Fatalf("Preview(reordered): %v", err)
	}
	if !reflect.DeepEqual(preview, secondPreview) {
		t.Fatalf("preview depends on caller order:\n%#v\n%#v", preview, secondPreview)
	}
	first, err := Build(sources, preview.ConsentDigest)
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	second, err := Build(reordered, preview.ConsentDigest)
	if err != nil {
		t.Fatalf("Build(reordered): %v", err)
	}
	if !bytes.Equal(first, second) || int64(len(first)) > MaxBundleBytes {
		t.Fatalf("bundle is not deterministic/bounded: first=%d second=%d", len(first), len(second))
	}
	assertArchive(t, first, []string{"manifest.json", "version.json", "config-shape.json", "doctor.json"})
	if _, err := Build(sources, "wrong-consent-digest"); err == nil {
		t.Fatal("Build() accepted mismatched preview consent")
	}
	sources[0].Bytes = []byte(`{"output_version":2}`)
	if _, err := Build(sources, preview.ConsentDigest); err == nil {
		t.Fatal("Build() accepted bytes changed after preview")
	}
}

func TestPreviewRejectsUnreviewedLayoutAndSensitiveOrUnboundedSources(t *testing.T) {
	valid := []byte(`{"ok":true}`)
	tests := []struct {
		name    string
		sources []Source
	}{
		{name: "unknown file", sources: []Source{{Name: "raw.log", Sensitivity: redaction.SystemMetadata, Bytes: valid}}},
		{name: "wrong label", sources: []Source{{Name: "version.json", Sensitivity: redaction.Pseudonymous, Bytes: valid}}},
		{name: "secret", sources: []Source{{Name: "version.json", Sensitivity: redaction.Secret, Bytes: valid}}},
		{name: "content", sources: []Source{{Name: "version.json", Sensitivity: redaction.Content, Bytes: valid}}},
		{name: "duplicate", sources: []Source{{Name: "version.json", Sensitivity: redaction.SystemMetadata, Bytes: valid}, {Name: "version.json", Sensitivity: redaction.SystemMetadata, Bytes: valid}}},
		{name: "invalid json", sources: []Source{{Name: "version.json", Sensitivity: redaction.SystemMetadata, Bytes: []byte("not-json")}}},
		{name: "oversize", sources: []Source{{Name: "version.json", Sensitivity: redaction.SystemMetadata, Bytes: make([]byte, MaxFileBytes+1)}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Preview(test.sources); err == nil {
				t.Fatal("Preview() accepted unsafe source set")
			}
		})
	}
}

func assertArchive(t *testing.T, bundle []byte, want []string) {
	t.Helper()
	compressed, err := gzip.NewReader(bytes.NewReader(bundle))
	if err != nil {
		t.Fatalf("gzip.NewReader(): %v", err)
	}
	reader := tar.NewReader(compressed)
	var names []string
	for {
		header, nextErr := reader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			t.Fatalf("tar.Next(): %v", nextErr)
		}
		if header.Mode != 0o600 || header.Uid != 0 || header.Gid != 0 || !header.ModTime.Equal(time.Unix(0, 0)) || header.Format != tar.FormatUSTAR {
			t.Fatalf("unsafe/non-deterministic header = %#v", header)
		}
		if _, err := io.Copy(io.Discard, io.LimitReader(reader, MaxFileBytes+1)); err != nil {
			t.Fatalf("read %q: %v", header.Name, err)
		}
		names = append(names, header.Name)
	}
	if err := compressed.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("archive names = %v, want %v", names, want)
	}
}
