package platform

import "testing"

func TestPublicTrustValidationRejectsUnknownPurpose(t *testing.T) {
	unknown := ValidationPurpose(255)
	if err := ValidatePrivateDirectory("/", unknown); err == nil {
		t.Fatal("ValidatePrivateDirectory() error = nil")
	}
	if err := ValidatePrivateFile("/config.json", unknown); err == nil {
		t.Fatal("ValidatePrivateFile() error = nil")
	}
}
