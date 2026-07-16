package statefs

import (
	"strings"
	"testing"
)

func TestFilesystemTypeForPathUsesDeepestExactMount(t *testing.T) {
	t.Parallel()

	mountInfo := strings.Join([]string{
		"20 1 8:1 / / rw - ext4 /dev/root rw",
		"21 20 8:2 / /home rw - ext4 /dev/home rw",
		"22 21 8:3 / /home/alice/state\\040root rw - xfs /dev/state rw",
	}, "\n")
	got, err := filesystemTypeForPath(strings.NewReader(mountInfo), "/home/alice/state root/amsftp")
	if err != nil {
		t.Fatalf("filesystemTypeForPath(): %v", err)
	}
	if got != "xfs" {
		t.Fatalf("filesystem type = %q, want xfs", got)
	}
}

func TestFilesystemTypeForPathRejectsMalformedOrUncoveredInput(t *testing.T) {
	t.Parallel()

	for name, input := range map[string]string{
		"missing separator": "20 1 8:1 / / rw ext4",
		"truncated":         "20 1 - ext4",
		"invalid escape":    "20 1 8:1 / /bad\\xx rw - ext4 /dev/root rw",
		"uncovered":         "20 1 8:1 / /other rw - ext4 /dev/root rw",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := filesystemTypeForPath(strings.NewReader(input), "/home/alice/state"); err == nil {
				t.Fatal("filesystemTypeForPath() error = nil")
			}
		})
	}
}

func TestValidateRootAcceptsNativeApprovedFilesystem(t *testing.T) {
	t.Parallel()

	root := privateTempDir(t)
	filesystem, err := ValidateRoot(root)
	if err != nil {
		t.Fatalf("ValidateRoot(): %v", err)
	}
	if filesystem != FilesystemAPFS && filesystem != FilesystemExt4 && filesystem != FilesystemXFS {
		t.Fatalf("filesystem = %q, want an approved native filesystem", filesystem)
	}
}
