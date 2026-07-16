//go:build darwin

package cacheprocess

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const darwinBirthPrefix = "darwin-start:"

func readPlatformBirthID(pid int) (string, lookupOutcome) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.ENOENT) {
			return "", lookupGone
		}
		// A missing Darwin PID can surface as EIO because the sysctl result is
		// empty. Confirm nonexistence without treating EPERM as death.
		if killErr := unix.Kill(pid, 0); errors.Is(killErr, unix.ESRCH) {
			return "", lookupGone
		}
		return "", lookupUncertain
	}
	if info == nil || int(info.Proc.P_pid) != pid {
		return "", lookupUncertain
	}
	started := info.Proc.P_starttime
	if started.Sec <= 0 || started.Usec < 0 || started.Usec >= 1_000_000 {
		return "", lookupUncertain
	}
	return fmt.Sprintf("%s%d:%d", darwinBirthPrefix, started.Sec, started.Usec), lookupFound
}

func validPlatformBirthID(value string) bool {
	if !strings.HasPrefix(value, darwinBirthPrefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(value, darwinBirthPrefix), ":")
	if len(parts) != 2 {
		return false
	}
	seconds, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || seconds <= 0 {
		return false
	}
	microseconds, err := strconv.ParseInt(parts[1], 10, 32)
	if err != nil || microseconds < 0 || microseconds >= 1_000_000 {
		return false
	}
	return value == fmt.Sprintf("%s%d:%d", darwinBirthPrefix, seconds, microseconds)
}
