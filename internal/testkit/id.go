package testkit

import (
	"errors"
	"math"
	"sync"
)

const (
	sequenceEncodedLength = 26
	sequenceAlphabet      = "abcdefghijklmnopqrstuvwxyz234567"
)

var errSequenceExhausted = errors.New("sequence generator exhausted")

type SequenceGenerator struct {
	mu        sync.Mutex
	next      uint64
	exhausted bool
}

func (g *SequenceGenerator) New(prefix string) (string, error) {
	g.mu.Lock()
	if g.exhausted {
		g.mu.Unlock()
		return "", errSequenceExhausted
	}
	value := g.next
	if value == math.MaxUint64 {
		g.exhausted = true
	} else {
		g.next++
	}
	g.mu.Unlock()

	encoded := [sequenceEncodedLength]byte{}
	for index := range encoded {
		encoded[index] = sequenceAlphabet[0]
	}
	for index := len(encoded) - 1; value > 0; index-- {
		encoded[index] = sequenceAlphabet[value&31]
		value >>= 5
	}
	return prefix + string(encoded[:]), nil
}
