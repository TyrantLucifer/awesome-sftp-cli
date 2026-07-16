//go:build darwin

package externalprocess

import (
	"fmt"
	"os"
	"syscall"
)

func platformChangeTime(info os.FileInfo) (int64, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("freeze file identity: Darwin stat metadata is unavailable")
	}
	return stat.Ctimespec.Sec*1_000_000_000 + stat.Ctimespec.Nsec, nil
}
