package workspace

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
)

const v1WorkspaceFixture = `{
  "schema_version": 1,
  "updated_at": "2026-07-15T08:00:00Z",
  "panes": [
    {
      "endpoint": {"kind": "local"},
      "path": {"base64": "L1VzZXJzL2FsaWNlL3Byb2plY3Q="},
      "filter": "*.go",
      "sort": {"key": "name", "direction": "ascending", "directories_first": true},
      "show_hidden": false
    },
    {
      "endpoint": {"kind": "ssh", "ssh_host_alias": "work-alias"},
      "path": {"base64": "L3Nydi9kYXRh"},
      "sort": {"key": "modified", "direction": "descending", "directories_first": true},
      "show_hidden": true
    }
  ],
  "layout": {"active_pane": 1, "preview_rows": 3},
  "cache_policy": "ephemeral"
}`

const v2WorkspaceFixture = `{
  "schema_version": 2,
  "updated_at": "2026-07-15T08:00:00Z",
  "panes": [
    {
      "endpoint": {"kind": "local"},
      "path": {"base64": "L1VzZXJzL2FsaWNlL3Byb2plY3Q="},
      "filter": "*.go",
      "sort": {"key": "name", "direction": "ascending", "directories_first": true},
      "show_hidden": false
    },
    {
      "endpoint": {"kind": "ssh", "ssh_host_alias": "work-alias"},
      "path": {"base64": "L3Nydi9kYXRh"},
      "sort": {"key": "modified", "direction": "descending", "directories_first": true},
      "show_hidden": true
    }
  ],
  "layout": {
    "active_pane": 1,
    "drawer": {"mode": "preview", "focus": "drawer", "rows": 7}
  },
  "cache_policy": "lru"
}`

