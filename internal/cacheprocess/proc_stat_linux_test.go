//go:build linux

package cacheprocess

import "testing"

func TestParseLinuxStartTicksHandlesSpacesAndParenthesesInComm(t *testing.T) {
	stat := []byte("314 (worker ) with spaces) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 424242 20\n")

	pid, ticks, err := parseLinuxStartTicks(stat)
	if err != nil {
		t.Fatalf("parseLinuxStartTicks() error = %v", err)
	}
	if pid != 314 || ticks != 424242 {
		t.Fatalf("parseLinuxStartTicks() = (%d, %d), want (314, 424242)", pid, ticks)
	}
}

func TestParseLinuxStartTicksRejectsMalformedStat(t *testing.T) {
	tests := [][]byte{
		nil,
		[]byte("1 no-parentheses S 1 2 3"),
		[]byte("not-a-pid (cmd) S 1 2 3"),
		[]byte("1 (cmd) S 1 2 3"),
		[]byte("1 (cmd) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 invalid"),
	}
	for _, stat := range tests {
		if _, _, err := parseLinuxStartTicks(stat); err == nil {
			t.Errorf("parseLinuxStartTicks(%q) unexpectedly succeeded", stat)
		}
	}
}
