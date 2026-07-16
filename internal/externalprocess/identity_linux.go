//go:build linux

package externalprocess

import (
	"fmt"
	"os"
	"syscall"
)

func platformChangeTime(info os.FileInfo) (int64, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("freeze file identity: Linux stat metadata is unavailable")
	}
	return stat.Ctim.Sec*1_000_000_000 + stat.Ctim.Nsec, nil
}
