package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDetectInstallChannel(t *testing.T) {
	tests := []struct {
		name       string
		executable string
		want       Installation
		wantErr    bool
	}{
		{
			name:       "apple silicon homebrew",
			executable: "/opt/homebrew/Cellar/amsftp/0.1.7/bin/amsftp",
			want:       Installation{Channel: ChannelHomebrew, Prefix: "/opt/homebrew", BrewExecutable: "/opt/homebrew/bin/brew", LaunchExecutable: "/opt/homebrew/bin/amsftp"},
		},
		{
			name:       "linuxbrew",
			executable: "/home/linuxbrew/.linuxbrew/Cellar/amsftp/0.1.7/bin/amsftp",
			want:       Installation{Channel: ChannelHomebrew, Prefix: "/home/linuxbrew/.linuxbrew", BrewExecutable: "/home/linuxbrew/.linuxbrew/bin/brew", LaunchExecutable: "/home/linuxbrew/.linuxbrew/bin/amsftp"},
		},
		{
			name:       "standalone",
			executable: "/home/alice/.local/bin/amsftp",
			want:       Installation{Channel: ChannelStandalone, Prefix: "/home/alice/.local", LaunchExecutable: "/home/alice/.local/bin/amsftp"},
		},
		{name: "ambiguous layout", executable: "/srv/tools/amsftp", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DetectInstallation(tt.executable)
			if (err != nil) != tt.wantErr {
				t.Fatalf("DetectInstallation() error = %v", err)
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("DetectInstallation() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestStandalonePlanResolvesStrictLatestRelease(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	mux.HandleFunc("/releases/latest", func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, "/releases/tag/v0.1.8", http.StatusFound)
	})
	mux.HandleFunc("/releases/tag/v0.1.8", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})

	manager, err := New(Config{
		GOOS: "linux", GOARCH: "amd64", Executable: "/home/alice/.local/bin/amsftp", CurrentVersion: "0.1.7",
		HTTPClient: server.Client(), LatestURL: server.URL + "/releases/latest", ReleaseRoot: server.URL + "/releases/download", AllowHTTP: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := manager.Plan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Channel != ChannelStandalone || plan.CurrentVersion != "0.1.7" || plan.TargetVersion != "0.1.8" || !plan.Available {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestStandalonePlanRejectsCrossOriginLatestRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL+"/releases/tag/v0.1.8", http.StatusFound)
	}))
	t.Cleanup(source.Close)
	manager, err := New(Config{
		GOOS: "linux", GOARCH: "amd64", Executable: "/home/alice/.local/bin/amsftp", CurrentVersion: "0.1.7",
		HTTPClient: source.Client(), LatestURL: source.URL + "/releases/latest", ReleaseRoot: source.URL + "/releases/download", AllowHTTP: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Plan(t.Context()); err == nil || !strings.Contains(err.Error(), "version tag") {
		t.Fatalf("Plan() error = %v", err)
	}
}

func TestHomebrewPlanUsesBrewMetadataWithoutMutatingFormula(t *testing.T) {
	var commands []Command
	runner := func(_ context.Context, command Command) ([]byte, error) {
		commands = append(commands, command)
		if len(command.Args) > 0 && command.Args[0] == "info" {
			return []byte(`{"formulae":[{"name":"amsftp","versions":{"stable":"0.1.8"}}]}`), nil
		}
		return nil, nil
	}
	manager, err := New(Config{
		GOOS: "darwin", GOARCH: "arm64", Executable: "/opt/homebrew/Cellar/amsftp/0.1.7/bin/amsftp", CurrentVersion: "0.1.7", Run: runner,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := manager.Plan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Available || plan.TargetVersion != "0.1.8" || plan.Channel != ChannelHomebrew {
		t.Fatalf("plan = %#v", plan)
	}
	want := []Command{
		{Path: "/opt/homebrew/bin/brew", Args: []string{"update"}},
		{Path: "/opt/homebrew/bin/brew", Args: []string{"info", "--json=v2", Formula}},
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
}

func TestHomebrewApplyFailureMarksInstallationEffectUnknown(t *testing.T) {
	manager, err := New(Config{
		GOOS: "darwin", GOARCH: "arm64", Executable: "/opt/homebrew/Cellar/amsftp/0.1.7/bin/amsftp", CurrentVersion: "0.1.7",
		Run: func(context.Context, Command) ([]byte, error) { return nil, errors.New("brew failed") },
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := Plan{Installation: manager.installation, CurrentVersion: "0.1.7", TargetVersion: "0.1.8", Available: true}
	err = manager.Apply(t.Context(), plan)
	if !ApplyEffectUnknown(err) {
		t.Fatalf("Apply() error = %v, want unknown effect", err)
	}
}

func TestStandaloneApplyVerifiesPublishedInstallerBeforeExecution(t *testing.T) {
	installer := []byte("#!/bin/sh\nexit 0\n")
	digest := sha256.Sum256(installer)
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	mux.HandleFunc("/releases/download/v0.1.8/install.sh", func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(installer)
	})
	mux.HandleFunc("/releases/download/v0.1.8/checksums.txt", func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(writer, "%s  install.sh\n", hex.EncodeToString(digest[:]))
	})
	var got Command
	manager, err := New(Config{
		GOOS: "linux", GOARCH: "amd64", Executable: "/home/alice/.local/bin/amsftp", CurrentVersion: "0.1.7",
		HTTPClient: server.Client(), ReleaseRoot: server.URL + "/releases/download", AllowHTTP: true,
		Run: func(_ context.Context, command Command) ([]byte, error) { got = command; return nil, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := Plan{Installation: Installation{Channel: ChannelStandalone, Prefix: "/home/alice/.local", LaunchExecutable: "/home/alice/.local/bin/amsftp"}, CurrentVersion: "0.1.7", TargetVersion: "0.1.8", Available: true}
	if err := manager.Apply(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if got.Path != "/bin/sh" || len(got.Args) != 6 || got.Args[1] != "--version" || got.Args[2] != "0.1.8" || got.Args[3] != "--prefix" || got.Args[4] != "/home/alice/.local" || got.Args[5] != "--no-start-daemon" {
		t.Fatalf("installer command = %#v", got)
	}
	if filepath.Base(got.Args[0]) != "install.sh" {
		t.Fatalf("installer path = %q", got.Args[0])
	}
}

func TestStandaloneApplyRejectsInstallerChecksumMismatch(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	mux.HandleFunc("/releases/download/v0.1.8/install.sh", func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("tampered"))
	})
	mux.HandleFunc("/releases/download/v0.1.8/checksums.txt", func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintln(writer, strings.Repeat("0", 64)+"  install.sh")
	})
	runs := 0
	manager, err := New(Config{
		GOOS: "linux", GOARCH: "amd64", Executable: "/home/alice/.local/bin/amsftp", CurrentVersion: "0.1.7",
		HTTPClient: server.Client(), ReleaseRoot: server.URL + "/releases/download", AllowHTTP: true,
		Run: func(context.Context, Command) ([]byte, error) { runs++; return nil, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := Plan{Installation: Installation{Channel: ChannelStandalone, Prefix: "/home/alice/.local", LaunchExecutable: "/home/alice/.local/bin/amsftp"}, CurrentVersion: "0.1.7", TargetVersion: "0.1.8", Available: true}
	if err := manager.Apply(t.Context(), plan); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("Apply() error = %v", err)
	}
	if runs != 0 {
		t.Fatalf("executed %d commands after checksum mismatch", runs)
	}
}
