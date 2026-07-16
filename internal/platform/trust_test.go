package platform

import (
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"strings"
	"testing"
)

func TestTrustValidatorAcceptsSafePrivateDirectory(t *testing.T) {
	filesystem := newFakeTrustFilesystem()
	filesystem.addDirectory("/", 0, 0o755)
	filesystem.addDirectory("/home", 0, 0o755)
	filesystem.addDirectory("/home/alice", 1000, 0o755)
	filesystem.addDirectory("/home/alice/.config", 1000, 0o755)
	filesystem.addDirectory("/home/alice/.config/amsftp", 1000, 0o700)
	validator := trustValidator{goos: "linux", euid: 1000, filesystem: filesystem, acls: filesystem}

	if err := validator.validatePrivateDirectory("/home/alice/.config/amsftp", ValidatePersistent); err != nil {
		t.Fatalf("validatePrivateDirectory(): %v", err)
	}

	wantProfiles := map[string]aclProfile{
		"/":                          aclIntegrityOnly,
		"/home":                      aclIntegrityOnly,
		"/home/alice":                aclIntegrityOnly,
		"/home/alice/.config":        aclIntegrityOnly,
		"/home/alice/.config/amsftp": aclOwnerPrivate,
	}
	if !reflect.DeepEqual(filesystem.checkedACLs, wantProfiles) {
		t.Fatalf("ACL profiles = %#v, want %#v", filesystem.checkedACLs, wantProfiles)
	}
}

func TestTrustValidatorRejectsUnsafeDirectoryComponents(t *testing.T) {
	base := func() *fakeTrustFilesystem {
		filesystem := newFakeTrustFilesystem()
		filesystem.addDirectory("/", 0, 0o755)
		filesystem.addDirectory("/safe", 0, 0o755)
		filesystem.addDirectory("/safe/user", 1000, 0o700)
		return filesystem
	}

	tests := map[string]func(*fakeTrustFilesystem){
		"other owner ancestor": func(filesystem *fakeTrustFilesystem) {
			filesystem.nodes["/safe"].uid = 2000
		},
		"writable ancestor": func(filesystem *fakeTrustFilesystem) {
			filesystem.nodes["/safe"].mode = fs.ModeDir | 0o775
		},
		"final wrong mode": func(filesystem *fakeTrustFilesystem) {
			filesystem.nodes["/safe/user"].mode = fs.ModeDir | 0o750
		},
		"final other owner": func(filesystem *fakeTrustFilesystem) {
			filesystem.nodes["/safe/user"].uid = 2000
		},
		"ancestor regular file": func(filesystem *fakeTrustFilesystem) {
			filesystem.nodes["/safe"].mode = 0o644
		},
		"ancestor symlink": func(filesystem *fakeTrustFilesystem) {
			filesystem.nodes["/safe"].mode = fs.ModeSymlink | 0o777
			filesystem.nodes["/safe"].target = "/elsewhere"
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			filesystem := base()
			mutate(filesystem)
			validator := trustValidator{goos: "linux", euid: 1000, filesystem: filesystem, acls: filesystem}
			if err := validator.validatePrivateDirectory("/safe/user", ValidatePersistent); err == nil {
				t.Fatal("validatePrivateDirectory() error = nil")
			}
		})
	}
}

func TestTrustValidatorRejectsACLFailure(t *testing.T) {
	filesystem := newFakeTrustFilesystem()
	filesystem.addDirectory("/", 0, 0o755)
	filesystem.addDirectory("/safe", 0, 0o755)
	filesystem.addDirectory("/safe/user", 1000, 0o700)
	filesystem.aclErrors["/safe"] = errors.New("sentinel ACL query failure")
	validator := trustValidator{goos: "linux", euid: 1000, filesystem: filesystem, acls: filesystem}

	err := validator.validatePrivateDirectory("/safe/user", ValidatePersistent)
	if err == nil || !strings.Contains(err.Error(), "sentinel ACL query failure") {
		t.Fatalf("validatePrivateDirectory() error = %v", err)
	}
}

func TestTrustValidatorValidatesPrivateFile(t *testing.T) {
	filesystem := newFakeTrustFilesystem()
	filesystem.addDirectory("/", 0, 0o755)
	filesystem.addDirectory("/safe", 0, 0o755)
	filesystem.addDirectory("/safe/app", 1000, 0o700)
	filesystem.addFile("/safe/app/config.json", 1000, 0o600)
	validator := trustValidator{goos: "linux", euid: 1000, filesystem: filesystem, acls: filesystem}

	if err := validator.validatePrivateFile("/safe/app/config.json", ValidatePersistent); err != nil {
		t.Fatalf("validatePrivateFile(): %v", err)
	}
	if got := filesystem.checkedACLs["/safe/app/config.json"]; got != aclOwnerPrivate {
		t.Fatalf("file ACL profile = %v, want owner-private", got)
	}

	for name, mutate := range map[string]func(*fakeTrustNode){
		"wrong owner": func(node *fakeTrustNode) { node.uid = 2000 },
		"wrong mode":  func(node *fakeTrustNode) { node.mode = 0o640 },
		"directory":   func(node *fakeTrustNode) { node.mode = fs.ModeDir | 0o600 },
		"symlink":     func(node *fakeTrustNode) { node.mode = fs.ModeSymlink | 0o777 },
	} {
		t.Run(name, func(t *testing.T) {
			clone := filesystem.clone()
			mutate(clone.nodes["/safe/app/config.json"])
			validator := trustValidator{goos: "linux", euid: 1000, filesystem: clone, acls: clone}
			if err := validator.validatePrivateFile("/safe/app/config.json", ValidatePersistent); err == nil {
				t.Fatal("validatePrivateFile() error = nil")
			}
		})
	}
}

