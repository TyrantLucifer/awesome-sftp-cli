package transfer

import "testing"

func TestProductionPolicyDefaultsFreezeCurrentRuntimeBehavior(t *testing.T) {
	if got := DefaultIntegrityPolicy(); got != IntegrityStrong {
		t.Fatalf("default integrity policy = %q, want %q", got, IntegrityStrong)
	}
	if ProductionDirectTransferOpen {
		t.Fatal("production direct transfer unexpectedly open")
	}
}
