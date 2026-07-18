package helper

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
)

func TestEndpointRegistryPersistsOpaqueIdentityAcrossDaemonRestarts(t *testing.T) {
	root := filepath.Join(testkit.PersistentTempDir(t), "helper-state")
	store, err := NewStateStore(root)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.ResolveEndpoint("production-sftp")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := domain.ParseEndpointID(string(first)); err != nil {
		t.Fatalf("resolved endpoint ID = %q: %v", first, err)
	}
	second, err := store.ResolveEndpoint("backup-sftp")
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatal("distinct host aliases shared one endpoint identity")
	}

	reopened, err := NewStateStore(root)
	if err != nil {
		t.Fatal(err)
	}
	got, exists, err := reopened.LookupEndpoint("production-sftp")
	if err != nil || !exists || got != first {
		t.Fatalf("reopened lookup = %q, %t, %v; want %q", got, exists, err, first)
	}
	alias, exists, err := reopened.LookupHostAlias(first)
	if err != nil || !exists || alias != "production-sftp" {
		t.Fatalf("reverse lookup = %q, %t, %v", alias, exists, err)
	}
	info, err := os.Stat(filepath.Join(root, "endpoints.json"))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("endpoint registry mode = %v, %v", info, err)
	}
}

func TestEndpointRegistryLookupDoesNotCreateStateAndRejectsUnsafeAliases(t *testing.T) {
	root := filepath.Join(testkit.PersistentTempDir(t), "helper-state")
	store, err := NewStateStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists, err := store.LookupEndpoint("missing"); err != nil || exists {
		t.Fatalf("missing lookup = exists %t, %v", exists, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "endpoints.json")); !os.IsNotExist(err) {
		t.Fatalf("read-only lookup created endpoint registry: %v", err)
	}
	for _, alias := range []string{"", "-oProxyCommand=evil", "bad\nname", string(make([]byte, maxHelperHostAliasBytes+1))} {
		if _, err := store.ResolveEndpoint(alias); err == nil {
			t.Fatalf("unsafe alias %q was accepted", alias)
		}
	}
}

func TestEndpointRegistryCorruptionAndReplacementFailClosed(t *testing.T) {
	root := filepath.Join(testkit.PersistentTempDir(t), "helper-state")
	store, err := NewStateStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolveEndpoint("production-sftp"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "endpoints.json")
	if err := os.Chmod(path, 0o644); err != nil { // #nosec G302 -- the test deliberately proves public state is rejected.
		t.Fatal(err)
	}
	if _, err := NewStateStore(root); err == nil {
		t.Fatal("public endpoint registry was accepted")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", path); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStateStore(root); err == nil {
		t.Fatal("symlink endpoint registry was accepted")
	}
}

func TestEndpointRegistryInterruptedReplacePreservesPreviousMapping(t *testing.T) {
	root := filepath.Join(testkit.PersistentTempDir(t), "helper-state")
	store, err := NewStateStore(root)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.ResolveEndpoint("production-sftp")
	if err != nil {
		t.Fatal(err)
	}
	store.beforeEndpointRegistryRename = func() error { return errors.New("injected interruption") }
	if _, err := store.ResolveEndpoint("backup-sftp"); err == nil {
		t.Fatal("injected registry replace failure was ignored")
	}

	reopened, err := NewStateStore(root)
	if err != nil {
		t.Fatal(err)
	}
	got, exists, err := reopened.LookupEndpoint("production-sftp")
	if err != nil || !exists || got != first {
		t.Fatalf("previous mapping after interrupted replace = %q, %t, %v", got, exists, err)
	}
	if _, exists, err := reopened.LookupEndpoint("backup-sftp"); err != nil || exists {
		t.Fatalf("uncommitted mapping became visible = %t, %v", exists, err)
	}
}
