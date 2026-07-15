package app

import (
	"reflect"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/tui"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/workspace"
)

func TestParseClientInvocation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    clientInvocation
		wantErr bool
	}{
		{name: "picker", want: clientInvocation{Pick: true}},
		{name: "one location", args: []string{"/left"}, want: clientInvocation{Locations: []string{"/left"}}},
		{name: "two locations", args: []string{"/left", "work:/right"}, want: clientInvocation{Locations: []string{"/left", "work:/right"}}},
		{name: "workspace", args: []string{"--workspace", "release"}, want: clientInvocation{Workspace: "release"}},
		{name: "workspace missing name", args: []string{"--workspace"}, wantErr: true},
		{name: "workspace with location", args: []string{"--workspace", "release", "/extra"}, wantErr: true},
		{name: "unknown option", args: []string{"--unknown"}, wantErr: true},
		{name: "too many locations", args: []string{"a", "b", "c"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseClientInvocation(test.args)
			if (err != nil) != test.wantErr {
				t.Fatalf("parseClientInvocation() error = %v, wantErr %v", err, test.wantErr)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("parseClientInvocation() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestStartupPickerChoicesPreserveWorkspaceRecoveryStateAndHosts(t *testing.T) {
	updated := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	got := startupPickerChoices(
		[]workspace.Summary{{Name: "release", UpdatedAt: updated}, {Name: "broken", Problem: "invalid schema"}},
		[]string{"bastion"},
	)
	want := []tui.PickerChoice{
		{Kind: tui.PickerWorkspace, Name: "release", Recent: updated},
		{Kind: tui.PickerWorkspace, Name: "broken", Problem: "invalid schema"},
		{Kind: tui.PickerHost, Name: "bastion"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("startupPickerChoices() = %#v, want %#v", got, want)
	}
}

func TestRemoveLastRuneDoesNotSplitUTF8(t *testing.T) {
	if got := removeLastRune("host界"); got != "host" {
		t.Fatalf("removeLastRune() = %q", got)
	}
	if got := removeLastRune(""); got != "" {
		t.Fatalf("removeLastRune(empty) = %q", got)
	}
}
