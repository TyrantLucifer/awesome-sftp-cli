package sshconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDiscoverAliasesFollowsIncludesAndFiltersTemplates(t *testing.T) {
	sshDir := filepath.Join(t.TempDir(), ".ssh")
	if err := os.MkdirAll(filepath.Join(sshDir, "conf.d"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(sshDir, "config"), `
# literal aliases are selectable; templates and negations are not.
Host work *.template !blocked
  User developer
Include conf.d/*.conf "extra config"
Match host work
  User matched
Host after-match
Host=equals
`)
	writeFixture(t, filepath.Join(sshDir, "conf.d", "10-first.conf"), `
Host included work
Include nested.conf
`)
	writeFixture(t, filepath.Join(sshDir, "nested.conf"), `
Host nested
Include conf.d/10-first.conf
`)
	writeFixture(t, filepath.Join(sshDir, "extra config"), `
Host "quoted-host" question? mark* !negative
`)

	got, err := DiscoverAliases(filepath.Join(sshDir, "config"), sshDir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"after-match", "equals", "included", "nested", "quoted-host", "work"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("aliases = %#v, want %#v", got, want)
	}
}

func TestDiscoverAliasesHandlesMissingAndMalformedConfig(t *testing.T) {
	sshDir := filepath.Join(t.TempDir(), ".ssh")
	if got, err := DiscoverAliases(filepath.Join(sshDir, "config"), sshDir); err != nil || len(got) != 0 {
		t.Fatalf("missing config = %#v, %v", got, err)
	}
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(sshDir, "config"), "Host \"unterminated\n")
	if _, err := DiscoverAliases(filepath.Join(sshDir, "config"), sshDir); err == nil {
		t.Fatal("malformed config error = nil")
	}
}

func TestDiscoverAliasesBoundsIncludeExpansion(t *testing.T) {
	sshDir := filepath.Join(t.TempDir(), ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(sshDir, "config"), "Include loop-00.conf\n")
	for index := 0; index < maximumIncludeDepth+1; index++ {
		content := "Host depth\n"
		if index < maximumIncludeDepth {
			content += "Include loop-" + twoDigits(index+1) + ".conf\n"
		}
		writeFixture(t, filepath.Join(sshDir, "loop-"+twoDigits(index)+".conf"), content)
	}
	if _, err := DiscoverAliases(filepath.Join(sshDir, "config"), sshDir); err == nil {
		t.Fatal("include depth error = nil")
	}
}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func twoDigits(value int) string {
	return string([]byte{'0' + byte(value/10), '0' + byte(value%10)})
}
