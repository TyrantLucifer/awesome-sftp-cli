package domain

import "time"

type Fingerprint struct {
	Size              *uint64
	ModifiedAt        *time.Time
	ModifiedPrecision *TimePrecision
	FileID            *string
	VersionID         *string
	HashAlgorithm     *string
	HashHex           *string
}

type FingerprintStrength string

const (
	FingerprintWeak     FingerprintStrength = "weak"
	FingerprintStat     FingerprintStrength = "stat"
	FingerprintIdentity FingerprintStrength = "identity"
	FingerprintStrong   FingerprintStrength = "strong"
)

func (f Fingerprint) Strength() FingerprintStrength {
	if nonEmpty(f.VersionID) || nonEmpty(f.HashAlgorithm) && nonEmpty(f.HashHex) {
		return FingerprintStrong
	}

	hasStat := f.Size != nil && f.ModifiedAt != nil && nonEmptyPrecision(f.ModifiedPrecision)
	if hasStat && nonEmpty(f.FileID) {
		return FingerprintIdentity
	}
	if hasStat {
		return FingerprintStat
	}
	return FingerprintWeak
}

func nonEmpty(value *string) bool {
	return value != nil && *value != ""
}

func nonEmptyPrecision(value *TimePrecision) bool {
	return value != nil && *value != ""
}
