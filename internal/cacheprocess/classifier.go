package cacheprocess

import (
	"fmt"
	"os"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/cache"
)

type lookupOutcome uint8

const (
	lookupUncertain lookupOutcome = iota
	lookupFound
	lookupGone
)

type birthReader func(pid int) (string, lookupOutcome)
type birthValidator func(string) bool

// Classifier compares a PID and its platform birth identity. It fails closed
// whenever either identity cannot be queried or validated.
type Classifier struct {
	read     birthReader
	validate birthValidator
}

var _ cache.ProcessClassifier = (*Classifier)(nil)

// NewClassifier returns the native process classifier for the current OS.
func NewClassifier() *Classifier {
	return newClassifier(readPlatformBirthID, validPlatformBirthID)
}

func newClassifier(read birthReader, validate birthValidator) *Classifier {
	return &Classifier{read: read, validate: validate}
}

// CurrentIdentity returns the current PID together with its native birth ID.
func CurrentIdentity() (cache.ProcessIdentity, error) {
	pid := os.Getpid()
	birthID, outcome := readPlatformBirthID(pid)
	if outcome != lookupFound || !validPlatformBirthID(birthID) {
		return cache.ProcessIdentity{}, fmt.Errorf("read current process birth identity for PID %d", pid)
	}
	return cache.ProcessIdentity{PID: pid, BirthID: birthID}, nil
}

// Classify reports matches only when both the PID and birth ID match exactly.
func (classifier *Classifier) Classify(identity cache.ProcessIdentity) cache.ProcessStatus {
	if classifier == nil || classifier.read == nil || classifier.validate == nil || identity.PID <= 0 || !classifier.validate(identity.BirthID) {
		return cache.ProcessUncertain
	}
	birthID, outcome := classifier.read(identity.PID)
	switch outcome {
	case lookupGone:
		return cache.ProcessGone
	case lookupFound:
		if birthID == "" || !classifier.validate(birthID) {
			return cache.ProcessUncertain
		}
		if birthID == identity.BirthID {
			return cache.ProcessMatches
		}
		return cache.ProcessBirthMismatch
	default:
		return cache.ProcessUncertain
	}
}
