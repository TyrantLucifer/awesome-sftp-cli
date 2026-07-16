package statefs

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
)

func filesystemTypeForPath(reader io.Reader, path string) (string, error) {
	if reader == nil {
		return "", fmt.Errorf("parse mountinfo: nil reader")
	}
	cleanPath := filepath.Clean(path)
	bestMount := ""
	bestType := ""
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		separator := strings.Index(line, " - ")
		if separator < 0 {
			return "", fmt.Errorf("parse mountinfo: missing separator")
		}
		left := strings.Fields(line[:separator])
		right := strings.Fields(line[separator+3:])
		if len(left) < 6 || len(right) < 1 {
			return "", fmt.Errorf("parse mountinfo: truncated entry")
		}
		mountPoint, err := unescapeMountInfo(left[4])
		if err != nil {
			return "", err
		}
		if !pathWithinMount(cleanPath, mountPoint) || len(mountPoint) < len(bestMount) {
			continue
		}
		bestMount = mountPoint
		bestType = right[0]
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("parse mountinfo: %w", err)
	}
	if bestMount == "" || bestType == "" {
		return "", fmt.Errorf("parse mountinfo: no mount covers %q", path)
	}
	return bestType, nil
}

func pathWithinMount(path, mountPoint string) bool {
	if path == mountPoint || mountPoint == "/" {
		return true
	}
	return strings.HasPrefix(path, strings.TrimSuffix(mountPoint, "/")+"/")
}

func unescapeMountInfo(value string) (string, error) {
	var result strings.Builder
	for index := 0; index < len(value); index++ {
		if value[index] != '\\' {
			result.WriteByte(value[index])
			continue
		}
		if index+3 >= len(value) {
			return "", fmt.Errorf("parse mountinfo: truncated escape in %q", value)
		}
		decoded, err := strconv.ParseUint(value[index+1:index+4], 8, 8)
		if err != nil {
			return "", fmt.Errorf("parse mountinfo: invalid escape in %q", value)
		}
		result.WriteByte(byte(decoded))
		index += 3
	}
	return result.String(), nil
}
