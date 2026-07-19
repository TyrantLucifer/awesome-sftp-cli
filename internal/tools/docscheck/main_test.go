package main

import "testing"

func TestREL010CLISelectsOrdinaryOrReleaseAuditExplicitly(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantRoot    string
		wantRelease bool
		wantErr     bool
	}{
		{name: "ordinary default", wantRoot: "."},
		{name: "ordinary root", args: []string{"/repo"}, wantRoot: "/repo"},
		{name: "release default", args: []string{"--release"}, wantRoot: ".", wantRelease: true},
		{name: "release root", args: []string{"--release", "/repo"}, wantRoot: "/repo", wantRelease: true},
		{name: "unknown flag", args: []string{"--unknown"}, wantErr: true},
		{name: "too many", args: []string{"--release", "/repo", "extra"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, release, err := parseRequest(test.args)
			if (err != nil) != test.wantErr || root != test.wantRoot || release != test.wantRelease {
				t.Fatalf("parseRequest() = (%q, %t, %v), want (%q, %t, error=%t)", root, release, err, test.wantRoot, test.wantRelease, test.wantErr)
			}
		})
	}
}
