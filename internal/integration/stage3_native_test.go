//go:build darwin || linux

package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/externalprocess"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
)

func TestStage3NativeEditorsDirectExecMaterialization(t *testing.T) {
	tests := []struct {
		name       string
		candidates []string
		arguments  func(string) []string
	}{
		{
			name:       "vim",
			candidates: []string{"/usr/bin/vim", "/opt/homebrew/bin/vim"},
			arguments: func(value string) []string {
				return []string{"-Nu", "NONE", "-n", "-es", "-c", "call setline(1, '" + value + "')", "-c", "wq"}
			},
		},
		{
			name:       "nvim",
			candidates: []string{"/usr/bin/nvim", "/opt/homebrew/bin/nvim", "/usr/local/bin/nvim"},
			arguments: func(value string) []string {
				return []string{"--clean", "--headless", "-c", "call setline(1, '" + value + "')", "-c", "wq"}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			executable := firstExecutable(test.candidates)
			if executable == "" {
				t.Skipf("native %s is unavailable at the supported absolute paths", test.name)
			}
			materialization := filepath.Join(t.TempDir(), "materialized.txt")
			if err := os.WriteFile(materialization, []byte("before\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			value := "stage3-native-" + test.name
			resolved, err := externalprocess.ResolveCommand(externalprocess.Command{Executable: executable, Args: test.arguments(value)}, "")
			if err != nil {
				t.Fatalf("resolve native %s: %v", test.name, err)
			}
			plan, err := externalprocess.NewPlan(resolved, materialization, os.Environ())
			if err != nil {
				t.Fatalf("plan native %s: %v", test.name, err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			command, err := plan.CommandContext(ctx)
			if err != nil {
				t.Fatalf("revalidate native %s plan: %v", test.name, err)
			}
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("native %s direct exec: %v\n%s", test.name, err, output)
			}
			// #nosec G304 -- materialization is confined to this test's private TempDir.
			content, err := os.ReadFile(materialization)
			if err != nil || strings.TrimSpace(string(content)) != value {
				t.Fatalf("native %s materialization = %q, %v", test.name, content, err)
			}
		})
	}
}

func TestStage3NativeDefaultOpenerDirectExec(t *testing.T) {
	want, ok := externalprocess.DefaultOpenerPath(runtime.GOOS)
	if !ok {
		t.Skipf("no Stage 3 native opener is defined for %s", runtime.GOOS)
	}
	if info, err := os.Stat(want); err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		t.Skipf("native opener %s is unavailable: %v", want, err)
	}
	materialization := filepath.Join(t.TempDir(), "materialized.txt")
	if err := os.WriteFile(materialization, []byte("stage3 native opener\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	arguments := []string(nil)
	environment := os.Environ()
	var marker string
	switch runtime.GOOS {
	case "darwin":
		probe := exec.Command("/usr/bin/open", "-Ra", "Finder")
		if output, err := probe.CombinedOutput(); err != nil {
			t.Skipf("LaunchServices session is unavailable: %v: %s", err, output)
		}
		arguments = []string{"-R"}
	case "linux":
		var err error
		environment, marker, err = isolatedXDGOpenerEnvironment(t, environment)
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unhandled native opener platform %q", runtime.GOOS)
	}

	resolved, err := externalprocess.ResolveCommand(externalprocess.Command{Executable: want, Args: arguments}, "")
	if err != nil {
		t.Fatalf("resolve native opener: %v", err)
	}
	plan, err := externalprocess.NewPlan(resolved, materialization, environment)
	if err != nil {
		t.Fatalf("plan native opener: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command, err := plan.CommandContext(ctx)
	if err != nil {
		t.Fatalf("revalidate native opener plan: %v", err)
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("native opener %s direct exec: %v\n%s", want, err, output)
	}
	if marker != "" {
		waitForNativeOpenerMarker(t, marker, materialization)
	}
}

func TestStage3LocalShellForegroundPTY(t *testing.T) {
	binary, observer := buildStage2MVPFixtures(t)
	root := testkit.PersistentTempDir(t)
	runStage3ShellProof(t, binary, observer, root)
}

func TestStage3TemporarySSHDRemoteShellForegroundPTY(t *testing.T) {
	if os.Getenv("AMSFTP_REAL_SSHD") != "1" {
		t.Skip("set AMSFTP_REAL_SSHD=1 in an isolated account")
	}
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	keyRoot := t.TempDir()
	clientKey := filepath.Join(keyRoot, "client_key")
	runSSHKeygen(t, "-q", "-t", "ed25519", "-N", "", "-f", clientKey)
	// #nosec G304 -- clientKey is generated inside this test's private TempDir.
	publicKey, err := os.ReadFile(clientKey + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	server := startTestSSHD(t, current.Username, publicKey, "stage3-shell")
	alias := "amsftp-stage3-shell"
	sshDir := filepath.Join(current.HomeDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(sshDir, "config")
	// #nosec G304 -- guarded integration test reads the isolated runner account's SSH config.
	original, readErr := os.ReadFile(configPath)
	hadConfig := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	// #nosec G703 -- guarded integration test writes only the isolated runner's canonical SSH config.
	if err := os.WriteFile(configPath, append(original, []byte(sshHostConfig(alias, server, current.Username, clientKey))...), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if hadConfig {
			// #nosec G703 -- restore the exact isolated runner config path captured above.
			_ = os.WriteFile(configPath, original, 0o600)
		} else {
			_ = os.Remove(configPath)
		}
	})
	binary, observer := buildStage2MVPFixtures(t)
	runStage3ShellProof(t, binary, observer, t.TempDir(), alias, server.root)
}

func runStage3ShellProof(t *testing.T, binary, observer, root string, remote ...string) {
	t.Helper()
	arguments := []string{"hosted-stage3-shell.py", binary, observer, root}
	arguments = append(arguments, remote...)
	// #nosec G204 -- script name is fixed and all remaining arguments are test-owned fixture paths.
	command := exec.Command("python3", arguments...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Stage 3 native shell PTY proof failed: %v\n%s", err, output)
	}
}

func firstExecutable(candidates []string) string {
	for _, candidate := range candidates {
		canonical, err := filepath.EvalSymlinks(candidate)
		if err != nil || !filepath.IsAbs(canonical) {
			continue
		}
		info, err := os.Stat(canonical)
		if err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return canonical
		}
	}
	return ""
}

func isolatedXDGOpenerEnvironment(t *testing.T, base []string) ([]string, string, error) {
	t.Helper()
	root := t.TempDir()
	configHome := filepath.Join(root, "config")
	dataHome := filepath.Join(root, "data")
	applications := filepath.Join(dataHome, "applications")
	if err := os.MkdirAll(applications, 0o700); err != nil {
		return nil, "", err
	}
	marker := filepath.Join(root, "opened-path")
	helper := filepath.Join(root, "opener-helper")
	helperBody := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$1\" > %q\n", marker)
	// #nosec G306 -- executable fixture is required to prove xdg-open dispatch.
	if err := os.WriteFile(helper, []byte(helperBody), 0o700); err != nil {
		return nil, "", err
	}
	desktop := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=amsftp Stage 3 opener proof\nExec=%s %%f\nTerminal=false\nNoDisplay=true\nMimeType=text/plain;\n", helper)
	if err := os.WriteFile(filepath.Join(applications, "amsftp-stage3-opener.desktop"), []byte(desktop), 0o600); err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(configHome, 0o700); err != nil {
		return nil, "", err
	}
	mimeapps := "[Default Applications]\ntext/plain=amsftp-stage3-opener.desktop\n"
	if err := os.WriteFile(filepath.Join(configHome, "mimeapps.list"), []byte(mimeapps), 0o600); err != nil {
		return nil, "", err
	}
	overrides := map[string]string{
		"HOME": root, "XDG_CONFIG_HOME": configHome, "XDG_DATA_HOME": dataHome,
		"XDG_CACHE_HOME": filepath.Join(root, "cache"),
		// xdg-open only consults MIME desktop handlers in a graphical
		// session. The isolated handler itself is headless and deterministic.
		"DISPLAY": ":99",
	}
	environment := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if _, replaced := overrides[key]; ok && replaced {
			continue
		}
		environment = append(environment, entry)
	}
	for key, value := range overrides {
		environment = append(environment, key+"="+value)
	}
	return environment, marker, nil
}

func waitForNativeOpenerMarker(t *testing.T, marker, materialization string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		// #nosec G304 -- marker is confined to this test's private TempDir.
		content, err := os.ReadFile(marker)
		if err == nil {
			got, evalErr := filepath.EvalSymlinks(strings.TrimSpace(string(content)))
			want, wantErr := filepath.EvalSymlinks(materialization)
			if evalErr != nil || wantErr != nil || got != want {
				t.Fatalf("native opener materialization = %q (%v), want %q (%v)", got, evalErr, want, wantErr)
			}
			return
		}
		if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("native opener did not invoke registered isolated handler; marker %s is absent", marker)
}
