//go:build darwin || linux

package platform

import (
	"io/fs"
	"os"
)

// SameExecutableIdentity rejects both path replacement and same-inode
// mutation between executable validation and process start.
func SameExecutableIdentity(before, after fs.FileInfo) bool {
	if before == nil || after == nil || !os.SameFile(before, after) || before.Size() != after.Size() || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		return false
	}
	beforeChangeTime, beforeOK := executableChangeTime(before)
	afterChangeTime, afterOK := executableChangeTime(after)
	return beforeOK && afterOK && beforeChangeTime == afterChangeTime
}
