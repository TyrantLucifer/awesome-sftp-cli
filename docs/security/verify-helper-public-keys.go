//go:build ignore

// verify-helper-public-keys validates public-only, non-production Helper key
// vectors. It never reads, generates, signs with, or writes private key material.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
)

const (
	wantClassification = "NON-PRODUCTION PUBLIC TEST VECTORS"
	wantAlgorithm      = "Ed25519"
)

var keyIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

type fixture struct {
	Classification string   `json:"classification"`
	Source         string   `json:"source"`
	Algorithm      string   `json:"algorithm"`
	Vectors        []vector `json:"vectors"`
}

type vector struct {
	Role         string `json:"role"`
	PublicKeyHex string `json:"public_key_hex"`
	ExpectedID   string `json:"expected_key_id"`
}

func main() {
	if len(os.Args) != 2 {
		fail("usage: go run ./docs/security/verify-helper-public-keys.go <fixture.json>")
	}

	file, err := os.Open(os.Args[1]) // #nosec G304 -- explicit operator-supplied public fixture.
	if err != nil {
		fail("open fixture: %v", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var input fixture
	if err := decoder.Decode(&input); err != nil {
		fail("decode fixture: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			fail("decode fixture: trailing JSON value")
		}
		fail("decode fixture: %v", err)
	}
	if input.Classification != wantClassification || input.Algorithm != wantAlgorithm {
		fail("fixture must be explicitly non-production Ed25519 material")
	}
	if len(input.Vectors) != 2 || input.Vectors[0].Role != "current" || input.Vectors[1].Role != "next" {
		fail("fixture must contain ordered current and next public vectors")
	}

	seen := make(map[string]struct{}, len(input.Vectors))
	for _, item := range input.Vectors {
		publicKey, err := hex.DecodeString(item.PublicKeyHex)
		if err != nil || len(publicKey) != 32 || hex.EncodeToString(publicKey) != item.PublicKeyHex {
			fail("%s public key is not canonical 32-byte lowercase hex", item.Role)
		}
		digest := sha256.Sum256(publicKey)
		keyID := "ed25519-" + hex.EncodeToString(digest[:])[:56]
		if keyID != item.ExpectedID || len(keyID) != 64 || !keyIDPattern.MatchString(keyID) {
			fail("%s key ID mismatch: derived %s", item.Role, keyID)
		}
		if _, duplicate := seen[keyID]; duplicate {
			fail("duplicate key ID: %s", keyID)
		}
		seen[keyID] = struct{}{}
		fmt.Printf("PASS %s %s\n", item.Role, keyID)
	}
}

func fail(format string, values ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", values...)
	os.Exit(1)
}
