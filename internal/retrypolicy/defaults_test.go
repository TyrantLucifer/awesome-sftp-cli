package retrypolicy

import (
	"reflect"
	"testing"
	"time"
)

func TestDefaultsFreezeCurrentRuntimeBehavior(t *testing.T) {
	if got, want := DefaultReconnectDelays(), []time.Duration{100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond}; !reflect.DeepEqual(got, want) {
		t.Fatalf("reconnect delays = %v, want %v", got, want)
	}
	if DefaultJobDelay != time.Minute {
		t.Fatalf("Job retry delay = %v, want %v", DefaultJobDelay, time.Minute)
	}
}