func TestDocumentV2RoundTripPersistsOnlyBoundedDrawerState(t *testing.T) {
	document, err := Decode(strings.NewReader(v2WorkspaceFixture))
	if err != nil {
		t.Fatal(err)
	}
	var encoded strings.Builder
	if err := Encode(&encoded, document); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"schema_version":2`,
		`"drawer":{"mode":"preview","focus":"drawer","rows":7}`,
		`"cache_policy":"lru"`,
	} {
		if !strings.Contains(encoded.String(), want) {
			t.Fatalf("encoded v2 workspace missing %s: %s", want, encoded.String())
		}
	}
	for _, forbidden := range []string{
		"preview_body", "preview_data", "log_data", "log_records", "password", "private_key", "ticket", "askpass", "agent_socket",
	} {
		if strings.Contains(encoded.String(), forbidden) {
			t.Fatalf("encoded v2 workspace contains forbidden field %q: %s", forbidden, encoded.String())
		}
	}
	roundTripped, err := Decode(strings.NewReader(encoded.String()))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(roundTripped, document) {
		t.Fatalf("v2 round trip = %#v, want %#v", roundTripped, document)
	}
}

func TestDocumentV2AcceptsOnlyFrozenCachePolicies(t *testing.T) {
	for _, policy := range []string{"lru", "ephemeral", "pinned_offline"} {
		t.Run(policy, func(t *testing.T) {
			raw := strings.Replace(v2WorkspaceFixture, `"cache_policy": "lru"`, `"cache_policy": "`+policy+`"`, 1)
			document, err := Decode(strings.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			if string(document.CachePolicy) != policy {
				t.Fatalf("cache policy = %q, want %q", document.CachePolicy, policy)
			}
		})
	}
}

func TestDocumentV2RoundTripsEveryDrawerModeAndFocus(t *testing.T) {
	tests := []DrawerState{
		{Mode: DrawerClosed, Focus: FocusPane, Rows: 7},
		{Mode: DrawerPreview, Focus: FocusPane, Rows: 0},
		{Mode: DrawerPreview, Focus: FocusDrawer, Rows: 20},
		{Mode: DrawerJobs, Focus: FocusPane, Rows: 6},
		{Mode: DrawerJobs, Focus: FocusDrawer, Rows: 6},
		{Mode: DrawerLog, Focus: FocusPane, Rows: 6},
		{Mode: DrawerLog, Focus: FocusDrawer, Rows: 6},
	}
	for _, drawer := range tests {
		name := string(drawer.Mode) + "-" + string(drawer.Focus)
		t.Run(name, func(t *testing.T) {
			want := testDocument()
			want.Layout.Drawer = drawer
			var encoded strings.Builder
			if err := Encode(&encoded, want); err != nil {
				t.Fatal(err)
			}
			got, err := Decode(strings.NewReader(encoded.String()))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("round trip = %#v, want %#v", got, want)
			}
		})
	}
}

func TestDocumentEncodeNormalizesZeroValueDrawerForSourceCompatibility(t *testing.T) {
	document := testDocument()
	document.Layout.Drawer = DrawerState{}
	if err := document.Validate(); err != nil {
		t.Fatal(err)
	}
	var encoded strings.Builder
	if err := Encode(&encoded, document); err != nil {
		t.Fatal(err)
	}
	if want := `"drawer":{"mode":"closed","focus":"pane","rows":0}`; !strings.Contains(encoded.String(), want) {
		t.Fatalf("normalized workspace missing %s: %s", want, encoded.String())
	}
	if _, err := Decode(strings.NewReader(encoded.String())); err != nil {
		t.Fatal(err)
	}
}

func TestDocumentV2RejectsUnknownSecretAndContentFields(t *testing.T) {
	tests := map[string]string{
		"root secret":         strings.Replace(v2WorkspaceFixture, `"cache_policy": "lru"`, `"password": "WORKSPACE_SECRET", "cache_policy": "lru"`, 1),
		"drawer preview body": strings.Replace(v2WorkspaceFixture, `"rows": 7`, `"rows": 7, "preview_body": "WORKSPACE_SECRET"`, 1),
		"layout log data":     strings.Replace(v2WorkspaceFixture, `"active_pane": 1`, `"active_pane": 1, "log_data": ["WORKSPACE_SECRET"]`, 1),
		"pane credential":     strings.Replace(v2WorkspaceFixture, `"filter": "*.go"`, `"filter": "*.go", "private_key": "WORKSPACE_SECRET"`, 1),
		"endpoint credential": strings.Replace(v2WorkspaceFixture, `"ssh_host_alias": "work-alias"`, `"ssh_host_alias": "work-alias", "password": "WORKSPACE_SECRET"`, 1),
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(raw)); err == nil {
				t.Fatal("Decode() error = nil")
			}
		})
	}
}

func TestDocumentV2RejectsInvalidDrawerAndPolicyValues(t *testing.T) {
	tests := map[string]string{
		"unknown mode":   strings.Replace(v2WorkspaceFixture, `"mode": "preview"`, `"mode": "search"`, 1),
		"unknown focus":  strings.Replace(v2WorkspaceFixture, `"focus": "drawer"`, `"focus": "modal"`, 1),
		"negative rows":  strings.Replace(v2WorkspaceFixture, `"rows": 7`, `"rows": -1`, 1),
		"excessive rows": strings.Replace(v2WorkspaceFixture, `"rows": 7`, `"rows": 21`, 1),
		"unknown policy": strings.Replace(v2WorkspaceFixture, `"cache_policy": "lru"`, `"cache_policy": "forever"`, 1),
		"empty drawer":   strings.Replace(v2WorkspaceFixture, `{"mode": "preview", "focus": "drawer", "rows": 7}`, `{}`, 1),
		"closed drawer focus": strings.NewReplacer(
			`"mode": "preview"`, `"mode": "closed"`,
			`"focus": "drawer"`, `"focus": "drawer"`,
		).Replace(v2WorkspaceFixture),
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(raw)); err == nil {
				t.Fatal("Decode() error = nil")
			}
		})
	}
}

func TestDocumentV1MigratesDeterministicallyToV2(t *testing.T) {
	document, err := Decode(strings.NewReader(v1WorkspaceFixture))
	if err != nil {
		t.Fatal(err)
	}
	if document.SchemaVersion != 2 {
		t.Fatalf("migrated schema version = %d, want 2", document.SchemaVersion)
	}
	if document.CachePolicy != CachePolicy("ephemeral") {
		t.Fatalf("migrated cache policy = %q, want ephemeral", document.CachePolicy)
	}
	var encoded strings.Builder
	if err := Encode(&encoded, document); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"schema_version":2`,
		`"drawer":{"mode":"preview","focus":"pane","rows":3}`,
		`"cache_policy":"ephemeral"`,
	} {
		if !strings.Contains(encoded.String(), want) {
			t.Fatalf("migrated workspace missing %s: %s", want, encoded.String())
		}
	}
	if strings.Contains(encoded.String(), "preview_rows") {
		t.Fatalf("migrated workspace retained v1 preview_rows: %s", encoded.String())
	}

	zeroRows := strings.Replace(v1WorkspaceFixture, `"preview_rows": 3`, `"preview_rows": 0`, 1)
	zeroDocument, err := Decode(strings.NewReader(zeroRows))
	if err != nil {
		t.Fatal(err)
	}
	var zeroEncoded strings.Builder
	if err := Encode(&zeroEncoded, zeroDocument); err != nil {
		t.Fatal(err)
	}
	if want := `"drawer":{"mode":"closed","focus":"pane","rows":0}`; !strings.Contains(zeroEncoded.String(), want) {
		t.Fatalf("zero-row v1 migration missing %s: %s", want, zeroEncoded.String())
	}
}

