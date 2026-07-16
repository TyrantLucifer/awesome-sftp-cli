package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/workspace"
)

func TestProviderSessionsShareWorkspaceStoreAcrossClients(t *testing.T) {
	store, err := workspace.NewStore(filepath.Join(testkit.PersistentTempDir(t), "workspaces"))
	if err != nil {
		t.Fatal(err)
	}
	factory, err := NewProviderSessions([]providerapi.Provider{testLocalProvider(t)}, 4)
	if err != nil {
		t.Fatal(err)
	}
	factory.SetWorkspaceStore(store)
	writer := factory.NewSession()
	reader := factory.NewSession()
	defer writer.Close()
	defer reader.Close()

	want := testWorkspaceDocument()
	handlePayload[workspace.SaveResponse](t, writer, WorkspaceSave, workspace.SaveRequest{Name: "release", Document: want})
	loaded := handlePayload[workspace.LoadResponse](t, reader, WorkspaceLoad, workspace.LoadRequest{Name: "release"})
	want.UpdatedAt = loaded.Document.UpdatedAt
	if !reflect.DeepEqual(loaded.Document, want) {
		t.Fatalf("loaded workspace = %#v, want %#v", loaded.Document, want)
	}
	listed := handlePayload[workspace.ListResponse](t, reader, WorkspaceList, workspace.ListRequest{})
	if len(listed.Workspaces) != 1 || listed.Workspaces[0].Name != "release" || listed.Workspaces[0].Problem != "" {
		t.Fatalf("workspace list = %#v", listed.Workspaces)
	}
}

func TestProviderSessionRejectsWorkspaceRoutesWithoutStore(t *testing.T) {
	factory, err := NewProviderSessions([]providerapi.Provider{testLocalProvider(t)}, 4)
	if err != nil {
		t.Fatal(err)
	}
	session := factory.NewSession()
	defer session.Close()
	payload, err := json.Marshal(workspace.ListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Handle(context.Background(), WorkspaceList, payload); !domain.IsCode(err, domain.CodeUnsupported) {
		t.Fatalf("workspace route error = %v, want unsupported", err)
	}
}

func testWorkspaceDocument() workspace.Document {
	return workspace.Document{
		SchemaVersion: workspace.SchemaVersion,
		UpdatedAt:     time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC),
		Panes: [2]workspace.Pane{
			{
				Endpoint: workspace.EndpointRef{Kind: domain.EndpointLocal},
				Path:     "/local",
				Sort:     workspace.SortState{Key: workspace.SortName, Direction: workspace.SortAscending, DirectoriesFirst: true},
			},
			{
				Endpoint: workspace.EndpointRef{Kind: domain.EndpointSSH, SSHHostAlias: "work"},
				Path:     string([]byte{'/', 'r', 'a', 'w', '-', 0xff}),
				Sort:     workspace.SortState{Key: workspace.SortName, Direction: workspace.SortAscending, DirectoriesFirst: true},
			},
		},
		Layout:      workspace.LayoutState{Drawer: workspace.DrawerState{Mode: workspace.DrawerClosed, Focus: workspace.FocusPane}},
		CachePolicy: workspace.CacheEphemeral,
	}
}
