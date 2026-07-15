package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
)

func TestDocumentRoundTripIsStrictAndSecretFree(t *testing.T) {
	want := testDocument()
	var encoded strings.Builder
	if err := Encode(&encoded, want); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"password", "private_key", "ticket", "askpass", "agent_socket"} {
		if strings.Contains(encoded.String(), forbidden) {
			t.Fatalf("encoded workspace contains forbidden field %q: %s", forbidden, encoded.String())
		}
	}
	got, err := Decode(strings.NewReader(encoded.String()))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip = %#v, want %#v", got, want)
	}

	invalid := []string{
		strings.Replace(encoded.String(), `"schema_version":1`, `"schema_version":2`, 1),
		strings.Replace(encoded.String(), `"cache_policy":"ephemeral"`, `"cache_policy":"ephemeral","password":"secret"`, 1),
		encoded.String() + `{}`,
	}
	for _, raw := range invalid {
		if _, err := Decode(strings.NewReader(raw)); err == nil {
			t.Fatalf("Decode(%q) error = nil", raw)
		}
	}
}

func TestDocumentRoundTripPreservesInvalidUTF8PathBytes(t *testing.T) {
	want := testDocument()
	want.Panes[0].Path = string([]byte{'/', 'r', 'a', 'w', '-', 0xff})
	var encoded strings.Builder
	if err := Encode(&encoded, want); err != nil {
		t.Fatal(err)
	}
	got, err := Decode(strings.NewReader(encoded.String()))
	if err != nil {
		t.Fatal(err)
	}
	if got.Panes[0].Path != want.Panes[0].Path {
		t.Fatalf("path bytes = %x, want %x", []byte(got.Panes[0].Path), []byte(want.Panes[0].Path))
	}
}

func TestDocumentValidationRejectsUnsafePaneState(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Document)
	}{
		{name: "relative local path", mutate: func(document *Document) { document.Panes[0].Path = "relative" }},
		{name: "relative remote path", mutate: func(document *Document) { document.Panes[1].Path = "relative" }},
		{name: "missing SSH alias", mutate: func(document *Document) { document.Panes[1].Endpoint.SSHHostAlias = "" }},
		{name: "option SSH alias", mutate: func(document *Document) { document.Panes[1].Endpoint.SSHHostAlias = "-proxy" }},
		{name: "local alias", mutate: func(document *Document) { document.Panes[0].Endpoint.SSHHostAlias = "unexpected" }},
		{name: "unknown endpoint kind", mutate: func(document *Document) { document.Panes[0].Endpoint.Kind = "unknown" }},
		{name: "invalid sort", mutate: func(document *Document) { document.Panes[0].Sort.Direction = "sideways" }},
		{name: "invalid active pane", mutate: func(document *Document) { document.Layout.ActivePane = 2 }},
		{name: "non-ephemeral cache", mutate: func(document *Document) { document.CachePolicy = "persist_credentials" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := testDocument()
			test.mutate(&document)
			if err := document.Validate(); err == nil {
				t.Fatal("Validate() error = nil")
			}
		})
	}
}

func TestStoreSaveIsPrivateAtomicAndCorruptionIsVisible(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspaces")
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	original := testDocument()
	store.now = func() time.Time { return original.UpdatedAt }
	if err := store.Save("release", original); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(store.path("release"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("workspace mode = %v, want regular 0600", info.Mode())
	}

	replacement := original
	replacement.Panes[0].Path = filepath.Join(string(filepath.Separator), "replacement")
	store.beforeRename = func() error { return errors.New("injected interruption") }
	if err := store.Save("release", replacement); err == nil {
		t.Fatal("interrupted Save() error = nil")
	}
	store.beforeRename = nil
	got, err := store.Load("release")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, original) {
		t.Fatalf("workspace after interrupted save = %#v, want original %#v", got, original)
	}

	if err := os.WriteFile(store.path("release"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load("release"); err == nil {
		t.Fatal("Load() corrupt workspace error = nil")
	}
	if _, err := os.Lstat(store.path("release")); err != nil {
		t.Fatalf("corrupt workspace was removed: %v", err)
	}
}

func TestStoreListsRecentWorkspacesDeterministically(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "workspaces"))
	if err != nil {
		t.Fatal(err)
	}
	older := testDocument()
	older.UpdatedAt = time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)
	newer := testDocument()
	newer.UpdatedAt = older.UpdatedAt.Add(time.Hour)
	store.now = func() time.Time { return older.UpdatedAt }
	if err := store.Save("z-older", older); err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return newer.UpdatedAt }
	if err := store.Save("a-newer", newer); err != nil {
		t.Fatal(err)
	}
	if err := store.Save("b-newer", newer); err != nil {
		t.Fatal(err)
	}
	summaries, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(summaries))
	for index, summary := range summaries {
		got[index] = summary.Name
	}
	if want := []string{"a-newer", "b-newer", "z-older"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("workspace order = %#v, want %#v", got, want)
	}
	for _, invalid := range []string{"", ".hidden", "../escape", "name/child", "name\x00"} {
		if err := store.Save(invalid, testDocument()); err == nil {
			t.Fatalf("Save(%q) error = nil", invalid)
		}
	}
}

func testDocument() Document {
	return Document{
		SchemaVersion: SchemaVersion,
		UpdatedAt:     time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC),
		Panes: [2]Pane{
			{
				Endpoint: EndpointRef{Kind: domain.EndpointLocal},
				Path:     filepath.Join(string(filepath.Separator), "Users", "alice", "project"),
				Filter:   "*.go",
				Sort:     SortState{Key: SortName, Direction: SortAscending, DirectoriesFirst: true},
			},
			{
				Endpoint: EndpointRef{Kind: domain.EndpointSSH, SSHHostAlias: "work-alias"},
				Path:     "/srv/data",
				Sort:     SortState{Key: SortModified, Direction: SortDescending, DirectoriesFirst: true},
			},
		},
		Layout:      LayoutState{ActivePane: 1, PreviewRows: 3},
		CachePolicy: CacheEphemeral,
	}
}
