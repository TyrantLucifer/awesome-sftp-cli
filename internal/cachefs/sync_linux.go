//go:build linux

package cachefs

import (
	"fmt"
	"os"
)

func fullSyncFile(file *os.File) error {
	if file == nil {
		return fmt.Errorf("full sync cache file: nil file")
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("fsync cache file: %w", err)
	}
	return nil
}
