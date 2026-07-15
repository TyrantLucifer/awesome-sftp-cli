//go:build darwin

package localfs

import (
	"fmt"
	"os"
	"syscall"
)

func platformMetadata(info os.FileInfo) (*uint32, *uint32, *string) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, nil, nil
	}
	uid, gid := stat.Uid, stat.Gid
	id := fmt.Sprintf("%d:%d", stat.Dev, stat.Ino)
	return &uid, &gid, &id
}
