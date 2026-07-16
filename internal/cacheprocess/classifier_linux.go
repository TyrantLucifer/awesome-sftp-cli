//go:build linux

package cacheprocess

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"syscall"
)

const linuxBirthPrefix = "linux-start-ticks:"

func readPlatformBirthID(pid int) (string, lookupOutcome) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
			return "", lookupGone
		}
		return "", lookupUncertain
	}
	observedPID, startTicks, err := parseLinuxStartTicks(data)
	if err != nil || observedPID != pid {
		return "", lookupUncertain
	}
	return fmt.Sprintf("%s%d", linuxBirthPrefix, startTicks), lookupFound
}

func validPlatformBirthID(value string) bool {
	if !strings.HasPrefix(value, linuxBirthPrefix) {
		return false
	}
	ticks, err := strconv.ParseUint(strings.TrimPrefix(value, linuxBirthPrefix), 10, 64)
	return err == nil && value == fmt.Sprintf("%s%d", linuxBirthPrefix, ticks)
}

func parseLinuxStartTicks(data []byte) (int, uint64, error) {
	stat := strings.TrimSpace(string(data))
	open := strings.IndexByte(stat, '(')
	close := strings.LastIndexByte(stat, ')')
	if open <= 0 || close <= open || close+1 >= len(stat) {
		return 0, 0, fmt.Errorf("malformed /proc PID stat")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(stat[:open]))
	if err != nil || pid <= 0 {
		return 0, 0, fmt.Errorf("malformed /proc PID stat PID")
	}
	fields := strings.Fields(stat[close+1:])
	// The suffix begins with field 3 (state); starttime is field 22.
	const startTimeIndex = 22 - 3
	if len(fields) <= startTimeIndex || len(fields[0]) != 1 {
		return 0, 0, fmt.Errorf("malformed /proc PID stat fields")
	}
	startTicks, err := strconv.ParseUint(fields[startTimeIndex], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("malformed /proc PID stat start time: %w", err)
	}
	return pid, startTicks, nil
}
