package config

import (
	"strings"
	"testing"
)

func TestDefaultIntegrityAndDirectTransferSettingsFreezeCurrentRuntimeBehavior(t *testing.T) {
	got := Default()
	if got.Integrity.TransferPolicy != "strong" {
		t.Fatalf("default transfer integrity = %q, want strong", got.Integrity.TransferPolicy)
	}
	if got.DirectTransfer.Enabled {
		t.Fatal("default direct transfer unexpectedly enabled")
	}
}

func TestDecodeAppliesPartialIntegrityPolicyWithoutOpeningDirectTransfer(t *testing.T) {
	got, err := Decode(strings.NewReader(`{"schema_version":1,"integrity":{"transfer_policy":"require_strong"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Integrity.TransferPolicy != "require_strong" || got.DirectTransfer.Enabled {
		t.Fatalf("integrity/direct settings = %#v / %#v", got.Integrity, got.DirectTransfer)
	}
}

func TestIntegrityAndDirectTransferSettingsFailClosed(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{name: "baseline weakens current verification", input: `{"schema_version":1,"integrity":{"transfer_policy":"baseline"}}`, want: "integrity.transfer_policy"},
		{name: "unknown integrity", input: `{"schema_version":1,"integrity":{"transfer_policy":"sha1"}}`, want: "integrity.transfer_policy"},
		{name: "production direct open", input: `{"schema_version":1,"direct_transfer":{"enabled":true}}`, want: "production direct transfer distribution is closed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertDecodeErrorContains(t, test.input, test.want)
		})
	}
}
