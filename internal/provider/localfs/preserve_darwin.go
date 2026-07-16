package localfs

import (
	"os"

	"golang.org/x/sys/unix"
)

func preserveNoReplace(parent *os.File, source, destination string) error {
	return unix.RenameatxNp(int(parent.Fd()), source, int(parent.Fd()), destination, unix.RENAME_EXCL)
}