func TestTrustValidatorAcceptsOnlyDarwinSystemVarAlias(t *testing.T) {
	filesystem := newFakeTrustFilesystem()
	filesystem.addDirectory("/", 0, 0o755)
	filesystem.addSymlink("/var", 0, "private/var")
	filesystem.addDirectory("/private", 0, 0o755)
	filesystem.addDirectory("/private/var", 0, 0o755)
	filesystem.addDirectory("/private/var/folders", 0, 0o755)
	filesystem.addDirectory("/private/var/folders/T", 501, 0o700)
	validator := trustValidator{goos: "darwin", euid: 501, filesystem: filesystem, acls: filesystem}

	if err := validator.validatePrivateDirectory("/var/folders/T", ValidateRuntime); err != nil {
		t.Fatalf("validatePrivateDirectory(): %v", err)
	}
	if _, checkedRawAlias := filesystem.checkedACLs["/var"]; checkedRawAlias {
		t.Fatal("raw alias was treated as a trusted directory")
	}
	if got := filesystem.checkedACLs["/private/var/folders/T"]; got != aclOwnerPrivate {
		t.Fatalf("resolved final profile = %v", got)
	}

	for name, mutate := range map[string]func(*fakeTrustFilesystem){
		"wrong target":          func(value *fakeTrustFilesystem) { value.nodes["/var"].target = "private/wrong" },
		"absolute wrong target": func(value *fakeTrustFilesystem) { value.nodes["/var"].target = "/private/wrong" },
		"non-root alias":        func(value *fakeTrustFilesystem) { value.nodes["/var"].uid = 501 },
	} {
		t.Run(name, func(t *testing.T) {
			clone := filesystem.clone()
			mutate(clone)
			validator := trustValidator{goos: "darwin", euid: 501, filesystem: clone, acls: clone}
			if err := validator.validatePrivateDirectory("/var/folders/T", ValidateRuntime); err == nil {
				t.Fatal("validatePrivateDirectory() error = nil")
			}
		})
	}
}

func TestTrustValidatorAllowsStickyTmpOnlyForRuntimeFallback(t *testing.T) {
	filesystem := newFakeTrustFilesystem()
	filesystem.addDirectory("/", 0, 0o755)
	filesystem.addDirectory("/tmp", 0, fs.ModeSticky|0o777)
	filesystem.addDirectory("/tmp/amsftp-1000", 1000, 0o700)
	validator := trustValidator{goos: "linux", euid: 1000, filesystem: filesystem, acls: filesystem}

	if err := validator.validatePrivateDirectory("/tmp/amsftp-1000", ValidateRuntimeFallback); err != nil {
		t.Fatalf("runtime fallback: %v", err)
	}
	if err := validator.validatePrivateDirectory("/tmp/amsftp-1000", ValidatePersistent); err == nil {
		t.Fatal("persistent path accepted sticky /tmp exception")
	}
	if err := validator.validatePrivateDirectory("/tmp/amsftp-1000", ValidateRuntime); err == nil {
		t.Fatal("preferred runtime accepted sticky /tmp exception")
	}

	filesystem.nodes["/tmp"].mode = fs.ModeDir | 0o777
	if err := validator.validatePrivateDirectory("/tmp/amsftp-1000", ValidateRuntimeFallback); err == nil {
		t.Fatal("non-sticky /tmp accepted")
	}
}

type fakeTrustFilesystem struct {
	nodes       map[string]*fakeTrustNode
	aclErrors   map[string]error
	checkedACLs map[string]aclProfile
}

type fakeTrustNode struct {
	mode   fs.FileMode
	uid    int
	target string
}

func newFakeTrustFilesystem() *fakeTrustFilesystem {
	return &fakeTrustFilesystem{
		nodes:       make(map[string]*fakeTrustNode),
		aclErrors:   make(map[string]error),
		checkedACLs: make(map[string]aclProfile),
	}
}

func (f *fakeTrustFilesystem) addDirectory(path string, uid int, mode fs.FileMode) {
	f.nodes[path] = &fakeTrustNode{mode: fs.ModeDir | mode, uid: uid}
}

func (f *fakeTrustFilesystem) addFile(path string, uid int, mode fs.FileMode) {
	f.nodes[path] = &fakeTrustNode{mode: mode, uid: uid}
}

func (f *fakeTrustFilesystem) addSymlink(path string, uid int, target string) {
	f.nodes[path] = &fakeTrustNode{mode: fs.ModeSymlink | 0o777, uid: uid, target: target}
}

func (f *fakeTrustFilesystem) lstat(path string) (trustMetadata, error) {
	node, ok := f.nodes[path]
	if !ok {
		return trustMetadata{}, fmt.Errorf("%s: %w", path, fs.ErrNotExist)
	}
	return trustMetadata{mode: node.mode, uid: node.uid}, nil
}

func (f *fakeTrustFilesystem) readlink(path string) (string, error) {
	node, ok := f.nodes[path]
	if !ok {
		return "", fs.ErrNotExist
	}
	if node.mode&fs.ModeSymlink == 0 {
		return "", errors.New("not a symlink")
	}
	return node.target, nil
}

func (f *fakeTrustFilesystem) validateACL(path string, profile aclProfile, _ bool) error {
	f.checkedACLs[path] = profile
	return f.aclErrors[path]
}

func (f *fakeTrustFilesystem) clone() *fakeTrustFilesystem {
	clone := newFakeTrustFilesystem()
	for path, node := range f.nodes {
		copy := *node
		clone.nodes[path] = &copy
	}
	for path, err := range f.aclErrors {
		clone.aclErrors[path] = err
	}
	return clone
}
