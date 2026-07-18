package app_test

import (
	"reflect"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/app"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/auth"
)

func TestParseInvocation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want app.Invocation
	}{
		{name: "default client", want: app.Invocation{Role: app.RoleClient}},
		{name: "single location", args: []string{"devbox:/remote/path"}, want: app.Invocation{Role: app.RoleClient}},
		{name: "two pane locations", args: []string{"/local/path", "devbox:/remote/path"}, want: app.Invocation{Role: app.RoleClient}},
		{name: "saved workspace", args: []string{"--workspace", "release"}, want: app.Invocation{Role: app.RoleClient}},
		{name: "client option", args: []string{"--future-client-option"}, want: app.Invocation{Role: app.RoleClient}},
		{name: "role-like location remains client", args: []string{"daemno"}, want: app.Invocation{Role: app.RoleClient}},
		{name: "explicit client", args: []string{"client"}, want: app.Invocation{Role: app.RoleClient}},
		{name: "daemon", args: []string{"daemon"}, want: app.Invocation{Role: app.RoleDaemon}},
		{name: "askpass", args: []string{"askpass"}, want: app.Invocation{Role: app.RoleAskpass}},
		{name: "helper", args: []string{"helper"}, want: app.Invocation{Role: app.RoleHelper}},
		{name: "job", args: []string{"job", "list"}, want: app.Invocation{Role: app.RoleJob}},
		{name: "config", args: []string{"config", "validate"}, want: app.Invocation{Role: app.RoleConfig}},
		{name: "doctor", args: []string{"doctor", "--format", "json"}, want: app.Invocation{Role: app.RoleDoctor}},
		{name: "support bundle", args: []string{"support-bundle", "preview"}, want: app.Invocation{Role: app.RoleSupportBundle}},
		{name: "completion", args: []string{"completion", "zsh"}, want: app.Invocation{Role: app.RoleCompletion}},
		{name: "help", args: []string{"--help"}, want: app.Invocation{Role: app.RoleClient, ShowHelp: true}},
		{name: "version", args: []string{"--version"}, want: app.Invocation{Role: app.RoleClient, ShowVersion: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := app.ParseInvocation(tt.args)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got %#v want %#v", got, tt.want)
			}
		})
	}
}

func TestInternalRoleArgsSelectsAskpassOnlyFromExactMarker(t *testing.T) {
	args := []string{"Password:"}
	got := app.InternalRoleArgs(args, func(key string) string {
		if key == auth.EnvInternalRole {
			return string(auth.InternalRoleAskpass)
		}
		return ""
	})
	if want := []string{"askpass", "Password:"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("InternalRoleArgs() = %#v, want %#v", got, want)
	}
	unchanged := app.InternalRoleArgs(args, func(string) string { return "client" })
	if !reflect.DeepEqual(unchanged, args) {
		t.Fatalf("unmarked args = %#v", unchanged)
	}
}