func TestDocumentV1RemainsStrictDuringMigration(t *testing.T) {
	for name, raw := range map[string]string{
		"unknown field": strings.Replace(v1WorkspaceFixture, `"cache_policy": "ephemeral"`, `"preview_body": "WORKSPACE_SECRET", "cache_policy": "ephemeral"`, 1),
		"new policy":    strings.Replace(v1WorkspaceFixture, `"cache_policy": "ephemeral"`, `"cache_policy": "lru"`, 1),
		"future schema": strings.Replace(v1WorkspaceFixture, `"schema_version": 1`, `"schema_version": 3`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(raw)); err == nil {
				t.Fatal("Decode() error = nil")
			}
		})
	}
}

func TestStoreDoesNotOverwriteV1FixtureWhenMigrationDecodeFails(t *testing.T) {
	store, err := NewStore(filepath.Join(testkit.PersistentTempDir(t), "workspaces"))
	if err != nil {
		t.Fatal(err)
	}
	invalidV1 := strings.Replace(v1WorkspaceFixture, `"cache_policy": "ephemeral"`, `"preview_body": "WORKSPACE_SECRET", "cache_policy": "ephemeral"`, 1)
	workspacePath := store.path("legacy")
	if err := os.WriteFile(workspacePath, []byte(invalidV1), 0o600); err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- workspacePath is generated by this test's Store under a private temporary root.
	before, err := os.ReadFile(workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save("legacy", testDocument()); err == nil {
		t.Fatal("Save() replaced invalid v1 workspace")
	}
	// #nosec G304 -- workspacePath is generated by this test's Store under a private temporary root.
	after, err := os.ReadFile(workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("invalid v1 workspace changed: got %q, want %q", after, before)
	}
}

func TestStoreLoadsV1AndNextSaveWritesOnlyV2(t *testing.T) {
	store, err := NewStore(filepath.Join(testkit.PersistentTempDir(t), "workspaces"))
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := store.path("legacy")
	if err := os.WriteFile(workspacePath, []byte(v1WorkspaceFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	document, err := store.Load("legacy")
	if err != nil {
		t.Fatal(err)
	}
	if document.SchemaVersion != SchemaVersion || document.CachePolicy != CacheEphemeral {
		t.Fatalf("loaded v1 = schema %d policy %q, want schema %d policy %q", document.SchemaVersion, document.CachePolicy, SchemaVersion, CacheEphemeral)
	}
	store.now = func() time.Time { return document.UpdatedAt }
	if err := store.Save("legacy", document); err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- workspacePath is generated by this test's Store under a private temporary root.
	saved, err := os.ReadFile(workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"schema_version":1`, "preview_rows"} {
		if strings.Contains(string(saved), forbidden) {
			t.Fatalf("saved migration contains legacy field %q: %s", forbidden, saved)
		}
	}
	for _, want := range []string{`"schema_version":2`, `"drawer":{"mode":"preview","focus":"pane","rows":3}`, `"cache_policy":"ephemeral"`} {
		if !strings.Contains(string(saved), want) {
			t.Fatalf("saved migration missing %s: %s", want, saved)
		}
	}
}
