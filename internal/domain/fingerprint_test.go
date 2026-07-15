package domain

import (
	"testing"
	"time"
)

func TestFingerprintStrength(t *testing.T) {
	size := uint64(0)
	modifiedAt := time.Unix(1_700_000_000, 123)
	precision := TimePrecision("nanosecond")
	fileID := "inode-42"
	versionID := "version-7"
	hashAlgorithm := "sha256"
	hashHex := "0123456789abcdef"

	tests := []struct {
		name        string
		fingerprint Fingerprint
		want        FingerprintStrength
	}{
		{name: "empty is weak", want: FingerprintWeak},
		{
			name:        "size without mtime is weak",
			fingerprint: Fingerprint{Size: &size},
			want:        FingerprintWeak,
		},
		{
			name: "mtime without precision is weak",
			fingerprint: Fingerprint{
				Size:       &size,
				ModifiedAt: &modifiedAt,
			},
			want: FingerprintWeak,
		},
		{
			name: "size and precise mtime are stat",
			fingerprint: Fingerprint{
				Size:              &size,
				ModifiedAt:        &modifiedAt,
				ModifiedPrecision: &precision,
			},
			want: FingerprintStat,
		},
		{
			name: "file identity without complete stat remains weak",
			fingerprint: Fingerprint{
				FileID: &fileID,
			},
			want: FingerprintWeak,
		},
		{
			name: "file identity and complete stat are identity",
			fingerprint: Fingerprint{
				Size:              &size,
				ModifiedAt:        &modifiedAt,
				ModifiedPrecision: &precision,
				FileID:            &fileID,
			},
			want: FingerprintIdentity,
		},
		{
			name:        "version identity is strong",
			fingerprint: Fingerprint{VersionID: &versionID},
			want:        FingerprintStrong,
		},
		{
			name: "complete hash is strong",
			fingerprint: Fingerprint{
				HashAlgorithm: &hashAlgorithm,
				HashHex:       &hashHex,
			},
			want: FingerprintStrong,
		},
		{
			name:        "hash text without algorithm remains weak",
			fingerprint: Fingerprint{HashHex: &hashHex},
			want:        FingerprintWeak,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.fingerprint.Strength(); got != test.want {
				t.Fatalf("Strength() = %q, want %q", got, test.want)
			}
		})
	}
}
