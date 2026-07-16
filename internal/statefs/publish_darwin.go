//go:build darwin

package statefs

import "golang.org/x/sys/unix"

func publishNoReplace(source, destination string) error {
	return unix.RenameatxNp(unix.AT_FDCWD, source, unix.AT_FDCWD, destination, unix.RENAME_EXCL)
}
