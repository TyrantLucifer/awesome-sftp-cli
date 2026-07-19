package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
)

func TestSchemaVersionIsTwo(t *testing.T) {
	if SchemaVersion != 2 {
		t.Fatalf("SchemaVersion = %d, want 2", SchemaVersion)
	}
}

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
		strings.Replace(encoded.String(), `"schema_version":2`, `"schema_version":3`, 1),
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

func TestDocumentJSONContractPreservesPathBytesAndRejectsUnknownFields(t *testing.T) {
	want := testDocument()
	want.Panes[1].Path = string([]byte{'/', 'r', 'e', 'm', 'o', 't', 'e', '-', 0xfe})
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got Document
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON round trip = %#v, want %#v", got, want)
	}
	withUnknown := strings.Replace(string(encoded), `"cache_policy":"ephemeral"`, `"cache_policy":"ephemeral","password":"secret"`, 1)
	if err := json.Unmarshal([]byte(withUnknown), &got); err == nil {
		t.Fatal("JSON unknown field error = nil")
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
	root := filepath.Join(testkit.PersistentTempDir(t), "workspaces")
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
	corrupt, err := os.ReadFile(store.path("release"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save("release", replacement); err == nil {
		t.Fatal("Save() replaced a corrupt workspace")
	}
	afterRejectedSave, err := os.ReadFile(store.path("release"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterRejectedSave, corrupt) {
		t.Fatalf("corrupt workspace changed: got %q, want %q", afterRejectedSave, corrupt)
	}
}

func TestStoreListsRecentWorkspacesDeterministically(t *testing.T) {
	store, err := NewStore(filepath.Join(testkit.PersistentTempDir(t), "workspaces"))
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

func TestCompletionNamesAreReadOnlyPrivateBoundedAndDeterministic(t *testing.T) {
	parent := testkit.PersistentTempDir(t)
	absent := filepath.Join(parent, "absent-workspaces")
	names, err := CompletionNames(absent)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 0 {
		t.Fatalf("absent completion names = %#v, want empty", names)
	}
	if _, err := os.Lstat(absent); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completion created absent root: %v", err)
	}

	root := filepath.Join(parent, "workspaces")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	for index := 259; index >= 0; index-- {
		name := fmt.Sprintf("workspace-%03d", index)
		if err := os.WriteFile(filepath.Join(root, name+".json"), []byte("corrupt but name-completable"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".hidden.json"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "not-a-workspace.txt"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "unsafe.json"), []byte("ignored"), 0o644); err != nil { //nolint:gosec // negative test deliberately creates a non-private workspace file.
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "directory.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "workspace-000.json"), filepath.Join(root, "linked.json")); err != nil {
		t.Fatal(err)
	}

	names, err = CompletionNames(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 256 {
		t.Fatalf("completion name count = %d, want 256", len(names))
	}
	for index, name := range names {
		want := fmt.Sprintf("workspace-%03d", index)
		if name != want {
			t.Fatalf("completion name %d = %q, want %q", index, name, want)
		}
	}
	overflowRoot := filepath.Join(parent, "overflow-workspaces")
	if err := os.Mkdir(overflowRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	for index := 0; index <= 1024; index++ {
		path := filepath.Join(overflowRoot, fmt.Sprintf("ignored-%04d.txt", index))
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := CompletionNames(overflowRoot); err == nil {
		t.Fatal("CompletionNames() accepted more than 1,024 directory entries")
	}

	if err := os.Chmod(root, 0o755); err != nil { //nolint:gosec // negative test deliberately makes the workspace root non-private.
		t.Fatal(err)
	}
	if _, err := CompletionNames(root); err == nil {
		t.Fatal("CompletionNames() accepted a non-private root")
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
		Layout: LayoutState{
			ActivePane: 1,
			Drawer:     DrawerState{Mode: DrawerPreview, Focus: FocusPane, Rows: 3},
		},
		CachePolicy: CacheEphemeral,
	}
}
